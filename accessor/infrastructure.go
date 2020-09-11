package accessor

import (
	"context"
	"errors"
	"strconv"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/jmoiron/sqlx"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Infrastructure struct {
	k8sClient     client.Client
	mySQLAccessor DataBaseAccessor
	password      string
	hosts         []string
	port          int
}

func NewInfrastructure(cli client.Client, acc DataBaseAccessor, password string, hosts []string, port int) Infrastructure {
	return Infrastructure{
		k8sClient:     cli,
		mySQLAccessor: acc,
		password:      password,
		hosts:         hosts,
		port:          port,
	}
}

func (i Infrastructure) GetClient() client.Client {
	return i.k8sClient
}

func (i Infrastructure) GetDB(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, index int) (*sqlx.DB, error) {
	if len(i.hosts) <= index {
		return nil, errors.New("index is out of range")
	}

	db, err := i.mySQLAccessor.Get(i.hosts[index]+":"+strconv.Itoa(i.port), moco.OperatorAdminUser, i.password)
	if err != nil {
		return nil, err
	}
	return db, nil
}
