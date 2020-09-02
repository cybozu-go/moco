package accessor

import (
	"context"
	"reflect"
	"testing"

	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/go-logr/logr"
)

func TestGetMySQLClusterStatus(t *testing.T) {
	type args struct {
		ctx     context.Context
		log     logr.Logger
		infra   Infrastructure
		cluster *mocov1alpha1.MySQLCluster
	}
	tests := []struct {
		name string
		args args
		want *MySQLClusterStatus
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetMySQLClusterStatus(tt.args.ctx, tt.args.log, tt.args.infra, tt.args.cluster); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetMySQLClusterStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}
