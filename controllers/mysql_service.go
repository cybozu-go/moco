package controllers

import (
	"context"
	"errors"
	"fmt"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/jmoiron/sqlx"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type DatabaseService interface {
	GetMySQLClusterStatus(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) *MySQLClusterStatus
	ChangeMaster(ctx context.Context, targetHost, targetPassword string, host string, port int, user, password string) error
	StartSlave(ctx context.Context, targetHost, password string) error
	StopSlave(ctx context.Context, targetHost, password string) error
	SetWaitForSlaveCount(ctx context.Context, targetHost, password string, count int) error
	TurnOffReadOnly(ctx context.Context, targetHost, password string) error
}

type MySQLService struct {
	client.Client
	MySQLAccessor DatabaseAccessor
}

// TODO
func (r *MySQLService) ChangeMaster(ctx context.Context, targetHost, targetPassword string, host string, port int, user, password string) error {
	db, err := r.getDB(ctx, targetHost, targetPassword)
	if err != nil {
		return err
	}
	_, err = db.Exec(`CHANGE MASTER TO MASTER_HOST = ?, MASTER_PORT = ?, MASTER_USER = ?, MASTER_PASSWORD = ?, MASTER_AUTO_POSITION = 1`)
	return err
}

// TODO
func (r *MySQLService) StartSlave(ctx context.Context, targetHost, password string) error {
	db, err := r.getDB(ctx, targetHost, password)
	if err != nil {
		return err
	}
	_, err = db.Exec("START SLAVE")
	return err
}

// TODO
func (r *MySQLService) StopSlave(ctx context.Context, targetHost, password string) error {
	db, err := r.getDB(ctx, targetHost, password)
	if err != nil {
		return err
	}
	_, err = db.Exec(`STOP SLAVE`)
	return err
}

// TODO
func (r *MySQLService) SetWaitForSlaveCount(ctx context.Context, targetHost, password string, count int) error {
	db, err := r.getDB(ctx, targetHost, password)
	if err != nil {
		return err
	}
	_, err = db.Exec("set global rpl_semi_sync_master_wait_for_slave_count=?", count)
	return err
}

// TODO
func (r *MySQLService) TurnOffReadOnly(ctx context.Context, targetHost, password string) error {
	db, err := r.getDB(ctx, targetHost, password)
	if err != nil {
		return err
	}
	_, err = db.Exec("set global read_only=0")
	return err
}

func (r *MySQLService) getDB(ctx context.Context, host, password string) (*sqlx.DB, error) {
	db, err := r.MySQLAccessor.Get(host, moco.OperatorAdminUser, password)
	if err != nil {
		return nil, err
	}
	return db, nil
}

func (r *MySQLService) GetMySQLClusterStatus(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) *MySQLClusterStatus {
	status := &MySQLClusterStatus{
		InstanceStatus: make([]MySQLInstanceStatus, int(cluster.Spec.Replicas)),
	}
	for instanceIdx := 0; instanceIdx < int(cluster.Spec.Replicas); instanceIdx++ {
		status.InstanceStatus[instanceIdx].Available = false

		podName := fmt.Sprintf("%s-%d", uniqueName(cluster), instanceIdx)

		targetHost, password, err := getTarget(ctx, r.Client, cluster, instanceIdx)
		if err != nil {
			log.Info("instance not available", "err", err, "podName", podName)
			continue
		}

		db, err := r.getDB(ctx, targetHost, password)
		if err != nil {
			log.Info("instance not available", "err", err, "podName", podName)
			continue
		}

		primaryStatus, err := r.getMySQLPrimaryStatus(ctx, log, db)
		if err != nil {
			log.Info("get primary status failed", "err", err, "podName", podName)
			continue
		}
		status.InstanceStatus[instanceIdx].PrimaryStatus = primaryStatus

		replicaStatus, err := r.getMySQLReplicaStatus(ctx, log, db)
		if err != nil {
			log.Info("get replica status failed", "err", err, "podName", podName)
			continue
		}
		status.InstanceStatus[instanceIdx].ReplicaStatus = replicaStatus

		readOnlyStatus, err := r.getMySQLGlobalVariablesStatus(ctx, log, db)
		if err != nil {
			log.Info("get readOnly status failed", "err", err, "podName", podName)
			continue
		}
		status.InstanceStatus[instanceIdx].GlobalVariableStatus = readOnlyStatus

		cloneStatus, err := r.getMySQLCloneStateStatus(ctx, log, db)
		if err != nil {
			log.Info("get clone status failed", "err", err, "podName", podName)
			continue
		}
		status.InstanceStatus[instanceIdx].CloneStateStatus = cloneStatus

		status.InstanceStatus[instanceIdx].Available = true
	}
	return status
}
func (r *MySQLService) getMySQLPrimaryStatus(ctx context.Context, log logr.Logger, db *sqlx.DB) (*MySQLPrimaryStatus, error) {
	rows, err := db.Unsafe().Queryx(`show master status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var status MySQLPrimaryStatus
	if rows.Next() {
		err = rows.StructScan(&status)
		if err != nil {
			return nil, err
		}
		return &status, nil
	}

	return nil, errors.New("primary status is empty")
}

func (r *MySQLService) getMySQLReplicaStatus(ctx context.Context, log logr.Logger, db *sqlx.DB) (*MySQLReplicaStatus, error) {
	rows, err := db.Unsafe().Queryx(`show slave status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var status MySQLReplicaStatus
	if rows.Next() {
		err = rows.StructScan(&status)
		if err != nil {
			return nil, err
		}
		return &status, nil
	}

	return nil, nil
}

func (r *MySQLService) getMySQLGlobalVariablesStatus(ctx context.Context, log logr.Logger, db *sqlx.DB) (*MySQLGlobalVariablesStatus, error) {
	rows, err := db.Queryx(`select @@read_only, @@super_read_only, @@rpl_semi_sync_master_wait_for_slave_count`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var status MySQLGlobalVariablesStatus
	if rows.Next() {
		err = rows.StructScan(&status)
		if err != nil {
			return nil, err
		}
		return &status, nil
	}

	return nil, errors.New("readOnly status is empty")
}

func (r *MySQLService) getMySQLCloneStateStatus(ctx context.Context, log logr.Logger, db *sqlx.DB) (*MySQLCloneStateStatus, error) {
	rows, err := db.Queryx(` select state from performance_schema.clone_status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var status MySQLCloneStateStatus
	if rows.Next() {
		err = rows.StructScan(&status)
		if err != nil {
			return nil, err
		}
		return &status, nil
	}

	return nil, nil
}
