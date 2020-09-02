package accessor

import (
	"context"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/jmoiron/sqlx"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Infrastructure struct {
	client.Client
	MySQLAccessor *MySQLAccessor
	Password      string
}

func (i Infrastructure) GetClient() client.Client {
	return i.Client
}

func (i Infrastructure) GetDB(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, index int) (*sqlx.DB, error) {
	db, err := i.MySQLAccessor.Get(moco.GetHost(cluster, index), moco.OperatorAdminUser, i.Password)
	if err != nil {
		return nil, err
	}
	return db, nil
}
