package controllers

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/cybozu-go/moco"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func Test_decideNextOperation(t *testing.T) {
	tests := []struct {
		name    string
		cluster *mocov1alpha1.MySQLCluster
		status  *MySQLClusterStatus
		want    *Operation
		wantErr error
	}{
		{
			name: "IncludeUnavailableInstance",
			cluster: &mocov1alpha1.MySQLCluster{
				Spec:   prepareCRSpec(),
				Status: prepareCRStatus(nil),
			},
			status: &MySQLClusterStatus{
				InstanceStatus: []MySQLInstanceStatus{
					prepareInstanceUnavailable(), readOnlyIns(1), readOnlyIns(1),
				},
			},
			want:    nil,
			wantErr: moco.ErrUnAvailableHost,
		},
		{
			name: "ConstraintsViolationWrongInstanceIsWritable",
			cluster: &mocov1alpha1.MySQLCluster{
				Spec:   prepareCRSpec(),
				Status: prepareCRStatus(intPointer(0)),
			},
			status: &MySQLClusterStatus{
				InstanceStatus: []MySQLInstanceStatus{
					readOnlyIns(1), writableIns(1), readOnlyIns(1),
				},
			},
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					violation("True", moco.ErrConstraintsViolation.Error()),
					failure("True", moco.ErrConstraintsViolation.Error()),
					available("False", moco.ErrConstraintsViolation.Error()),
					healthy("False", moco.ErrConstraintsViolation.Error()),
				},
			},
			wantErr: moco.ErrConstraintsViolation,
		},
		{
			name: "ConstraintsViolationIncludeTwoWritableInstances",
			cluster: &mocov1alpha1.MySQLCluster{
				Spec:   prepareCRSpec(),
				Status: prepareCRStatus(nil),
			},
			status: &MySQLClusterStatus{
				InstanceStatus: []MySQLInstanceStatus{
					writableIns(1), writableIns(1), readOnlyIns(1),
				},
			},
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					violation("True", moco.ErrConstraintsViolation.Error()),
					failure("True", moco.ErrConstraintsViolation.Error()),
					available("False", moco.ErrConstraintsViolation.Error()),
					healthy("False", moco.ErrConstraintsViolation.Error()),
				},
			},
			wantErr: moco.ErrConstraintsViolation,
		},
		{
			name: "UpdateCurrentPrimaryIndexBeforeBootstrap",
			cluster: &mocov1alpha1.MySQLCluster{
				Spec:   prepareCRSpec(),
				Status: prepareCRStatus(nil),
			},
			status: &MySQLClusterStatus{
				InstanceStatus: []MySQLInstanceStatus{
					readOnlyIns(1), readOnlyIns(1), readOnlyIns(1),
				},
			},
			want: &Operation{
				Wait:      false,
				Operators: []Operator{&updatePrimaryOp{}},
			},
		},
		{
			name: "ConfigureReplicationInitially",
			cluster: &mocov1alpha1.MySQLCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
					UID:       "test-uid",
				},
				Spec:   prepareCRSpec(),
				Status: prepareCRStatus(intPointer(0)),
			},
			status: &MySQLClusterStatus{
				InstanceStatus: []MySQLInstanceStatus{
					readOnlyIns(1), readOnlyIns(1), readOnlyIns(1),
				},
			},
			want: &Operation{
				Wait: false,
				Operators: []Operator{
					&configureReplicationOp{
						index: 1,
						host:  "test-uid-0.test-cluster.test-namespace",
					},
					&configureReplicationOp{
						index: 2,
						host:  "test-uid-0.test-cluster.test-namespace",
					},
				},
			},
		},
	}
	logger := ctrl.Log.WithName("controllers").WithName("MySQLCluster")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decideNextOperation(context.Background(), logger, tt.cluster, tt.status)

			if !assertOperation(got, tt.want) {
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

func prepareCRSpec() mocov1alpha1.MySQLClusterSpec {
	// TODO implement
	return mocov1alpha1.MySQLClusterSpec{}
}

func prepareCRStatus(primaryIdx *int) mocov1alpha1.MySQLClusterStatus {
	// TODO implement
	return mocov1alpha1.MySQLClusterStatus{CurrentPrimaryIndex: primaryIdx}
}

func prepareInstanceUnavailable() MySQLInstanceStatus {
	return MySQLInstanceStatus{
		Available:            false,
		PrimaryStatus:        nil,
		ReplicaStatus:        nil,
		GlobalVariableStatus: nil,
		CloneStateStatus:     nil,
	}
}

func writableIns(syncWaitCount int) MySQLInstanceStatus {
	return MySQLInstanceStatus{
		Available:     true,
		PrimaryStatus: nil,
		ReplicaStatus: nil,
		GlobalVariableStatus: &MySQLGlobalVariablesStatus{
			ReadOnly:                           false,
			SuperReadOnly:                      false,
			RplSemiSyncMasterWaitForSlaveCount: syncWaitCount,
		},
		CloneStateStatus: nil,
	}
}

func readOnlyIns(syncWaitCount int) MySQLInstanceStatus {
	return MySQLInstanceStatus{
		Available:     true,
		PrimaryStatus: nil,
		ReplicaStatus: nil,
		GlobalVariableStatus: &MySQLGlobalVariablesStatus{
			ReadOnly:                           true,
			SuperReadOnly:                      true,
			RplSemiSyncMasterWaitForSlaveCount: syncWaitCount,
		},
		CloneStateStatus: nil,
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
	return assertOperators(expected.Operators, actual.Operators) && assertConditions(expected.Conditions, actual.Conditions) && expected.Wait == actual.Wait
}

func assertOperators(expected, actual []Operator) bool {
	if len(expected) == 0 && len(actual) == 0 {
		return true
	}
	expectedOpNames := make([]string, len(expected))
	actualOpNames := make([]string, len(actual))
	for i, o := range expected {
		expectedOpNames[i] = o.Name()
	}
	for i, o := range actual {
		actualOpNames[i] = o.Name()
	}
	sort.Strings(expectedOpNames)
	sort.Strings(actualOpNames)
	return cmp.Equal(expectedOpNames, actualOpNames)
}

func assertConditions(expected, actual []mocov1alpha1.MySQLClusterCondition) bool {
	if len(expected) == 0 && len(actual) == 0 {
		return true
	}

	expectedTypes, expectedStatuses, expectedReasons, expectedMessages := convConditionsToArray(expected)
	actualTypes, actualStatuses, actualReasons, actualMessages := convConditionsToArray(actual)

	return cmp.Equal(expectedTypes, actualTypes) && cmp.Equal(expectedStatuses, actualStatuses) && cmp.Equal(expectedReasons, actualReasons) && cmp.Equal(expectedMessages, actualMessages)
}

func convConditionsToArray(conditions []mocov1alpha1.MySQLClusterCondition) ([]string, []string, []string, []string) {
	types := make([]string, len(conditions))
	statuses := make([]string, len(conditions))
	reasons := make([]string, len(conditions))
	messages := make([]string, len(conditions))

	for i, c := range conditions {
		types[i] = string(c.Type)
		statuses[i] = string(c.Status)
		reasons[i] = c.Reason
		messages[i] = c.Message
	}

	sort.Strings(types)
	sort.Strings(statuses)
	sort.Strings(reasons)
	sort.Strings(messages)

	return types, statuses, reasons, messages
}

func intPointer(i int) *int {
	return &i
}
