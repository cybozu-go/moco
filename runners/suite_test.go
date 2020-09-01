package runners

import (
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/cybozu-go/moco"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var mgr manager.Manager

func TestRunners(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Runner Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func(done Done) {
	logf.SetLogger(zap.LoggerTo(GinkgoWriter, true))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	sch := runtime.NewScheme()
	err = mocov1alpha1.AddToScheme(sch)
	Expect(err).NotTo(HaveOccurred())

	mgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: sch,
	})
	Expect(err).ToNot(HaveOccurred())

	err = mgr.GetFieldIndexer().IndexField(&mocov1alpha1.MySQLCluster{}, moco.InitializedClusterIndexField, selectInitializedCluster)
	Expect(err).ToNot(HaveOccurred())

	go mgr.Start(ctrl.SetupSignalHandler())

	k8sClient = mgr.GetClient()
	Expect(k8sClient).ToNot(BeNil())

	close(done)
}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
})

var _ = Describe("Test runners", func() {
	Context("cluster-watcher", testMySQLClusterWatcher)
})

func selectInitializedCluster(obj runtime.Object) []string {
	cluster := obj.(*mocov1alpha1.MySQLCluster)

	for _, cond := range cluster.Status.Conditions {
		if cond.Type == mocov1alpha1.ConditionInitialized {
			return []string{string(cond.Status)}
		}
	}
	return []string{string(corev1.ConditionUnknown)}
}
