package controllers

import (
	"database/sql"
	"reflect"
	"testing"

	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func Test_decideNextOperation(t *testing.T) {
	type args struct {
		cluster *mocov1alpha1.MySQLCluster
		status  *MySQLClusterStatus
	}
	tests := []struct {
		name    string
		cluster *mocov1alpha1.MySQLCluster
		status  *MySQLClusterStatus
		want    *Operation
		wantErr bool
	}{
		{
			name: "testCaseSample",
			cluster: &mocov1alpha1.MySQLCluster{
				Spec:   prepareCRSpec(),
				Status: prepareCRStatus(nil),
			},
			status: &MySQLClusterStatus{
				InstanceStatus: []MySQLInstanceStatus{
					{
						Available: false,
						PrimaryStatus: &MySQLPrimaryStatus{
							ExecutedGtidSet: sql.NullString{"", true},
						},
						ReplicaStatus: nil,
						GlobalVariableStatus: &MySQLGlobalVariablesStatus{
							ReadOnly:                           true,
							SuperReadOnly:                      true,
							RplSemiSyncMasterWaitForSlaveCount: 1,
						},
						CloneStateStatus: nil,
					},
				},
			},
			want: &Operation{
				Wait:       false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{},
				Operators:  []Operator{},
			},
			wantErr: true,
		},
		{
			name: "includeUnavailableInstance",
			cluster: &mocov1alpha1.MySQLCluster{
				Spec:   prepareCRSpec(),
				Status: prepareCRStatus(nil),
			},
			status: &MySQLClusterStatus{
				InstanceStatus: []MySQLInstanceStatus{
					prepareInstanceUnavailable(),
				},
			},
			want: &Operation{
				Wait:       false,
				Conditions: []mocov1alpha1.MySQLClusterCondition{},
				Operators:  []Operator{},
			},
			wantErr: true,
		},
	}
	logger := ctrl.Log.WithName("controllers").WithName("MySQLCluster")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decideNextOperation(nil, logger, tt.cluster, tt.status)
			if (err != nil) != tt.wantErr {
				t.Errorf("decideNextOperation() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("decideNextOperation() got = %v, want %v", got, tt.want)
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
	return mocov1alpha1.MySQLClusterStatus{}
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
