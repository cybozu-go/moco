package operators

import (
	"context"

	"github.com/cybozu-go/moco/accessor"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
)

type stopReplicaIOThread struct {
	Index int
}

// StopReplicaIOThread returns the StopReplicaIOThread Operator
func StopReplicaIOThread(index int) Operator {
	return &stopReplicaIOThread{
		Index: index,
	}
}

func (s stopReplicaIOThread) Name() string {
	return OperatorStopReplicaIOThread
}

func (s stopReplicaIOThread) Run(ctx context.Context, infra accessor.Infrastructure, cluster *mocov1alpha1.MySQLCluster, status *accessor.MySQLClusterStatus) error {
	db, err := infra.GetDB(ctx, cluster, s.Index)
	if err != nil {
		return err
	}

	_, err = db.Exec(`STOP SLAVE IO_THREAD`)
	if err != nil {
		return err
	}

	return nil
}
