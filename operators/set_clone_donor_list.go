package operators

import (
	"context"
	"fmt"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
)

type setCloneDonorListOp struct{}

// SetCloneDonorListOp returns the SetCLoneDonorListOp Operator
func SetCloneDonorListOp() Operator {
	return &setCloneDonorListOp{}
}

func (setCloneDonorListOp) Name() string {
	return moco.OperatorSetCloneDonorList
}

func (setCloneDonorListOp) Run(ctx context.Context, infra accessor.Infrastructure, cluster *mocov1alpha1.MySQLCluster, status *accessor.MySQLClusterStatus) error {
	primaryHost := moco.GetHost(cluster, *cluster.Status.CurrentPrimaryIndex)
	primaryHostWithPort := fmt.Sprintf("%s:%d", primaryHost, moco.MySQLAdminPort)

	for i := 0; i < int(cluster.Spec.Replicas); i++ {
		db, err := infra.GetDB(ctx, cluster, i)
		if err != nil {
			return err
		}

		_, err = db.Exec(`SET GLOBAL clone_valid_donor_list = ?`, primaryHostWithPort)
		if err != nil {
			return err
		}
	}

	return nil
}
