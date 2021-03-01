package accessor

import (
	"sync"

	"google.golang.org/grpc"
)

type AgentAccessor struct {
	mu    sync.Mutex
	conns map[string]*grpc.ClientConn
}

func NewAgentAccessor() *AgentAccessor {
	return &AgentAccessor{
		conns: make(map[string]*grpc.ClientConn),
	}
}

func (acc *AgentAccessor) Get(addr string) (*grpc.ClientConn, error) {
	acc.mu.Lock()
	defer acc.mu.Unlock()

	_, ok := acc.conns[addr]
	if !ok {
		if conn, err := grpc.Dial(addr, grpc.WithInsecure()); err == nil {
			acc.conns[addr] = conn
		} else {
			return nil, err
		}
	}

	return acc.conns[addr], nil
}
