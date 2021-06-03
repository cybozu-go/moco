package clustering

import (
	"context"
	"sync"
	"time"

	"github.com/cybozu-go/moco/pkg/dbop"
	"github.com/go-logr/logr"
	_ "github.com/go-sql-driver/mysql"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
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
	Update(context.Context, types.NamespacedName)
	UpdateNoStart(context.Context, types.NamespacedName)
	Stop(types.NamespacedName)
	StopAll()
}

func NewClusterManager(interval time.Duration, m manager.Manager, opf dbop.OperatorFactory, af AgentFactory, log logr.Logger) ClusterManager {
	return &clusterManager{
		client:    m.GetClient(),
		reader:    m.GetAPIReader(),
		recorder:  m.GetEventRecorderFor("moco-controller"),
		dbf:       opf,
		agentf:    af,
		interval:  interval,
		log:       log,
		processes: make(map[string]*managerProcess),
	}
}

//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;update;patch

type clusterManager struct {
	client   client.Client
	reader   client.Reader
	recorder record.EventRecorder
	dbf      dbop.OperatorFactory
	agentf   AgentFactory
	interval time.Duration
	log      logr.Logger

	mu        sync.Mutex
	processes map[string]*managerProcess

	wg sync.WaitGroup
}

func (m *clusterManager) Update(ctx context.Context, name types.NamespacedName) {
	m.update(ctx, name, false)
}

func (m *clusterManager) UpdateNoStart(ctx context.Context, name types.NamespacedName) {
	m.update(ctx, name, true)
}

func (m *clusterManager) update(ctx context.Context, name types.NamespacedName, noStart bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := name.String()
	p, ok := m.processes[key]
	if ok {
		p.Update()
		return
	}
	if noStart {
		return
	}

	ctx, cancel := context.WithCancel(ctx)

	p = newManagerProcess(m.client, m.reader, m.recorder, m.dbf, m.agentf, name, m.log.WithName(key), cancel)
	m.wg.Add(1)
	go func() {
		p.Start(ctx, m.interval)
		m.wg.Done()
	}()
	m.processes[key] = p
	p.Update()
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

func (m *clusterManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, p := range m.processes {
		p.Cancel()
	}
	m.processes = nil

	m.wg.Wait()
}
