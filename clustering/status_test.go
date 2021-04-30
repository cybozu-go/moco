package clustering

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/pkg/dbop"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

const testPrimaryHostname = "moco-test-0.moco-test.ns.svc"

type ssBuilder struct {
	replicas       int32
	primaryIndex   int
	isIntermediate bool
	pods           []*corev1.Pod
	mysqlStatus    []*dbop.MySQLInstanceStatus
}

func (b *ssBuilder) build() *StatusSet {
	if len(b.pods) != int(b.replicas) {
		panic(fmt.Errorf("pods and replicas mismatch: %d, %d", len(b.pods), b.replicas))
	}
	if len(b.mysqlStatus) != int(b.replicas) {
		panic(fmt.Errorf("mysql status and replicas mismatch: %d, %d", len(b.mysqlStatus), b.replicas))
	}

	cluster := &mocov1beta1.MySQLCluster{}
	cluster.Name = "test"
	cluster.Namespace = "ns"
	cluster.Spec.Replicas = b.replicas
	cluster.Spec.ServerIDBase = 10
	cluster.Status.CurrentPrimaryIndex = b.primaryIndex
	if b.isIntermediate {
		cluster.Spec.ReplicationSourceSecretName = pointer.String("hoge")
	}
	var errants []int
	for i, ist := range b.mysqlStatus {
		if i == b.primaryIndex {
			continue
		}
		if ist == nil {
			continue
		}
		if ist.IsErrant {
			errants = append(errants, i)
		}
	}
	cluster.Status.ErrantReplicaList = errants
	var gtid string
	if pst := b.mysqlStatus[b.primaryIndex]; pst != nil {
		gtid = pst.GlobalVariables.ExecutedGTID
	}
	return &StatusSet{
		Cluster:      cluster,
		Pods:         b.pods,
		MySQLStatus:  b.mysqlStatus,
		Errants:      errants,
		ExecutedGTID: gtid,
	}
}

func newSS(replicas int32, primary int, intermediate bool) *ssBuilder {
	return &ssBuilder{
		replicas:       replicas,
		primaryIndex:   primary,
		isIntermediate: intermediate,
	}
}

func (b *ssBuilder) withPod(ready, deleting, demoting bool) *ssBuilder {
	pod := &corev1.Pod{}
	if ready {
		pod.Status.Conditions = append(pod.Status.Conditions,
			corev1.PodCondition{
				Type:   corev1.ContainersReady,
				Status: corev1.ConditionTrue,
			},
			corev1.PodCondition{
				Type:   corev1.PodScheduled,
				Status: corev1.ConditionFalse,
			},
			corev1.PodCondition{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			},
		)
	} else {
		pod.Status.Conditions = append(pod.Status.Conditions,
			corev1.PodCondition{
				Type:   corev1.ContainersReady,
				Status: corev1.ConditionTrue,
			},
			corev1.PodCondition{
				Type:   corev1.PodScheduled,
				Status: corev1.ConditionTrue,
			},
		)
	}
	if deleting {
		pod.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	}
	if demoting {
		pod.Annotations = map[string]string{
			"moco.cybozu.com/demote": "true",
		}
	}
	b.pods = append(b.pods, pod)
	return b
}

func (b *ssBuilder) withMySQL(ist *dbop.MySQLInstanceStatus) *ssBuilder {
	b.mysqlStatus = append(b.mysqlStatus, ist)
	return b
}

type mysqlBuilder struct {
	gtid         string
	readonly     bool
	errant       bool
	cloning      bool
	sourceHost   string
	replicaHosts []dbop.ReplicaHost
}

func (b *mysqlBuilder) build() *dbop.MySQLInstanceStatus {
	st := &dbop.MySQLInstanceStatus{}
	st.GlobalVariables.ExecutedGTID = b.gtid
	if b.readonly {
		st.GlobalVariables.ReadOnly = true
		st.GlobalVariables.SuperReadOnly = true
	}
	if b.errant {
		st.IsErrant = true
	}
	if b.cloning {
		st.CloneStatus = &dbop.CloneStatus{State: sql.NullString{Valid: true, String: "In Progress"}}
	}
	if len(b.sourceHost) > 0 {
		st.ReplicaStatus = &dbop.ReplicaStatus{
			MasterHost: b.sourceHost,
		}
	}
	st.ReplicaHosts = b.replicaHosts
	return st
}

func newMySQL(gtid string, readonly, errant, cloning bool) *mysqlBuilder {
	return &mysqlBuilder{
		gtid:     gtid,
		readonly: readonly,
		errant:   errant,
		cloning:  cloning,
	}
}

func (b *mysqlBuilder) withPrimary(hostname string) *mysqlBuilder {
	b.sourceHost = hostname
	return b
}

func (b *mysqlBuilder) withReplica(serverID int32, hostname string) *mysqlBuilder {
	b.replicaHosts = append(b.replicaHosts, dbop.ReplicaHost{
		ServerID: serverID,
		Host:     hostname,
		Port:     3306,
	})
	return b
}

func TestStatusSet(t *testing.T) {
	testCases := []struct {
		name           string
		statusSet      *StatusSet
		expectedState  ClusterState
		expectedSwitch bool
	}{
		{
			name: "healthy1",
			statusSet: newSS(1, 0, false).
				withPod(true, false, false).
				withMySQL(newMySQL("123", false, false, false).build()).
				build(),
			expectedState: StateHealthy,
		},
		{
			name: "lost1",
			statusSet: newSS(1, 0, false).
				withPod(true, false, false).
				withMySQL(nil).
				build(),
			expectedState: StateLost,
		},
		{
			name: "incomplete-pod-not-ready",
			statusSet: newSS(1, 0, false).
				withPod(false, false, false).
				withMySQL(newMySQL("123", false, false, false).build()).
				build(),
			expectedState: StateIncomplete,
		},
		{
			name: "incomplete-primary-read-only",
			statusSet: newSS(1, 0, false).
				withPod(true, false, false).
				withMySQL(newMySQL("123", true, false, false).build()).
				build(),
			expectedState: StateIncomplete,
		},
		{
			name: "incomplete3-initializing",
			statusSet: newSS(3, 0, false).
				withPod(false, false, false).
				withPod(false, false, false).
				withPod(false, false, false).
				withMySQL(newMySQL("", true, false, false).build()).
				withMySQL(newMySQL("", true, false, false).build()).
				withMySQL(newMySQL("", true, false, false).build()).
				build(),
			expectedState: StateIncomplete,
		},
		{
			name: "healthy3",
			statusSet: newSS(3, 0, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withMySQL(newMySQL("1234", false, false, false).
					withReplica(11, "replica1").
					withReplica(12, "replica2").
					build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				build(),
			expectedState: StateHealthy,
		},
		{
			name: "healthy3-extra-replica",
			statusSet: newSS(3, 0, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withMySQL(newMySQL("1234", false, false, false).
					withReplica(11, "replica1").
					withReplica(12, "replica2").
					withReplica(1000, "unknown").
					build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				build(),
			expectedState: StateHealthy,
		},
		{
			name: "healthy3-primary-deleting",
			statusSet: newSS(3, 0, false).
				withPod(true, true, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withMySQL(newMySQL("1234", false, false, false).
					withReplica(11, "replica1").
					withReplica(12, "replica2").
					withReplica(1000, "unknown").
					build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				build(),
			expectedState:  StateHealthy,
			expectedSwitch: true,
		},
		{
			name: "healthy3-primary-demoting",
			statusSet: newSS(3, 0, false).
				withPod(true, false, true).
				withPod(true, false, false).
				withPod(true, false, false).
				withMySQL(newMySQL("1234", false, false, false).
					withReplica(11, "replica1").
					withReplica(12, "replica2").
					withReplica(1000, "unknown").
					build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				build(),
			expectedState:  StateHealthy,
			expectedSwitch: true,
		},
		{
			name: "healthy3-replica-deleting",
			statusSet: newSS(3, 0, false).
				withPod(true, false, false).
				withPod(true, true, false).
				withPod(true, false, false).
				withMySQL(newMySQL("1234", false, false, false).
					withReplica(11, "replica1").
					withReplica(12, "replica2").
					build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				build(),
			expectedState: StateHealthy,
		},
		{
			name: "healthy3-replica-demoting",
			statusSet: newSS(3, 0, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withPod(true, false, true).
				withMySQL(newMySQL("1234", false, false, false).
					withReplica(11, "replica1").
					withReplica(12, "replica2").
					build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				build(),
			expectedState: StateHealthy,
		},
		{
			name: "cloning-not-started",
			statusSet: newSS(3, 0, true).
				withPod(false, false, false).
				withPod(false, false, false).
				withPod(false, false, false).
				withMySQL(newMySQL("", true, false, false).build()).
				withMySQL(newMySQL("", true, false, false).build()).
				withMySQL(newMySQL("", true, false, false).build()).
				build(),
			expectedState: StateCloning,
		},
		{
			name: "cloning-in-progress",
			statusSet: newSS(3, 0, true).
				withPod(false, false, false).
				withPod(false, false, false).
				withPod(false, false, false).
				withMySQL(newMySQL("1234", true, false, true).build()).
				withMySQL(newMySQL("", true, false, false).build()).
				withMySQL(newMySQL("", true, false, false).build()).
				build(),
			expectedState: StateCloning,
		},
		{
			name: "health3-intermediate",
			statusSet: newSS(3, 0, true).
				withPod(true, false, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withMySQL(newMySQL("1234", true, false, false).
					withReplica(11, "replica1").
					withReplica(12, "replica2").
					build()).
				withMySQL(newMySQL("", true, false, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("", true, false, false).withPrimary(testPrimaryHostname).build()).
				build(),
			expectedState: StateHealthy,
		},
		{
			name: "degraded3-replica-not-ready",
			statusSet: newSS(3, 0, false).
				withPod(true, false, false).
				withPod(false, false, false).
				withPod(true, false, false).
				withMySQL(newMySQL("1234", false, false, false).
					withReplica(11, "replica1").
					withReplica(12, "replica2").
					build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				build(),
			expectedState: StateDegraded,
		},
		{
			name: "degraded3-replica-stopping",
			statusSet: newSS(3, 0, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withMySQL(newMySQL("1234", false, false, false).
					withReplica(12, "replica2").
					build()).
				withMySQL(nil).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				build(),
			expectedState: StateDegraded,
		},
		{
			name: "degraded3-replica-not-started",
			statusSet: newSS(3, 0, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withMySQL(newMySQL("1234", false, false, false).
					withReplica(12, "replica2").
					build()).
				withMySQL(newMySQL("123", true, false, false).build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				build(),
			expectedState: StateDegraded,
		},
		{
			name: "degraded3-replica-writable",
			statusSet: newSS(3, 0, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withMySQL(newMySQL("1234", false, false, false).
					withReplica(11, "replica1").
					withReplica(12, "replica2").
					build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("123", false, false, false).withPrimary(testPrimaryHostname).build()).
				build(),
			expectedState: StateDegraded,
		},
		{
			name: "degraded3-replica-errant",
			statusSet: newSS(3, 0, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withMySQL(newMySQL("1234", false, false, false).
					withReplica(11, "replica1").
					withReplica(12, "replica2").
					build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("123", true, true, false).withPrimary(testPrimaryHostname).build()).
				build(),
			expectedState: StateDegraded,
		},
		{
			name: "degraded3-replica-lost-data",
			statusSet: newSS(3, 0, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withPod(false, false, false).
				withMySQL(newMySQL("1234", false, false, false).
					withReplica(11, "replica1").
					build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("", true, false, false).build()).
				build(),
			expectedState: StateDegraded,
		},
		{
			name: "degraded5-two-replicas-are-bad",
			statusSet: newSS(5, 0, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withPod(false, false, false).
				withPod(true, false, false).
				withMySQL(newMySQL("1234", false, false, false).
					withReplica(11, "replica1").
					withReplica(12, "replica2").
					withReplica(14, "replica4").
					build()).
				withMySQL(newMySQL("123", true, true, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(nil).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				build(),
			expectedState: StateDegraded,
		},
		{
			name: "failed3-primary-stopping",
			statusSet: newSS(3, 0, false).
				withPod(false, false, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withMySQL(nil).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				build(),
			expectedState: StateFailed,
		},
		{
			name: "failed3-primary-lost-data",
			statusSet: newSS(3, 0, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withMySQL(newMySQL("", true, false, false).build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				build(),
			expectedState: StateFailed,
		},
		{
			name: "failed5-1-replica-errant",
			statusSet: newSS(5, 0, false).
				withPod(false, false, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withMySQL(nil).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("123", true, true, false).withPrimary(testPrimaryHostname).build()).
				withMySQL(newMySQL("123", true, false, false).withPrimary(testPrimaryHostname).build()).
				build(),
			expectedState: StateFailed,
		},
		{
			name: "lost3-too-few-replicas",
			statusSet: newSS(3, 0, false).
				withPod(false, false, false).
				withPod(true, false, false).
				withPod(false, false, false).
				withMySQL(nil).
				withMySQL(newMySQL("123", true, false, false).build()).
				withMySQL(nil).
				build(),
			expectedState: StateLost,
		},
		{
			name: "lost5-too-few-replicas",
			statusSet: newSS(5, 0, false).
				withPod(false, false, false).
				withPod(true, false, false).
				withPod(true, false, false).
				withPod(false, false, false).
				withPod(true, false, false).
				withMySQL(nil).
				withMySQL(newMySQL("123", true, false, false).build()).
				withMySQL(newMySQL("123", true, false, false).build()).
				withMySQL(nil).
				withMySQL(newMySQL("123", true, true, false).build()).
				build(),
			expectedState: StateLost,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tc.statusSet.DecideState()
			if tc.statusSet.State != tc.expectedState {
				t.Errorf("unexpected state %s: expected=%s", tc.statusSet.State.String(), tc.expectedState.String())
			}
			if tc.statusSet.NeedSwitch != tc.expectedSwitch {
				t.Errorf("wrong NeedSwitch: expected=%v", tc.expectedSwitch)
			}
		})
	}
}
