package operators

import (
	"context"
	"fmt"

	"github.com/cybozu-go/moco/accessor"
	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
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
	return OperatorTurnOffReadOnly
}

func (o turnOffReadOnlyOp) Run(ctx context.Context, infra accessor.Infrastructure, cluster *mocov1beta1.MySQLCluster, status *accessor.MySQLClusterStatus) error {
	db, err := infra.GetDB(o.primaryIndex)
	if err != nil {
		return err
	}
	_, err = db.Exec("SET GLOBAL read_only=0")
	return err
}

func (o turnOffReadOnlyOp) Describe() string {
	return fmt.Sprintf("%#v", o)
}
