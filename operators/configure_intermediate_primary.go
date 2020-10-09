package operators

import (
	"context"
	"fmt"

	"github.com/cybozu-go/moco/accessor"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
)

type configureIntermediatePrimaryOp struct {
	Index   int
	Options map[string]string
}

// ConfigureIntermediatePrimaryOp returns the ConfigureIntermediatePrimaryOp Operator
func ConfigureIntermediatePrimaryOp(index int, options map[string]string) Operator {
	return &configureIntermediatePrimaryOp{
		Index:   index,
		Options: options,
	}
}

func (o configureIntermediatePrimaryOp) Name() string {
	return OperatorConfigureIntermediatePrimary
}

func (o configureIntermediatePrimaryOp) Run(ctx context.Context, infra accessor.Infrastructure, cluster *mocov1alpha1.MySQLCluster, status *accessor.MySQLClusterStatus) error {
	if cluster.Spec.ReplicationSourceSecretName == nil {
		panic("unreachable code")
	}

	db, err := infra.GetDB(ctx, cluster, o.Index)
	if err != nil {
		return err
	}

	query := "CHANGE MASTER TO "
	for k, v := range o.Options {
		query += fmt.Sprintf("%s = %s, ", k, v)
	}
	query = query[:len(query)-2]

	_, err = db.Exec(query)
	if err != nil {
		return err
	}
	_, err = db.Exec("SET GLOBAL rpl_semi_sync_slave_enabled=OFF")
	if err != nil {
		return err
	}
	_, err = db.Exec(`START SLAVE`)
	return err
}

func (o configureIntermediatePrimaryOp) Describe() string {
	return fmt.Sprintf("%#v", o)
}
