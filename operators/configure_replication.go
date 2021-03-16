package operators

import (
	"context"
	"fmt"
	"os"

	"github.com/cybozu-go/moco/accessor"
	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type configureReplicationOp struct {
	Index          int
	PrimaryHost    string
	PrimaryPort    int
	ReplicatorUser string
}

// ConfigureReplicationOp returns the ConfigureReplicationOp Operator
func ConfigureReplicationOp(index int, primaryHost string) Operator {
	return &configureReplicationOp{
		Index:          index,
		PrimaryHost:    primaryHost,
		PrimaryPort:    constants.MySQLPort,
		ReplicatorUser: constants.ReplicationUser,
	}
}

func (o configureReplicationOp) Name() string {
	return OperatorConfigureReplication
}

func getPassword(ctx context.Context, cluster *mocov1beta1.MySQLCluster, c client.Client) (*MySQLPassword, error) {
	ctrlNamespace := os.Getenv(constants.PodNamespaceEnvName)
	ctrlSecretName := cluster.ControllerSecretName()
	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: ctrlNamespace, Name: ctrlSecretName}
	err := c.Get(ctx, key, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get password secret %s: %w", key.String(), err)
	}
	return NewMySQLPasswordFromSecret(secret)
}

func (o configureReplicationOp) Run(ctx context.Context, infra accessor.Infrastructure, cluster *mocov1beta1.MySQLCluster, status *accessor.MySQLClusterStatus) error {
	password, err := getPassword(ctx, cluster, infra.GetClient())
	if err != nil {
		return err
	}

	db, err := infra.GetDB(o.Index)
	if err != nil {
		return err
	}

	_, err = db.Exec(`STOP SLAVE`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`CHANGE MASTER TO MASTER_HOST = ?, MASTER_PORT = ?, MASTER_USER = ?, MASTER_PASSWORD = ?, MASTER_AUTO_POSITION = 1`, o.PrimaryHost, o.PrimaryPort, o.ReplicatorUser, password.Replicator())
	if err != nil {
		return err
	}
	_, err = db.Exec("SET GLOBAL rpl_semi_sync_master_enabled=OFF,GLOBAL rpl_semi_sync_slave_enabled=ON")
	if err != nil {
		return err
	}
	_, err = db.Exec(`START SLAVE`)
	return err
}

func (o configureReplicationOp) Describe() string {
	return fmt.Sprintf("%#v", o)
}
