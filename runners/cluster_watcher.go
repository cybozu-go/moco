package runners

import (
	"context"
	"time"

	"github.com/cybozu-go/moco"
	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
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
func (w mySQLClusterWatcher) Start(ch <-chan struct{}) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ch:
			return nil
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
			Meta: &metav1.ObjectMeta{
				Namespace: cluster.Namespace,
				Name:      cluster.Name,
			},
		}
	}
	return nil
}

// NeedLeaderElection implements LeaderElectionRunnable
func (w mySQLClusterWatcher) NeedLeaderElection() bool {
	return true
}
