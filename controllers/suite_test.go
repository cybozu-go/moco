package controllers

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var scheme = runtime.NewScheme()
var cancelFunc context.CancelFunc

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	SetDefaultEventuallyTimeout(20 * time.Second)
	SetDefaultEventuallyPollingInterval(100 * time.Millisecond)
	SetDefaultConsistentlyDuration(3 * time.Second)
	SetDefaultConsistentlyPollingInterval(100 * time.Millisecond)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.StacktraceLevel(zapcore.DPanicLevel)))

	ctx, cancel := context.WithCancel(context.Background())
	cancelFunc = cancel

	var err error

	err = clientgoscheme.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	err = mocov1beta1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	err = mocov1beta2.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			// Use CRD with conversion webhook enabled for testing.
			filepath.Join("..", "config", "crd", "tests"),
			filepath.Join("..", "config", "crd", "tests", "third"),
		},
		CRDInstallOptions: envtest.CRDInstallOptions{
			Scheme: scheme,
		},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	webhookInstallOptions := &testEnv.CRDInstallOptions.WebhookOptions
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:             scheme,
		Host:               webhookInstallOptions.LocalServingHost,
		Port:               webhookInstallOptions.LocalServingPort,
		CertDir:            webhookInstallOptions.LocalServingCertDir,
		LeaderElection:     false,
		MetricsBindAddress: "0",
	})
	Expect(err).NotTo(HaveOccurred())

	// enable conversion webhook
	err = (&mocov1beta1.MySQLCluster{}).SetupWebhookWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// prepare resources
	ns := &corev1.Namespace{}
	ns.Name = testMocoSystemNamespace
	err = k8sClient.Create(context.Background(), ns)
	Expect(err).NotTo(HaveOccurred())

	ns = &corev1.Namespace{}
	ns.Name = "test"
	err = k8sClient.Create(context.Background(), ns)
	Expect(err).NotTo(HaveOccurred())

	// start conversion webhook server
	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		if err != nil {
			Expect(err).NotTo(HaveOccurred())
		}
	}()

	// wait for the webhook server to get ready
	dialer := &net.Dialer{Timeout: time.Second}
	addrPort := fmt.Sprintf("%s:%d", webhookInstallOptions.LocalServingHost, webhookInstallOptions.LocalServingPort)
	Eventually(func() error {
		conn, err := tls.DialWithDialer(dialer, "tcp", addrPort, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			return err
		}
		conn.Close()
		return nil
	}).Should(Succeed())

}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancelFunc()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
