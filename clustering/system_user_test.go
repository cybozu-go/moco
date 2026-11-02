package clustering

import (
	"context"
	"errors"
	"strings"
	"testing"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/dbop"
	"github.com/cybozu-go/moco/pkg/metrics"
	"github.com/cybozu-go/moco/pkg/password"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newSystemUserTestStatus(t *testing.T, replicas int32, plugin string) (*StatusSet, *mockOpFactory) {
	t.Helper()

	passwd, err := password.NewMySQLPassword()
	if err != nil {
		t.Fatalf("failed to generate passwords: %v", err)
	}
	cluster := &mocov1beta2.MySQLCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: mocov1beta2.MySQLClusterSpec{
			Replicas:                       replicas,
			SystemUserAuthenticationPlugin: plugin,
		},
	}
	factory := newMockOpFactory()
	ops := make([]dbop.Operator, replicas)
	for i := range replicas {
		op, err := factory.New(context.Background(), cluster, passwd, int(i))
		if err != nil {
			t.Fatalf("failed to create mock operator: %v", err)
		}
		ops[i] = op
	}

	return &StatusSet{
		Cluster: cluster,
		DBOps:   ops,
		State:   StateHealthy,
	}, factory
}

func TestConfigureSystemUserAuthenticationPlugin(t *testing.T) {
	ss, factory := newSystemUserTestStatus(t, 3, "caching_sha2_password")
	p := &managerProcess{recorder: record.NewFakeRecorder(10)}

	if err := p.configureSystemUserAuthenticationPlugin(context.Background(), ss); err != nil {
		t.Fatalf("configureSystemUserAuthenticationPlugin returned an error: %v", err)
	}

	for i := 0; i < 3; i++ {
		m := factory.mysqls[ss.Cluster.PodHostname(i)]
		if m.authPluginCalls != 1 {
			t.Errorf("instance %d: expected one plugin converge call, got %d", i, m.authPluginCalls)
		}
		for _, user := range constants.MocoUsers {
			if got := m.systemUserPlugins[user]; got != "caching_sha2_password" {
				t.Errorf("instance %d user %s: expected caching_sha2_password, got %s", i, user, got)
			}
		}
	}

	// The operation is idempotent: a second pass must not report changes.
	changed, err := ss.DBOps[0].SetSystemUserAuthenticationPlugin(context.Background(), "caching_sha2_password")
	if err != nil {
		t.Fatalf("second SetSystemUserAuthenticationPlugin returned an error: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("expected no changed users on the second pass, got %v", changed)
	}
}

func TestConfigureSystemUserAuthenticationPluginDisabledOrNotHealthy(t *testing.T) {
	tests := []struct {
		name       string
		plugin     string
		state      ClusterState
		needSwitch bool
	}{
		{name: "unset", state: StateHealthy},
		{name: "degraded", plugin: "caching_sha2_password", state: StateDegraded},
		{name: "need switch", plugin: "caching_sha2_password", state: StateHealthy, needSwitch: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ss, factory := newSystemUserTestStatus(t, 1, tt.plugin)
			ss.State = tt.state
			ss.NeedSwitch = tt.needSwitch
			p := &managerProcess{recorder: record.NewFakeRecorder(10)}

			if err := p.configureSystemUserAuthenticationPlugin(context.Background(), ss); err != nil {
				t.Fatalf("configureSystemUserAuthenticationPlugin returned an error: %v", err)
			}
			if got := factory.mysqls[ss.Cluster.PodHostname(0)].authPluginCalls; got != 0 {
				t.Errorf("expected no plugin converge calls, got %d", got)
			}
		})
	}
}

func TestConfigureSystemUserAuthenticationPluginError(t *testing.T) {
	ss, factory := newSystemUserTestStatus(t, 2, "caching_sha2_password")
	factory.mysqls[ss.Cluster.PodHostname(1)].authPluginErr = errors.New("plugin is not installed")
	p := &managerProcess{recorder: record.NewFakeRecorder(10)}

	err := p.configureSystemUserAuthenticationPlugin(context.Background(), ss)
	if err == nil || !strings.Contains(err.Error(), "instance 1") {
		t.Fatalf("expected an instance-specific error, got %v", err)
	}
}

func TestDoConvergesSystemUserAuthenticationPluginWhenHealthy(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add client-go scheme: %v", err)
	}
	if err := mocov1beta2.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add moco scheme: %v", err)
	}

	passwd, err := password.NewMySQLPassword()
	if err != nil {
		t.Fatalf("failed to generate passwords: %v", err)
	}
	cluster := &mocov1beta2.MySQLCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: mocov1beta2.MySQLClusterSpec{
			Replicas:                       1,
			SystemUserAuthenticationPlugin: "caching_sha2_password",
		},
		Status: mocov1beta2.MySQLClusterStatus{CurrentPrimaryIndex: 0},
	}
	secret := passwd.ToSecret()
	secret.Namespace = cluster.Namespace
	secret.Name = cluster.UserSecretName()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.PodName(0),
			Namespace: cluster.Namespace,
			Labels: map[string]string{
				constants.LabelAppName:     constants.AppNameMySQL,
				constants.LabelAppInstance: cluster.Name,
			},
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}

	factory := newMockOpFactory()
	m := &mockMySQL{systemUserPlugins: make(map[string]string)}
	for _, user := range constants.MocoUsers {
		m.systemUserPlugins[user] = "mysql_native_password"
	}
	m.status.GlobalVariables.UUID = "p0"
	m.status.GlobalVariables.ReadOnly = false
	m.status.GlobalVariables.SuperReadOnly = false
	m.status.GlobalStatus = &dbop.GlobalStatus{}
	factory.mysqls[cluster.PodHostname(0)] = m

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mocov1beta2.MySQLCluster{}).
		WithObjects(cluster, secret, pod).
		Build()
	metrics.Register(prometheus.NewRegistry())
	name := types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}
	p := newManagerProcess(c, c, record.NewFakeRecorder(10), factory, nil, name, func() {})

	if _, err := p.do(context.Background()); err != nil {
		t.Fatalf("do returned an error: %v", err)
	}
	if got := m.authPluginCalls; got != 1 {
		t.Errorf("expected the healthy cluster to converge the plugin once, got %d calls", got)
	}
	for _, user := range constants.MocoUsers {
		if got := m.systemUserPlugins[user]; got != "caching_sha2_password" {
			t.Errorf("user %s: expected caching_sha2_password, got %s", user, got)
		}
	}
}
