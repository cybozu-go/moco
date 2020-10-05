package controllers

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"testing"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	ops "github.com/cybozu-go/moco/operators"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	CLUSTER     = "test-cluster"
	NAMESPACE   = "test-namespace"
	UID         = "test-uid"
	REPLICAS    = 3
	PRIMARYUUID = "3e11fa47-71ca-11e1-9e33-c80aa9429562:"
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
			name:  "It should be unavailable error when it contains unavailable instance",
			input: newTestData().withUnAvailableInstances(),
			want: &Operation{
				Wait: true,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					outOfSync(false, ""),
					failure(false, ""),
					available(false, ""),
					healthy(false, ""),
				},
			},
			wantErr: nil,
		},
		{
			name:  "It should be constraints violation error when the writable instance is wrong",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withOneWritableInstance(),
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					violation(true, moco.ErrConstraintsViolation.Error()),
					failure(true, ""),
					available(false, ""),
					healthy(false, ""),
				},
			},
			wantErr: moco.ErrConstraintsViolation,
		},
		{
			name:  "It should be constraints violation error when it includes multiple writable instances",
			input: newTestData().withTwoWritableInstances(),
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					violation(true, moco.ErrConstraintsViolation.Error()),
					failure(true, ""),
					available(false, ""),
					healthy(false, ""),
				},
			},
			wantErr: moco.ErrConstraintsViolation,
		},
		{
			name:  "It should update primary index when the primary is not yet selected",
			input: newTestData().withReadableInstances(),
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					failure(false, ""),
					outOfSync(false, ""),
					available(false, ""),
					healthy(false, ""),
				},
				Operators: []ops.Operator{ops.UpdatePrimaryOp(0)},
			},
		},
		{
			name:  "It should set clone donor list when the donor list is wrong",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withWrongDonorListInstances(),
			want: &Operation{
				Wait:      false,
				Operators: []ops.Operator{ops.SetCloneDonorListOp()},
			},
		},
		{
			name:  "It should clone when a replica instance is empty and not yet cloned",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withEmptyReplicaInstances(false),
			want: &Operation{
				Wait:      false,
				Operators: []ops.Operator{ops.SetCloneDonorListOp(), ops.CloneOp(2)},
			},
		},
		{
			name:  "It should clone when a replica instance is empty and cloning is failed",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withEmptyReplicaInstances(true),
			want: &Operation{
				Wait:      false,
				Operators: []ops.Operator{ops.SetCloneDonorListOp(), ops.CloneOp(2)},
			},
		},
		{
			name:  "It should not clone when a replica instance is NOT empty",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withNotEmptyReplicaInstances(false),
			want: &Operation{
				Wait:      false,
				Operators: []ops.Operator{ops.SetCloneDonorListOp()},
			},
		},
		{
			name:  "It should be wait for clone when the most replicas are cloning",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withMostCloningReplicaInstances(),
			want: &Operation{
				Wait: true,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					failure(false, ""),
					outOfSync(true, "outOfSync instances: []int{1, 2}"),
					available(false, ""),
					healthy(false, ""),
				},
			},
		},
		{
			name:  "It should be available when few replicas are cloning",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withFewCloningReplicaInstances(),
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					failure(false, ""),
					outOfSync(true, "outOfSync instances: []int{2}"),
					available(true, ""),
					healthy(false, ""),
				},
				SyncedReplicas: intPointer(2),
			},
		},
		{
			name:  "It should configure replications when the replication is not yet configured",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withReadableInstances(),
			want: &Operation{
				Wait: false,
				Operators: []ops.Operator{
					ops.ConfigureReplicationOp(2, hostName(0)),
					ops.ConfigureReplicationOp(2, hostName(0)),
					ops.SetLabelsOp(),
				},
			},
		},
		{
			name:  "It should set service labels when readonly instance labels are wrong",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withWrongLabelReadOnlyInstances(),
			want: &Operation{
				Wait: false,
				Operators: []ops.Operator{
					ops.SetLabelsOp(),
				},
			},
		},
		{
			name:  "It should set service labels when writable instance labels are wrong",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withWrongLabelWritableInstances(),
			want: &Operation{
				Wait: false,
				Operators: []ops.Operator{
					ops.SetLabelsOp(),
				},
			},
		},
		{
			name:  "It should wait for replication when replicas are lagged",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withLaggedReplicas(),
			want: &Operation{
				Wait: true,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					failure(false, ""),
					outOfSync(false, ""),
					available(false, ""),
					healthy(false, ""),
				},
			},
		},
		{
			name:  "It should be writable when it ready to accept write request",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withReplicas(),
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					failure(false, ""),
					outOfSync(false, ""),
					available(true, ""),
					healthy(true, ""),
				},
				Operators:      []ops.Operator{ops.TurnOffReadOnlyOp(0)},
				SyncedReplicas: intPointer(3),
			},
		},
		{
			name:  "It should be available when it contains few lagged replica",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withSyncedReplicas(2).withLaggedReplica(),
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					failure(false, ""),
					outOfSync(true, "outOfSync instances: []int{1}"),
					available(true, ""),
					healthy(false, ""),
				},
				SyncedReplicas: intPointer(2),
			},
		},
		{
			name:  "It should be healthy when all replicas are synced",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withSyncedReplicas(3).withAvailableCluster(),
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					failure(false, ""),
					outOfSync(false, ""),
					available(true, ""),
					healthy(true, ""),
				},
				SyncedReplicas: intPointer(3),
			},
		},
		{
			name:  "It should update primary index which has latest GTID set",
			input: newTestData().withCurrentPrimaryIndex(intPointer(0)).withEmptyPrimary(true),
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					failure(false, ""),
					outOfSync(false, ""),
					available(false, ""),
					healthy(false, ""),
				},
				Operators: []ops.Operator{ops.UpdatePrimaryOp(1)},
			},
		},
		{
			name:  "It should update primary index because primary is behind of others",
			input: newTestData().withCurrentPrimaryIndex(intPointer(2)).withLaggedPrimary(2),
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					failure(false, ""),
					outOfSync(false, ""),
					available(false, ""),
					healthy(false, ""),
				},
				Operators: []ops.Operator{ops.UpdatePrimaryOp(0)},
			},
		},
		{
			name:    "It should return error if there are few data replicas",
			input:   newTestData().withCurrentPrimaryIndex(intPointer(0)).withEmptyPrimary(false),
			want:    nil,
			wantErr: moco.ErrTooFewDataReplicas,
		},
		{
			name:    "It should return error if cannot performe GTID comparsion",
			input:   newTestData().withCurrentPrimaryIndex(intPointer(0)).withInconsistentGTIDs(0),
			want:    nil,
			wantErr: moco.ErrCannotCompareGITDs,
		},
	}
	logger := ctrl.Log.WithName("controllers").WithName("MySQLCluster")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decideNextOperation(logger, tt.input.ClusterResource, tt.input.ClusterStatus)

			if !assertOperation(got, tt.want) {
				sortOp(got)
				sortOp(tt.want)

				var buf bytes.Buffer
				diff := cmp.Diff(got, tt.want, cmpopts.IgnoreInterfaces(struct{ ops.Operator }{}))
				buf.WriteString(fmt.Sprintf("diff: %s\n", diff))
				buf.WriteString("got:\n")
				for _, op := range got.Operators {
					buf.WriteString(fmt.Sprintf("- %#v\n", op))
				}
				buf.WriteString("want:\n")
				for _, op := range tt.want.Operators {
					buf.WriteString(fmt.Sprintf("- %#v\n", op))
				}
				t.Errorf("decideNextOperation() diff: %s", buf.String())
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
	ClusterResource *mocov1alpha1.MySQLCluster
	ClusterStatus   *accessor.MySQLClusterStatus
}

func newTestData() testData {
	return testData{
		ClusterResource: &mocov1alpha1.MySQLCluster{
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
		ClusterStatus: nil,
	}
}

func (d testData) withUnAvailableInstances() testData {
	primaryIndex := 0
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			unavailableIns(),
			readOnlyIns(primaryIndex, ""),
			readOnlyIns(primaryIndex, ""),
		},
	}
	return d
}

func (d testData) withOneWritableInstance() testData {
	primaryIndex := 1
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			readOnlyIns(primaryIndex, moco.ReplicaRole),
			writableIns(primaryIndex, moco.PrimaryRole),
			readOnlyIns(primaryIndex, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withTwoWritableInstances() testData {
	primaryIndex := 0
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			writableIns(primaryIndex, moco.PrimaryRole),
			writableIns(primaryIndex, moco.PrimaryRole),
			readOnlyIns(primaryIndex, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withReadableInstances() testData {
	primaryIndex := 0
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			readOnlyIns(primaryIndex, ""),
			readOnlyIns(primaryIndex, ""),
			readOnlyIns(primaryIndex, ""),
		},
	}
	return d
}

func (d testData) withWrongDonorListInstances() testData {
	wrongPrimaryIndex := 1
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			writableIns(wrongPrimaryIndex, moco.PrimaryRole),
			readOnlyInsWithReplicaStatus(wrongPrimaryIndex, false, moco.ReplicaRole),
			readOnlyInsWithReplicaStatus(wrongPrimaryIndex, false, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withEmptyReplicaInstances(cloneFailed bool) testData {
	primaryIndex := 1
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			writableIns(primaryIndex, moco.PrimaryRole),
			readOnlyInsWithReplicaStatus(primaryIndex, false, moco.ReplicaRole),
			emptyIns(cloneFailed),
		},
	}
	return d
}

func (d testData) withNotEmptyReplicaInstances(cloneFailed bool) testData {
	primaryIndex := 1
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			writableIns(primaryIndex, moco.PrimaryRole),
			readOnlyInsWithReplicaStatus(primaryIndex, false, moco.ReplicaRole),
			notEmptyIns(cloneFailed),
		},
	}
	return d
}

func (d testData) withMostCloningReplicaInstances() testData {
	primaryIndex := 0
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			writableIns(primaryIndex, moco.PrimaryRole),
			cloningIns(primaryIndex),
			cloningIns(primaryIndex),
		},
	}
	return d
}

func (d testData) withFewCloningReplicaInstances() testData {
	primaryIndex := 0
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			writableIns(primaryIndex, moco.PrimaryRole),
			readOnlyInsWithReplicaStatus(primaryIndex, false, moco.ReplicaRole),
			cloningIns(primaryIndex),
		},
	}
	return d
}

func (d testData) withWrongLabelReadOnlyInstances() testData {
	primaryIndex := 0
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			writableIns(primaryIndex, moco.PrimaryRole),
			readOnlyInsWithReplicaStatus(primaryIndex, false, moco.PrimaryRole),
			readOnlyInsWithReplicaStatus(primaryIndex, false, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withWrongLabelWritableInstances() testData {
	primaryIndex := 0
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			writableIns(primaryIndex, moco.ReplicaRole),
			readOnlyInsWithReplicaStatus(primaryIndex, false, moco.ReplicaRole),
			readOnlyInsWithReplicaStatus(primaryIndex, false, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withReplicas() testData {
	primaryIndex := 0
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			readOnlyIns(primaryIndex, moco.PrimaryRole),
			readOnlyInsWithReplicaStatus(primaryIndex, false, moco.ReplicaRole),
			readOnlyInsWithReplicaStatus(primaryIndex, false, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withLaggedReplica() testData {
	primaryIndex := 0
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			writableIns(primaryIndex, moco.PrimaryRole),
			outOfSyncIns(primaryIndex),
			readOnlyInsWithReplicaStatus(primaryIndex, false, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withLaggedReplicas() testData {
	primaryIndex := 0
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			readOnlyIns(primaryIndex, moco.PrimaryRole),
			readOnlyInsWithReplicaStatus(primaryIndex, true, moco.ReplicaRole),
			readOnlyInsWithReplicaStatus(primaryIndex, true, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withCurrentPrimaryIndex(primaryIndex *int) testData {
	d.ClusterResource.Status.CurrentPrimaryIndex = primaryIndex
	return d
}

func (d testData) withSyncedReplicas(replicas int) testData {
	d.ClusterResource.Status.SyncedReplicas = replicas
	return d
}

func (d testData) withAvailableCluster() testData {
	primaryIndex := 0
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			writableIns(primaryIndex, moco.PrimaryRole),
			readOnlyInsWithReplicaStatus(primaryIndex, false, moco.ReplicaRole),
			readOnlyInsWithReplicaStatus(primaryIndex, false, moco.ReplicaRole),
		},
	}
	return d
}

func (d testData) withEmptyPrimary(synced bool) testData {
	primaryIndex := 0

	gtid := "1-5"
	if !synced {
		gtid = "1-4"
	}

	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			emptyIns(false),
			stoppedReadOnlyIns(primaryIndex, moco.ReplicaRole, "1-5", "1-5"),
			stoppedReadOnlyIns(primaryIndex, moco.ReplicaRole, gtid, gtid),
		},
	}

	return d
}

func (d testData) withLaggedPrimary(primary int) testData {
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			stoppedReadOnlyIns(primary, moco.ReplicaRole, "1-5", "1-5"),
			stoppedReadOnlyIns(primary, moco.ReplicaRole, "1-5", "1-5"),
			stoppedReadOnlyIns(primary, moco.PrimaryRole, "1-5", "1-5"),
		},
	}
	d.ClusterStatus.InstanceStatus[primary] = stoppedReadOnlyIns(primary, moco.PrimaryRole, "1-4", "1-4")
	return d
}

func (d testData) withInconsistentGTIDs(primary int) testData {
	d.ClusterStatus = &accessor.MySQLClusterStatus{
		InstanceStatus: []accessor.MySQLInstanceStatus{
			stoppedReadOnlyIns(primary, moco.ReplicaRole, "1-5", "1-5"),
			stoppedReadOnlyIns(primary, moco.ReplicaRole, "1-5", "1-5"),
			stoppedReadOnlyIns(primary, moco.ReplicaRole, "1-4", "1-4"),
		},
	}
	d.ClusterStatus.InstanceStatus[primary].Role = moco.PrimaryRole
	d.ClusterStatus.InstanceStatus[0].PrimaryStatus.ExecutedGtidSet = "dummy-source-id:1-5"
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

func emptyIns(cloneFailed bool) accessor.MySQLInstanceStatus {
	state := accessor.MySQLInstanceStatus{
		Available:     true,
		PrimaryStatus: &accessor.MySQLPrimaryStatus{},
		ReplicaStatus: nil,
		GlobalVariablesStatus: &accessor.MySQLGlobalVariablesStatus{
			ReadOnly:                           true,
			SuperReadOnly:                      true,
			RplSemiSyncMasterWaitForSlaveCount: 1,
		},
		CloneStateStatus: &accessor.MySQLCloneStateStatus{},
		Role:             moco.ReplicaRole,
	}

	if cloneFailed {
		state.CloneStateStatus.State = sql.NullString{
			Valid:  true,
			String: moco.CloneStatusFailed,
		}
	}

	return state
}

func notEmptyIns(cloneFailed bool) accessor.MySQLInstanceStatus {
	state := accessor.MySQLInstanceStatus{
		Available: true,
		PrimaryStatus: &accessor.MySQLPrimaryStatus{
			ExecutedGtidSet: PRIMARYUUID + "1",
		},
		ReplicaStatus: nil,
		GlobalVariablesStatus: &accessor.MySQLGlobalVariablesStatus{
			ReadOnly:                           true,
			SuperReadOnly:                      true,
			RplSemiSyncMasterWaitForSlaveCount: 1,
		},
		CloneStateStatus: &accessor.MySQLCloneStateStatus{},
		Role:             moco.ReplicaRole,
	}

	if cloneFailed {
		state.CloneStateStatus.State = sql.NullString{
			Valid:  true,
			String: moco.CloneStatusFailed,
		}
	}

	return state
}

func cloningIns(primaryIndex int) accessor.MySQLInstanceStatus {
	state := emptyIns(false)
	state.GlobalVariablesStatus.CloneValidDonorList = sql.NullString{
		String: fmt.Sprintf("%s:%d", hostName(primaryIndex), moco.MySQLAdminPort),
		Valid:  true,
	}
	state.CloneStateStatus = &accessor.MySQLCloneStateStatus{
		State: sql.NullString{
			String: moco.CloneStatusInProgress,
			Valid:  true,
		},
	}
	return state
}

func readOnlyIns(primaryIndex int, role string) accessor.MySQLInstanceStatus {
	state := emptyIns(false)
	state.PrimaryStatus = &accessor.MySQLPrimaryStatus{ExecutedGtidSet: PRIMARYUUID + "1-5"}
	state.GlobalVariablesStatus.RplSemiSyncMasterWaitForSlaveCount = 1
	state.GlobalVariablesStatus.CloneValidDonorList = sql.NullString{
		String: fmt.Sprintf("%s:%d", hostName(primaryIndex), moco.MySQLAdminPort),
		Valid:  true,
	}
	state.Role = role
	return state
}

func readOnlyInsWithReplicaStatus(primaryIndex int, lagged bool, role string) accessor.MySQLInstanceStatus {
	state := readOnlyIns(primaryIndex, role)
	exeGtid := "1-5"
	if lagged {
		exeGtid = "1"
	}

	state.ReplicaStatus = &accessor.MySQLReplicaStatus{
		LastIoErrno:      0,
		LastIoError:      "",
		LastSQLErrno:     0,
		LastSQLError:     "",
		MasterHost:       hostName(0),
		RetrievedGtidSet: PRIMARYUUID + "1-5",
		ExecutedGtidSet:  PRIMARYUUID + exeGtid,
		SlaveIORunning:   "Yes",
		SlaveSQLRunning:  "Yes",
	}
	return state
}

func stoppedReadOnlyIns(primaryIndex int, role string, retGtid, exeGtid string) accessor.MySQLInstanceStatus {
	state := readOnlyIns(primaryIndex, role)
	state.PrimaryStatus = &accessor.MySQLPrimaryStatus{ExecutedGtidSet: PRIMARYUUID + exeGtid}
	state.GlobalVariablesStatus.CloneValidDonorList = sql.NullString{
		String: fmt.Sprintf("%s:%d", hostName(primaryIndex), moco.MySQLAdminPort),
		Valid:  true,
	}
	state.ReplicaStatus = &accessor.MySQLReplicaStatus{
		LastIoErrno:      0,
		LastIoError:      "",
		LastSQLErrno:     0,
		LastSQLError:     "",
		MasterHost:       hostName(0),
		RetrievedGtidSet: PRIMARYUUID + retGtid,
		ExecutedGtidSet:  PRIMARYUUID + exeGtid,
		SlaveIORunning:   "No",
		SlaveSQLRunning:  "Yes",
	}
	return state
}

func writableIns(primaryIndex int, role string) accessor.MySQLInstanceStatus {
	state := readOnlyIns(primaryIndex, role)
	state.GlobalVariablesStatus.ReadOnly = false
	state.GlobalVariablesStatus.SuperReadOnly = false
	return state
}

func outOfSyncIns(primaryIndex int) accessor.MySQLInstanceStatus {
	state := readOnlyIns(primaryIndex, moco.ReplicaRole)
	primaryGTID := PRIMARYUUID + ":1-5"
	state.ReplicaStatus = &accessor.MySQLReplicaStatus{
		LastIoErrno:      1,
		LastIoError:      "",
		LastSQLErrno:     0,
		LastSQLError:     "",
		MasterHost:       hostName(0),
		RetrievedGtidSet: primaryGTID,
		ExecutedGtidSet:  primaryGTID,
		SlaveIORunning:   "Yes",
		SlaveSQLRunning:  "Yes",
	}
	return state
}

func status(s bool) corev1.ConditionStatus {
	if s {
		return corev1.ConditionTrue
	}
	return corev1.ConditionFalse
}

func violation(s bool, message string) mocov1alpha1.MySQLClusterCondition {
	return mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionViolation,
		Status:  status(s),
		Message: message,
	}
}

func available(s bool, message string) mocov1alpha1.MySQLClusterCondition {
	return mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionAvailable,
		Status:  status(s),
		Message: message,
	}
}

func failure(s bool, message string) mocov1alpha1.MySQLClusterCondition {
	return mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionFailure,
		Status:  status(s),
		Message: message,
	}
}

func healthy(s bool, message string) mocov1alpha1.MySQLClusterCondition {
	return mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionHealthy,
		Status:  status(s),
		Message: message,
	}
}

func outOfSync(s bool, message string) mocov1alpha1.MySQLClusterCondition {
	return mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionOutOfSync,
		Status:  status(s),
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

func assertOperators(expected, actual []ops.Operator) bool {
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

type Operators []ops.Operator

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
