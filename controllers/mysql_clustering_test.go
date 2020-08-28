package controllers

import (
	"database/sql"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"testing"
)

func Test_decideNextOperation(t *testing.T) {
	type args struct {
		cluster *mocov1alpha1.MySQLCluster
		status  *MySQLClusterStatus
	}
	tests := []struct {
		name    string
		args    args
		want    *Operation
		wantErr bool
	}{
		// TODO: Add test cases.
		mocov1alpha1.MySQLClusterStatus{},
		MySQLClusterStatus{[]MySQLInstanceStatus{{
			Available: true,
			PrimaryStatus: MySQLPrimaryStatus{sql.NullString{
				"", true,
			}},
			ReplicaStatus:        nil,
			GlobalVariableStatus: nil,
			CloneStateStatus:     nil,
		}}},
	}
	logger := ctrl.Log.WithName("controllers").WithName("MySQLCluster")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decideNextOperation(nil, logger, tt.args.cluster, tt.args.status)
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
