package runners

import (
	"context"
	"time"

	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func NewMySQLClusterWatcher(client client.Client, ch chan<- event.GenericEvent) *mySQLClusterWatcher {
	return &mySQLClusterWatcher{
		client:  client,
		channel: ch,
	}
}

type mySQLClusterWatcher struct {
	client  client.Client
	channel chan<- event.GenericEvent
}

func (w mySQLClusterWatcher) Start(ch <-chan struct{}) error {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ch:
			return nil
		case <-ticker.C:
			err := w.fire(context.Background())
			if err != nil {
				//TODO
			}
		}
	}
}

func (w mySQLClusterWatcher) fire(ctx context.Context) error {
	clusters := mocov1alpha1.MySQLClusterList{}
	err := w.client.List(ctx, &clusters, client.MatchingFields(map[string]string{".status.ready": "True"}))
	if err != err {
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

func (w mySQLClusterWatcher) NeedLeaderElection() bool {
	return true
}
