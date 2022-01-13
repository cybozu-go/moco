package clustering

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/dbop"
	"github.com/cybozu-go/moco/pkg/password"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	statusCheckRetryMax      = 3
	statusCheckRetryInterval = 3 * time.Second
)

func init() {
	intervalStr := os.Getenv("MOCO_CHECK_INTERVAL")
	if intervalStr == "" {
		return
	}
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		return
	}
	statusCheckRetryInterval = interval
}

// ClusterState represents the state of a MySQL cluster.
// Consult docs/clustering.md for details.
type ClusterState int

// List of possible ClusterState.
const (
	StateUndecided ClusterState = iota
	StateIncomplete
	StateHealthy
	StateCloning
	StateRestoring
	StateDegraded
	StateFailed
	StateLost
)

// String returns a unique string for each ClusterState.
func (s ClusterState) String() string {
	switch s {
	case StateUndecided:
		return "Undecided"
	case StateIncomplete:
		return "Incomplete"
	case StateHealthy:
		return "Healthy"
	case StateCloning:
		return "Cloning"
	case StateRestoring:
		return "Restoring"
	case StateDegraded:
		return "Degraded"
	case StateFailed:
		return "Failed"
	case StateLost:
		return "Lost"
	}

	panic(int(s))
}

// StatusSet represents the set of information to determine the ClusterState
// and later operations.
type StatusSet struct {
	Primary      int
	Cluster      *mocov1beta1.MySQLCluster
	Password     *password.MySQLPassword
	Pods         []*corev1.Pod
	DBOps        []dbop.Operator
	MySQLStatus  []*dbop.MySQLInstanceStatus
	ExecutedGTID string
	Errants      []int
	Candidates   []int

	NeedSwitch bool
	Candidate  int
	State      ClusterState
}

// Close closes `ss.DBOps`.
func (ss *StatusSet) Close() {
	for _, op := range ss.DBOps {
		if op != nil {
			op.Close()
		}
	}
}

// DecideState decides the ClusterState and set it to `ss.State`.
// It may also set `ss.NeedSwitch` and `ss.Candidate` for switchover.
func (ss *StatusSet) DecideState() {
	switch {
	case isCloning(ss):
		ss.State = StateCloning
	case isRestoring(ss):
		ss.State = StateRestoring
	case isHealthy(ss):
		ss.State = StateHealthy
	case isDegraded(ss):
		ss.State = StateDegraded
	case isFailed(ss):
		ss.State = StateFailed
	case isLost(ss):
		ss.State = StateLost
	default:
		ss.State = StateIncomplete
	}
	if len(ss.Candidates) > 0 {
		ss.NeedSwitch = needSwitch(ss.Pods[ss.Primary])
		// Choose the lowest ordinal for a switchover target.
		sort.Ints(ss.Candidates)
		ss.Candidate = ss.Candidates[0]
	}
}

// GatherStatus collects information and Kubernetes resources and construct
// StatusSet.  It calls `StatusSet.DecideState` before returning.
func (p *managerProcess) GatherStatus(ctx context.Context) (*StatusSet, error) {
	ss := &StatusSet{}

	cluster := &mocov1beta1.MySQLCluster{}
	if err := p.reader.Get(ctx, p.name, cluster); err != nil {
		return nil, fmt.Errorf("failed to get MySQLCluster: %w", err)
	}
	ss.Cluster = cluster
	ss.Primary = cluster.Status.CurrentPrimaryIndex

	passwdSecret := &corev1.Secret{}
	if err := p.client.Get(ctx, client.ObjectKey{Namespace: p.name.Namespace, Name: cluster.UserSecretName()}, passwdSecret); err != nil {
		return nil, fmt.Errorf("failed to get password secret: %w", err)
	}
	passwd, err := password.NewMySQLPasswordFromSecret(passwdSecret)
	if err != nil {
		return nil, err
	}
	ss.Password = passwd

	pods := &corev1.PodList{}
	if err := p.client.List(ctx, pods, client.InNamespace(p.name.Namespace), client.MatchingLabels{
		constants.LabelAppName:     constants.AppNameMySQL,
		constants.LabelAppInstance: p.name.Name,
	}); err != nil {
		return nil, fmt.Errorf("failed to list Pods: %w", err)
	}

	if int(cluster.Spec.Replicas) != len(pods.Items) {
		return nil, fmt.Errorf("too few pods; only %d pods exist", len(pods.Items))
	}
	ss.Pods = make([]*corev1.Pod, cluster.Spec.Replicas)
	for i, pod := range pods.Items {
		fields := strings.Split(pod.Name, "-")
		index, err := strconv.Atoi(fields[len(fields)-1])
		if err != nil {
			return nil, fmt.Errorf("bad pod name: %s", pod.Name)
		}

		if index < 0 || index >= len(pods.Items) {
			return nil, fmt.Errorf("index out of range: %d", index)
		}
		ss.Pods[index] = &pods.Items[i]
	}

	ss.DBOps = make([]dbop.Operator, cluster.Spec.Replicas)
	defer func() {
		if ss.State == StateUndecided {
			ss.Close()
		}
	}()
	for i := 0; i < int(cluster.Spec.Replicas); i++ {
		op, err := p.dbf.New(ctx, cluster, passwd, i)
		if err != nil {
			return nil, err
		}
		ss.DBOps[i] = op
	}

	ss.MySQLStatus = make([]*dbop.MySQLInstanceStatus, cluster.Spec.Replicas)
	var wg sync.WaitGroup
	for i := 0; i < len(ss.MySQLStatus); i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			for j := 0; j < statusCheckRetryMax; j++ {
				ist, err := ss.DBOps[index].GetStatus(ctx)
				if err == dbop.ErrNop {
					return
				}
				if err != nil {
					p.log.Error(err, "failed to get mysqld status")
					time.Sleep(statusCheckRetryInterval)
					continue
				}
				ss.MySQLStatus[index] = ist
				return
			}
		}(i)
	}
	wg.Wait()

	// re-check the primary MySQL status to retrieve the latest executed GTID set
	if ss.MySQLStatus[ss.Primary] != nil {
		time.Sleep(100 * time.Millisecond)
		pst, err := ss.DBOps[ss.Primary].GetStatus(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to re-check the primary instance: %w", err)
		}
		ss.MySQLStatus[ss.Primary] = pst
		ss.ExecutedGTID = pst.GlobalVariables.ExecutedGTID
	}

	if ss.ExecutedGTID != "" {
		for i, ist := range ss.MySQLStatus {
			if i == ss.Primary {
				continue
			}
			if ist == nil {
				continue
			}

			ok, err := ss.DBOps[ss.Primary].IsSubsetGTID(ctx, ist.GlobalVariables.ExecutedGTID, ss.ExecutedGTID)
			if err != nil {
				return nil, fmt.Errorf("failed to compare GTID %s and %s: %w", ist.GlobalVariables.ExecutedGTID, ss.ExecutedGTID, err)
			}
			if !ok {
				ist.IsErrant = true
				ss.Errants = append(ss.Errants, i)
			}
		}
	} else {
		// restore errant replica status from information stored in MySQLCluster
		// when the primary is down or possibly lost data.
		for _, index := range cluster.Status.ErrantReplicaList {
			ss.MySQLStatus[index].IsErrant = true
			ss.Errants = append(ss.Errants, index)
		}
	}

	ss.DecideState()
	return ss, nil
}

func isPodReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type != corev1.PodReady {
			continue
		}
		if cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func replicasInCluster(cluster *mocov1beta1.MySQLCluster, replicas []dbop.ReplicaHost) int32 {
	var n int32
	for _, r := range replicas {
		if r.ServerID >= cluster.Spec.ServerIDBase && r.ServerID < (cluster.Spec.ServerIDBase+cluster.Spec.Replicas) {
			n++
		}
	}
	return n
}

func lostData(ss *StatusSet) bool {
	if ss.ExecutedGTID != "" {
		return false
	}

	for i, ist := range ss.MySQLStatus {
		if i == ss.Primary {
			continue
		}
		if ist == nil {
			continue
		}
		if ist.GlobalVariables.ExecutedGTID != "" {
			return true
		}
	}
	return false
}

func isHealthy(ss *StatusSet) bool {
	for _, pod := range ss.Pods {
		if !isPodReady(pod) {
			return false
		}
	}

	primaryHostname := ss.Cluster.PodHostname(ss.Primary)
	for i, ist := range ss.MySQLStatus {
		if i == ss.Primary {
			continue
		}
		if ist == nil {
			return false
		}
		if ist.IsErrant {
			return false
		}
		if !ist.GlobalVariables.SuperReadOnly {
			return false
		}
		if ist.ReplicaStatus == nil {
			return false
		}
		if ist.ReplicaStatus.MasterHost != primaryHostname {
			return false
		}
		ss.Candidates = append(ss.Candidates, i)
	}

	pst := ss.MySQLStatus[ss.Primary]
	if pst == nil {
		return false
	}
	if replicasInCluster(ss.Cluster, pst.ReplicaHosts) != (ss.Cluster.Spec.Replicas - 1) {
		return false
	}
	if ss.Cluster.Spec.ReplicationSourceSecretName != nil {
		if !pst.GlobalVariables.SuperReadOnly {
			return false
		}
	} else {
		if pst.GlobalVariables.ReadOnly {
			return false
		}
	}

	return true
}

func isCloning(ss *StatusSet) bool {
	if ss.Cluster.Spec.ReplicationSourceSecretName == nil {
		return false
	}

	if ss.Cluster.Status.Cloned {
		return false
	}

	pst := ss.MySQLStatus[ss.Primary]
	if pst == nil {
		return true
	}

	if pst.CloneStatus != nil && pst.CloneStatus.State.String != "Completed" {
		return true
	}
	if pst.CloneStatus == nil && pst.GlobalVariables.ExecutedGTID == "" {
		return true
	}

	return false
}

func isRestoring(ss *StatusSet) bool {
	if ss.Cluster.Spec.Restore == nil {
		return false
	}
	if ss.Cluster.Status.RestoredTime != nil {
		return false
	}
	return true
}

func isDegraded(ss *StatusSet) bool {
	ppod := ss.Pods[ss.Primary]
	if !isPodReady(ppod) {
		return false
	}
	if lostData(ss) {
		return false
	}

	pst := ss.MySQLStatus[ss.Primary]
	if pst == nil {
		return false
	}
	if ss.Cluster.Spec.ReplicationSourceSecretName != nil {
		if !pst.GlobalVariables.SuperReadOnly {
			return false
		}
	} else {
		if pst.GlobalVariables.ReadOnly {
			return false
		}
	}
	if replicasInCluster(ss.Cluster, pst.ReplicaHosts) < (ss.Cluster.Spec.Replicas / 2) {
		return false
	}

	primaryHostname := ss.Cluster.PodHostname(ss.Primary)
	var okReplicas int
	for i, ist := range ss.MySQLStatus {
		if i == ss.Primary {
			continue
		}
		if ist == nil {
			continue
		}
		if !isPodReady(ss.Pods[i]) {
			continue
		}
		if !ist.GlobalVariables.SuperReadOnly {
			continue
		}
		if ist.ReplicaStatus == nil {
			continue
		}
		if ist.ReplicaStatus.MasterHost != primaryHostname {
			continue
		}
		if ist.IsErrant {
			continue
		}
		okReplicas++
		ss.Candidates = append(ss.Candidates, i)
	}

	return okReplicas >= (int(ss.Cluster.Spec.Replicas)/2) && okReplicas != int(ss.Cluster.Spec.Replicas-1)
}

func isFailed(ss *StatusSet) bool {
	pst := ss.MySQLStatus[ss.Primary]
	if pst != nil && !lostData(ss) {
		return false
	}

	var okReplicas int
	for i, ist := range ss.MySQLStatus {
		if i == ss.Primary {
			continue
		}
		if ist == nil {
			continue
		}
		if ist.ReplicaStatus == nil {
			continue
		}
		if ist.IsErrant {
			continue
		}
		if ist.GlobalVariables.ExecutedGTID == "" {
			continue
		}
		okReplicas++
	}

	return okReplicas > (int(ss.Cluster.Spec.Replicas) / 2)
}

func isLost(ss *StatusSet) bool {
	pst := ss.MySQLStatus[ss.Primary]
	if pst != nil && !lostData(ss) {
		return false
	}

	var okReplicas int
	for i, ist := range ss.MySQLStatus {
		if i == ss.Primary {
			continue
		}
		if ist == nil {
			continue
		}
		if ist.ReplicaStatus == nil {
			continue
		}
		if ist.IsErrant {
			continue
		}
		if ist.GlobalVariables.ExecutedGTID == "" {
			continue
		}
		okReplicas++
	}

	return okReplicas <= (int(ss.Cluster.Spec.Replicas) / 2)
}

func needSwitch(pod *corev1.Pod) bool {
	if pod.DeletionTimestamp != nil {
		return true
	}
	if pod.Annotations[constants.AnnDemote] == "true" {
		return true
	}
	return false
}
