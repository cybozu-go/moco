package clustering

import (
	"context"
	"io"
	"net"
	"strconv"
	"time"

	agent "github.com/cybozu-go/moco-agent/proto"
	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/dbop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
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

// AgentFactory represents the interface of a factory to create AgentConn
type AgentFactory interface {
	New(ctx context.Context, cluster *mocov1beta1.MySQLCluster, index int) (AgentConn, error)
}

// NewAgentFactory returns a new AgentFactory.
func NewAgentFactory(r dbop.Resolver) AgentFactory {
	return defaultAgentFactory{resolver: r}
}

type defaultAgentFactory struct {
	resolver dbop.Resolver
}

var _ AgentFactory = defaultAgentFactory{}

func (f defaultAgentFactory) New(ctx context.Context, cluster *mocov1beta1.MySQLCluster, index int) (AgentConn, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ip, err := f.resolver.Resolve(ctx, cluster, index)
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(ip, strconv.Itoa(constants.AgentPort))
	kp := keepalive.ClientParameters{
		Time: 1 * time.Minute,
	}
	conn, err := grpc.DialContext(ctx, addr, grpc.WithBlock(), grpc.WithInsecure(), grpc.WithKeepaliveParams(kp))
	if err != nil {
		return agentConn{}, err
	}
	return agentConn{
		AgentClient: agent.NewAgentClient(conn),
		ClientConn:  conn,
	}, nil
}
