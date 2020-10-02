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
			name: "It should be unavailable error when it contains unavailable instance",
			input: testData{
				cluster(nil),
				mySQLStatus(
					unavailableIns(),
					readOnlyIns(0, moco.ReplicaRole),
					readOnlyIns(0, moco.ReplicaRole),
				),
			},
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
			name: "It should be constraints violation error when the writable instance is wrong",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					readOnlyIns(0, moco.ReplicaRole),
					writableIns(0, moco.PrimaryRole),
					readOnlyIns(0, moco.ReplicaRole),
				),
			},
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
			name: "It should be constraints violation error when it includes multiple writable instances",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					writableIns(0, moco.PrimaryRole),
					writableIns(0, moco.PrimaryRole),
					readOnlyIns(0, moco.ReplicaRole),
				),
			},
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
			name: "It should update primary index when the primary is not yet selected",
			input: testData{
				cluster(nil),
				mySQLStatus(
					emptyIns("", false),
					emptyIns("", false),
					emptyIns("", false),
				),
			},
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
			name: "It should set clone donor list when the donor list is wrong",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					writableIns(0, moco.PrimaryRole),
					setDonorList(readOnlyIns(0, moco.ReplicaRole), 1),
					readOnlyIns(0, moco.ReplicaRole),
				),
			},
			want: &Operation{
				Wait:      false,
				Operators: []ops.Operator{ops.SetCloneDonorListOp()},
			},
		},
		{
			name: "It should clone when a replica instance is empty and not yet cloned",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					writableIns(0, moco.PrimaryRole),
					emptyIns(moco.ReplicaRole, false),
					readOnlyIns(0, moco.ReplicaRole),
				),
			},
			want: &Operation{
				Wait:      false,
				Operators: []ops.Operator{ops.SetCloneDonorListOp(), ops.CloneOp(2)},
			},
		},
		{
			name: "It should clone when a replica instance is empty and cloning is failed",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					writableIns(0, moco.PrimaryRole),
					emptyIns(moco.ReplicaRole, false),
					readOnlyIns(0, moco.ReplicaRole),
				),
			},
			want: &Operation{
				Wait:      false,
				Operators: []ops.Operator{ops.SetCloneDonorListOp(), ops.CloneOp(2)},
			},
		},
		{
			name: "It should wait for clone when the most replicas are cloning",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					setDonorList(writableIns(0, moco.PrimaryRole), 0),
					setCloneInProgress(setDonorList(emptyIns(moco.ReplicaRole, false), 0)),
					setCloneInProgress(setDonorList(emptyIns(moco.ReplicaRole, false), 0)),
				),
			},
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
			name: "It should be available when few replicas are cloning",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					writableIns(0, moco.PrimaryRole),
					setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true),
					setCloneInProgress(setDonorList(emptyIns(moco.ReplicaRole, false), 0)),
				),
			},
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
			name: "It should configure replications when the replication is not yet configured",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					readOnlyIns(0, ""),
					readOnlyIns(0, ""),
					readOnlyIns(0, ""),
				),
			},
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
			name: "It should set service labels when readonly instance labels are wrong",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					writableIns(0, moco.PrimaryRole),
					setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true),
					setReplicaStatus(readOnlyIns(0, moco.PrimaryRole), 0, true),
				),
			},
			want: &Operation{
				Wait: false,
				Operators: []ops.Operator{
					ops.SetLabelsOp(),
				},
			},
		},
		{
			name: "It should set service labels when writable instance labels are wrong",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					writableIns(0, moco.ReplicaRole),
					setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true),
					setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true),
				),
			},
			want: &Operation{
				Wait: false,
				Operators: []ops.Operator{
					ops.SetLabelsOp(),
				},
			},
		},
		{
			name: "It should wait for applying relay log when most replicas are lagged",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					emptyIns(moco.PrimaryRole, false),
					setIOThreadStopped(setGTIDLagged(setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true))),
					setIOThreadStopped(setGTIDLagged(setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true))),
				),
			},
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
			name: "It should be writable when it ready to accept write request",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					readOnlyIns(0, moco.PrimaryRole),
					setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true),
					setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true),
				),
			},
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
			name: "It should be available when it contains few outOfSync replica",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					writableIns(0, moco.PrimaryRole),
					setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true),
					setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, false),
				),
			},
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
			name: "It should be healthy when all replicas are synced",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					writableIns(0, moco.PrimaryRole),
					setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true),
					setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true),
				),
			},
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
			name: "It should update primary index which has latest GTID set",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					emptyIns(moco.PrimaryRole, false),
					setIOThreadStopped(setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true)),
					setIOThreadStopped(setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true)),
				),
			},
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
			name: "It should update primary index because primary is behind of others",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					setGTIDLagged(readOnlyIns(0, moco.PrimaryRole)),
					setIOThreadStopped(setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true)),
					setIOThreadStopped(setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true)),
				),
			},
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
			name: "It should return error if cannot performe GTID comparsion",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(
					emptyIns(moco.PrimaryRole, false),
					setIOThreadStopped(setGTIDInconsistent(setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true))),
					setIOThreadStopped(setReplicaStatus(readOnlyIns(0, moco.ReplicaRole), 0, true)),
				),
			},
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
				if got != nil {
					for _, op := range got.Operators {
						buf.WriteString(fmt.Sprintf("- %#v\n", op.Describe()))
					}
				}
				buf.WriteString("want:\n")
				if tt.want != nil {
					for _, op := range tt.want.Operators {
						buf.WriteString(fmt.Sprintf("- %#v\n", op.Describe()))
					}
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

func cluster(primary *int) *mocov1alpha1.MySQLCluster {
	return &mocov1alpha1.MySQLCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CLUSTER,
			Namespace: NAMESPACE,
			UID:       UID,
		},
		Spec: mocov1alpha1.MySQLClusterSpec{
			Replicas: REPLICAS,
		},
		Status: mocov1alpha1.MySQLClusterStatus{
			CurrentPrimaryIndex: primary,
		},
		Latest: intPointer(0),
	}
}

func mySQLStatus(ss ...accessor.MySQLInstanceStatus) *accessor.MySQLClusterStatus {
	return &accessor.MySQLClusterStatus{
		InstanceStatus: ss,
	}
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

func emptyIns(role string, cloneFailed bool) accessor.MySQLInstanceStatus {
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
		Role:             role,
	}

	if cloneFailed {
		state.CloneStateStatus.State = sql.NullString{
			Valid:  true,
			String: moco.CloneStatusFailed,
		}
	}

	return state
}

func writableIns(primaryIndex int, role string) accessor.MySQLInstanceStatus {
	state := readOnlyIns(primaryIndex, role)
	state.GlobalVariablesStatus.ReadOnly = false
	state.GlobalVariablesStatus.SuperReadOnly = false
	return state
}

func readOnlyIns(primaryIndex int, role string) accessor.MySQLInstanceStatus {
	state := emptyIns(role, false)
	state.PrimaryStatus = &accessor.MySQLPrimaryStatus{ExecutedGtidSet: PRIMARYUUID + "1-5"}
	state.GlobalVariablesStatus.RplSemiSyncMasterWaitForSlaveCount = 1
	state.GlobalVariablesStatus.CloneValidDonorList = sql.NullString{
		String: fmt.Sprintf("%s:%d", hostName(primaryIndex), moco.MySQLAdminPort),
		Valid:  true,
	}
	return state
}

func setDonorList(s accessor.MySQLInstanceStatus, primary int) accessor.MySQLInstanceStatus {
	s.GlobalVariablesStatus.CloneValidDonorList = sql.NullString{
		String: fmt.Sprintf("%s:%d", hostName(primary), moco.MySQLAdminPort),
		Valid:  true,
	}
	return s
}

func setCloneInProgress(s accessor.MySQLInstanceStatus) accessor.MySQLInstanceStatus {
	s.CloneStateStatus.State = sql.NullString{
		String: moco.CloneStatusInProgress,
		Valid:  true,
	}
	return s
}

func setReplicaStatus(s accessor.MySQLInstanceStatus, primary int, synced bool) accessor.MySQLInstanceStatus {
	var ioErrno int
	if !synced {
		ioErrno = 1
	}

	s.ReplicaStatus = &accessor.MySQLReplicaStatus{
		LastIoErrno:      ioErrno,
		LastIoError:      "",
		LastSQLErrno:     0,
		LastSQLError:     "",
		MasterHost:       hostName(primary),
		RetrievedGtidSet: PRIMARYUUID + "1-5",
		ExecutedGtidSet:  PRIMARYUUID + "1-5",
		SlaveIORunning:   "Yes",
		SlaveSQLRunning:  "Yes",
	}

	return s
}

func setGTIDLagged(s accessor.MySQLInstanceStatus) accessor.MySQLInstanceStatus {
	s.PrimaryStatus.ExecutedGtidSet = PRIMARYUUID + "1"
	if s.ReplicaStatus != nil {
		s.ReplicaStatus.ExecutedGtidSet = PRIMARYUUID + "1"
	}

	state.AllRelayLogExecuted = retGtid == exeGtid
	return s
}

func setGTIDBehind(s accessor.MySQLInstanceStatus) accessor.MySQLInstanceStatus {
	s.PrimaryStatus.ExecutedGtidSet = PRIMARYUUID + "1-4"
	if s.ReplicaStatus != nil {
		s.ReplicaStatus.ExecutedGtidSet = PRIMARYUUID + "1-4"
		s.ReplicaStatus.RetrievedGtidSet = PRIMARYUUID + "1-4"
	}

	return s
}

func setGTIDInconsistent(s accessor.MySQLInstanceStatus) accessor.MySQLInstanceStatus {
	s.PrimaryStatus.ExecutedGtidSet = "dummy-uuid:1-5"
	if s.ReplicaStatus != nil {
		s.ReplicaStatus.ExecutedGtidSet = "dummy-uuid:1-5"
		s.ReplicaStatus.RetrievedGtidSet = "dummy-uuid:1-5"
	}

	state.AllRelayLogExecuted = true
	return s
}

func setIOThreadStopped(s accessor.MySQLInstanceStatus) accessor.MySQLInstanceStatus {
	s.ReplicaStatus.SlaveIORunning = moco.ReplicaNotRun
	return s
}

func status(s bool) corev1.ConditionStatus {
	if s {
		return corev1.ConditionTrue
	}
	return corev1.ConditionFalse
}

// Functions to generate mocov1alpha1.MySQLClusterCondition

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

// Functions for assertion

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

// Functions for utilities

func hostName(index int) string {
	uniqueName := fmt.Sprintf("%s-%s", CLUSTER, UID)
	return fmt.Sprintf("%s-%d.%s.%s.svc", uniqueName, index, uniqueName, NAMESPACE)
}

func intPointer(i int) *int {
	return &i
}
