// +build old

package controllers

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"

	"github.com/cybozu-go/moco/accessor"
	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/metrics"
	"github.com/cybozu-go/moco/operators"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/event"
	"github.com/go-logr/logr"
	_ "github.com/go-sql-driver/mysql"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Operation defines operations to MySQL Cluster
type Operation struct {
	Operators      []operators.Operator
	Wait           bool
	Conditions     []mocov1beta1.MySQLClusterCondition
	SyncedReplicas *int
	Phase          constants.OperationPhase
	Event          *event.MOCOEvent
}

func getPassword(ctx context.Context, cluster *mocov1beta1.MySQLCluster, c client.Client) (*operators.MySQLPassword, error) {
	ctrlNamespace := os.Getenv(constants.PodNamespaceEnvName)
	ctrlSecretName := cluster.ControllerSecretName()
	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: ctrlNamespace, Name: ctrlSecretName}
	err := c.Get(ctx, key, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get password secret %s: %w", key.String(), err)
	}
	return operators.NewMySQLPasswordFromSecret(secret)
}

// reconcileMySQLCluster reconciles MySQL cluster
func (r *OldMySQLClusterReconciler) reconcileClustering(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) (ctrl.Result, error) {
	password, err := getPassword(ctx, cluster, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	var addrs, agentAddrs []string
	for i := 0; i < int(cluster.Spec.Replicas); i++ {
		addrs = append(addrs, fmt.Sprintf("%s:%d", cluster.PodHostname(i), constants.MySQLAdminPort))
		agentAddrs = append(agentAddrs, fmt.Sprintf("%s:%d", cluster.PodHostname(i), constants.AgentPort))
	}
	infra := accessor.NewInfrastructure(r.Client, r.AgentAccessor, r.MySQLAccessor, password.Admin(), addrs, agentAddrs)
	status, err := accessor.GetMySQLClusterStatus(ctx, log, infra, cluster)
	if err != nil {
		condErr := r.setFailureCondition(ctx, cluster, err, nil)
		if condErr != nil {
			log.Error(condErr, "unable to update status")
		}
		return ctrl.Result{}, err
	}

	op, err := decideNextOperation(log, cluster, status)
	if err != nil {
		condErr := r.setFailureCondition(ctx, cluster, err, nil)
		if condErr != nil {
			log.Error(condErr, "unable to update status")
		}
		return ctrl.Result{}, err
	}

	for _, o := range op.Operators {
		log.Info("run operation", "name", o.Name(), "description", o.Describe())
		err = o.Run(ctx, infra, cluster, status)
		if err != nil {
			condErr := r.setFailureCondition(ctx, cluster, err, nil)
			if condErr != nil {
				log.Error(condErr, "unable to update status")
			}
			if condErr == nil {
				updateMetrics(cluster, op)
			}
			return ctrl.Result{}, err
		}
	}
	if op.Event != nil {
		r.Recorder.Event(cluster, op.Event.Type, op.Event.Reason, op.Event.Message)
	}

	err = r.setMySQLClusterStatus(ctx, cluster, op.Conditions, op.SyncedReplicas)
	if err != nil {
		return ctrl.Result{}, err
	}
	updateMetrics(cluster, op)

	if op.Wait {
		log.Info("waiting")
		return ctrl.Result{RequeueAfter: r.WaitTime}, nil
	}
	if len(op.Operators) > 0 {
		return ctrl.Result{
			Requeue: true,
		}, nil
	}
	return ctrl.Result{}, nil
}

func decideNextOperation(log logr.Logger, cluster *mocov1beta1.MySQLCluster, status *accessor.MySQLClusterStatus) (*Operation, error) {
	var unavailable []int
	for i, is := range status.InstanceStatus {
		if !is.Available {
			log.Info("unavailable host exists", "index", i)
			unavailable = append(unavailable, i)
		}
	}
	if len(unavailable) > 0 {
		return &Operation{
			Conditions: unavailableCondition(nil),
			Wait:       true,
			Event:      event.EventWaitingAllInstancesAvailable.FillVariables(unavailable),
		}, nil
	}

	err := validateConstraints(status, cluster)
	if err != nil {
		return &Operation{
			Conditions: violationCondition(err),
			Event:      event.EventViolationOccurred.FillVariables(err),
		}, err
	}

	op, wait := waitForRelayLogExecution(log, status, cluster)
	if wait || len(op) != 0 {
		return &Operation{
			Operators:  op,
			Conditions: unavailableCondition(nil),
			Wait:       wait,
			Phase:      constants.PhaseWaitRelayLog,
			Event:      &event.EventWatingRelayLogExecution,
		}, nil
	}

	currentPrimary := "<nil>"
	if cluster.Status.CurrentPrimaryIndex != nil {
		currentPrimary = strconv.Itoa(*cluster.Status.CurrentPrimaryIndex)
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
			Event:      event.EventPrimaryChanged.FillVariables(currentPrimary, strconv.Itoa(primaryIndex)),
		}, nil
	}

	op = cloneFromExternal(status, cluster)
	if len(op) != 0 {
		return &Operation{
			Wait:       true,
			Operators:  op,
			Conditions: unavailableCondition(nil),
			Phase:      constants.PhaseRestoreInstance,
			Event:      &event.EventWaitingCloneFromExternal,
		}, nil
	}

	wait = waitForPrimaryClone(status, cluster)
	if wait {
		return &Operation{
			Wait:       true,
			Conditions: unavailableCondition(nil),
			Phase:      constants.PhaseRestoreInstance,
		}, nil
	}

	op = restoreEmptyInstance(status, cluster)
	if len(op) != 0 {
		var wait bool
		for _, o := range op {
			if o.Name() == operators.OperatorClone {
				wait = true
			}
		}
		return &Operation{
			Wait:      wait,
			Operators: op,
			Phase:     constants.PhaseRestoreInstance,
			Event:     &event.EventRestoringReplicaInstances,
		}, nil
	}

	wait, outOfSyncIns := waitForReplicaClone(status, cluster)
	if wait {
		return &Operation{
			Wait:       true,
			Conditions: unavailableCondition(outOfSyncIns),
			Phase:      constants.PhaseRestoreInstance,
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

	op = configureIntermediatePrimary(status, cluster)
	if len(op) != 0 {
		ev := &event.EventIntermediatePrimaryUnset
		if cluster.Spec.ReplicationSourceSecretName != nil {
			ev = event.EventIntermediatePrimaryConfigured.FillVariables(status.IntermediatePrimaryOptions.PrimaryHost)
		}
		return &Operation{
			Conditions: unavailableCondition(outOfSyncIns),
			Operators:  op,
			Event:      ev,
		}, nil
	}

	op = acceptWriteRequest(status, cluster)
	operation := &Operation{
		Operators:      op,
		Conditions:     availableCondition(outOfSyncIns),
		SyncedReplicas: &syncedReplicas,
		Phase:          constants.PhaseCompleted,
	}
	if cluster.Status.SyncedReplicas < syncedReplicas &&
		len(outOfSyncIns) == 0 {
		operation.Event = &event.EventClusteringCompletedSynced
	}
	if len(outOfSyncIns) > 0 {
		operation.Event = event.EventClusteringCompletedNotSynced.FillVariables(outOfSyncIns)
	}
	return operation, nil
}

func (r *OldMySQLClusterReconciler) setMySQLClusterStatus(ctx context.Context, cluster *mocov1beta1.MySQLCluster, conditions []mocov1beta1.MySQLClusterCondition, syncedStatus *int) error {
	cluster2 := cluster.DeepCopy()

	for _, cond := range conditions {
		if cond.Type == mocov1beta1.ConditionAvailable {
			cluster2.Status.Ready = cond.Status
		}
		setCondition(&cluster2.Status.Conditions, cond)
	}
	if syncedStatus != nil {
		cluster2.Status.SyncedReplicas = *syncedStatus
	}

	if equality.Semantic.DeepEqual(cluster, cluster2) {
		return nil
	}

	return r.Status().Update(ctx, cluster2)
}

func (r *OldMySQLClusterReconciler) setFailureCondition(ctx context.Context, cluster *mocov1beta1.MySQLCluster, e error, outOfSyncInstances []int) error {
	setCondition(&cluster.Status.Conditions, mocov1beta1.MySQLClusterCondition{
		Type:    mocov1beta1.ConditionFailure,
		Status:  corev1.ConditionTrue,
		Message: e.Error(),
	})
	setCondition(&cluster.Status.Conditions, mocov1beta1.MySQLClusterCondition{
		Type:   mocov1beta1.ConditionAvailable,
		Status: corev1.ConditionFalse,
	})
	setCondition(&cluster.Status.Conditions, mocov1beta1.MySQLClusterCondition{
		Type:   mocov1beta1.ConditionHealthy,
		Status: corev1.ConditionFalse,
	})
	if len(outOfSyncInstances) != 0 {
		msg := fmt.Sprintf("outOfSync instances: %#v", outOfSyncInstances)
		setCondition(&cluster.Status.Conditions, mocov1beta1.MySQLClusterCondition{
			Type:    mocov1beta1.ConditionOutOfSync,
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

func violationCondition(e error) []mocov1beta1.MySQLClusterCondition {
	var conditions []mocov1beta1.MySQLClusterCondition
	setCondition(&conditions, mocov1beta1.MySQLClusterCondition{
		Type:    mocov1beta1.ConditionViolation,
		Status:  corev1.ConditionTrue,
		Message: e.Error(),
	})
	setCondition(&conditions, mocov1beta1.MySQLClusterCondition{
		Type:   mocov1beta1.ConditionFailure,
		Status: corev1.ConditionTrue,
	})
	setCondition(&conditions, mocov1beta1.MySQLClusterCondition{
		Type:   mocov1beta1.ConditionAvailable,
		Status: corev1.ConditionFalse,
	})
	setCondition(&conditions, mocov1beta1.MySQLClusterCondition{
		Type:   mocov1beta1.ConditionHealthy,
		Status: corev1.ConditionFalse,
	})
	return conditions
}

func unavailableCondition(outOfSyncInstances []int) []mocov1beta1.MySQLClusterCondition {
	var conditions []mocov1beta1.MySQLClusterCondition
	if len(outOfSyncInstances) == 0 {
		setCondition(&conditions, mocov1beta1.MySQLClusterCondition{
			Type:   mocov1beta1.ConditionOutOfSync,
			Status: corev1.ConditionFalse,
		})
	} else {
		msg := fmt.Sprintf("outOfSync instances: %#v", outOfSyncInstances)
		setCondition(&conditions, mocov1beta1.MySQLClusterCondition{
			Type:    mocov1beta1.ConditionOutOfSync,
			Status:  corev1.ConditionTrue,
			Message: msg,
		})
	}
	setCondition(&conditions, mocov1beta1.MySQLClusterCondition{
		Type:   mocov1beta1.ConditionFailure,
		Status: corev1.ConditionFalse,
	})
	setCondition(&conditions, mocov1beta1.MySQLClusterCondition{
		Type:   mocov1beta1.ConditionHealthy,
		Status: corev1.ConditionFalse,
	})
	setCondition(&conditions, mocov1beta1.MySQLClusterCondition{
		Type:   mocov1beta1.ConditionAvailable,
		Status: corev1.ConditionFalse,
	})

	return conditions
}

func availableCondition(outOfSyncInstances []int) []mocov1beta1.MySQLClusterCondition {
	var conditions []mocov1beta1.MySQLClusterCondition
	if len(outOfSyncInstances) == 0 {
		setCondition(&conditions, mocov1beta1.MySQLClusterCondition{
			Type:   mocov1beta1.ConditionOutOfSync,
			Status: corev1.ConditionFalse,
		})
		setCondition(&conditions, mocov1beta1.MySQLClusterCondition{
			Type:   mocov1beta1.ConditionHealthy,
			Status: corev1.ConditionTrue,
		})
	} else {
		msg := fmt.Sprintf("outOfSync instances: %#v", outOfSyncInstances)
		setCondition(&conditions, mocov1beta1.MySQLClusterCondition{
			Type:    mocov1beta1.ConditionOutOfSync,
			Status:  corev1.ConditionTrue,
			Message: msg,
		})
		setCondition(&conditions, mocov1beta1.MySQLClusterCondition{
			Type:   mocov1beta1.ConditionHealthy,
			Status: corev1.ConditionFalse,
		})
	}
	setCondition(&conditions, mocov1beta1.MySQLClusterCondition{
		Type:   mocov1beta1.ConditionFailure,
		Status: corev1.ConditionFalse,
	})
	setCondition(&conditions, mocov1beta1.MySQLClusterCondition{
		Type:   mocov1beta1.ConditionAvailable,
		Status: corev1.ConditionTrue,
	})

	return conditions
}

func updateMetrics(cluster *mocov1beta1.MySQLCluster, op *Operation) {
	if op.Phase != "" {
		metrics.UpdateOperationPhase(cluster.Name, op.Phase)
	}

	for _, o := range op.Operators {
		if o.Name() == operators.OperatorUpdatePrimary {
			metrics.IncrementFailoverCountTotalMetrics(cluster.Name)
			break
		}
	}
	metrics.UpdateSyncedReplicasMetrics(cluster.Name, op.SyncedReplicas)

	for _, s := range cluster.Status.Conditions {
		switch s.Type {
		case mocov1beta1.ConditionViolation:
			metrics.UpdateClusterStatusViolationMetrics(cluster.Name, s.Status)
		case mocov1beta1.ConditionFailure:
			metrics.UpdateClusterStatusFailureMetrics(cluster.Name, s.Status)
		case mocov1beta1.ConditionHealthy:
			metrics.UpdateClusterStatusAvailableMetrics(cluster.Name, s.Status)
		case mocov1beta1.ConditionAvailable:
			metrics.UpdateClusterStatusAvailableMetrics(cluster.Name, s.Status)
		}
	}
}

func validateConstraints(status *accessor.MySQLClusterStatus, cluster *mocov1beta1.MySQLCluster) error {
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
		return constants.ErrConstraintsViolation
	}

	if cluster.Status.CurrentPrimaryIndex != nil && writableInstanceCounts == 1 {
		if *cluster.Status.CurrentPrimaryIndex != primaryIndex {
			return constants.ErrConstraintsViolation
		}
	}

	cond := findCondition(cluster.Status.Conditions, mocov1beta1.ConditionViolation)
	if cond != nil {
		return constants.ErrConstraintsRecovered
	}

	return nil
}

func selectPrimary(status *accessor.MySQLClusterStatus, cluster *mocov1beta1.MySQLCluster) (int, error) {
	if cluster.Status.CurrentPrimaryIndex == nil {
		return 0, nil
	}

	if !status.InstanceStatus[*cluster.Status.CurrentPrimaryIndex].GlobalVariablesStatus.ReadOnly {
		return *cluster.Status.CurrentPrimaryIndex, nil
	}

	return *status.Latest, nil
}

func updatePrimary(cluster *mocov1beta1.MySQLCluster, newPrimaryIndex int) []operators.Operator {
	currentPrimaryIndex := cluster.Status.CurrentPrimaryIndex
	if currentPrimaryIndex != nil && *currentPrimaryIndex == newPrimaryIndex {
		return nil
	}

	return []operators.Operator{
		operators.UpdatePrimaryOp(newPrimaryIndex),
	}
}

func cloneFromExternal(status *accessor.MySQLClusterStatus, cluster *mocov1beta1.MySQLCluster) []operators.Operator {
	// Do nothing if ReplicationSourceSecretName is not given
	if cluster.Spec.ReplicationSourceSecretName == nil {
		return nil
	}

	currentPrimaryIndex := cluster.Status.CurrentPrimaryIndex
	if !isCloneable(&status.InstanceStatus[*currentPrimaryIndex]) {
		return nil
	}

	externalHostWithPort := status.IntermediatePrimaryOptions.PrimaryHost + ":" + strconv.Itoa(status.IntermediatePrimaryOptions.PrimaryPort)
	return []operators.Operator{
		operators.SetCloneDonorListOp([]int{*currentPrimaryIndex}, externalHostWithPort),
		operators.CloneOp(*currentPrimaryIndex, true),
	}
}

func isCloneable(s *accessor.MySQLInstanceStatus) bool {
	if s.PrimaryStatus.ExecutedGtidSet != "" {
		return false
	}

	if !s.CloneStateStatus.State.Valid {
		return true
	}

	if s.CloneStateStatus.State.String == constants.CloneStatusFailed {
		return true
	}

	return false
}

func isCloning(state sql.NullString) bool {
	return state.String == constants.CloneStatusNotStarted || state.String == constants.CloneStatusInProgress
}

func restoreEmptyInstance(status *accessor.MySQLClusterStatus, cluster *mocov1beta1.MySQLCluster) []operators.Operator {
	primaryIndex := *cluster.Status.CurrentPrimaryIndex

	if status.InstanceStatus[primaryIndex].PrimaryStatus.ExecutedGtidSet == "" {
		return nil
	}

	op := make([]operators.Operator, 0)

	primaryHost := cluster.PodHostname(primaryIndex)
	primaryHostWithPort := fmt.Sprintf("%s:%d", primaryHost, constants.MySQLAdminPort)

	var target []int
	for i, s := range status.InstanceStatus {
		if !s.GlobalVariablesStatus.CloneValidDonorList.Valid || s.GlobalVariablesStatus.CloneValidDonorList.String != primaryHostWithPort {
			target = append(target, i)
		}
	}
	if len(target) > 0 {
		op = append(op, operators.SetCloneDonorListOp(target, primaryHostWithPort))
	}

	for i, s := range status.InstanceStatus {
		if i == primaryIndex {
			continue
		}

		if isCloneable(&s) {
			op = append(op, operators.CloneOp(i, false))
		}
	}

	return op
}

func waitForPrimaryClone(status *accessor.MySQLClusterStatus, cluster *mocov1beta1.MySQLCluster) bool {
	primaryIndex := *cluster.Status.CurrentPrimaryIndex
	return isCloning(status.InstanceStatus[primaryIndex].CloneStateStatus.State)
}

func waitForReplicaClone(status *accessor.MySQLClusterStatus, cluster *mocov1beta1.MySQLCluster) (bool, []int) {
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

func configureReplication(status *accessor.MySQLClusterStatus, cluster *mocov1beta1.MySQLCluster) []operators.Operator {
	primaryHost := cluster.PodHostname(*cluster.Status.CurrentPrimaryIndex)

	var ops []operators.Operator
	for i, is := range status.InstanceStatus {
		if i == *cluster.Status.CurrentPrimaryIndex {
			continue
		}

		if isCloning(is.CloneStateStatus.State) {
			continue
		}

		if is.ReplicaStatus == nil || is.ReplicaStatus.MasterHost != primaryHost ||
			is.ReplicaStatus.SlaveIORunning != constants.ReplicaRunConnect {
			ops = append(ops, operators.ConfigureReplicationOp(i, primaryHost))
		}
	}

	for i, is := range status.InstanceStatus {
		if i == *cluster.Status.CurrentPrimaryIndex {
			if is.Role != constants.RolePrimary {
				ops = append(ops, operators.SetRoleLabelsOp())
				break
			}
			continue
		}

		if is.Role != constants.RoleReplica {
			ops = append(ops, operators.SetRoleLabelsOp())
			break
		}
	}

	return ops
}

func waitForReplication(status *accessor.MySQLClusterStatus, cluster *mocov1beta1.MySQLCluster) (bool, []int) {
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

func acceptWriteRequest(status *accessor.MySQLClusterStatus, cluster *mocov1beta1.MySQLCluster) []operators.Operator {
	if status.IntermediatePrimaryOptions != nil {
		return nil
	}

	primaryIndex := *cluster.Status.CurrentPrimaryIndex

	if !status.InstanceStatus[primaryIndex].GlobalVariablesStatus.ReadOnly {
		return nil
	}
	return []operators.Operator{
		operators.TurnOffReadOnlyOp(primaryIndex)}
}

func configureIntermediatePrimary(status *accessor.MySQLClusterStatus, cluster *mocov1beta1.MySQLCluster) []operators.Operator {
	if cluster.Status.CurrentPrimaryIndex == nil {
		panic("unreachable code")
	}
	primary := *cluster.Status.CurrentPrimaryIndex
	options := status.IntermediatePrimaryOptions

	rs := status.InstanceStatus[primary].ReplicaStatus
	// Stop slave if ReplicationSourceSecretName has been deleted
	if cluster.Spec.ReplicationSourceSecretName == nil &&
		rs != nil &&
		(rs.SlaveIORunning != constants.ReplicaNotRun || rs.SlaveSQLRunning != constants.ReplicaNotRun) {
		return []operators.Operator{
			operators.ConfigureIntermediatePrimaryOp(primary, options),
		}
	}

	// Do nothing if ReplicationSourceSecretName is not given
	if cluster.Spec.ReplicationSourceSecretName == nil {
		return nil
	}

	// Do nothing if intermediate primary works fine
	if cluster.Spec.ReplicationSourceSecretName != nil && rs != nil &&
		rs.MasterHost == options.PrimaryHost &&
		rs.SlaveIORunning != constants.ReplicaNotRun &&
		rs.LastIoErrno == 0 {
		return nil
	}

	return []operators.Operator{
		operators.ConfigureIntermediatePrimaryOp(primary, options),
	}
}

func waitForRelayLogExecution(log logr.Logger, status *accessor.MySQLClusterStatus, cluster *mocov1beta1.MySQLCluster) ([]operators.Operator, bool) {
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

	var op []operators.Operator
	for i := 0; i < int(cluster.Spec.Replicas); i++ {
		if i == primary {
			continue
		}
		if status.InstanceStatus[i].ReplicaStatus == nil {
			continue
		}
		if status.InstanceStatus[i].ReplicaStatus.SlaveIORunning != constants.ReplicaNotRun {
			op = append(op, operators.StopReplicaIOThread(i))
		}
	}
	if len(op) != 0 {
		return op, false
	}

	var wait bool
	for i := 0; i < int(cluster.Spec.Replicas); i++ {
		if i == primary {
			continue
		}
		if status.InstanceStatus[i].ReplicaStatus == nil {
			continue
		}
		if status.InstanceStatus[i].AllRelayLogExecuted {
			continue
		}

		wait = true
	}

	return nil, wait
}
