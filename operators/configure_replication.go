package operators

import (
	"context"
	"fmt"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
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
		PrimaryPort:    moco.MySQLPort,
		ReplicatorUser: moco.ReplicatorUser,
	}
}

func (o configureReplicationOp) Name() string {
	return OperatorConfigureReplication
}

func (o configureReplicationOp) Run(ctx context.Context, infra accessor.Infrastructure, cluster *mocov1alpha1.MySQLCluster, status *accessor.MySQLClusterStatus) error {
	password, err := moco.GetPassword(ctx, cluster, infra.GetClient(), moco.ReplicationPasswordKey)
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
	_, err = db.Exec(`CHANGE MASTER TO MASTER_HOST = ?, MASTER_PORT = ?, MASTER_USER = ?, MASTER_PASSWORD = ?, MASTER_AUTO_POSITION = 1`, o.PrimaryHost, o.PrimaryPort, o.ReplicatorUser, password)
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
