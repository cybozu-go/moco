package controllers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/go-logr/logr"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// reconcileMySQLCluster recoclies MySQL cluster
func (r *MySQLClusterReconciler) reconcileClustering(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (ctrl.Result, error) {
	status := r.getMySQLClusterStatus(ctx, log, cluster)
	var unavailable bool
	for i, is := range status.InstanceStatus {
		if !is.Available {
			log.Info("unavailable host exists", "index", i)
			unavailable = true
		}
	}
	if unavailable {
		return ctrl.Result{}, nil
	}
	log.Info("MySQLClusterStatus", "ClusterStatus", status)

	err := r.validateConstraints(ctx, log, status, cluster)
	if err != nil {
		condition := mocov1alpha1.MySQLClusterCondition{
			Type:    mocov1alpha1.ConditionViolation,
			Status:  corev1.ConditionTrue,
			Message: err.Error(),
		}
		setCondition(&cluster.Status.Conditions, condition)

		apiErr := r.Status().Update(ctx, cluster)
		if apiErr != nil {
			return ctrl.Result{}, apiErr
		}
		return ctrl.Result{}, err
	}

	primaryIndex, err := r.selectPrimary(ctx, log, status, cluster)
	if err != nil {
		condition := mocov1alpha1.MySQLClusterCondition{
			Type:    mocov1alpha1.ConditionFailure,
			Status:  corev1.ConditionTrue,
			Message: err.Error(),
		}
		setCondition(&cluster.Status.Conditions, condition)

		apiErr := r.Status().Update(ctx, cluster)
		if apiErr != nil {
			return ctrl.Result{}, apiErr
		}
		return ctrl.Result{}, err
	}

	err = r.updatePrimary(ctx, log, status, cluster, primaryIndex)
	if err != nil {
		condition := mocov1alpha1.MySQLClusterCondition{
			Type:    mocov1alpha1.ConditionFailure,
			Status:  corev1.ConditionTrue,
			Message: err.Error(),
		}
		setCondition(&cluster.Status.Conditions, condition)

		apiErr := r.Status().Update(ctx, cluster)
		if apiErr != nil {
			return ctrl.Result{}, apiErr
		}
		return ctrl.Result{}, err
	}

	err = r.configureReplication(ctx, log, status, cluster)
	if err != nil {
		condition := mocov1alpha1.MySQLClusterCondition{
			Type:    mocov1alpha1.ConditionFailure,
			Status:  corev1.ConditionTrue,
			Message: err.Error(),
		}
		setCondition(&cluster.Status.Conditions, condition)

		apiErr := r.Status().Update(ctx, cluster)
		if apiErr != nil {
			return ctrl.Result{}, apiErr
		}
		return ctrl.Result{}, err
	}

	wait, err := r.waitForReplication(ctx, log, status, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	if wait {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	return ctrl.Result{}, nil
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
	Available            bool
	PrimaryStatus        *MySQLPrimaryStatus
	ReplicaStatus        *MySQLReplicaStatus
	GlobalVariableStatus *MySQLGlobalVariablesStatus
	CloneStateStatus     *MySQLCloneStateStatus
}

func (r *MySQLClusterReconciler) getDB(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, index int) (*sqlx.DB, error) {
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

func (r *MySQLClusterReconciler) getMySQLClusterStatus(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) *MySQLClusterStatus {
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

func (r *MySQLClusterReconciler) getMySQLPrimaryStatus(ctx context.Context, log logr.Logger, db *sqlx.DB) (*MySQLPrimaryStatus, error) {
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

func (r *MySQLClusterReconciler) getMySQLReplicaStatus(ctx context.Context, log logr.Logger, db *sqlx.DB) (*MySQLReplicaStatus, error) {
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

func (r *MySQLClusterReconciler) getMySQLGlobalVariablesStatus(ctx context.Context, log logr.Logger, db *sqlx.DB) (*MySQLGlobalVariablesStatus, error) {
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

func (r *MySQLClusterReconciler) getMySQLCloneStateStatus(ctx context.Context, log logr.Logger, db *sqlx.DB) (*MySQLCloneStateStatus, error) {
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

func (r *MySQLClusterReconciler) validateConstraints(ctx context.Context, log logr.Logger, status *MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster) error {
	if status == nil {
		panic("unreachable condition")
	}

	var writableInstanceCounts int
	var primaryIndex int
	for i, status := range status.InstanceStatus {
		if !status.GlobalVariableStatus.ReadOnly {
			writableInstanceCounts++
			primaryIndex = i
		}
	}
	if writableInstanceCounts > 1 {
		return moco.ErrConstraintsViolation
	}

	if cluster.Status.CurrentPrimaryIndex != nil && writableInstanceCounts == 1 {
		if *cluster.Status.CurrentPrimaryIndex != primaryIndex {
			return moco.ErrConstraintsViolation
		}
	}

	cond := findCondition(cluster.Status.Conditions, mocov1alpha1.ConditionViolation)
	if cond != nil {
		return moco.ErrConstraintsRecovered
	}

	return nil
}

func (r *MySQLClusterReconciler) selectPrimary(ctx context.Context, log logr.Logger, status *MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster) (int, error) {
	return 0, nil
}

func (r *MySQLClusterReconciler) updatePrimary(ctx context.Context, log logr.Logger, status *MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster, newPrimaryIndex int) error {
	cluster.Status.CurrentPrimaryIndex = &newPrimaryIndex
	err := r.Status().Update(ctx, cluster)
	if err != nil {
		return err
	}

	expectedRplSemiSyncMasterWaitForSlaveCount := int(cluster.Spec.Replicas / 2)
	st := status.InstanceStatus[newPrimaryIndex]
	if st.GlobalVariableStatus.RplSemiSyncMasterWaitForSlaveCount == expectedRplSemiSyncMasterWaitForSlaveCount {
		return nil
	}
	db, err := r.getDB(ctx, cluster, newPrimaryIndex)
	if err != nil {
		return err
	}
	_, err = db.Exec("set global rpl_semi_sync_master_wait_for_slave_count=?", expectedRplSemiSyncMasterWaitForSlaveCount)
	return err
}

func (r *MySQLClusterReconciler) configureReplication(ctx context.Context, log logr.Logger, status *MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster) error {
	podName := fmt.Sprintf("%s-%d", uniqueName(cluster), *cluster.Status.CurrentPrimaryIndex)
	masterHost := fmt.Sprintf("%s.%s.%s.svc", podName, uniqueName(cluster), cluster.Namespace)
	password, err := r.getPassword(ctx, cluster, moco.ReplicationPasswordKey)
	if err != nil {
		return err
	}

	for i, is := range status.InstanceStatus {
		if i == *cluster.Status.CurrentPrimaryIndex {
			continue
		}
		if is.ReplicaStatus == nil || is.ReplicaStatus.MasterHost != masterHost {
			db, err := r.getDB(ctx, cluster, i)
			if err != nil {
				return err
			}
			_, err = db.Exec("STOP SLAVE")
			if err != nil {
				return err
			}
			_, err = db.Exec(`CHANGE MASTER TO MASTER_HOST = ?, MASTER_PORT = ?, MASTER_USER = ?, MASTER_PASSWORD = ?, MASTER_AUTO_POSITION = 1`,
				masterHost, moco.MySQLPort, moco.ReplicatorUser, password)
			if err != nil {
				return err
			}
		}
	}

	for i := range status.InstanceStatus {
		if i == *cluster.Status.CurrentPrimaryIndex {
			continue
		}
		db, err := r.getDB(ctx, cluster, i)
		if err != nil {
			return err
		}
		_, err = db.Exec("START SLAVE")
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *MySQLClusterReconciler) waitForReplication(ctx context.Context, log logr.Logger, status *MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	primaryIndex := *cluster.Status.CurrentPrimaryIndex
	primaryStatus := status.InstanceStatus[primaryIndex]
	if !primaryStatus.GlobalVariableStatus.ReadOnly {
		return false, nil
	}

	primaryGTID := primaryStatus.PrimaryStatus.ExecutedGtidSet
	count := 0
	var outOfSyncIns []int
	for i, is := range status.InstanceStatus {
		if i == primaryIndex {
			continue
		}

		if is.ReplicaStatus.LastIoErrno != 0 {
			outOfSyncIns = append(outOfSyncIns, i)
			continue
		}

		if is.ReplicaStatus.ExecutedGtidSet == primaryGTID {
			count++
		}
	}

	if len(outOfSyncIns) != 0 {
		inss := fmt.Sprintf("%#v", outOfSyncIns)
		condition := mocov1alpha1.MySQLClusterCondition{
			Type:    mocov1alpha1.ConditionOutOfSync,
			Status:  corev1.ConditionTrue,
			Message: inss,
		}
		setCondition(&cluster.Status.Conditions, condition)

		err := r.Status().Update(ctx, cluster)
		if err != nil {
			return false, err
		}
	}

	if count < int(cluster.Spec.Replicas/2) {
		return true, nil
	}
	return false, nil
}

func (r *MySQLClusterReconciler) getPassword(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, passwordKey string) (string, error) {
	secret := &corev1.Secret{}
	myNS, mySecretName := r.getSecretNameForController(cluster)
	err := r.Get(ctx, client.ObjectKey{Namespace: myNS, Name: mySecretName}, secret)
	if err != nil {
		return "", err
	}
	return string(secret.Data[passwordKey]), nil
}
