package controllers

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	ops "github.com/cybozu-go/moco/operators"
	"github.com/go-logr/logr"
	_ "github.com/go-sql-driver/mysql"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// Operation defines operations to MySQL Cluster
type Operation struct {
	Operators      []ops.Operator
	Wait           bool
	Conditions     []mocov1alpha1.MySQLClusterCondition
	SyncedReplicas *int
}

// reconcileMySQLCluster reconciles MySQL cluster
func (r *MySQLClusterReconciler) reconcileClustering(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (ctrl.Result, error) {
	password, err := moco.GetPassword(ctx, cluster, r.Client, moco.OperatorPasswordKey)
	if err != nil {
		return ctrl.Result{}, err
	}
	var hosts []string
	for i := 0; i < int(cluster.Spec.Replicas); i++ {
		hosts = append(hosts, moco.GetHost(cluster, i))
	}
	infra := accessor.NewInfrastructure(r.Client, r.MySQLAccessor, password, hosts, moco.MySQLAdminPort)
	status := accessor.GetMySQLClusterStatus(ctx, log, infra, cluster)

	op, err := decideNextOperation(log, cluster, status)
	if err != nil {
		condErr := r.setFailureCondition(ctx, cluster, err, nil)
		if condErr != nil {
			log.Error(condErr, "unable to update status")
		}
		return ctrl.Result{}, err
	}

	for _, o := range op.Operators {
		log.Info("Run operation", "name", o.Name())
		err = o.Run(ctx, infra, cluster, status)
		if err != nil {
			condErr := r.setFailureCondition(ctx, cluster, err, nil)
			if condErr != nil {
				log.Error(condErr, "unable to update status")
			}
			return ctrl.Result{}, err
		}
	}
	err = r.setMySQLClusterStatus(ctx, cluster, op.Conditions, op.SyncedReplicas)
	if err != nil {
		return ctrl.Result{}, err
	}
	if op.Wait {
		log.Info("Waiting")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	if len(op.Operators) > 0 {
		return ctrl.Result{
			Requeue: true,
		}, nil
	}
	return ctrl.Result{}, nil
}

func decideNextOperation(log logr.Logger, cluster *mocov1alpha1.MySQLCluster, status *accessor.MySQLClusterStatus) (*Operation, error) {
	var unavailable bool
	for i, is := range status.InstanceStatus {
		if !is.Available {
			log.Info("unavailable host exists", "index", i)
			unavailable = true
		}
	}
	if unavailable {
		return nil, moco.ErrUnavailableHost
	}

	err := validateConstraints(status, cluster)
	if err != nil {
		return &Operation{
			Conditions: violationCondition(err),
		}, err
	}

	op, wait := waitForRelayLogExecution(status, cluster)
	if wait || len(op) != 0 {
		return &Operation{
			Operators:  op,
			Conditions: unavailableCondition(nil),
			Wait:       wait,
		}, nil
	}

	primaryIndex, err := selectPrimary(status, cluster)
	if err != nil {
		return nil, err
	}
	op = updatePrimary(cluster, primaryIndex)
	if len(op) != 0 {
		return &Operation{
			Operators:  op,
			Conditions: unavailableCondition(nil),
		}, nil
	}

	op = restoreEmptyInstance(status, cluster)
	if len(op) != 0 {
		return &Operation{
			Operators: op,
		}, nil
	}

	wait, outOfSyncIns := waitForClone(status, cluster)
	if wait {
		return &Operation{
			Wait:       true,
			Conditions: unavailableCondition(outOfSyncIns),
		}, nil
	}

	op = configureReplication(status, cluster)
	if len(op) != 0 {
		return &Operation{
			Operators: op,
		}, nil
	}

	wait, outOfSyncIns = waitForReplication(status, cluster)
	if wait {
		return &Operation{
			Wait:       true,
			Conditions: unavailableCondition(outOfSyncIns),
		}, nil
	}

	syncedReplicas := int(cluster.Spec.Replicas) - len(outOfSyncIns)
	op = acceptWriteRequest(status, cluster)
	if len(op) != 0 {
		return &Operation{
			Conditions:     availableCondition(outOfSyncIns),
			Operators:      op,
			SyncedReplicas: &syncedReplicas,
		}, nil
	}

	return &Operation{
		Conditions:     availableCondition(outOfSyncIns),
		SyncedReplicas: &syncedReplicas,
	}, nil
}

func (r *MySQLClusterReconciler) setMySQLClusterStatus(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, conditions []mocov1alpha1.MySQLClusterCondition, syncedStatus *int) error {
	for _, cond := range conditions {
		if cond.Type == mocov1alpha1.ConditionAvailable {
			cluster.Status.Ready = cond.Status
		}
		setCondition(&cluster.Status.Conditions, cond)
	}
	if syncedStatus != nil {
		cluster.Status.SyncedReplicas = *syncedStatus
	}
	err := r.Status().Update(ctx, cluster)
	if err != nil {
		return err
	}
	return nil
}

func (r *MySQLClusterReconciler) setFailureCondition(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, e error, outOfSyncInstances []int) error {
	setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionFailure,
		Status:  corev1.ConditionTrue,
		Message: e.Error(),
	})
	setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
		Type:   mocov1alpha1.ConditionAvailable,
		Status: corev1.ConditionFalse,
	})
	setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
		Type:   mocov1alpha1.ConditionHealthy,
		Status: corev1.ConditionFalse,
	})
	if len(outOfSyncInstances) != 0 {
		msg := fmt.Sprintf("outOfSync instances: %#v", outOfSyncInstances)
		setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
			Type:    mocov1alpha1.ConditionOutOfSync,
			Status:  corev1.ConditionTrue,
			Message: msg,
		})
	}

	err := r.Status().Update(ctx, cluster)
	if err != nil {
		return err
	}
	return nil
}

func violationCondition(e error) []mocov1alpha1.MySQLClusterCondition {
	var conditions []mocov1alpha1.MySQLClusterCondition
	setCondition(&conditions, mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionViolation,
		Status:  corev1.ConditionTrue,
		Message: e.Error(),
	})
	setCondition(&conditions, mocov1alpha1.MySQLClusterCondition{
		Type:   mocov1alpha1.ConditionFailure,
		Status: corev1.ConditionTrue,
	})
	setCondition(&conditions, mocov1alpha1.MySQLClusterCondition{
		Type:   mocov1alpha1.ConditionAvailable,
		Status: corev1.ConditionFalse,
	})
	setCondition(&conditions, mocov1alpha1.MySQLClusterCondition{
		Type:   mocov1alpha1.ConditionHealthy,
		Status: corev1.ConditionFalse,
	})
	return conditions
}

func unavailableCondition(outOfSyncInstances []int) []mocov1alpha1.MySQLClusterCondition {
	var conditions []mocov1alpha1.MySQLClusterCondition
	if len(outOfSyncInstances) == 0 {
		setCondition(&conditions, mocov1alpha1.MySQLClusterCondition{
			Type:   mocov1alpha1.ConditionOutOfSync,
			Status: corev1.ConditionFalse,
		})
	} else {
		msg := fmt.Sprintf("outOfSync instances: %#v", outOfSyncInstances)
		setCondition(&conditions, mocov1alpha1.MySQLClusterCondition{
			Type:    mocov1alpha1.ConditionOutOfSync,
			Status:  corev1.ConditionTrue,
			Message: msg,
		})
	}
	setCondition(&conditions, mocov1alpha1.MySQLClusterCondition{
		Type:   mocov1alpha1.ConditionFailure,
		Status: corev1.ConditionFalse,
	})
	setCondition(&conditions, mocov1alpha1.MySQLClusterCondition{
		Type:   mocov1alpha1.ConditionHealthy,
		Status: corev1.ConditionFalse,
	})
	setCondition(&conditions, mocov1alpha1.MySQLClusterCondition{
		Type:   mocov1alpha1.ConditionAvailable,
		Status: corev1.ConditionFalse,
	})

	return conditions
}

func availableCondition(outOfSyncInstances []int) []mocov1alpha1.MySQLClusterCondition {
	var conditions []mocov1alpha1.MySQLClusterCondition
	if len(outOfSyncInstances) == 0 {
		setCondition(&conditions, mocov1alpha1.MySQLClusterCondition{
			Type:   mocov1alpha1.ConditionOutOfSync,
			Status: corev1.ConditionFalse,
		})
		setCondition(&conditions, mocov1alpha1.MySQLClusterCondition{
			Type:   mocov1alpha1.ConditionHealthy,
			Status: corev1.ConditionTrue,
		})
	} else {
		msg := fmt.Sprintf("outOfSync instances: %#v", outOfSyncInstances)
		setCondition(&conditions, mocov1alpha1.MySQLClusterCondition{
			Type:    mocov1alpha1.ConditionOutOfSync,
			Status:  corev1.ConditionTrue,
			Message: msg,
		})
		setCondition(&conditions, mocov1alpha1.MySQLClusterCondition{
			Type:   mocov1alpha1.ConditionHealthy,
			Status: corev1.ConditionFalse,
		})
	}
	setCondition(&conditions, mocov1alpha1.MySQLClusterCondition{
		Type:   mocov1alpha1.ConditionFailure,
		Status: corev1.ConditionFalse,
	})
	setCondition(&conditions, mocov1alpha1.MySQLClusterCondition{
		Type:   mocov1alpha1.ConditionAvailable,
		Status: corev1.ConditionTrue,
	})

	return conditions
}

func validateConstraints(status *accessor.MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster) error {
	if status == nil {
		panic("unreachable condition")
	}

	var writableInstanceCounts int
	var primaryIndex int
	for i, status := range status.InstanceStatus {
		if !status.GlobalVariablesStatus.ReadOnly {
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

func selectPrimary(status *accessor.MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster) (int, error) {
	if cluster.Status.CurrentPrimaryIndex == nil {
		return 0, nil
	}

	if !status.InstanceStatus[*cluster.Status.CurrentPrimaryIndex].GlobalVariablesStatus.ReadOnly {
		return *cluster.Status.CurrentPrimaryIndex, nil
	}

	latestGTIDSet := make(MySQLGTIDSet)
	var latest, count int
	for i := 0; i < int(cluster.Spec.Replicas); i++ {
		gtidSet, err := ParseGTIDSet(status.InstanceStatus[i].PrimaryStatus.ExecutedGtidSet)
		if err != nil {
			return 0, err
		}
		cmp, err := Compare(latestGTIDSet, gtidSet)
		if err != nil {
			return 0, err
		}
		switch {
		case cmp < 0:
			latest = i
			latestGTIDSet = gtidSet
			count = 1
		case cmp == 0:
			count++
		}
	}

	if count <= int(cluster.Spec.Replicas/2) {
		return 0, moco.ErrTooFewDataReplicas
	}

	return latest, nil
}

func updatePrimary(cluster *mocov1alpha1.MySQLCluster, newPrimaryIndex int) []ops.Operator {
	currentPrimaryIndex := cluster.Status.CurrentPrimaryIndex
	if currentPrimaryIndex != nil && *currentPrimaryIndex == newPrimaryIndex {
		return nil
	}

	return []ops.Operator{
		ops.UpdatePrimaryOp(newPrimaryIndex),
	}
}

func isCloneable(state sql.NullString) bool {
	if !state.Valid {
		return true
	}

	if state.String == moco.CloneStatusFailed {
		return true
	}

	return false
}

func isCloning(state sql.NullString) bool {
	return state.String == moco.CloneStatusNotStarted || state.String == moco.CloneStatusInProgress
}

func restoreEmptyInstance(status *accessor.MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster) []ops.Operator {
	primaryIndex := *cluster.Status.CurrentPrimaryIndex

	if status.InstanceStatus[primaryIndex].PrimaryStatus.ExecutedGtidSet == "" {
		return nil
	}

	op := make([]ops.Operator, 0)

	primaryHost := moco.GetHost(cluster, primaryIndex)
	primaryHostWithPort := fmt.Sprintf("%s:%d", primaryHost, moco.MySQLAdminPort)

	for _, s := range status.InstanceStatus {
		if !s.GlobalVariablesStatus.CloneValidDonorList.Valid || s.GlobalVariablesStatus.CloneValidDonorList.String != primaryHostWithPort {
			op = append(op, ops.SetCloneDonorListOp())
			break
		}
	}

	for i, s := range status.InstanceStatus {
		if i == primaryIndex {
			continue
		}

		if isCloneable(s.CloneStateStatus.State) && s.PrimaryStatus.ExecutedGtidSet == "" {
			op = append(op, ops.CloneOp(i))
		}
	}

	return op
}

func waitForClone(status *accessor.MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster) (bool, []int) {
	primaryIndex := *cluster.Status.CurrentPrimaryIndex
	count := 0
	var outOfSyncIns []int

	for i, is := range status.InstanceStatus {
		if i == primaryIndex {
			continue
		}

		if isCloning(is.CloneStateStatus.State) {
			count++
			outOfSyncIns = append(outOfSyncIns, i)
		}
	}

	return count > int(cluster.Spec.Replicas/2), outOfSyncIns
}

func configureReplication(status *accessor.MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster) []ops.Operator {
	primaryHost := moco.GetHost(cluster, *cluster.Status.CurrentPrimaryIndex)

	var operators []ops.Operator
	for i, is := range status.InstanceStatus {
		if i == *cluster.Status.CurrentPrimaryIndex {
			continue
		}

		if isCloning(is.CloneStateStatus.State) {
			continue
		}

		if is.ReplicaStatus == nil || is.ReplicaStatus.MasterHost != primaryHost ||
			is.ReplicaStatus.SlaveIORunning != moco.ReplicaRunConnect {
			operators = append(operators, ops.ConfigureReplicationOp(i, primaryHost))
		}
	}

	for i, is := range status.InstanceStatus {
		if i == *cluster.Status.CurrentPrimaryIndex {
			if is.Role != moco.PrimaryRole {
				operators = append(operators, ops.SetLabelsOp())
				break
			}
			continue
		}

		if is.Role != moco.ReplicaRole {
			operators = append(operators, ops.SetLabelsOp())
			break
		}
	}

	return operators
}

func waitForReplication(status *accessor.MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster) (bool, []int) {
	primaryIndex := *cluster.Status.CurrentPrimaryIndex
	primaryStatus := status.InstanceStatus[primaryIndex]

	primaryGTID := primaryStatus.PrimaryStatus.ExecutedGtidSet
	count := 0
	var outOfSyncIns []int
	for i, is := range status.InstanceStatus {
		if i == primaryIndex {
			continue
		}

		if isCloning(is.CloneStateStatus.State) {
			outOfSyncIns = append(outOfSyncIns, i)
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

	if !primaryStatus.GlobalVariablesStatus.ReadOnly {
		return false, outOfSyncIns
	}

	return count < int(cluster.Spec.Replicas/2), outOfSyncIns
}

func acceptWriteRequest(status *accessor.MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster) []ops.Operator {
	primaryIndex := *cluster.Status.CurrentPrimaryIndex

	if !status.InstanceStatus[primaryIndex].GlobalVariablesStatus.ReadOnly {
		return nil
	}
	return []ops.Operator{
		ops.TurnOffReadOnlyOp(primaryIndex)}
}

func waitForRelayLogExecution(status *accessor.MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster) ([]ops.Operator, bool) {
	if cluster.Status.CurrentPrimaryIndex == nil {
		return nil, false
	}

	primary := *cluster.Status.CurrentPrimaryIndex
	if status.InstanceStatus[primary].PrimaryStatus.ExecutedGtidSet != "" {
		return nil, false
	}

	var hasData bool
	for i := 0; i < int(cluster.Spec.Replicas); i++ {
		if i == primary {
			continue
		}
		if status.InstanceStatus[i].PrimaryStatus.ExecutedGtidSet != "" {
			hasData = true
		}
	}
	if !hasData {
		return nil, false
	}

	var op []ops.Operator
	for i := 0; i < int(cluster.Spec.Replicas); i++ {
		if i == primary {
			continue
		}
		if status.InstanceStatus[i].ReplicaStatus == nil {
			continue
		}
		if status.InstanceStatus[i].ReplicaStatus.SlaveIORunning != moco.ReplicaNotRun {
			op = append(op, ops.StopReplicaIOThread(i))
		}
	}
	if len(op) != 0 {
		return op, true
	}

	var wait bool
	for i := 0; i < int(cluster.Spec.Replicas); i++ {
		if i == primary {
			continue
		}
		if status.InstanceStatus[i].ReplicaStatus == nil {
			continue
		}
		if status.InstanceStatus[i].ReplicaStatus.RetrievedGtidSet == status.InstanceStatus[i].ReplicaStatus.ExecutedGtidSet {
			continue
		}

		wait = true
	}

	return nil, wait
}
