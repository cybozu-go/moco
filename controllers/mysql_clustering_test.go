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

var intermediatePrimaryOptions = accessor.IntermediatePrimaryOptions{
	MasterHost:     "intermediate-master-host",
	MasterPort:     3306,
	MasterPassword: "intermediate-password",
	MasterUser:     moco.ReplicatorUser,
}

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
				mySQLStatus(intPointer(0), nil,
					unavailableIns().build(),
					readOnlyIns(0, moco.ReplicaRole).build(),
					readOnlyIns(0, moco.ReplicaRole).build(),
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
				mySQLStatus(intPointer(0), nil,
					readOnlyIns(0, moco.ReplicaRole).build(),
					writableIns(0, moco.PrimaryRole).build(),
					readOnlyIns(0, moco.ReplicaRole).build(),
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
				mySQLStatus(intPointer(0), nil,
					writableIns(0, moco.PrimaryRole).build(),
					writableIns(0, moco.PrimaryRole).build(),
					readOnlyIns(0, moco.ReplicaRole).build(),
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
				mySQLStatus(intPointer(0), nil,
					emptyIns("", false).build(),
					emptyIns("", false).build(),
					emptyIns("", false).build(),
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
				mySQLStatus(intPointer(0), nil,
					writableIns(0, moco.PrimaryRole).build(),
					readOnlyIns(0, moco.ReplicaRole).setDonorList(1).build(),
					readOnlyIns(0, moco.ReplicaRole).build(),
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
				mySQLStatus(intPointer(0), nil,
					writableIns(0, moco.PrimaryRole).build(),
					emptyIns(moco.ReplicaRole, false).build(),
					readOnlyIns(0, moco.ReplicaRole).build(),
				),
			},
			want: &Operation{
				Wait:      false,
				Operators: []ops.Operator{ops.SetCloneDonorListOp(), ops.CloneOp(1)},
			},
		},
		{
			name: "It should clone when a replica instance is empty and cloning is failed",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(intPointer(0), nil,
					writableIns(0, moco.PrimaryRole).build(),
					emptyIns(moco.ReplicaRole, false).build(),
					readOnlyIns(0, moco.ReplicaRole).build(),
				),
			},
			want: &Operation{
				Wait:      false,
				Operators: []ops.Operator{ops.SetCloneDonorListOp(), ops.CloneOp(1)},
			},
		},
		{
			name: "It should wait for clone when the most replicas are cloning",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(intPointer(0), nil,
					writableIns(0, moco.PrimaryRole).setDonorList(0).build(),
					emptyIns(moco.ReplicaRole, false).setDonorList(0).setCloneInProgress().build(),
					emptyIns(moco.ReplicaRole, false).setDonorList(0).setCloneInProgress().build(),
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
				mySQLStatus(intPointer(0), nil,
					writableIns(0, moco.PrimaryRole).build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().build(),
					emptyIns(moco.ReplicaRole, false).setDonorList(0).setCloneInProgress().build(),
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
				mySQLStatus(intPointer(0), nil,
					readOnlyIns(0, "").build(),
					readOnlyIns(0, "").build(),
					readOnlyIns(0, "").build(),
				),
			},
			want: &Operation{
				Wait: false,
				Operators: []ops.Operator{
					ops.ConfigureReplicationOp(1, hostName(0)),
					ops.ConfigureReplicationOp(2, hostName(0)),
					ops.SetLabelsOp(),
				},
			},
		},
		{
			name: "It should set service labels when readonly instance labels are wrong",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(intPointer(0), nil,
					writableIns(0, moco.PrimaryRole).build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().build(),
					readOnlyIns(0, moco.PrimaryRole).setReplicaStatus().build(),
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
				mySQLStatus(intPointer(0), nil,
					writableIns(0, moco.ReplicaRole).build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().build(),
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
				mySQLStatus(intPointer(1), nil,
					emptyIns(moco.PrimaryRole, false).build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().setGTIDLagged().setIOThreadStopped().build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().setGTIDLagged().setIOThreadStopped().build(),
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
				mySQLStatus(intPointer(0), nil,
					readOnlyIns(0, moco.PrimaryRole).build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().build(),
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
				mySQLStatus(intPointer(0), nil,
					writableIns(0, moco.PrimaryRole).build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().setIOError().build(),
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
				mySQLStatus(intPointer(0), nil,
					writableIns(0, moco.PrimaryRole).build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().build(),
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
				mySQLStatus(intPointer(1), nil,
					emptyIns(moco.PrimaryRole, false).build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().setIOThreadStopped().build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().setIOThreadStopped().build(),
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
				mySQLStatus(intPointer(1), nil,
					readOnlyIns(0, moco.PrimaryRole).setGTIDBehind().build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().setIOThreadStopped().build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().setIOThreadStopped().build(),
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
			name: "It should return error if cannot performe GTID comparsion",
			input: testData{
				cluster(intPointer(0)),
				mySQLStatus(nil, nil,
					emptyIns(moco.PrimaryRole, false).build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().setIOThreadStopped().setGTIDInconsistent().build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().setIOThreadStopped().build(),
				),
			},
			want:    nil,
			wantErr: moco.ErrCannotCompareGITDs,
		},
		{
			name: "It should configure intermediate primary",
			input: testData{
				intermediate(cluster(intPointer(0))),
				mySQLStatus(intPointer(0), &intermediatePrimaryOptions,
					readOnlyIns(0, moco.PrimaryRole).build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().build(),
				),
			},
			want: &Operation{
				Operators: []ops.Operator{
					ops.ConfigureIntermediatePrimaryOp(0, &intermediatePrimaryOptions),
				},
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
			name: "It should be healthy when intermediate primary mode works fine",
			input: testData{
				intermediate(cluster(intPointer(0))),
				mySQLStatus(intPointer(0), &intermediatePrimaryOptions,
					readOnlyIns(0, moco.PrimaryRole).setReplicaStatus().setIntermediate(&intermediatePrimaryOptions).build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().build(),
					readOnlyIns(0, moco.ReplicaRole).setReplicaStatus().build(),
				),
			},
			want: &Operation{
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					failure(false, ""),
					outOfSync(false, ""),
					available(true, ""),
					healthy(true, ""),
				},
				SyncedReplicas: intPointer(3),
			},
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
				opts := cmp.Options{cmpopts.IgnoreFields(mocov1alpha1.MySQLClusterCondition{}, "LastTransitionTime"), cmpopts.IgnoreInterfaces(struct{ ops.Operator }{})}
				if !cmp.Equal(got, tt.want, opts) {
					buf.WriteString("\n" + cmp.Diff(got, tt.want, opts))
				}
				buf.WriteString("\nOperators:\ngot:\n")
				if got != nil {
					for _, op := range got.Operators {
						buf.WriteString(fmt.Sprintf("- %#v\n", op.Describe()))
					}
				}
				if got == nil || len(got.Operators) == 0 {
					buf.WriteString("  <empty>\n")
				}
				buf.WriteString("want:\n")
				if tt.want != nil {
					for _, op := range tt.want.Operators {
						buf.WriteString(fmt.Sprintf("- %#v\n", op.Describe()))
					}
				}
				if tt.want == nil || len(tt.want.Operators) == 0 {
					buf.WriteString("  <empty>\n")
				}
				t.Errorf("%s", buf.String())
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want = %v", err, tt.wantErr)
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
	}
}

func intermediate(c *mocov1alpha1.MySQLCluster) *mocov1alpha1.MySQLCluster {
	secret := "dummy-secret"
	c.Spec.ReplicationSourceSecretName = &secret
	return c
}

type mySQLStatusBuilder struct {
	primary int
	status  accessor.MySQLInstanceStatus
}

func mySQLStatus(latest *int, intermediatePrimaryOptions *accessor.IntermediatePrimaryOptions, ss ...accessor.MySQLInstanceStatus) *accessor.MySQLClusterStatus {
	return &accessor.MySQLClusterStatus{
		InstanceStatus:             ss,
		Latest:                     latest,
		IntermediatePrimaryOptions: intermediatePrimaryOptions,
	}
}

func unavailableIns() *mySQLStatusBuilder {
	return &mySQLStatusBuilder{
		primary: 0,
		status: accessor.MySQLInstanceStatus{
			Available:             false,
			PrimaryStatus:         nil,
			ReplicaStatus:         nil,
			GlobalVariablesStatus: nil,
			CloneStateStatus:      nil,
		},
	}
}

func emptyIns(role string, cloneFailed bool) *mySQLStatusBuilder {
	status := accessor.MySQLInstanceStatus{
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
		status.CloneStateStatus.State = sql.NullString{
			Valid:  true,
			String: moco.CloneStatusFailed,
		}
	}

	return &mySQLStatusBuilder{
		primary: 0,
		status:  status,
	}
}

func writableIns(primaryIndex int, role string) *mySQLStatusBuilder {
	b := readOnlyIns(primaryIndex, role)
	b.status.GlobalVariablesStatus.ReadOnly = false
	b.status.GlobalVariablesStatus.SuperReadOnly = false

	return b
}

func readOnlyIns(primaryIndex int, role string) *mySQLStatusBuilder {
	b := emptyIns(role, false)
	b.status.PrimaryStatus = &accessor.MySQLPrimaryStatus{ExecutedGtidSet: PRIMARYUUID + "1-5"}
	b.status.GlobalVariablesStatus.RplSemiSyncMasterWaitForSlaveCount = 1
	b.status.GlobalVariablesStatus.CloneValidDonorList = sql.NullString{
		String: fmt.Sprintf("%s:%d", hostName(primaryIndex), moco.MySQLAdminPort),
		Valid:  true,
	}

	return b
}

func (b *mySQLStatusBuilder) build() accessor.MySQLInstanceStatus {
	return b.status
}

func (b *mySQLStatusBuilder) setDonorList(donor int) *mySQLStatusBuilder {
	b.status.GlobalVariablesStatus.CloneValidDonorList = sql.NullString{
		String: fmt.Sprintf("%s:%d", hostName(donor), moco.MySQLAdminPort),
		Valid:  true,
	}
	return b
}

func (b *mySQLStatusBuilder) setCloneInProgress() *mySQLStatusBuilder {
	b.status.CloneStateStatus.State = sql.NullString{
		String: moco.CloneStatusInProgress,
		Valid:  true,
	}
	return b
}

func (b *mySQLStatusBuilder) setReplicaStatus() *mySQLStatusBuilder {
	b.status.ReplicaStatus = &accessor.MySQLReplicaStatus{
		LastIoErrno:      0,
		LastIoError:      "",
		LastSQLErrno:     0,
		LastSQLError:     "",
		MasterHost:       hostName(b.primary),
		RetrievedGtidSet: PRIMARYUUID + "1-5",
		ExecutedGtidSet:  PRIMARYUUID + "1-5",
		SlaveIORunning:   "Yes",
		SlaveSQLRunning:  "Yes",
	}
	b.status.AllRelayLogExecuted = true

	return b
}

func (b *mySQLStatusBuilder) setIOError() *mySQLStatusBuilder {
	b.status.ReplicaStatus.LastIoErrno = 1
	return b
}

func (b *mySQLStatusBuilder) setGTIDLagged() *mySQLStatusBuilder {
	b.status.PrimaryStatus.ExecutedGtidSet = PRIMARYUUID + "1"
	if b.status.ReplicaStatus != nil {
		b.status.ReplicaStatus.ExecutedGtidSet = PRIMARYUUID + "1"
	}

	b.status.AllRelayLogExecuted = false
	return b
}

func (b *mySQLStatusBuilder) setGTIDBehind() *mySQLStatusBuilder {
	b.status.PrimaryStatus.ExecutedGtidSet = PRIMARYUUID + "1-4"
	if b.status.ReplicaStatus != nil {
		b.status.ReplicaStatus.ExecutedGtidSet = PRIMARYUUID + "1-4"
		b.status.ReplicaStatus.RetrievedGtidSet = PRIMARYUUID + "1-4"
	}

	b.status.AllRelayLogExecuted = true
	return b
}

func (b *mySQLStatusBuilder) setGTIDInconsistent() *mySQLStatusBuilder {
	b.status.PrimaryStatus.ExecutedGtidSet = "dummy-uuid:1-5"
	if b.status.ReplicaStatus != nil {
		b.status.ReplicaStatus.ExecutedGtidSet = "dummy-uuid:1-5"
		b.status.ReplicaStatus.RetrievedGtidSet = "dummy-uuid:1-5"
	}

	b.status.AllRelayLogExecuted = true
	return b
}

func (b *mySQLStatusBuilder) setIOThreadStopped() *mySQLStatusBuilder {
	b.status.ReplicaStatus.SlaveIORunning = moco.ReplicaNotRun
	return b
}

func (b *mySQLStatusBuilder) setIntermediate(options *accessor.IntermediatePrimaryOptions) *mySQLStatusBuilder {
	b.status.ReplicaStatus.MasterHost = options.MasterHost
	b.status.ReplicaStatus.SlaveIORunning = moco.ReplicaRunConnect
	b.status.ReplicaStatus.LastErrno = 0
	return b
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
		if expected[i].Describe() != actual[i].Describe() {
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
