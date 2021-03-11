package accessor

import (
	"errors"

	"github.com/cybozu-go/moco"
	"github.com/jmoiron/sqlx"
	"google.golang.org/grpc"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Infrastructure struct {
	k8sClient      client.Client
	agentAccessor  *AgentAccessor
	agentAddresses []string
	mySQLAccessor  DataBaseAccessor
	dbPassword     string
	dbAddresses    []string
}

func NewInfrastructure(cli client.Client, aacc *AgentAccessor, dbacc DataBaseAccessor, password string, addrs, agentAddrs []string) Infrastructure {
	return Infrastructure{
		k8sClient:      cli,
		agentAccessor:  aacc,
		mySQLAccessor:  dbacc,
		dbPassword:     password,
		dbAddresses:    addrs,
		agentAddresses: agentAddrs,
	}
}

func (i Infrastructure) GetClient() client.Client {
	return i.k8sClient
}

func (i Infrastructure) GetAgentConn(index int) (*grpc.ClientConn, error) {
	if len(i.dbAddresses) <= index {
		return nil, errors.New("index is out of range")
	}

	conn, err := i.agentAccessor.Get(i.agentAddresses[index])
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (i Infrastructure) GetDB(index int) (*sqlx.DB, error) {
	if len(i.dbAddresses) <= index {
		return nil, errors.New("index is out of range")
	}

	db, err := i.mySQLAccessor.Get(i.dbAddresses[index], moco.AdminUser, i.dbPassword)
	if err != nil {
		return nil, err
	}
	return db, nil
}
