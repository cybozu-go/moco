package operators

import (
	"context"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
)

type stopReplicaIOThread struct {
	// TODO: field of target name
}

// StopReplicaIOThread returns the StopReplicaIOThread Operator
func StopReplicaIOThread() Operator {
	return &stopReplicaIOThread{}
}

func (stopReplicaIOThread) Name() string {
	return moco.OperatorStopReplicaIOThread
}

func (stopReplicaIOThread) Run(ctx context.Context, infra accessor.Infrastructure, cluster *mocov1alpha1.MySQLCluster, status *accessor.MySQLClusterStatus) error {
	// TODO: Stop IO Thread
	return nil
}
