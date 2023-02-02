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
	Update(types.NamespacedName, string)
	UpdateNoStart(types.NamespacedName, string)
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
	stopped   bool

	wg sync.WaitGroup
}

func (m *clusterManager) Update(name types.NamespacedName, origin string) {
	m.update(name, false, origin)
}

func (m *clusterManager) UpdateNoStart(name types.NamespacedName, origin string) {
	m.update(name, true, origin)
}

func (m *clusterManager) update(name types.NamespacedName, noStart bool, origin string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stopped {
		return
	}

	key := name.String()
	p, ok := m.processes[key]
	if ok {
		p.Update(origin)
		return
	}
	if noStart {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())

	p = newManagerProcess(m.client, m.reader, m.recorder, m.dbf, m.agentf, name, cancel)
	m.wg.Add(1)
	go func() {
		p.Start(ctx, m.log.WithName(key), m.interval)
		m.wg.Done()
	}()
	m.processes[key] = p
	p.Update(origin)
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
	m.stopped = true
}
