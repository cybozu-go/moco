package operators

import (
	"context"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
)

type turnOffReadOnlyOp struct {
	primaryIndex int
}

// TurnOffReadOnlyOp returns the TurnOffReadOnlyOp Operator
func TurnOffReadOnlyOp(primaryIndex int) Operator {
	return &turnOffReadOnlyOp{
		primaryIndex: primaryIndex,
	}
}

func (o turnOffReadOnlyOp) Name() string {
	return moco.OperatorTurnOffReadOnly
}

func (o turnOffReadOnlyOp) Run(ctx context.Context, infra accessor.Infrastructure, cluster *mocov1alpha1.MySQLCluster, status *accessor.MySQLClusterStatus) error {
	db, err := infra.GetDB(ctx, cluster, o.primaryIndex)
	if err != nil {
		return err
	}
	_, err = db.Exec("set global read_only=0")
	return err
}
