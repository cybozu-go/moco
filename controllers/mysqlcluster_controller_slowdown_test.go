package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func testNewStoppedMySQLCluster(ns, name, configMapName string) *mocov1beta2.MySQLCluster {
	cluster := testNewMySQLCluster(ns)
	cluster.Name = name
	cluster.Finalizers = nil
	cluster.Annotations = map[string]string{constants.AnnReconciliationStopped: "true"}
	cluster.Spec.MySQLConfigMapName = new(configMapName)
	return cluster
}

func testDeleteAllMySQLClusters(ctx context.Context, ns string) {
	clusters := &mocov1beta2.MySQLClusterList{}
	err := k8sClient.List(ctx, clusters, client.InNamespace(ns))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	for _, cluster := range clusters.Items {
		if len(cluster.Finalizers) == 0 {
			continue
		}
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latest := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(&cluster), latest); err != nil {
				return client.IgnoreNotFound(err)
			}
			latest.Finalizers = nil
			return k8sClient.Update(ctx, latest)
		})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
	}
	err = k8sClient.DeleteAllOf(ctx, &mocov1beta2.MySQLCluster{}, client.InNamespace(ns))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	EventuallyWithOffset(1, func() (int, error) {
		clusters := &mocov1beta2.MySQLClusterList{}
		if err := k8sClient.List(ctx, clusters, client.InNamespace(ns)); err != nil {
			return 0, err
		}
		return len(clusters.Items), nil
	}).Should(BeZero())
}

var _ = Describe("MySQLCluster reconciler startup", func() {
	const largeScaleStartupObjectCount = 1000

	BeforeEach(func(ctx SpecContext) {
		testDeleteAllMySQLClusters(ctx, "test")
		err := k8sClient.DeleteAllOf(ctx, &corev1.ConfigMap{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func(ctx SpecContext) {
		testDeleteAllMySQLClusters(ctx, "test")
		err := k8sClient.DeleteAllOf(ctx, &corev1.ConfigMap{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
	})

	It("should start with many MySQLClusters and ConfigMaps", func(ctx SpecContext) {
		By("creating many MySQLClusters and ConfigMaps before the manager starts")
		for i := range largeScaleStartupObjectCount {
			configMapName := fmt.Sprintf("startup-config-%04d", i)
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: configMapName},
				Data:       map[string]string{"my.cnf": "[mysqld]\n"},
			}
			err := k8sClient.Create(ctx, cm)
			Expect(err).NotTo(HaveOccurred())

			cluster := testNewStoppedMySQLCluster("test", fmt.Sprintf("startup-%04d", i), configMapName)
			err = k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())
		}

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:         scheme,
			LeaderElection: false,
			Metrics: metricsserver.Options{
				BindAddress: "0",
			},
			Controller: config.Controller{
				SkipNameValidation: new(true),
			},
		})
		Expect(err).ToNot(HaveOccurred())

		mockMgr := &mockManager{
			clusters: make(map[string]struct{}),
		}
		mysqlr := &MySQLClusterReconciler{
			Client:                     mgr.GetClient(),
			Scheme:                     scheme,
			Recorder:                   mgr.GetEventRecorderFor("moco-controller"),
			SystemNamespace:            testMocoSystemNamespace,
			ClusterManager:             mockMgr,
			AgentImage:                 testAgentImage,
			BackupImage:                testBackupImage,
			FluentBitImage:             testFluentBitImage,
			ExporterImage:              testExporterImage,
			MySQLConfigMapHistoryLimit: 2,
		}
		err = mysqlr.SetupWithManager(ctx, mgr)
		Expect(err).ToNot(HaveOccurred())

		startCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			defer GinkgoRecover()
			err := mgr.Start(startCtx)
			Expect(err).NotTo(HaveOccurred())
		}()

		By("waiting for the controller to start and process an initial cluster event")
		start := time.Now()
		Eventually(func() error {
			if !mockMgr.getKeys()["test/startup-0000"] {
				GinkgoLogr.Info("controller has not processed the startup cluster yet")
				return errors.New("controller has not processed the startup cluster yet")
			}
			return nil
		}, 15*time.Second, 2*time.Second).Should(Succeed())
		GinkgoLogr.Info("controller has processed the startup cluster", "elapsed", time.Since(start))
	})
})
