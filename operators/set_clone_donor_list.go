package operators

import (
	"context"
	"fmt"

	"github.com/cybozu-go/moco/accessor"
	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
)

type setCloneDonorListOp struct {
	index []int
	donar string
}

// SetCloneDonorListOp returns the SetCLoneDonorListOp Operator
func SetCloneDonorListOp(index []int, donar string) Operator {
	return &setCloneDonorListOp{index, donar}
}

func (o setCloneDonorListOp) Name() string {
	return OperatorSetCloneDonorList
}

func (o setCloneDonorListOp) Run(ctx context.Context, infra accessor.Infrastructure, cluster *mocov1beta1.MySQLCluster, status *accessor.MySQLClusterStatus) error {
	for _, i := range o.index {
		db, err := infra.GetDB(i)
		if err != nil {
			return err
		}

		_, err = db.Exec(`SET GLOBAL clone_valid_donor_list = ?`, o.donar)
		if err != nil {
			return err
		}
	}

	return nil
}

func (o setCloneDonorListOp) Describe() string {
	return fmt.Sprintf("%#v", o)
}
