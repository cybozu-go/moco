package runners

import (
	"time"

	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestMySQLClusterWatcher() {
	ch := make(chan event.GenericEvent)
	watcher := NewMySQLClusterWatcher(k8sClient, ch, time.Second)

}
