package accessor

import (
	"context"
	"reflect"
	"testing"
	"time"

	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestGetMySQLClusterStatus(t *testing.T) {
	acc := NewMySQLAccessor(&MySQLAccessorConfig{
		ConnMaxLifeTime:   30 * time.Minute,
		ConnectionTimeout: 3 * time.Second,
		ReadTimeout:       30 * time.Second,
	})
	// port number is fixed as moco.MySQLAdminPort = 33062
	hosts := []string{"localhost"}
	password := ""
	inf := NewInfrastructure(nil, acc, password, hosts)
	cluster := mocov1alpha1.MySQLCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			ClusterName: "test-cluster",
			Namespace:   "test-namespace",
			UID:         "test-uid",
		},
		Spec: mocov1alpha1.MySQLClusterSpec{
			Replicas: 3,
		},
	}
	logger := ctrl.Log.WithName("controllers").WithName("MySQLCluster")

	GetMySQLClusterStatus(context.Background(), logger, inf, &cluster)

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
