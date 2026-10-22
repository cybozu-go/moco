package clustering

import (
	"context"
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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// nopOperatorFactory mimics the production factory for a cluster whose Pods do
// not exist: defaultFactory.New returns a NopOperator, not an error, when the
// instance address cannot be resolved.
type nopOperatorFactory struct{}

func (nopOperatorFactory) New(context.Context, *mocov1beta2.MySQLCluster, *password.MySQLPassword, int) (dbop.Operator, error) {
	return dbop.NopOperator{}, nil
}

func (nopOperatorFactory) Cleanup() {}

func newOfflineTestProcess(t *testing.T, replicas int32, objs ...client.Object) (*managerProcess, client.Client) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	if err := mocov1beta2.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	cluster := &mocov1beta2.MySQLCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: mocov1beta2.MySQLClusterSpec{
			Replicas: replicas,
			Offline:  true,
		},
	}

	passwd, err := password.NewMySQLPassword()
	if err != nil {
		t.Fatalf("failed to generate a password: %v", err)
	}
	secret := passwd.ToSecret()
	secret.Namespace = cluster.Namespace
	secret.Name = cluster.UserSecretName()

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&mocov1beta2.MySQLCluster{}).
		WithObjects(append([]client.Object{cluster, secret}, objs...)...).
		Build()

	metrics.Register(prometheus.NewRegistry())

	name := types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}
	return newManagerProcess(c, c, record.NewFakeRecorder(10), nopOperatorFactory{}, nil, name, func() {}), c
}

// An offline MySQLCluster has no mysqld Pods. GatherStatus skips the Pod count
// check when spec.offline is true, so StatusSet.Pods is left as a slice of nil
// pointers. do() must not dereference them.
func TestDoWithOfflineClusterHavingNoPods(t *testing.T) {
	p, _ := newOfflineTestProcess(t, 1)
	ctx := context.Background()

	ss, err := p.GatherStatus(ctx)
	if err != nil {
		t.Fatalf("GatherStatus returned an error: %v", err)
	}
	defer ss.Close()

	if ss.State != StateOffline {
		t.Errorf("expected the state to be %v, got %v", StateOffline, ss.State)
	}
	if len(ss.Pods) != 1 || ss.Pods[0] != nil {
		t.Fatalf("expected Pods to hold exactly one nil entry, got %v", ss.Pods)
	}

	// Without the fix, this panics with a nil pointer dereference.
	redo, err := p.do(ctx)
	if err != nil {
		t.Errorf("do returned an error: %v", err)
	}
	if redo {
		t.Error("do returned redo=true for an offline cluster")
	}

	// addAnnPreventDelete must not dereference a missing primary Pod either.
	if err := p.addAnnPreventDelete(ctx, ss); err != nil {
		t.Errorf("addAnnPreventDelete returned an error: %v", err)
	}
}

// While an offline cluster is being scaled down, the remaining Pods may still
// have the prevent-delete annotation. It must be removed; otherwise the Pod
// deletion is denied and the scale-down never completes.
func TestDoRemovesPreventDeleteFromOfflineClusterPods(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "moco-test-0",
			Namespace: "default",
			Labels: map[string]string{
				constants.LabelAppName:     constants.AppNameMySQL,
				constants.LabelAppInstance: "test",
			},
			Annotations: map[string]string{
				constants.AnnPreventDelete: "true",
			},
		},
	}
	p, c := newOfflineTestProcess(t, 3, pod)
	ctx := context.Background()

	if _, err := p.do(ctx); err != nil {
		t.Fatalf("do returned an error: %v", err)
	}

	updated := &corev1.Pod{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(pod), updated); err != nil {
		t.Fatalf("failed to get pod: %v", err)
	}
	if _, exists := updated.Annotations[constants.AnnPreventDelete]; exists {
		t.Errorf("%s annotation was not removed", constants.AnnPreventDelete)
	}
}
