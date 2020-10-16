package accessor

import (
	"errors"

	"github.com/cybozu-go/moco"
	"github.com/jmoiron/sqlx"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Infrastructure struct {
	k8sClient     client.Client
	mySQLAccessor DataBaseAccessor
	password      string
	addresses     []string
}

func NewInfrastructure(cli client.Client, acc DataBaseAccessor, password string, addrs []string) Infrastructure {
	return Infrastructure{
		k8sClient:     cli,
		mySQLAccessor: acc,
		password:      password,
		addresses:     addrs,
	}
}

func (i Infrastructure) GetClient() client.Client {
	return i.k8sClient
}

func (i Infrastructure) GetDB(index int) (*sqlx.DB, error) {
	if len(i.addresses) <= index {
		return nil, errors.New("index is out of range")
	}

	db, err := i.mySQLAccessor.Get(i.addresses[index], moco.OperatorAdminUser, i.password)
	if err != nil {
		return nil, err
	}
	return db, nil
}
