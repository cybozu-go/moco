package operators

import (
	"context"
	"fmt"

	"github.com/cybozu-go/moco/accessor"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
)

type configureIntermediatePrimaryOp struct {
	Index   int
	Options *accessor.IntermediatePrimaryOptions
}

// ConfigureIntermediatePrimaryOp returns the ConfigureIntermediatePrimaryOp Operator
func ConfigureIntermediatePrimaryOp(index int, options *accessor.IntermediatePrimaryOptions) Operator {
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

	_, err = db.Exec("STOP SLAVE")
	if err != nil {
		return err
	}
	if o.Options == nil {
		return nil
	}

	_, err = db.Exec("SET GLOBAL read_only=1")
	if err != nil {
		return err
	}
	_, err = db.NamedExec(`CHANGE MASTER TO MASTER_HOST = :MasterHost, MASTER_USER = :MasterUser, MASTER_PORT = :MasterPort, MASTER_PASSWORD = :MasterPassword, MASTER_AUTO_POSITION = 1`, *o.Options)
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
	return fmt.Sprintf("operators.%s{Index:%d, Options:<masked>}", o.Name(), o.Index)
}
