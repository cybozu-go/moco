package runners

import (
	"context"
	"time"

	"github.com/cybozu-go/moco"
	corev1 "k8s.io/api/core/v1"

	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// NewMySQLClusterWatcher creates new mySQLClusterWatcher
func NewMySQLClusterWatcher(client client.Client, ch chan<- event.GenericEvent, tick time.Duration) manager.Runnable {
	return &mySQLClusterWatcher{
		client:  client,
		channel: ch,
		tick:    tick,
	}
}

type mySQLClusterWatcher struct {
	client  client.Client
	channel chan<- event.GenericEvent
	tick    time.Duration
}

// Start implements Runnable.Start
func (w mySQLClusterWatcher) Start(ctx context.Context) error {
	ticker := time.NewTicker(w.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			err := w.fireEventForInitializedMySQLClusters(context.Background())
			if err != nil {
				return err
			}
		}
	}
}

func (w mySQLClusterWatcher) fireEventForInitializedMySQLClusters(ctx context.Context) error {
	clusters := mocov1alpha1.MySQLClusterList{}
	err := w.client.List(ctx, &clusters, client.MatchingFields(map[string]string{moco.InitializedClusterIndexField: string(corev1.ConditionTrue)}))
	if err != nil {
		return err
	}

	for _, cluster := range clusters.Items {
		w.channel <- event.GenericEvent{
			Object: &cluster,
		}
	}
	return nil
}
