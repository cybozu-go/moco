package operators

import (
	"context"
	"fmt"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	"github.com/cybozu-go/moco/agentrpc"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
)

type cloneOp struct {
	replicaIndex int
	fromExternal bool
}

// CloneOp returns the CloneOp Operator
func CloneOp(replicaIndex int, fromExternal bool) Operator {
	return &cloneOp{
		replicaIndex: replicaIndex,
		fromExternal: fromExternal,
	}
}

func (o cloneOp) Name() string {
	return OperatorClone
}

func (o cloneOp) Run(ctx context.Context, infra accessor.Infrastructure, cluster *mocov1alpha1.MySQLCluster, status *accessor.MySQLClusterStatus) error {
	req := &agentrpc.CloneRequest{
		External: o.fromExternal,
		Token:    cluster.Status.AgentToken,
	}

	if !o.fromExternal {
		primaryHost := moco.GetHost(cluster, *cluster.Status.CurrentPrimaryIndex)
		req.DonorHost = primaryHost
		req.DonorPort = moco.MySQLAdminPort
	}

	conn, err := infra.GetAgentConn(o.replicaIndex)
	if err != nil {
		return fmt.Errorf("failed to connect agent gRPC server: err=%+v", err)
	}
	cli := agentrpc.NewCloneServiceClient(conn)
	_, err = cli.Clone(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to clone: err=%+v", err)
	}

	return nil
}

func (o cloneOp) Describe() string {
	return fmt.Sprintf("%#v", o)
}
