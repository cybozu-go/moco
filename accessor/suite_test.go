package accessor

import (
	"fmt"
	"log" // restrictpkg:ignore to suppress mysql client logs.
	"testing"
	"time"

	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/cybozu-go/moco/test_utils"
	"github.com/go-sql-driver/mysql"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment

const (
	namespace      = "test-namespace"
	mysqldName     = "moco-accessor-test"
	mysqldPort     = 13306
	mysqldServerID = 1
)

func TestAccessors(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Accessor Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func(done Done) {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	mysql.SetLogger(mysql.Logger(log.New(GinkgoWriter, "[mysql] ", log.Ldate|log.Ltime|log.Lshortfile)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	sch := runtime.NewScheme()
	err = clientgoscheme.AddToScheme(sch)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: sch})
	Expect(err).ToNot(HaveOccurred())
	Expect(k8sClient).ToNot(BeNil())

	By("starting MySQL server")
	test_utils.StopAndRemoveMySQLD(mysqldName)
	test_utils.RemoveNetwork()

	Eventually(func() error {
		return test_utils.CreateNetwork()
	}, 10*time.Second).Should(Succeed())
	err = test_utils.StartMySQLD(mysqldName, mysqldPort, mysqldServerID)
	Expect(err).ShouldNot(HaveOccurred())

	By("initializing MySQL server")
	err = test_utils.InitializeMySQL(mysqldPort)
	Expect(err).ShouldNot(HaveOccurred())
	err = test_utils.StartSlaveWithInvalidSettings(mysqldPort)
	Expect(err).ShouldNot(HaveOccurred())

	close(done)
}, 60)

var _ = AfterSuite(func() {
	By("stopping MySQL server")
	test_utils.StopAndRemoveMySQLD(mysqldName)
	test_utils.RemoveNetwork()

	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
})

func getAccessorInfraCluster() (*MySQLAccessor, Infrastructure, mocov1alpha1.MySQLCluster) {
	agentAcc := NewAgentAccessor()
	dbAcc := NewMySQLAccessor(&MySQLAccessorConfig{
		ConnMaxLifeTime:   30 * time.Minute,
		ConnectionTimeout: 3 * time.Second,
		ReadTimeout:       30 * time.Second,
	})
	inf := NewInfrastructure(k8sClient, agentAcc, dbAcc, test_utils.OperatorAdminUserPassword, []string{fmt.Sprintf("%s:%d", test_utils.Host, mysqldPort)}, []string{fmt.Sprintf("%s:%d", test_utils.Host, test_utils.AgentPort)})
	cluster := mocov1alpha1.MySQLCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			ClusterName: "test-cluster",
			Namespace:   namespace,
			UID:         "test-uid",
		},
		Spec: mocov1alpha1.MySQLClusterSpec{
			Replicas: 1,
		},
	}

	return dbAcc, inf, cluster
}
