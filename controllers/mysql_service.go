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
	ChangeMaster()
	StartSlave()
	StopSlave()
}

type MySQLService struct {
	client.Client
	MySQLAccessor DatabaseAccessor
}

func (r *MySQLService) getDB(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, index int) (*sqlx.DB, error) {
	operatorPassword, err := r.getPassword(ctx, cluster, moco.OperatorPasswordKey)
	if err != nil {
		return nil, err
	}

	podName := fmt.Sprintf("%s-%d", uniqueName(cluster), index)
	host := fmt.Sprintf("%s.%s.%s.svc", podName, uniqueName(cluster), cluster.Namespace)

	db, err := r.MySQLAccessor.Get(host, moco.OperatorAdminUser, operatorPassword)
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

		db, err := r.getDB(ctx, cluster, instanceIdx)
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
