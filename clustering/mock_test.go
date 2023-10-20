package clustering

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	agent "github.com/cybozu-go/moco-agent/proto"
	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/dbop"
	"github.com/cybozu-go/moco/pkg/password"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var testGTIDLock sync.Mutex

var testGTIDMap map[string]string

func resetGTIDMap() {
	testGTIDLock.Lock()
	testGTIDMap = map[string]string{
		"external": "ex:1,ex:2,ex:3,ex:4",
	}
	testGTIDLock.Unlock()
}

func testSetGTID(host, gtid string) {
	testGTIDLock.Lock()
	testGTIDMap[host] = gtid
	testGTIDLock.Unlock()
}

func testGetGTID(host string) (string, bool) {
	testGTIDLock.Lock()
	defer testGTIDLock.Unlock()

	gtid, ok := testGTIDMap[host]
	return gtid, ok
}

type mockAgentConn struct {
	orphaned *int64
	hostname string
}

var _ AgentConn = &mockAgentConn{}

func (a *mockAgentConn) Close() error {
	atomic.AddInt64(a.orphaned, -1)
	return nil
}

func (a *mockAgentConn) Clone(ctx context.Context, in *agent.CloneRequest, opts ...grpc.CallOption) (*agent.CloneResponse, error) {
	gtid, ok := testGetGTID(in.Host)
	if !ok {
		return nil, fmt.Errorf("host not found: %s", in.Host)
	}
	var validPasswd string
	switch in.User {
	case "external-donor":
		validPasswd = "p1"
	case "moco-clone-donor":
		validPasswd = mysqlPassword.Donor()
	default:
		return nil, fmt.Errorf("bad donor %s", in.User)
	}
	if in.Password != validPasswd {
		return nil, fmt.Errorf("authentication failed: bad password for %s", in.User)
	}

	var validInitPasswd string
	switch in.InitUser {
	case "external-init":
		validInitPasswd = "init"
	case "moco-admin":
		validInitPasswd = mysqlPassword.Admin()
	default:
		return nil, fmt.Errorf("bad init user: %s", in.InitUser)
	}
	if in.InitPassword != validInitPasswd {
		return nil, fmt.Errorf("authentication failed: bad password for %s", in.InitUser)
	}

	testSetGTID(a.hostname, gtid)

	return &agent.CloneResponse{}, nil
}

func setPodReadiness(ctx context.Context, name string, ready bool) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pod := &corev1.Pod{}
		err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: name}, pod)
		if err != nil {
			return err
		}

		if ready {
			pod.Status.Conditions = []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			}
		} else {
			pod.Status.Conditions = nil
		}
		return k8sClient.Status().Update(ctx, pod)
	})
}

type mockAgentFactory struct {
	orphaned int64
}

var _ AgentFactory = &mockAgentFactory{}

func (af *mockAgentFactory) New(ctx context.Context, cluster *mocov1beta2.MySQLCluster, index int) (AgentConn, error) {
	a := &mockAgentConn{
		orphaned: &af.orphaned,
		hostname: cluster.PodHostname(index),
	}
	atomic.AddInt64(&af.orphaned, 1)
	return a, nil
}

func (af *mockAgentFactory) allClosed() bool {
	return atomic.LoadInt64(&af.orphaned) == 0
}

type mockOperator struct {
	cluster *mocov1beta2.MySQLCluster
	passwd  *password.MySQLPassword
	index   int

	failing  bool
	orphaned *int64
	mysql    *mockMySQL
	factory  *mockOpFactory
}

var _ dbop.Operator = &mockOperator{}

func (o *mockOperator) Name() string {
	return o.cluster.PodHostname(o.index)
}

// Close closes the underlying connections.
func (o *mockOperator) Close() error {
	atomic.AddInt64(o.orphaned, -1)
	return nil
}

func (o *mockOperator) GetStatus(_ context.Context) (*dbop.MySQLInstanceStatus, error) {
	if o.failing {
		return nil, errors.New("mysqld is down")
	}
	gtid, _ := testGetGTID(o.cluster.PodHostname(o.index))
	st := o.mysql.getStatus()
	st.GlobalVariables.ExecutedGTID = gtid
	return st, nil
}

// SubtractGTID returns GTID subset of set1 that are not in set2.
func (o *mockOperator) SubtractGTID(ctx context.Context, set1, set2 string) (string, error) {
	if o.failing {
		return "", errors.New("mysqld is down")
	}
	if set1 == "" {
		return "", nil
	}
	if set2 == "" {
		return set1, nil
	}

	map1 := map[string]struct{}{}
	for _, s := range strings.Split(set1, ",") {
		map1[s] = struct{}{}
	}
	for _, s := range strings.Split(set2, ",") {
		delete(map1, s)
	}

	var diff []string
	for k := range map1 {
		diff = append(diff, k)
	}
	sort.Strings(diff)
	return strings.Join(diff, ","), nil
}

// IsSubsetGTID returns true if set1 is a subset of set2.
func (o *mockOperator) IsSubsetGTID(ctx context.Context, set1, set2 string) (bool, error) {
	if o.failing {
		return false, errors.New("mysqld is down")
	}
	if set1 == "" {
		return true, nil
	}
	if set2 == "" {
		return false, nil
	}

	map1 := map[string]struct{}{}
	for _, s := range strings.Split(set1, ",") {
		map1[s] = struct{}{}
	}
	for _, s := range strings.Split(set2, ",") {
		delete(map1, s)
	}
	return len(map1) == 0, nil
}

// ConfigureReplica configures client-side replication.
// If `symisync` is true, it enables client-side semi-synchronous replication.
// In either case, it disables server-side semi-synchronous replication.
func (o *mockOperator) ConfigureReplica(ctx context.Context, source dbop.AccessInfo, semisync bool) error {
	if o.failing {
		return errors.New("mysqld is down")
	}
	o.mysql.mu.Lock()
	defer o.mysql.mu.Unlock()

	hostnames := map[string]bool{
		"external": true,
	}
	for i := 0; i < int(o.cluster.Spec.Replicas); i++ {
		hostnames[o.cluster.PodHostname(i)] = true
	}
	if _, ok := hostnames[source.Host]; !ok {
		return fmt.Errorf("configureReplica: wrong host: %s", source.Host)
	}

	var validPasswd string
	switch source.User {
	case "external-donor":
		validPasswd = "p1"
	case "moco-repl":
		validPasswd = mysqlPassword.Replicator()
	default:
		return fmt.Errorf("configureReplica: invalid replication user: %s", source.User)
	}
	if source.Password != validPasswd {
		return fmt.Errorf("configureReplica: wrong password for %s", source.User)
	}
	if source.Port != 3306 {
		return fmt.Errorf("configureReplica: wrong replication port %d", source.Port)
	}

	si := o.factory.getInstance(source.Host)
	if si == nil {
		return fmt.Errorf("configureReplica: no mysql status for %s", source.Host)
	}
	si.mu.Lock()
	defer si.mu.Unlock()
	if semisync && !si.status.GlobalVariables.SemiSyncMasterEnabled {
		return fmt.Errorf("configureReplica: semi-sync master is not enabled for %s", source.Host)
	}

	if o.mysql.status.ReplicaStatus != nil {
		var oldi *mockMySQL
		if o.mysql.status.ReplicaStatus.MasterHost == source.Host {
			oldi = si
		} else {
			oldi = o.factory.getInstance(o.mysql.status.ReplicaStatus.MasterHost)
			if oldi == nil {
				panic(oldi)
			}
			oldi.mu.Lock()
			defer oldi.mu.Unlock()
		}

		var newReplicas []dbop.ReplicaHost
		for _, rep := range oldi.status.ReplicaHosts {
			if rep.Host == o.Name() {
				continue
			}
			newReplicas = append(newReplicas, rep)
		}
		oldi.status.ReplicaHosts = newReplicas
	}
	si.status.ReplicaHosts = append(si.status.ReplicaHosts, dbop.ReplicaHost{
		ServerID: o.cluster.Spec.ServerIDBase + int32(o.index),
		Host:     o.Name(),
	})

	gtid, _ := testGetGTID(source.Host)
	o.mysql.status.ReplicaStatus = &dbop.ReplicaStatus{
		MasterHost:       source.Host,
		RetrievedGtidSet: gtid,
		SlaveIORunning:   "Yes",
		SlaveSQLRunning:  "Yes",
	}
	o.mysql.status.GlobalVariables.SemiSyncSlaveEnabled = semisync
	return setPodReadiness(ctx, o.cluster.PodName(o.index), true)
}

// ConfigurePrimary configures server-side semi-synchronous replication.
// For asynchronous replication, this method should not be called.
func (o *mockOperator) ConfigurePrimary(ctx context.Context, waitForCount int) error {
	if o.failing {
		return errors.New("mysqld is down")
	}
	o.mysql.mu.Lock()
	defer o.mysql.mu.Unlock()

	o.mysql.status.GlobalVariables.WaitForSlaveCount = waitForCount
	o.mysql.status.GlobalVariables.SemiSyncMasterEnabled = true
	return nil
}

// StopReplicaIOThread executes `STOP SLAVE IO_THREAD`.
func (o *mockOperator) StopReplicaIOThread(ctx context.Context) error {
	if o.failing {
		return errors.New("mysqld is down")
	}
	o.mysql.mu.Lock()
	defer o.mysql.mu.Unlock()

	if o.mysql.status.ReplicaStatus == nil {
		return nil
	}
	o.mysql.status.ReplicaStatus.SlaveIORunning = "No"
	return setPodReadiness(ctx, o.cluster.PodName(o.index), false)
}

// WaitForGTID waits for `mysqld` to execute all GTIDs in `gtidSet`.
// If timeout happens, this return ErrTimeout.
// If `timeoutSeconds` is zero, this will not timeout.
func (o *mockOperator) WaitForGTID(_ context.Context, gtidSet string, _ int) error {
	if o.failing {
		return errors.New("mysqld is down")
	}
	o.mysql.mu.Lock()
	defer o.mysql.mu.Unlock()

	myGTID, _ := testGetGTID(o.Name())
	if myGTID == gtidSet {
		return nil
	}
	if o.mysql.status.ReplicaStatus == nil {
		return errors.New("waitForGTID: replication is stopped")
	}
	if o.mysql.status.ReplicaStatus.RetrievedGtidSet == gtidSet {
		testSetGTID(o.Name(), gtidSet)
		o.mysql.status.GlobalVariables.ExecutedGTID = gtidSet
		return nil
	}
	if o.mysql.status.ReplicaStatus.SlaveIORunning == "Yes" {
		primary := o.factory.getInstance(o.mysql.status.ReplicaStatus.MasterHost)
		if primary == nil {
			return errors.New("waitForGTID: primary not found")
		}
		primaryGTID, _ := testGetGTID(o.mysql.status.ReplicaStatus.MasterHost)
		if primaryGTID == gtidSet {
			testSetGTID(o.Name(), gtidSet)
			o.mysql.status.GlobalVariables.ExecutedGTID = gtidSet
			return nil
		}
	}
	return errors.New("waitForGTID: timed out")
}

// SetReadOnly makes the instance super_read_only if `true` is passed.
// Otherwise, this stops the replication and makes the instance writable.
func (o *mockOperator) SetReadOnly(ctx context.Context, readonly bool) error {
	if o.failing {
		return errors.New("mysqld is down")
	}
	o.mysql.mu.Lock()
	defer o.mysql.mu.Unlock()

	if readonly {
		o.mysql.status.GlobalVariables.ReadOnly = true
		o.mysql.status.GlobalVariables.SuperReadOnly = true
		return nil
	}

	if o.mysql.status.ReplicaStatus != nil {
		primary := o.factory.getInstance(o.mysql.status.ReplicaStatus.MasterHost)
		if primary == nil {
			return errors.New("setReadOnly: primary not found")
		}
		primary.mu.Lock()
		defer primary.mu.Unlock()

		var newReplicas []dbop.ReplicaHost
		for _, h := range primary.status.ReplicaHosts {
			if h.Host == o.Name() {
				continue
			}
			newReplicas = append(newReplicas, h)
		}
		primary.status.ReplicaHosts = newReplicas
		o.mysql.status.ReplicaStatus = nil
	}
	o.mysql.status.GlobalVariables.ReadOnly = false
	o.mysql.status.GlobalVariables.SuperReadOnly = false
	return setPodReadiness(ctx, o.cluster.PodName(o.index), true)
}

func (o *mockOperator) KillConnections(ctx context.Context) error {
	return nil
}

type mockMySQL struct {
	mu     sync.Mutex
	status dbop.MySQLInstanceStatus
}

func (m *mockMySQL) getStatus() *dbop.MySQLInstanceStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	st := m.status
	if st.ReplicaStatus != nil {
		crs := *m.status.ReplicaStatus
		st.ReplicaStatus = &crs
	}
	if st.CloneStatus != nil {
		ccs := *m.status.CloneStatus
		st.CloneStatus = &ccs
	}
	if len(st.ReplicaHosts) > 0 {
		crh := make([]dbop.ReplicaHost, len(m.status.ReplicaHosts))
		copy(crh, m.status.ReplicaHosts)
		st.ReplicaHosts = crh
	}
	return &st
}

func (m *mockMySQL) setRetrievedGTIDSet(gtid string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.ReplicaStatus.RetrievedGtidSet = gtid
}

type mockOpFactory struct {
	orphaned int64

	mu      sync.Mutex
	mysqls  map[string]*mockMySQL
	failing map[string]bool
}

func newMockOpFactory() *mockOpFactory {
	return &mockOpFactory{
		mysqls:  make(map[string]*mockMySQL),
		failing: make(map[string]bool),
	}
}

var _ dbop.OperatorFactory = &mockOpFactory{}

func (f *mockOpFactory) New(ctx context.Context, cluster *mocov1beta2.MySQLCluster, passwd *password.MySQLPassword, index int) (dbop.Operator, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	atomic.AddInt64(&f.orphaned, 1)
	hostname := cluster.PodHostname(index)
	m, ok := f.mysqls[hostname]
	if !ok {
		m = &mockMySQL{}
		m.status.GlobalVariables.UUID = fmt.Sprintf("p%d", index)
		m.status.GlobalVariables.ReadOnly = true
		m.status.GlobalVariables.SuperReadOnly = true
		f.mysqls[hostname] = m
	}
	return &mockOperator{
		cluster:  cluster,
		passwd:   passwd,
		index:    index,
		failing:  f.failing[hostname],
		orphaned: &f.orphaned,
		mysql:    m,
		factory:  f,
	}, nil
}

func (f *mockOpFactory) Cleanup() {}

func (f *mockOpFactory) getInstance(name string) *mockMySQL {
	if name == "external" {
		m := &mockMySQL{}
		gtid, _ := testGetGTID("external")
		m.status.GlobalVariables.UUID = "ex"
		m.status.GlobalVariables.ExecutedGTID = gtid
		return m
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mysqls[name]
}

func (f *mockOpFactory) getInstanceStatus(name string) *dbop.MySQLInstanceStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.mysqls[name]
	if m == nil {
		return nil
	}

	return m.getStatus()
}

func (f *mockOpFactory) allClosed() bool {
	return atomic.LoadInt64(&f.orphaned) == 0
}

func (f *mockOpFactory) setFailing(name string, failing bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if failing {
		f.failing[name] = true
	} else {
		delete(f.failing, name)
	}
}

func (f *mockOpFactory) setRetrievedGTIDSet(name string, gtid string) {
	m := f.getInstance(name)
	m.setRetrievedGTIDSet(gtid)
}
