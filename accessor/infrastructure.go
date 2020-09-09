package accessor

import (
	"context"
	"strconv"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/jmoiron/sqlx"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Infrastructure struct {
	client.Client
	MySQLAccessor DataBaseAccessor
	password      string
	hosts         []string
	port          int
}

func NewInfrastructure(cli client.Client, acc DataBaseAccessor, password string, hosts []string, port int) Infrastructure {
	return Infrastructure{
		Client:        cli,
		MySQLAccessor: acc,
		password:      password,
		hosts:         hosts,
		port:          port,
	}
}

func (i Infrastructure) GetClient() client.Client {
	return i.Client
}

func (i Infrastructure) GetDB(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, index int) (*sqlx.DB, error) {
	db, err := i.MySQLAccessor.Get(i.hosts[index]+":"+strconv.Itoa(i.port), moco.OperatorAdminUser, i.password)
	if err != nil {
		return nil, err
	}
	return db, nil
}
