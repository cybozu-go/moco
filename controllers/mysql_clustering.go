package controllers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/go-logr/logr"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// reconcileMySQLCluster recoclies MySQL cluster
func (r *MySQLClusterReconciler) reconcileClustering(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	status, err := r.getMySQLClusterStatus(ctx, log, cluster)
	log.Info("MySQLClusterStatus", "ClusterStatus", status)

	return true, err
}

// MySQLClusterStatus contains MySQLCluster status
type MySQLClusterStatus struct {
	InstanceStatus []MySQLInstanceStatus
}

type MySQLPrimaryStatus struct {
	ExecutedGtidSet sql.NullString `db:"Executed_Gtid_Set"`
}

type MySQLReplicaStatus struct {
	ID               int            `db:"id"`
	LastIoErrno      int            `db:"Last_IO_Errno"`
	LastIoError      sql.NullString `db:"Last_IO_Error"`
	LastSqlErrno     int            `db:"Last_SQL_Errno"`
	LastSqlError     sql.NullString `db:"Last_SQL_Error"`
	MasterHost       string         `db:"Master_Host"`
	RetrievedGtidSet sql.NullString `db:"Retrieved_Gtid_Set"`
	ExecutedGtidSet  sql.NullString `db:"Executed_Gtid_Set"`
	SlaveIoRunning   string         `db:"Slave_IO_Running"`
	SlaveSqlRunning  string         `db:"Slave_SQL_Running"`
}

type MySQLGlobalVariablesStatus struct {
	ReadOnly                           bool `db:"@@read_only"`
	SuperReadOnly                      bool `db:"@@super_read_only"`
	RplSemiSyncMasterWaitForSlaveCount int  `db:"@@rpl_semi_sync_master_wait_for_slave_count"`
}

type MySQLCloneStateStatus struct {
	State sql.NullString `db:"state"`
}

type MySQLInstanceStatus struct {
	Available        bool
	PrimaryStatus    *MySQLPrimaryStatus
	ReplicaStatus    *MySQLReplicaStatus
	ReadOnlyStatus   *MySQLGlobalVariablesStatus
	CloneStateStatus *MySQLCloneStateStatus
}

func (r *MySQLClusterReconciler) getMySQLClusterStatus(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (*MySQLClusterStatus, error) {
	secret := &corev1.Secret{}
	myNS, mySecretName := r.getSecretNameForController(cluster)
	err := r.Get(ctx, client.ObjectKey{Namespace: myNS, Name: mySecretName}, secret)
	if err != nil {
		return nil, err
	}
	operatorPassword := string(secret.Data[moco.OperatorPasswordKey])

	status := &MySQLClusterStatus{
		InstanceStatus: make([]MySQLInstanceStatus, int(cluster.Spec.Replicas)),
	}
	for instanceIdx := 0; instanceIdx < int(cluster.Spec.Replicas); instanceIdx++ {
		status.InstanceStatus[instanceIdx].Available = false

		podName := fmt.Sprintf("%s-%d", uniqueName(cluster), instanceIdx)
		host := fmt.Sprintf("%s.%s.%s", podName, uniqueName(cluster), cluster.Namespace)

		db, err := r.MySQLAccessor.Get(host, moco.OperatorAdminUser, operatorPassword)
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
		status.InstanceStatus[instanceIdx].ReadOnlyStatus = readOnlyStatus

		cloneStatus, err := r.getMySQLCloneStateStatus(ctx, log, db)
		if err != nil {
			log.Info("get clone status failed", "err", err, "podName", podName)
			continue
		}
		status.InstanceStatus[instanceIdx].CloneStateStatus = cloneStatus

		status.InstanceStatus[instanceIdx].Available = true
	}
	return status, nil
}

func (r *MySQLClusterReconciler) getMySQLPrimaryStatus(ctx context.Context, log logr.Logger, db *sqlx.DB) (*MySQLPrimaryStatus, error) {
	rows, err := db.Unsafe().Queryx(`show master status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var status MySQLPrimaryStatus
	for rows.Next() {
		err = rows.StructScan(&status)
		if err != nil {
			return nil, err
		}
		return &status, nil
	}

	return nil, errors.New("primary status is empty")
}

func (r *MySQLClusterReconciler) getMySQLReplicaStatus(ctx context.Context, log logr.Logger, db *sqlx.DB) (*MySQLReplicaStatus, error) {
	rows, err := db.Unsafe().Queryx(`show slave status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var status MySQLReplicaStatus
	for rows.Next() {
		err = rows.StructScan(&status)
		if err != nil {
			return nil, err
		}
		return &status, nil
	}

	return nil, nil
}

func (r *MySQLClusterReconciler) getMySQLGlobalVariablesStatus(ctx context.Context, log logr.Logger, db *sqlx.DB) (*MySQLGlobalVariablesStatus, error) {
	rows, err := db.Queryx(`select @@read_only, @@super_read_only, @@rpl_semi_sync_master_wait_for_slave_count`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var status MySQLGlobalVariablesStatus
	for rows.Next() {
		err = rows.StructScan(&status)
		if err != nil {
			return nil, err
		}
		return &status, nil
	}

	return nil, errors.New("readOnly status is empty")
}

func (r *MySQLClusterReconciler) getMySQLCloneStateStatus(ctx context.Context, log logr.Logger, db *sqlx.DB) (*MySQLCloneStateStatus, error) {
	rows, err := db.Queryx(` select state from performance_schema.clone_status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var status MySQLCloneStateStatus
	for rows.Next() {
		err = rows.StructScan(&status)
		if err != nil {
			return nil, err
		}
		return &status, nil
	}

	return nil, nil
}
