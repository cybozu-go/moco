package operators

import (
	"context"
	"sync"
	"time"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ClusterManager represents the interface to manage goroutines
// to maintain MySQL clusters.
//
// A goroutine for a MySQLCluster is started when `Update` method
// is called for the first time, and stops when `Stop` is called.
// Internally, context.Context is used to stop the goroutine.
//
// This interface is meant to be used by MySQLClusterReconciler.
type ClusterManager interface {
	Update(context.Context, *mocov1beta1.MySQLCluster)
	Stop(types.NamespacedName)
}

func NewClusterManager(interval time.Duration, client client.Client, log logr.Logger) ClusterManager {
	return &clusterManager{
		client:    client,
		interval:  interval,
		log:       log,
		processes: make(map[string]*ManagerProcess),
	}
}

//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;update

type clusterManager struct {
	client   client.Client
	interval time.Duration
	log      logr.Logger

	mu        sync.Mutex
	processes map[string]*ManagerProcess
}

func (m *clusterManager) Update(ctx context.Context, cluster *mocov1beta1.MySQLCluster) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := client.ObjectKeyFromObject(cluster)
	key := name.String()
	p, ok := m.processes[key]
	if ok {
		p.Update()
		return
	}

	ctx, cancel := context.WithCancel(ctx)

	p = &ManagerProcess{
		client: m.client,
		name:   name,
		log:    m.log.WithName(key),
		ch:     make(chan struct{}, 1),
		cancel: cancel,
	}
	go p.Start(ctx, m.interval)
	m.processes[key] = p
}

func (m *clusterManager) Stop(name types.NamespacedName) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := name.String()
	p, ok := m.processes[key]
	if ok {
		p.Cancel()
		delete(m.processes, key)
	}
}

type ManagerProcess struct {
	client client.Client
	name   types.NamespacedName
	log    logr.Logger
	ch     chan struct{}
	cancel func()
}

func (p *ManagerProcess) Update() {
	select {
	case p.ch <- struct{}{}:
	default:
	}
}

func (p *ManagerProcess) Cancel() {
	p.cancel()
}

func (p *ManagerProcess) Start(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()

	for {
		select {
		case <-p.ch:
			p.do(ctx)
		case <-tick.C:
			p.do(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (p *ManagerProcess) do(ctx context.Context) {
}
