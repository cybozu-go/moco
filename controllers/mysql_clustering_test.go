package controllers

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"testing"

	"github.com/cybozu-go/moco"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cybozu-go/moco/accessor"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	CLUSTER   = "test-cluster"
	NAMESPACE = "test-namespace"
	UID       = "test-uid"
	REPLICAS  = 3
)

func TestDecideNextOperation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   testData
		want    *Operation
		wantErr error
	}{
		{
			name:    "IncludeUnavailableInstance",
			input:   newTestData().withUnAvailableInstances(),
			want:    nil,
			wantErr: moco.ErrUnavailableHost,
		},
		{
			name:  "ConstraintsViolationWrongInstanceIsWritable",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withOneWritableInstance(),
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					violation("True", moco.ErrConstraintsViolation.Error()),
					failure("True", ""),
					available("False", ""),
					healthy("False", ""),
				},
			},
			wantErr: moco.ErrConstraintsViolation,
		},
		{
			name:  "ConstraintsViolationIncludeTwoWritableInstances",
			input: newTestData().withTwoWritableInstances(),
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					violation("True", moco.ErrConstraintsViolation.Error()),
					failure("True", ""),
					available("False", ""),
					healthy("False", ""),
				},
			},
			wantErr: moco.ErrConstraintsViolation,
		},
		{
			name:  "PrimaryIsNotYetSelected",
			input: newTestData().withReadableInstances(),
			want: &Operation{
				Wait:      false,
				Operators: []Operator{&updatePrimaryOp{}},
			},
		},
		{
			name:  "DonorListIsWrong",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withWrongDonorListInstances(),
			want: &Operation{
				Wait:      false,
				Operators: []Operator{&setCloneDonorListOp{}},
			},
		},
		{
			name:  "ReplicationIsNotYetConfigured",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withReadableInstances(),
			want: &Operation{
				Wait: false,
				Operators: []Operator{
					&configureReplicationOp{
						Index:       1,
						PrimaryHost: hostName(0),
					},
					&configureReplicationOp{
						Index:       2,
						PrimaryHost: hostName(0),
					},
					&setLabelsOp{},
				},
			},
		},
		{
			name:  "ReadOnlyInstanceLabelsAreWrong",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withWrongLabelReadOnlyInstances(),
			want: &Operation{
				Wait: false,
				Operators: []Operator{
					&setLabelsOp{},
				},
			},
		},
		{
			name:  "WritableInstanceLabelsAreWrong",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withWrongLabelWritableInstances(),
			want: &Operation{
				Wait: false,
				Operators: []Operator{
					&setLabelsOp{},
				},
			},
		},
		{
			name:  "ReplicasAreLagged",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withLaggedReplicas(),
			want: &Operation{
				Wait: true,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					failure("False", ""),
					outOfSync("False", ""),
					available("False", ""),
					healthy("False", ""),
				},
			},
		},
		{
			name:  "ReadyToAcceptWriteRequestWithSyncedInstances",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withReplicas(),
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					failure("False", ""),
					outOfSync("False", ""),
					available("True", ""),
					healthy("True", ""),
				},
				Operators: []Operator{
					turnOffReadOnlyOp{
						primaryIndex: 0,
					},
				},
				SyncedReplicas: intPointer(3),
			},
		},
		{
			name:  "WorkingProperlyWithLaggedOneReplica",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withSyncedReplicas(2).withLaggedReplica(),
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					failure("False", ""),
					outOfSync("True", "outOfSync instances: []int{1}"),
					available("True", ""),
					healthy("False", ""),
				},
				SyncedReplicas: intPointer(2),
			},
		},
		{
			name:  "WorkingProperly",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withSyncedReplicas(3).withAvailableCluster(),
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					failure("False", ""),
					outOfSync("False", ""),
					available("True", ""),
					healthy("True", ""),
				},
				SyncedReplicas: intPointer(3),
			},
		},
	}
	logger := ctrl.Log.WithName("controllers").WithName("MySQLCluster")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decideNextOperation(logger, tt.input.Cluster, tt.input.Status)

			if !assertOperation(got, tt.want) {
				sortOp(got)
				sortOp(tt.want)
				diff := cmp.Diff(tt.want, got)
				t.Errorf("decideNextOperation() diff: %s", diff)
			}

			if !errors.Is(err, tt.wantErr) {
				if err != nil && tt.wantErr == nil {
					t.Errorf("decideNextOperation() error = %v, want = nil", err)
				}
				if err == nil && tt.wantErr != nil {
					t.Errorf("decideNextOperation() error = nil, want = %v", tt.wantErr)
				}
				t.Errorf("decideNextOperation() error = %v, want = %v", err, tt.wantErr)
			}
		})
	}
}

type testData struct {
	Cluster *mocov1alpha1.MySQLCluster
	Status  *accessor.MySQLClusterStatus
}

func newTestData() testData {
	return testData{
		Cluster: &mocov1alpha1.MySQLCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      CLUSTER,
				Namespace: NAMESPACE,
				UID:       UID,
			},
			Spec: mocov1alpha1.MySQLClusterSpec{
				Replicas: REPLICAS,
			},
			Status: mocov1alpha1.MySQLClusterStatus{
				CurrentPrimaryIndex: nil,
			},
		},
		Status: nil,
	}
}

func (d testData) withUnAvailableInstances() testData {
	d.Status = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			unavailableIns(), readOnlyIns(1, 0, ""), readOnlyIns(1, 0, ""),
		},
	}
	return d
}

func (d testData) withOneWritableInstance() testData {
	d.Status = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			readOnlyIns(1, 1, moco.ReplicaRole), writableIns(1, 1, moco.PrimaryRole), readOnlyIns(1, 1, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withTwoWritableInstances() testData {
	d.Status = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			writableIns(1, 0, moco.PrimaryRole), writableIns(1, 0, moco.PrimaryRole), readOnlyIns(1, 0, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withReadableInstances() testData {
	d.Status = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			readOnlyIns(1, 0, ""), readOnlyIns(1, 0, ""), readOnlyIns(1, 0, ""),
		},
	}
	return d
}

func (d testData) withWrongDonorListInstances() testData {
	d.Status = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			writableIns(1, 1, moco.PrimaryRole), readOnlyInsWithReplicaStatus(1, 1, false, moco.ReplicaRole), readOnlyInsWithReplicaStatus(1, 1, false, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withWrongLabelReadOnlyInstances() testData {
	d.Status = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			writableIns(1, 0, moco.PrimaryRole), readOnlyInsWithReplicaStatus(1, 0, false, moco.PrimaryRole), readOnlyInsWithReplicaStatus(1, 0, false, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withWrongLabelWritableInstances() testData {
	d.Status = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			writableIns(1, 0, moco.ReplicaRole), readOnlyInsWithReplicaStatus(1, 0, false, moco.ReplicaRole), readOnlyInsWithReplicaStatus(1, 0, false, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withReplicas() testData {
	d.Status = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			readOnlyIns(1, 0, moco.PrimaryRole), readOnlyInsWithReplicaStatus(1, 0, false, moco.ReplicaRole), readOnlyInsWithReplicaStatus(1, 0, false, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withLaggedReplica() testData {
	d.Status = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			writableIns(1, 0, moco.PrimaryRole), outOfSyncIns(1, 0), readOnlyInsWithReplicaStatus(1, 0, false, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withLaggedReplicas() testData {
	d.Status = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			readOnlyIns(1, 0, moco.PrimaryRole), readOnlyInsWithReplicaStatus(1, 0, true, moco.ReplicaRole), readOnlyInsWithReplicaStatus(1, 0, true, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withCurrentPrimaryIndex(primaryIndex *int) testData {
	d.Cluster.Status.CurrentPrimaryIndex = primaryIndex
	return d
}

func (d testData) withSyncedReplicas(replicas int) testData {
	d.Cluster.Status.SyncedReplicas = replicas
	return d
}

func (d testData) withAvailableCluster() testData {
	d.Status = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			writableIns(1, 0, moco.PrimaryRole), readOnlyInsWithReplicaStatus(1, 0, false, moco.ReplicaRole), readOnlyInsWithReplicaStatus(1, 0, false, moco.ReplicaRole),
		},
	}
	return d
}

func unavailableIns() accessor.MySQLInstanceStatus {
	return accessor.MySQLInstanceStatus{
		Available:             false,
		PrimaryStatus:         nil,
		ReplicaStatus:         nil,
		GlobalVariablesStatus: nil,
		CloneStateStatus:      nil,
	}
}

func writableIns(syncWaitCount, primaryIndex int, role string) accessor.MySQLInstanceStatus {
	return accessor.MySQLInstanceStatus{
		Available:     true,
		PrimaryStatus: &accessor.MySQLPrimaryStatus{ExecutedGtidSet: "3e11fa47-71ca-11e1-9e33-c80aa9429562:1-5"},
		ReplicaStatus: nil,
		GlobalVariablesStatus: &accessor.MySQLGlobalVariablesStatus{
			ReadOnly:                           false,
			SuperReadOnly:                      false,
			RplSemiSyncMasterWaitForSlaveCount: syncWaitCount,
			CloneValidDonorList: sql.NullString{
				String: fmt.Sprintf("%s:%d", hostName(primaryIndex), moco.MySQLPort),
				Valid:  true,
			},
		},
		CloneStateStatus: &accessor.MySQLCloneStateStatus{},
		Role:             role,
	}
}

func readOnlyIns(syncWaitCount, primaryIndex int, role string) accessor.MySQLInstanceStatus {
	return accessor.MySQLInstanceStatus{
		Available:     true,
		PrimaryStatus: &accessor.MySQLPrimaryStatus{ExecutedGtidSet: "3e11fa47-71ca-11e1-9e33-c80aa9429562:1-5"},
		ReplicaStatus: nil,
		GlobalVariablesStatus: &accessor.MySQLGlobalVariablesStatus{
			ReadOnly:                           true,
			SuperReadOnly:                      true,
			RplSemiSyncMasterWaitForSlaveCount: syncWaitCount,
			CloneValidDonorList: sql.NullString{
				String: fmt.Sprintf("%s:%d", hostName(primaryIndex), moco.MySQLPort),
				Valid:  true,
			},
		},
		CloneStateStatus: &accessor.MySQLCloneStateStatus{},
		Role:             role,
	}
}

func readOnlyInsWithReplicaStatus(syncWaitCount, primaryIndex int, lagged bool, role string) accessor.MySQLInstanceStatus {
	primaryUUID := "3e11fa47-71ca-11e1-9e33-c80aa9429562"
	exeGtid := "1-5"
	if lagged {
		exeGtid = "1"
	}

	return accessor.MySQLInstanceStatus{
		Available: true,
		PrimaryStatus: &accessor.MySQLPrimaryStatus{
			ExecutedGtidSet: primaryUUID + exeGtid,
		},
		ReplicaStatus: &accessor.MySQLReplicaStatus{
			LastIoErrno:      0,
			LastIoError:      "",
			LastSQLErrno:     0,
			LastSQLError:     "",
			MasterHost:       hostName(0),
			RetrievedGtidSet: primaryUUID + exeGtid,
			ExecutedGtidSet:  primaryUUID + exeGtid,
			SlaveIORunning:   "Yes",
			SlaveSQLRunning:  "Yes",
		},
		GlobalVariablesStatus: &accessor.MySQLGlobalVariablesStatus{
			ReadOnly:                           true,
			SuperReadOnly:                      true,
			RplSemiSyncMasterWaitForSlaveCount: syncWaitCount,
			CloneValidDonorList: sql.NullString{
				String: fmt.Sprintf("%s:%d", hostName(primaryIndex), moco.MySQLPort),
				Valid:  true,
			},
		},
		CloneStateStatus: &accessor.MySQLCloneStateStatus{},
		Role:             role,
	}
}

func outOfSyncIns(syncWaitCount, primaryIndex int) accessor.MySQLInstanceStatus {
	return accessor.MySQLInstanceStatus{
		Available:     true,
		PrimaryStatus: &accessor.MySQLPrimaryStatus{},
		ReplicaStatus: &accessor.MySQLReplicaStatus{
			LastIoErrno:      1,
			LastIoError:      "",
			LastSQLErrno:     0,
			LastSQLError:     "",
			MasterHost:       hostName(0),
			RetrievedGtidSet: "3e11fa47-71ca-11e1-9e33-c80aa9429562:1-5",
			ExecutedGtidSet:  "3e11fa47-71ca-11e1-9e33-c80aa9429562:1-5",
			SlaveIORunning:   "Yes",
			SlaveSQLRunning:  "Yes",
		},
		GlobalVariablesStatus: &accessor.MySQLGlobalVariablesStatus{
			ReadOnly:                           true,
			SuperReadOnly:                      true,
			RplSemiSyncMasterWaitForSlaveCount: syncWaitCount,
			CloneValidDonorList: sql.NullString{
				String: fmt.Sprintf("%s:%d", hostName(primaryIndex), moco.MySQLPort),
				Valid:  true,
			},
		},
		CloneStateStatus: &accessor.MySQLCloneStateStatus{},
		Role:             moco.ReplicaRole,
	}
}

func violation(status string, message string) mocov1alpha1.MySQLClusterCondition {
	return mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionViolation,
		Status:  corev1.ConditionStatus(status),
		Message: message,
	}
}

func available(status corev1.ConditionStatus, message string) mocov1alpha1.MySQLClusterCondition {
	return mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionAvailable,
		Status:  status,
		Message: message,
	}
}

func failure(status corev1.ConditionStatus, message string) mocov1alpha1.MySQLClusterCondition {
	return mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionFailure,
		Status:  status,
		Message: message,
	}
}

func healthy(status corev1.ConditionStatus, message string) mocov1alpha1.MySQLClusterCondition {
	return mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionHealthy,
		Status:  status,
		Message: message,
	}
}

func outOfSync(status corev1.ConditionStatus, message string) mocov1alpha1.MySQLClusterCondition {
	return mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionOutOfSync,
		Status:  status,
		Message: message,
	}
}

func assertOperation(expected, actual *Operation) bool {
	if expected == nil || actual == nil {
		return expected == nil && actual == nil
	}

	return assertOperators(expected.Operators, actual.Operators) &&
		assertConditions(expected.Conditions, actual.Conditions) &&
		expected.Wait == actual.Wait &&
		cmp.Equal(expected.SyncedReplicas, actual.SyncedReplicas)
}

func assertOperators(expected, actual []Operator) bool {
	if len(expected) != len(actual) {
		return false
	}
	sort.Sort(Operators(expected))
	sort.Sort(Operators(actual))
	for i := range expected {
		if expected[i].Name() != actual[i].Name() {
			return false
		}
	}
	return true
}

func assertConditions(expected, actual []mocov1alpha1.MySQLClusterCondition) bool {
	if len(expected) != len(actual) {
		return false
	}
	sort.Sort(Conditions(expected))
	sort.Sort(Conditions(actual))
	for i := range expected {
		if !equalCondition(expected[i], actual[i]) {
			return false
		}
	}
	return true
}

func equalCondition(cond1, cond2 mocov1alpha1.MySQLClusterCondition) bool {
	return cond1.Type == cond2.Type && cond1.Status == cond2.Status && cond1.Message == cond2.Message && cond1.Reason == cond2.Reason
}

func hostName(index int) string {
	uniqueName := fmt.Sprintf("%s-%s", CLUSTER, UID)
	return fmt.Sprintf("%s-%d.%s.%s.svc", uniqueName, index, uniqueName, NAMESPACE)
}

func intPointer(i int) *int {
	return &i
}

type Operators []Operator

func (o Operators) Len() int {
	return len(o)
}

func (o Operators) Less(i, j int) bool {
	return o[i].Name() < o[j].Name()
}

func (o Operators) Swap(i, j int) {
	o[i], o[j] = o[j], o[i]
}

type Conditions []mocov1alpha1.MySQLClusterCondition

func (c Conditions) Len() int {
	return len(c)
}

func (c Conditions) Less(i, j int) bool {
	return c[i].Type < c[j].Type
}

func (c Conditions) Swap(i, j int) {
	c[i], c[j] = c[j], c[i]
}

func sortOp(op *Operation) {
	if op != nil {
		sort.Sort(Operators(op.Operators))
		sort.Sort(Conditions(op.Conditions))
	}
}
