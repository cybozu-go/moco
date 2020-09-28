package operators

import (
	"context"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
)

type updatePrimaryOp struct {
	newPrimaryIndex int
}

// UpdatePrimaryOp returns the UpdatePrimaryOp Operator
func UpdatePrimaryOp(newPrimaryIndex int) Operator {
	return &updatePrimaryOp{
		newPrimaryIndex: newPrimaryIndex,
	}
}

func (o *updatePrimaryOp) Name() string {
	return moco.OperatorUpdatePrimary
}

func (o *updatePrimaryOp) Run(ctx context.Context, infra accessor.Infrastructure, cluster *mocov1alpha1.MySQLCluster, status *accessor.MySQLClusterStatus) error {
	db, err := infra.GetDB(ctx, cluster, o.newPrimaryIndex)
	if err != nil {
		return err
	}
	cluster.Status.CurrentPrimaryIndex = &o.newPrimaryIndex
	err = infra.GetClient().Status().Update(ctx, cluster)
	if err != nil {
		return err
	}

	_, err = db.Exec("SET GLOBAL rpl_semi_sync_master_enabled=ON,GLOBAL rpl_semi_sync_slave_enabled=OFF")
	if err != nil {
		return err
	}

	expectedRplSemiSyncMasterWaitForSlaveCount := int(cluster.Spec.Replicas / 2)
	st := status.InstanceStatus[o.newPrimaryIndex]
	if st.GlobalVariablesStatus.RplSemiSyncMasterWaitForSlaveCount == expectedRplSemiSyncMasterWaitForSlaveCount {
		return nil
	}
	_, err = db.Exec("SET GLOBAL rpl_semi_sync_master_wait_for_slave_count=?", expectedRplSemiSyncMasterWaitForSlaveCount)
	return err
}
