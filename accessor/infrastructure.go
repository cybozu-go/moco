package accessor

import (
	"errors"

	"github.com/cybozu-go/moco"
	"github.com/jmoiron/sqlx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Infrastructure struct {
	k8sClient      client.Client
	agentConns     []*grpc.ClientConn
	mySQLAccessor  DataBaseAccessor
	password       string
	addresses      []string
	agentAddresses []string
}

func NewInfrastructure(cli client.Client, acc DataBaseAccessor, password string, addrs, agentAddrs []string) Infrastructure {
	return Infrastructure{
		k8sClient:      cli,
		mySQLAccessor:  acc,
		password:       password,
		addresses:      addrs,
		agentAddresses: agentAddrs,
	}
}

func (i Infrastructure) GetClient() client.Client {
	return i.k8sClient
}

func (i Infrastructure) GetAgentConn(index int) (*grpc.ClientConn, error) {
	if len(i.agentConns) != len(i.agentAddresses) {
		for range i.addresses {
			i.agentConns = append(i.agentConns, nil)
		}
	}

	if i.agentConns[index] != nil && i.agentConns[index].GetState() == connectivity.Shutdown {
		return i.agentConns[index], nil
	}

	conn, err := grpc.Dial(i.agentAddresses[index], grpc.WithInsecure())
	if err != nil {
		return nil, err
	}
	i.agentConns[index] = conn
	return i.agentConns[index], nil
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
