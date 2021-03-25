package clustering

import (
	"context"
	"io"

	agent "github.com/cybozu-go/moco-agent/proto"
	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"google.golang.org/grpc"
)

// AgentConn represents a gRPC connection to a moco-agent
type AgentConn interface {
	agent.AgentClient
	io.Closer
}

type agentConn struct {
	agent.AgentClient
	*grpc.ClientConn
}

var _ AgentConn = agentConn{}

type agentFactory interface {
	New(ctx context.Context, cluster *mocov1beta1.MySQLCluster, index int) (AgentConn, error)
}

type defaultAgentFactory struct{}

var _ agentFactory = defaultAgentFactory{}

func (defaultAgentFactory) New(ctx context.Context, cluster *mocov1beta1.MySQLCluster, index int) (AgentConn, error) {
	conn, err := grpc.DialContext(ctx, cluster.PodHostname(index), grpc.WithBlock())
	if err != nil {
		return agentConn{}, err
	}
	return agentConn{
		AgentClient: agent.NewAgentClient(conn),
		ClientConn:  conn,
	}, nil
}
