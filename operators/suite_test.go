package operators

import (
	"context"
	"log" // restrictpkg:ignore to suppress mysql client logs.
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/cybozu-go/moco/accessor"
	agentmock "github.com/cybozu-go/moco/agentrpc/mock"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/cybozu-go/moco/test_utils"
	"github.com/go-sql-driver/mysql"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var ctx = context.Background()

const (
	mysqldName1     = "moco-operators-test-mysqld-1"
	mysqldName2     = "moco-operators-test-mysqld-2"
	mysqldName3     = "moco-operators-test-mysqld-3"
	mysqldPort1     = 3309
	mysqldPort2     = 3310
	mysqldPort3     = 3311
	mysqldServerID1 = 1001
	mysqldServerID2 = 1002
	mysqldServerID3 = 1003
	systemNamespace = "test-moco-system"
	namespace       = "test-namespace"
	token           = "test-token"
)

func TestOperators(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Operator Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func(done Done) {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	mysql.SetLogger(mysql.Logger(log.New(GinkgoWriter, "[mysql] ", log.Ldate|log.Ltime|log.Lshortfile)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	sch := runtime.NewScheme()
	err = clientgoscheme.AddToScheme(sch)
	Expect(err).NotTo(HaveOccurred())
	err = mocov1alpha1.AddToScheme(sch)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: sch})
	Expect(err).ToNot(HaveOccurred())
	Expect(k8sClient).ToNot(BeNil())

	agentmock.Start(ctx)

	test_utils.StopAndRemoveMySQLD(mysqldName1)
	test_utils.StopAndRemoveMySQLD(mysqldName2)
	test_utils.StopAndRemoveMySQLD(mysqldName3)
	test_utils.RemoveNetwork()

	Eventually(func() error {
		return test_utils.CreateNetwork()
	}, 20*time.Second).Should(Succeed())

	close(done)
}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
	ctx.Done()

	test_utils.StopAndRemoveMySQLD(mysqldName1)
	test_utils.StopAndRemoveMySQLD(mysqldName2)
	test_utils.StopAndRemoveMySQLD(mysqldName3)
	test_utils.RemoveNetwork()
})

var _ = Describe("Test Operators", func() {
	Context("clone", testClone)
	Context("configureIntermediatePrimary", testConfigureIntermediatePrimary)
	Context("configureReplication", testConfigureReplication)
	Context("setCloneDonorList", testSetCloneDonorList)
	Context("setLables", testSetLabels)
	Context("stopReplicaIOThread", testStopReplicaIOThread)
	Context("turnOffReadOnly", testTurnOffReadOnly)
	Context("updatePrimary", testUpdatePrimary)
})

func getAccessorInfraCluster() (*accessor.MySQLAccessor, accessor.Infrastructure, mocov1alpha1.MySQLCluster) {
	agentAcc := accessor.NewAgentAccessor()
	dbAcc := accessor.NewMySQLAccessor(&accessor.MySQLAccessorConfig{
		ConnMaxLifeTime:   30 * time.Minute,
		ConnectionTimeout: 3 * time.Second,
		ReadTimeout:       30 * time.Second,
	})
	inf := accessor.NewInfrastructure(k8sClient, agentAcc, dbAcc, test_utils.OperatorAdminUserPassword,
		[]string{test_utils.Host + ":" + strconv.Itoa(mysqldPort1), test_utils.Host + ":" + strconv.Itoa(mysqldPort2)},
		[]string{test_utils.Host + ":" + strconv.Itoa(test_utils.AgentPort), test_utils.Host + ":" + strconv.Itoa(test_utils.AgentPort)})
	primaryIndex := 0
	cluster := mocov1alpha1.MySQLCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			ClusterName: "test-cluster",
			Namespace:   namespace,
			UID:         "test-uid",
		},
		Spec: mocov1alpha1.MySQLClusterSpec{
			Replicas: 2,
			PodTemplate: mocov1alpha1.PodTemplateSpec{
				ObjectMeta: mocov1alpha1.ObjectMeta{
					Name: "test",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test",
							Image: "test",
						},
					},
				},
			},
			VolumeClaimTemplates: []mocov1alpha1.PersistentVolumeClaim{
				{
					ObjectMeta: mocov1alpha1.ObjectMeta{
						Name: "test",
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						VolumeName: "test",
					},
				},
			},
		},
		Status: mocov1alpha1.MySQLClusterStatus{
			CurrentPrimaryIndex: &primaryIndex,
			AgentToken:          token,
		},
	}

	return dbAcc, inf, cluster
}
