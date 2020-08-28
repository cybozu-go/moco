package controllers

import (
	"errors"
	"github.com/cybozu-go/moco"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"sort"
	"testing"

	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func Test_decideNextOperation(t *testing.T) {
	tests := []struct {
		name      string
		cluster   *mocov1alpha1.MySQLCluster
		status    *MySQLClusterStatus
		want      *Operation
		isWantErr bool
		wantErr   error
	}{
		{
			name: "includeUnavailableInstance",
			cluster: &mocov1alpha1.MySQLCluster{
				Spec:   prepareCRSpec(),
				Status: prepareCRStatus(nil),
			},
			status: &MySQLClusterStatus{
				InstanceStatus: []MySQLInstanceStatus{
					prepareInstanceUnavailable(),
					prepareReadOnlyInstance(),
					prepareReadOnlyInstance(),
				},
			},
			want:      nil,
			isWantErr: true,
			wantErr:   moco.ErrUnAvailableHost,
		},
		{
			name: "ConstraintsViolationWrongInstanceIsWritable",
			cluster: &mocov1alpha1.MySQLCluster{
				Spec:   prepareCRSpec(),
				Status: prepareCRStatus(intPointer(0)),
			},
			status: &MySQLClusterStatus{
				InstanceStatus: []MySQLInstanceStatus{
					prepareReadOnlyInstance(),
					prepareWritableInstance(),
					prepareReadOnlyInstance(),
				},
			},
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					{
						Type:    mocov1alpha1.ConditionViolation,
						Status:  corev1.ConditionTrue,
						Message: moco.ErrConstraintsViolation.Error(),
					},
					{
						Type:    mocov1alpha1.ConditionFailure,
						Status:  corev1.ConditionTrue,
						Message: moco.ErrConstraintsViolation.Error(),
					},
					{
						Type:    mocov1alpha1.ConditionAvailable,
						Status:  corev1.ConditionFalse,
						Message: moco.ErrConstraintsViolation.Error(),
					},
					{
						Type:    mocov1alpha1.ConditionHealthy,
						Status:  corev1.ConditionFalse,
						Message: moco.ErrConstraintsViolation.Error(),
					},
				},
			},
			isWantErr: true,
			wantErr:   moco.ErrConstraintsViolation,
		},
		{
			name: "ConstraintsViolationIncludeTwoWritableInstances",
			cluster: &mocov1alpha1.MySQLCluster{
				Spec:   prepareCRSpec(),
				Status: prepareCRStatus(nil),
			},
			status: &MySQLClusterStatus{
				InstanceStatus: []MySQLInstanceStatus{
					prepareWritableInstance(),
					prepareWritableInstance(),
					prepareReadOnlyInstance(),
				},
			},
			want: &Operation{
				Wait: false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{
					{
						Type:    mocov1alpha1.ConditionViolation,
						Status:  corev1.ConditionTrue,
						Message: moco.ErrConstraintsViolation.Error(),
					},
					{
						Type:    mocov1alpha1.ConditionFailure,
						Status:  corev1.ConditionTrue,
						Message: moco.ErrConstraintsViolation.Error(),
					},
					{
						Type:    mocov1alpha1.ConditionAvailable,
						Status:  corev1.ConditionFalse,
						Message: moco.ErrConstraintsViolation.Error(),
					},
					{
						Type:    mocov1alpha1.ConditionHealthy,
						Status:  corev1.ConditionFalse,
						Message: moco.ErrConstraintsViolation.Error(),
					},
				},
			},
			isWantErr: true,
			wantErr:   moco.ErrConstraintsViolation,
		},
		{
			name: "UpdateCurrentPrimaryIndexBeforeBootstrap",
			cluster: &mocov1alpha1.MySQLCluster{
				Spec:   prepareCRSpec(),
				Status: prepareCRStatus(nil),
			},
			status: &MySQLClusterStatus{
				InstanceStatus: []MySQLInstanceStatus{
					prepareReadOnlyInstance(),
					prepareReadOnlyInstance(),
					prepareReadOnlyInstance(),
				},
			},
			want: &Operation{
				Wait:      false,
				Operators: []Operator{&updatePrimaryOp{}},
			},
			isWantErr: false,
		},
	}
	logger := ctrl.Log.WithName("controllers").WithName("MySQLCluster")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decideNextOperation(nil, logger, tt.cluster, tt.status)
			if (err != nil) != tt.isWantErr {
				t.Errorf("decideNextOperation() error = %v, isWantErr %v", err, tt.isWantErr)
				return
			}
			if !assertOperation(got, tt.want) {
				t.Errorf("decideNextOperation() got = %v, want %v", got, tt.want)
			}
			if tt.isWantErr && !errors.Is(err, tt.wantErr) {
				t.Errorf("decideNextOperation() error = %v, wantErr %v", err, tt.wantErr)
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

func prepareWritableInstance() MySQLInstanceStatus {
	return MySQLInstanceStatus{
		Available:     true,
		PrimaryStatus: nil,
		ReplicaStatus: nil,
		GlobalVariableStatus: &MySQLGlobalVariablesStatus{
			ReadOnly:                           false,
			SuperReadOnly:                      false,
			RplSemiSyncMasterWaitForSlaveCount: 1,
		},
		CloneStateStatus: nil,
	}
}
func prepareReadOnlyInstance() MySQLInstanceStatus {
	return MySQLInstanceStatus{
		Available:     true,
		PrimaryStatus: nil,
		ReplicaStatus: nil,
		GlobalVariableStatus: &MySQLGlobalVariablesStatus{
			ReadOnly:                           true,
			SuperReadOnly:                      true,
			RplSemiSyncMasterWaitForSlaveCount: 1,
		},
		CloneStateStatus: nil,
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
