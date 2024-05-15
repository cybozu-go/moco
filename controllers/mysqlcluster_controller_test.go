package controllers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const (
	testMocoSystemNamespace = "moco-system"
	testAgentImage          = "foobar:123"
	testBackupImage         = "backup:123"
	testFluentBitImage      = "fluent-hoge:134"
	testExporterImage       = "mysqld_exporter:111"
)

func testNewMySQLCluster(ns string) *mocov1beta2.MySQLCluster {
	cluster := &mocov1beta2.MySQLCluster{}
	cluster.Namespace = ns
	cluster.Name = "test"
	cluster.Finalizers = []string{constants.MySQLClusterFinalizer}
	cluster.Spec.Replicas = 3
	cluster.Spec.PodTemplate.Spec.Containers = []corev1ac.ContainerApplyConfiguration{
		*corev1ac.Container().WithName("mysqld").WithImage("moco-mysql:latest"),
	}
	cluster.Spec.VolumeClaimTemplates = []mocov1beta2.PersistentVolumeClaim{
		{
			ObjectMeta: mocov1beta2.ObjectMeta{Name: "mysql-data"},
			Spec: mocov1beta2.PersistentVolumeClaimSpecApplyConfiguration(*corev1ac.PersistentVolumeClaimSpec().
				WithStorageClassName("hoge").
				WithResources(
					corev1ac.VolumeResourceRequirements().WithRequests(
						corev1.ResourceList{corev1.ResourceStorage: *resource.NewQuantity(1<<30, resource.BinarySI)},
					),
				),
			),
		},
	}
	return cluster
}

func testDeleteMySQLCluster(ctx context.Context, ns, name string) {
	cluster := &mocov1beta2.MySQLCluster{}
	cluster.Namespace = ns
	cluster.Name = name
	err := k8sClient.Delete(ctx, cluster)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
}

var _ = Describe("MySQLCluster reconciler", func() {
	ctx := context.Background()
	var stopFunc func()
	var mockMgr *mockManager

	BeforeEach(func() {
		cs := &mocov1beta2.MySQLClusterList{}
		err := k8sClient.List(ctx, cs, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		for _, cluster := range cs.Items {
			cluster.Finalizers = nil
			err := k8sClient.Update(ctx, &cluster)
			Expect(err).NotTo(HaveOccurred())
		}
		svcs := &corev1.ServiceList{}
		err = k8sClient.List(ctx, svcs, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		for _, svc := range svcs.Items {
			err := k8sClient.Delete(ctx, &svc)
			Expect(err).NotTo(HaveOccurred())
		}
		err = k8sClient.DeleteAllOf(ctx, &mocov1beta2.MySQLCluster{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &appsv1.StatefulSet{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &corev1.Secret{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &corev1.ConfigMap{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &corev1.ServiceAccount{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &policyv1.PodDisruptionBudget{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:         scheme,
			LeaderElection: false,
			Metrics: metricsserver.Options{
				BindAddress: "0",
			},
		})
		Expect(err).ToNot(HaveOccurred())

		mockMgr = &mockManager{
			clusters: make(map[string]struct{}),
		}
		mysqlr := &MySQLClusterReconciler{
			Client:          mgr.GetClient(),
			Scheme:          scheme,
			Recorder:        mgr.GetEventRecorderFor("moco-controller"),
			SystemNamespace: testMocoSystemNamespace,
			ClusterManager:  mockMgr,
			AgentImage:      testAgentImage,
			BackupImage:     testBackupImage,
			FluentBitImage:  testFluentBitImage,
			ExporterImage:   testExporterImage,
		}
		err = mysqlr.SetupWithManager(mgr)
		Expect(err).ToNot(HaveOccurred())

		ctx, cancel := context.WithCancel(ctx)
		stopFunc = cancel
		go func() {
			err := mgr.Start(ctx)
			if err != nil {
				panic(err)
			}
		}()
		time.Sleep(100 * time.Millisecond)
	})

	AfterEach(func() {
		stopFunc()
		time.Sleep(100 * time.Millisecond)
	})

	It("should create password secrets", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			secret := &corev1.Secret{}
			key := client.ObjectKey{Namespace: testMocoSystemNamespace, Name: "mysql-test.test"}
			if err := k8sClient.Get(ctx, key, secret); err != nil {
				return err
			}

			if secret.Annotations["moco.cybozu.com/secret-version"] != "1" {
				return fmt.Errorf("the controller secret does not have an annotation for version")
			}

			if secret.Labels["app.kubernetes.io/name"] != "mysql" {
				return fmt.Errorf("the controller secret does not have the correct app name label: %s", secret.Labels["app.kubernetes.io/name"])
			}
			if secret.Labels["app.kubernetes.io/instance"] != "test" {
				return fmt.Errorf("the controller secret does not have the correct app instance label: %s", secret.Labels["app.kubernetes.io/instance"])
			}
			if secret.Labels["app.kubernetes.io/instance-namespace"] != "test" {
				return fmt.Errorf("the controller secret does not have the correct app namespace label: %s", secret.Labels["app.kubernetes.io/instance-namespace"])
			}

			userSecret := &corev1.Secret{}
			key = client.ObjectKey{Namespace: "test", Name: "moco-test"}
			if err := k8sClient.Get(ctx, key, userSecret); err != nil {
				return err
			}

			if userSecret.Annotations["moco.cybozu.com/secret-version"] != "1" {
				return fmt.Errorf("the user secret does not have an annotation for version")
			}
			if userSecret.Labels["app.kubernetes.io/name"] != "mysql" {
				return fmt.Errorf("the user secret does not have the correct app name label: %s", userSecret.Labels["app.kubernetes.io/name"])
			}
			if userSecret.Labels["app.kubernetes.io/instance"] != "test" {
				return fmt.Errorf("the user secret does not have the correct app instance label: %s", userSecret.Labels["app.kubernetes.io/instance"])
			}
			if len(userSecret.OwnerReferences) != 1 {
				return fmt.Errorf("the user secret does not have an owner reference")
			}

			mycnfSecret := &corev1.Secret{}
			key = client.ObjectKey{Namespace: "test", Name: "moco-my-cnf-test"}
			if err := k8sClient.Get(ctx, key, mycnfSecret); err != nil {
				return err
			}

			if mycnfSecret.Annotations["moco.cybozu.com/secret-version"] != "1" {
				return fmt.Errorf("the my.cnf secret does not have an annotation for version")
			}
			if mycnfSecret.Labels["app.kubernetes.io/name"] != "mysql" {
				return fmt.Errorf("the my.cnf secret does not have the correct app name label: %s", mycnfSecret.Labels["app.kubernetes.io/name"])
			}
			if mycnfSecret.Labels["app.kubernetes.io/instance"] != "test" {
				return fmt.Errorf("the my.cnf secret does not have the correct app instance label: %s", mycnfSecret.Labels["app.kubernetes.io/instance"])
			}
			if len(mycnfSecret.OwnerReferences) != 1 {
				return fmt.Errorf("the my.cnf secret does not have an owner reference")
			}

			return nil
		}).Should(Succeed())
	})

	It("should update user secret", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		var userSecret *corev1.Secret
		Eventually(func() error {
			userSecret = &corev1.Secret{}
			key := client.ObjectKey{Namespace: "test", Name: "moco-test"}
			return k8sClient.Get(ctx, key, userSecret)
		}).Should(Succeed())

		Expect(userSecret.OwnerReferences).NotTo(BeEmpty())

		userSecret.Data = nil
		err = k8sClient.Update(ctx, userSecret)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			userSecret = &corev1.Secret{}
			key := client.ObjectKey{Namespace: "test", Name: "moco-test"}
			if err := k8sClient.Get(ctx, key, userSecret); err != nil {
				return err
			}
			if len(userSecret.Data) == 0 {
				return fmt.Errorf("the user secret is not reconciled yet")
			}
			return nil
		})
	})

	It("should create certificate and copy secret", func() {
		By("creating a cluster")
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		var cert *unstructured.Unstructured
		Eventually(func() error {
			cert = certificateObj.DeepCopy()
			key := client.ObjectKey{Namespace: testMocoSystemNamespace, Name: "moco-agent-test.test"}
			return k8sClient.Get(ctx, key, cert)
		}).Should(Succeed())

		By("creating a TLS secret in the controller namespace")
		cs := &corev1.Secret{}
		cs.Namespace = testMocoSystemNamespace
		cs.Name = "moco-agent-test.test"
		cs.Data = map[string][]byte{"foo": []byte("bar")}
		err = k8sClient.Create(ctx, cs)
		Expect(err).NotTo(HaveOccurred())

		cert.SetAnnotations(map[string]string{"test": "foo"})
		err = k8sClient.Update(ctx, cert)
		Expect(err).NotTo(HaveOccurred())

		var us *corev1.Secret
		Eventually(func() error {
			us = &corev1.Secret{}
			return k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test-grpc"}, us)
		}).Should(Succeed())

		Expect(us.OwnerReferences).NotTo(BeEmpty())
		Expect(us.Labels).NotTo(BeEmpty())
		Expect(us.Data).To(HaveKeyWithValue("foo", []byte("bar")))

		By("updating the TLS secret in the controller namespace")
		cs.Data["foo"] = []byte("baz")
		err = k8sClient.Update(ctx, cs)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			cert := certificateObj.DeepCopy()
			key := client.ObjectKey{Namespace: testMocoSystemNamespace, Name: "moco-agent-test.test"}
			if err := k8sClient.Get(ctx, key, cert); err != nil {
				return err
			}
			cert.SetAnnotations(map[string]string{"test": "bar"})
			return k8sClient.Update(ctx, cert)
		}).Should(Succeed())

		Eventually(func() []byte {
			us = &corev1.Secret{}
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test-grpc"}, us)
			if err != nil {
				return nil
			}
			return us.Data["foo"]
		}).Should(Equal([]byte("baz")))
	})

	It("should create config maps for fluent-bit", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		var slowCM *corev1.ConfigMap
		Eventually(func() error {
			slowCM = &corev1.ConfigMap{}
			key := client.ObjectKey{Namespace: "test", Name: "moco-slow-log-agent-config-test"}
			return k8sClient.Get(ctx, key, slowCM)
		}).Should(Succeed())

		Expect(slowCM.OwnerReferences).NotTo(BeEmpty())

		slowCM.Data = nil
		err = k8sClient.Update(ctx, slowCM)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			slowCM = &corev1.ConfigMap{}
			key := client.ObjectKey{Namespace: "test", Name: "moco-slow-log-agent-config-test"}
			if err := k8sClient.Get(ctx, key, slowCM); err != nil {
				return err
			}
			if len(slowCM.Data) == 0 {
				return fmt.Errorf("the config map is not reconciled yet")
			}
			return nil
		}).Should(Succeed())

		cluster = &mocov1beta2.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
		Expect(err).NotTo(HaveOccurred())
		cluster.Spec.DisableSlowQueryLogContainer = true
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() bool {
			slowCM = &corev1.ConfigMap{}
			key := client.ObjectKey{Namespace: "test", Name: "moco-slow-log-agent-config-test"}
			err := k8sClient.Get(ctx, key, slowCM)
			return apierrors.IsNotFound(err)
		}).Should(BeTrue())
	})

	It("should create config maps for my.cnf", func() {
		cluster := testNewMySQLCluster("test")
		cluster.Spec.PodTemplate.Spec.Containers[0].WithResources(
			corev1ac.ResourceRequirements().WithLimits(corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("1000Mi"),
			}))
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			c := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, c); err != nil {
				return err
			}
			if c.Status.ReconcileInfo.Generation != c.Generation {
				return fmt.Errorf("not yet reconciled: generation=%d", c.Status.ReconcileInfo.Generation)
			}
			return nil
		}).Should(Succeed())

		var cm *corev1.ConfigMap
		Eventually(func() error {
			cms := &corev1.ConfigMapList{}
			if err := k8sClient.List(ctx, cms, client.InNamespace("test")); err != nil {
				return err
			}

			var mycnfCMs []*corev1.ConfigMap
			for i, cm := range cms.Items {
				if strings.HasPrefix(cm.Name, "moco-test.") {
					mycnfCMs = append(mycnfCMs, &cms.Items[i])
				}
			}

			if len(mycnfCMs) != 1 {
				return fmt.Errorf("the number of config maps is not 1: %d", len(mycnfCMs))
			}

			cm = mycnfCMs[0]
			return nil
		}).Should(Succeed())

		Expect(cm.OwnerReferences).NotTo(BeEmpty())
		Expect(cm.Data).To(HaveKey("my.cnf"))
		Expect(cm.Data["my.cnf"]).To(ContainSubstring("innodb_buffer_pool_size = 734003200"))

		userCM := &corev1.ConfigMap{}
		userCM.Namespace = "test"
		userCM.Name = "user-conf"
		userCM.Data = map[string]string{
			"foo":                                "bar",
			constants.LowerCaseTableNamesConfKey: "1",
		}

		err = k8sClient.Create(ctx, userCM)
		Expect(err).NotTo(HaveOccurred())

		cluster = &mocov1beta2.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
		Expect(err).NotTo(HaveOccurred())
		cluster.Spec.MySQLConfigMapName = ptr.To[string](userCM.Name)
		cluster.Spec.PodTemplate.Spec.Containers[0].Resources.WithRequests(corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("500Mi"),
		})
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		oldName := cm.Name
		Eventually(func() error {
			cms := &corev1.ConfigMapList{}
			if err := k8sClient.List(ctx, cms, client.InNamespace("test")); err != nil {
				return err
			}

			var mycnfCMs []*corev1.ConfigMap
			for i, cm := range cms.Items {
				if cm.Name == oldName {
					continue
				}
				if strings.HasPrefix(cm.Name, "moco-test.") {
					mycnfCMs = append(mycnfCMs, &cms.Items[i])
				}
			}

			if len(mycnfCMs) != 1 {
				return fmt.Errorf("the number of config maps is not 1: %d", len(mycnfCMs))
			}

			cm = mycnfCMs[0]
			return nil
		}).Should(Succeed())

		Expect(cm.Data["my.cnf"]).To(ContainSubstring("foo = bar"))
		Expect(cm.Data["my.cnf"]).To(ContainSubstring("lower_case_table_names = 1"))
		Expect(cm.Data["my.cnf"]).To(ContainSubstring("innodb_buffer_pool_size = 367001600"))

		userCM = &corev1.ConfigMap{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "user-conf"}, userCM)
		Expect(err).NotTo(HaveOccurred())
		userCM.Data["foo"] = "baz"
		err = k8sClient.Update(ctx, userCM)
		Expect(err).NotTo(HaveOccurred())

		oldName = cm.Name
		Eventually(func() error {
			cms := &corev1.ConfigMapList{}
			if err := k8sClient.List(ctx, cms, client.InNamespace("test")); err != nil {
				return err
			}

			var mycnfCMs []*corev1.ConfigMap
			for i, cm := range cms.Items {
				if cm.Name == oldName {
					continue
				}
				if strings.HasPrefix(cm.Name, "moco-test.") {
					mycnfCMs = append(mycnfCMs, &cms.Items[i])
				}
			}

			if len(mycnfCMs) != 1 {
				return fmt.Errorf("the number of config maps is not 1: %d", len(mycnfCMs))
			}

			cm = mycnfCMs[0]
			return nil
		}).Should(Succeed())

		Expect(cm.Data["my.cnf"]).To(ContainSubstring("foo = baz"))
	})

	It("should reconcile service account", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		var sa *corev1.ServiceAccount
		Eventually(func() error {
			sa = &corev1.ServiceAccount{}
			return k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test"}, sa)
		}).Should(Succeed())

		Expect(sa.OwnerReferences).NotTo(BeEmpty())
	})

	It("should reconcile services", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		var headless, primary, replica *corev1.Service
		Eventually(func() error {
			headless = &corev1.Service{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test"}, headless); err != nil {
				return err
			}
			primary = &corev1.Service{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test-primary"}, primary); err != nil {
				return err
			}
			replica = &corev1.Service{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test-replica"}, replica); err != nil {
				return err
			}
			return nil
		}).Should(Succeed())

		Expect(headless.OwnerReferences).NotTo(BeEmpty())
		Expect(primary.OwnerReferences).NotTo(BeEmpty())
		Expect(replica.OwnerReferences).NotTo(BeEmpty())

		Expect(headless.Spec.ClusterIP).To(Equal("None"))
		Expect(primary.Spec.ClusterIP).NotTo(Equal("None"))
		Expect(primary.Spec.ClusterIP).NotTo(BeEmpty())
		Expect(replica.Spec.ClusterIP).NotTo(Equal("None"))
		Expect(replica.Spec.ClusterIP).NotTo(BeEmpty())

		Expect(headless.Spec.Selector).NotTo(HaveKey("moco.cybozu.com/role"))
		Expect(primary.Spec.Selector).To(HaveKeyWithValue("moco.cybozu.com/role", "primary"))
		Expect(replica.Spec.Selector).To(HaveKeyWithValue("moco.cybozu.com/role", "replica"))

		Expect(headless.Spec.PublishNotReadyAddresses).To(BeTrue())

		Eventually(func() error {
			cluster = &mocov1beta2.MySQLCluster{}
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
			if err != nil {
				return err
			}
			cluster.Spec.PrimaryServiceTemplate = &mocov1beta2.ServiceTemplate{
				ObjectMeta: mocov1beta2.ObjectMeta{
					Annotations: map[string]string{"foo": "bar"},
					Labels:      map[string]string{"foo": "baz"},
				},
			}
			cluster.Spec.ReplicaServiceTemplate = &mocov1beta2.ServiceTemplate{
				ObjectMeta: mocov1beta2.ObjectMeta{
					Annotations: map[string]string{"qux": "quux"},
					Labels:      map[string]string{"qux": "corge"},
				},
			}
			return k8sClient.Update(ctx, cluster)
		}).Should(Succeed())

		Eventually(func() error {
			primary = &corev1.Service{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test-primary"}, primary); err != nil {
				return err
			}
			if primary.Annotations["foo"] != "bar" {
				return errors.New("no annotation")
			}
			if primary.Labels["foo"] != "baz" {
				return errors.New("no label")
			}

			replica = &corev1.Service{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test-replica"}, replica); err != nil {
				return err
			}
			if replica.Annotations["qux"] != "quux" {
				return errors.New("no annotation")
			}
			if replica.Labels["qux"] != "corge" {
				return errors.New("no label")
			}

			return nil
		}).Should(Succeed())

		Eventually(func() error {
			cluster = &mocov1beta2.MySQLCluster{}
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
			if err != nil {
				return err
			}

			svcSpec := mocov1beta2.ServiceSpecApplyConfiguration(*corev1ac.ServiceSpec().
				WithType(corev1.ServiceTypeLoadBalancer).
				WithExternalTrafficPolicy(corev1.ServiceExternalTrafficPolicyTypeLocal))

			cluster.Spec.PrimaryServiceTemplate = &mocov1beta2.ServiceTemplate{
				Spec: &svcSpec,
			}
			cluster.Spec.ReplicaServiceTemplate = &mocov1beta2.ServiceTemplate{
				Spec: &svcSpec,
			}
			return k8sClient.Update(ctx, cluster)
		}).Should(Succeed())

		Eventually(func() error {
			primary = &corev1.Service{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test-primary"}, primary); err != nil {
				return err
			}
			if primary.Spec.Type != corev1.ServiceTypeLoadBalancer {
				return errors.New("service type is not updated")
			}

			replica = &corev1.Service{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test-replica"}, replica); err != nil {
				return err
			}
			if replica.Spec.Type != corev1.ServiceTypeLoadBalancer {
				return errors.New("service type is not updated")
			}

			return nil
		}).Should(Succeed())

		headless = &corev1.Service{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test"}, headless)
		Expect(err).NotTo(HaveOccurred())
		Expect(headless.Spec.ExternalTrafficPolicy).NotTo(Equal(corev1.ServiceExternalTrafficPolicyTypeLocal))

		// Edit Service again should succeed
		Eventually(func() error {
			cluster = &mocov1beta2.MySQLCluster{}
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
			if err != nil {
				return err
			}

			svcSpec := mocov1beta2.ServiceSpecApplyConfiguration(*corev1ac.ServiceSpec().
				WithType(corev1.ServiceTypeLoadBalancer).
				WithExternalTrafficPolicy(corev1.ServiceExternalTrafficPolicyTypeLocal))

			cluster.Spec.PrimaryServiceTemplate = &mocov1beta2.ServiceTemplate{
				ObjectMeta: mocov1beta2.ObjectMeta{
					Annotations: map[string]string{"foo": "bar"},
				},
				Spec: &svcSpec,
			}
			cluster.Spec.ReplicaServiceTemplate = &mocov1beta2.ServiceTemplate{
				ObjectMeta: mocov1beta2.ObjectMeta{
					Annotations: map[string]string{"qux": "quux"},
				},
				Spec: &svcSpec,
			}
			return k8sClient.Update(ctx, cluster)
		}).Should(Succeed())

		Eventually(func() error {
			primary = &corev1.Service{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test-primary"}, primary); err != nil {
				return err
			}
			if primary.Annotations["foo"] != "bar" {
				return errors.New("service does not have annotation foo")
			}

			replica = &corev1.Service{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test-replica"}, replica); err != nil {
				return err
			}
			if replica.Annotations["qux"] != "quux" {
				return errors.New("service does not have annotation foo")
			}
			return nil
		}).Should(Succeed())
	})

	It("should reconcile statefulset", func() {
		cluster := testNewMySQLCluster("test")
		cluster.Spec.ReplicationSourceSecretName = ptr.To[string]("source-secret")
		cluster.Spec.PodTemplate.Annotations = map[string]string{"foo": "bar"}
		cluster.Spec.PodTemplate.Labels = map[string]string{"foo": "baz"}

		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		var sts *appsv1.StatefulSet
		Eventually(func() error {
			sts = &appsv1.StatefulSet{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test"}, sts); err != nil {
				return err
			}

			cluster = &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster); err != nil {
				return err
			}
			if cluster.Status.ReconcileInfo.Generation != cluster.Generation {
				return fmt.Errorf("status is not updated")
			}

			return nil
		}).Should(Succeed())

		By("checking new statefulset")
		Expect(sts.OwnerReferences).NotTo(BeEmpty())
		Expect(sts.Spec.Template.Annotations).To(HaveKeyWithValue("foo", "bar"))
		Expect(sts.Spec.Template.Labels).To(HaveKeyWithValue("foo", "baz"))
		Expect(sts.Spec.Replicas).NotTo(BeNil())
		Expect(*sts.Spec.Replicas).To(Equal(cluster.Spec.Replicas))
		Expect(sts.Spec.Template.Spec.TerminationGracePeriodSeconds).NotTo(BeNil())
		Expect(*sts.Spec.Template.Spec.TerminationGracePeriodSeconds).To(BeNumerically("==", defaultTerminationGracePeriodSeconds))
		Expect(sts.Spec.Template.Spec.SecurityContext).NotTo(BeNil())
		Expect(*sts.Spec.Template.Spec.SecurityContext.FSGroup).To(Equal(int64(constants.ContainerGID)))
		Expect(*sts.Spec.Template.Spec.SecurityContext.FSGroupChangePolicy).To(Equal(corev1.FSGroupChangeOnRootMismatch))
		Expect(sts.Spec.Template.Spec.Affinity).NotTo(BeNil())
		Expect(sts.Spec.Template.Spec.Affinity.PodAntiAffinity).NotTo(BeNil())
		Expect(sts.Spec.Template.Spec.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution).NotTo(BeNil())

		Expect(sts.Spec.Template.Spec.Containers).To(HaveLen(3))
		foundMysqld := false
		foundAgent := false
		foundSlowLogAgent := false
		foundExporter := false
		for _, c := range sts.Spec.Template.Spec.Containers {
			Expect(c.SecurityContext).NotTo(BeNil())
			Expect(c.SecurityContext.RunAsUser).NotTo(BeNil())
			Expect(*c.SecurityContext.RunAsUser).To(Equal(int64(constants.ContainerUID)))
			Expect(c.SecurityContext.RunAsGroup).NotTo(BeNil())
			Expect(*c.SecurityContext.RunAsGroup).To(Equal(int64(constants.ContainerGID)))
			switch c.Name {
			case constants.MysqldContainerName:
				foundMysqld = true
				Expect(c.Image).To(Equal("moco-mysql:latest"))
				Expect(c.StartupProbe).NotTo(BeNil())
				Expect(c.StartupProbe.FailureThreshold).To(Equal(int32(360)))
				Expect(c.SecurityContext.ReadOnlyRootFilesystem).To(BeNil())
			case constants.AgentContainerName:
				foundAgent = true
				Expect(c.Image).To(Equal(testAgentImage))
				Expect(c.Args).To(Equal([]string{"--max-delay", "60s"}))
				Expect(c.Resources.Requests).To(Equal(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("100Mi")}))
				Expect(c.Resources.Limits).To(Equal(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("100Mi")}))
			case constants.SlowQueryLogAgentContainerName:
				foundSlowLogAgent = true
				Expect(c.Image).To(Equal(testFluentBitImage))
				Expect(c.Resources.Requests).To(Equal(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("20Mi")}))
				Expect(c.Resources.Limits).To(Equal(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("20Mi")}))
			case constants.ExporterContainerName:
				foundExporter = true
				Expect(c.Resources.Requests).To(Equal(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m"), corev1.ResourceMemory: resource.MustParse("100Mi")}))
				Expect(c.Resources.Limits).To(Equal(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m"), corev1.ResourceMemory: resource.MustParse("100Mi")}))
			}
		}
		Expect(foundMysqld).To(BeTrue())
		Expect(foundAgent).To(BeTrue())
		Expect(foundSlowLogAgent).To(BeTrue())
		Expect(foundExporter).To(BeFalse())

		Expect(sts.Spec.Template.Spec.InitContainers).To(HaveLen(2))

		cpInitContainer := &sts.Spec.Template.Spec.InitContainers[0]
		Expect(cpInitContainer.Name).To(Equal(constants.CopyInitContainerName))
		Expect(cpInitContainer.Image).To(Equal(testAgentImage))
		Expect(cpInitContainer.Command).To(ContainElement("cp"))
		Expect(cpInitContainer.SecurityContext).NotTo(BeNil())
		Expect(cpInitContainer.SecurityContext.RunAsUser).NotTo(BeNil())
		Expect(*cpInitContainer.SecurityContext.RunAsUser).To(Equal(int64(constants.ContainerUID)))
		Expect(cpInitContainer.SecurityContext.RunAsGroup).NotTo(BeNil())
		Expect(*cpInitContainer.SecurityContext.RunAsGroup).To(Equal(int64(constants.ContainerGID)))

		initContainer := &sts.Spec.Template.Spec.InitContainers[1]
		Expect(initContainer.Name).To(Equal(constants.InitContainerName))
		Expect(initContainer.Image).To(Equal("moco-mysql:latest"))
		Expect(initContainer.Command).To(ContainElement(fmt.Sprintf("%d", cluster.Spec.ServerIDBase)))
		Expect(initContainer.Resources.Requests).To(Equal(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("300Mi")}))
		Expect(initContainer.Resources.Limits).To(Equal(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("300Mi")}))
		Expect(initContainer.SecurityContext).NotTo(BeNil())
		Expect(initContainer.SecurityContext.RunAsUser).NotTo(BeNil())
		Expect(*initContainer.SecurityContext.RunAsUser).To(Equal(int64(constants.ContainerUID)))
		Expect(initContainer.SecurityContext.RunAsGroup).NotTo(BeNil())
		Expect(*initContainer.SecurityContext.RunAsGroup).To(Equal(int64(constants.ContainerGID)))

		Expect(sts.Spec.VolumeClaimTemplates).To(HaveLen(1))
		Expect(sts.Spec.VolumeClaimTemplates[0].Name).To(Equal(constants.MySQLDataVolumeName))

		foundUserSecret := false
		foundMyCnfConfig := false
		foundSlowLogConfig := false
		for _, v := range sts.Spec.Template.Spec.Volumes {
			switch v.Name {
			case constants.MySQLConfSecretVolumeName:
				foundUserSecret = true
			case constants.MySQLConfVolumeName:
				foundMyCnfConfig = true
			case constants.SlowQueryLogAgentConfigVolumeName:
				foundSlowLogConfig = true
			}
		}
		Expect(foundUserSecret).To(BeTrue())
		Expect(foundMyCnfConfig).To(BeTrue())
		Expect(foundSlowLogConfig).To(BeTrue())

		By("editing statefulset")
		// Sleep before editing statefulset to avoid slow query agent container from being restored by the controller
		time.Sleep(1 * time.Second)

		for i, c := range sts.Spec.Template.Spec.Containers {
			switch c.Name {
			case constants.AgentContainerName, constants.SlowQueryLogAgentContainerName:
				sts.Spec.Template.Spec.Containers[i].Image = "invalid"
			}
		}
		err = k8sClient.Update(ctx, sts)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			sts = &appsv1.StatefulSet{}
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test"}, sts)
			if err != nil {
				return err
			}
			for _, c := range sts.Spec.Template.Spec.Containers {
				if c.Name != constants.AgentContainerName {
					continue
				}
				if c.Image != testAgentImage {
					return errors.New("c.Image is not reconciled yet")
				}
			}
			return nil
		}).Should(Succeed())
		for _, c := range sts.Spec.Template.Spec.Containers {
			switch c.Name {
			case constants.SlowQueryLogAgentContainerName:
				Expect(c.Image).To(Equal("invalid"), c.Name)
			}
		}

		By("updating MySQLCluster")
		cluster = &mocov1beta2.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
		Expect(err).NotTo(HaveOccurred())

		cluster.Spec.Replicas = 5
		cluster.Spec.ReplicationSourceSecretName = nil
		cluster.Spec.Collectors = []string{"engine_innodb_status", "info_schema.innodb_metrics"}
		cluster.Spec.MaxDelaySeconds = ptr.To[int](20)
		cluster.Spec.StartupWaitSeconds = 3
		cluster.Spec.LogRotationSchedule = "0 * * * *"
		cluster.Spec.AgentUseLocalhost = true
		cluster.Spec.DisableSlowQueryLogContainer = true
		cluster.Spec.PodTemplate.OverwriteContainers = []mocov1beta2.OverwriteContainer{
			{
				Name: mocov1beta2.AgentContainerName,
				Resources: (*mocov1beta2.ResourceRequirementsApplyConfiguration)(corev1ac.ResourceRequirements().
					WithLimits(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}).
					WithRequests(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}),
				),
			},
			{
				Name: mocov1beta2.ExporterContainerName,
				Resources: (*mocov1beta2.ResourceRequirementsApplyConfiguration)(corev1ac.ResourceRequirements().
					WithLimits(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")}).
					WithRequests(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")}),
				),
			},
			{
				Name: mocov1beta2.InitContainerName,
				Resources: (*mocov1beta2.ResourceRequirementsApplyConfiguration)(corev1ac.ResourceRequirements().
					WithLimits(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("300m")}).
					WithRequests(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("300m")}),
				),
			},
		}

		podSpec := corev1ac.PodSpec().
			WithTerminationGracePeriodSeconds(512).
			WithPriorityClassName("hoge").
			WithContainers(corev1ac.Container().WithName("dummy").WithImage("dummy:latest")).
			WithInitContainers(corev1ac.Container().WithName("init-dummy").WithImage("init-dummy:latest").
				WithSecurityContext(corev1ac.SecurityContext().WithReadOnlyRootFilesystem(true))).
			WithVolumes(corev1ac.Volume().WithName("dummy-vol").WithEmptyDir(corev1ac.EmptyDirVolumeSource())).
			WithSecurityContext(corev1ac.PodSecurityContext().WithFSGroup(123)).
			WithAffinity(corev1ac.Affinity().
				WithPodAntiAffinity(corev1ac.PodAntiAffinity().
					WithRequiredDuringSchedulingIgnoredDuringExecution(corev1ac.PodAffinityTerm().
						WithLabelSelector(metav1ac.LabelSelector().
							WithMatchExpressions(metav1ac.LabelSelectorRequirement().
								WithKey(constants.LabelAppName).
								WithOperator(metav1.LabelSelectorOpIn).
								WithValues(constants.AppNameMySQL),
							).
							WithMatchExpressions(metav1ac.LabelSelectorRequirement().
								WithKey(constants.LabelAppInstance).
								WithOperator(metav1.LabelSelectorOpIn).
								WithValues(cluster.Name),
							),
						),
					),
				))

		for _, c := range cluster.Spec.PodTemplate.Spec.Containers {
			switch *c.Name {
			case constants.MysqldContainerName:
				c.WithSecurityContext(corev1ac.SecurityContext().WithReadOnlyRootFilesystem(true)).
					WithLivenessProbe(corev1ac.Probe().
						WithTerminationGracePeriodSeconds(int64(200)).
						WithHTTPGet(corev1ac.HTTPGetAction().
							WithPath("/healthz").
							WithPort(intstr.FromString(constants.MySQLHealthPortName)).
							WithScheme(corev1.URISchemeHTTP)))
			}
			podSpec.WithContainers(&c)
		}

		cluster.Spec.PodTemplate.Spec = mocov1beta2.PodSpecApplyConfiguration(*podSpec)

		userCM := &corev1.ConfigMap{}
		userCM.Namespace = "test"
		userCM.Name = "user-conf"
		userCM.Data = map[string]string{
			constants.LowerCaseTableNamesConfKey: "1",
		}

		err = k8sClient.Create(ctx, userCM)
		Expect(err).NotTo(HaveOccurred())

		cluster.Spec.MySQLConfigMapName = ptr.To[string](userCM.Name)

		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			c := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, c); err != nil {
				return err
			}
			if c.Status.ReconcileInfo.Generation != c.Generation {
				return fmt.Errorf("not yet reconciled: generation=%d", c.Status.ReconcileInfo.Generation)
			}
			return nil
		}).Should(Succeed())

		sts = &appsv1.StatefulSet{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test"}, sts)
		Expect(err).NotTo(HaveOccurred())

		Expect(sts.Spec.Replicas).NotTo(BeNil())
		Expect(*sts.Spec.Replicas).To(Equal(cluster.Spec.Replicas))
		Expect(sts.Spec.Template.Spec.TerminationGracePeriodSeconds).NotTo(BeNil())
		Expect(*sts.Spec.Template.Spec.TerminationGracePeriodSeconds).To(BeNumerically("==", 512))
		Expect(sts.Spec.Template.Spec.PriorityClassName).To(Equal("hoge"))
		Expect(*sts.Spec.Template.Spec.SecurityContext.FSGroup).To(Equal(int64(123)))
		Expect(sts.Spec.Template.Spec.Affinity).NotTo(BeNil())
		Expect(sts.Spec.Template.Spec.Affinity.PodAntiAffinity).NotTo(BeNil())
		Expect(sts.Spec.Template.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution).NotTo(BeNil())
		Expect(sts.Spec.Template.Spec.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution).To(BeNil())

		foundDummyContainer := false
		for _, c := range sts.Spec.Template.Spec.Containers {
			Expect(c.Name).NotTo(Equal(constants.SlowQueryLogAgentContainerName))
			Expect(c.SecurityContext).NotTo(BeNil())
			Expect(c.SecurityContext.RunAsUser).NotTo(BeNil())
			Expect(*c.SecurityContext.RunAsUser).To(Equal(int64(constants.ContainerUID)))
			Expect(c.SecurityContext.RunAsGroup).NotTo(BeNil())
			Expect(*c.SecurityContext.RunAsGroup).To(Equal(int64(constants.ContainerGID)))
			switch c.Name {
			case constants.MysqldContainerName:
				Expect(c.StartupProbe).NotTo(BeNil())
				Expect(c.StartupProbe.FailureThreshold).To(Equal(int32(1)))
				Expect(c.LivenessProbe).NotTo(BeNil())
				Expect(c.LivenessProbe.TerminationGracePeriodSeconds).To(Equal(ptr.To[int64](200)))
				Expect(c.SecurityContext.ReadOnlyRootFilesystem).NotTo(BeNil())
				Expect(*c.SecurityContext.ReadOnlyRootFilesystem).To(BeTrue())
			case constants.AgentContainerName:
				Expect(c.Args).To(ContainElement("20s"))
				Expect(c.Args).To(ContainElement("0 * * * *"))
				Expect(c.Args).To(ContainElements("--mysqld-localhost", "true"))
				Expect(c.Resources.Requests).To(Equal(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}))
				Expect(c.Resources.Limits).To(Equal(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}))
			case constants.ExporterContainerName:
				foundExporter = true
				Expect(c.Image).To(Equal(testExporterImage))
				Expect(c.Args).To(HaveLen(3))
				Expect(c.Resources.Requests).To(Equal(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")}))
				Expect(c.Resources.Limits).To(Equal(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")}))
			case "dummy":
				foundDummyContainer = true
				Expect(c.Image).To(Equal("dummy:latest"))
				Expect(c.SecurityContext.ReadOnlyRootFilesystem).To(BeNil())
			}
		}
		Expect(foundExporter).To(BeTrue())
		Expect(foundDummyContainer).To(BeTrue())

		foundInitDummyContainer := false
		for _, c := range sts.Spec.Template.Spec.InitContainers {
			Expect(c.SecurityContext).NotTo(BeNil())
			Expect(c.SecurityContext.RunAsUser).NotTo(BeNil())
			Expect(*c.SecurityContext.RunAsUser).To(Equal(int64(constants.ContainerUID)))
			Expect(c.SecurityContext.RunAsGroup).NotTo(BeNil())
			Expect(*c.SecurityContext.RunAsGroup).To(Equal(int64(constants.ContainerGID)))
			switch c.Name {
			case constants.InitContainerName:
				Expect(c.Args).To(ContainElement(fmt.Sprintf("%s=1", constants.MocoInitLowerCaseTableNamesFlag)))
				Expect(c.Resources.Requests).To(Equal(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("300m")}))
				Expect(c.Resources.Limits).To(Equal(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("300m")}))
			case "init-dummy":
				foundInitDummyContainer = true
				Expect(c.Image).To(Equal("init-dummy:latest"))
				Expect(c.SecurityContext.ReadOnlyRootFilesystem).NotTo(BeNil())
				Expect(*c.SecurityContext.ReadOnlyRootFilesystem).To(BeTrue())
			}
		}
		Expect(foundInitDummyContainer).To(BeTrue())

		foundDummyVolume := false
		for _, v := range sts.Spec.Template.Spec.Volumes {
			switch v.Name {
			case "dummy-vol":
				foundDummyVolume = true
			}
		}
		Expect(foundDummyVolume).To(BeTrue())

		By("updating MySQLCluster (MaxDelaySeconds=0)")
		cluster = &mocov1beta2.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
		Expect(err).NotTo(HaveOccurred())

		cluster.Spec.MaxDelaySeconds = ptr.To[int](0)

		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			c := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, c); err != nil {
				return err
			}
			if c.Status.ReconcileInfo.Generation != c.Generation {
				return fmt.Errorf("not yet reconciled: generation=%d", c.Status.ReconcileInfo.Generation)
			}
			return nil
		}).Should(Succeed())

		sts = &appsv1.StatefulSet{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test"}, sts)
		Expect(err).NotTo(HaveOccurred())

		for _, c := range sts.Spec.Template.Spec.Containers {
			switch c.Name {
			case constants.AgentContainerName:
				Expect(c.Args).To(ContainElement("0s"))
			}
		}
	})

	It("should reconcile a pod disruption budget", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			cluster = &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster); err != nil {
				return err
			}
			if cluster.Status.ReconcileInfo.Generation != cluster.Generation {
				return fmt.Errorf("not yet reconciled")
			}
			return nil
		}).Should(Succeed())

		var pdb *policyv1.PodDisruptionBudget
		Eventually(func() error {
			pdb = &policyv1.PodDisruptionBudget{}
			return k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.PrefixedName()}, pdb)
		}).Should(Succeed())

		Expect(pdb.Spec.MaxUnavailable).NotTo(BeNil())
		Expect(pdb.Spec.MaxUnavailable.IntVal).To(Equal(int32(1)))

		Eventually(func() error {
			cluster = &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster); err != nil {
				return err
			}
			if cluster.Status.ReconcileInfo.Generation != cluster.Generation {
				return fmt.Errorf("not yet reconciled")
			}
			return nil
		}).Should(Succeed())
		cluster.Spec.Replicas = 1
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() bool {
			pdb = &policyv1.PodDisruptionBudget{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.PrefixedName()}, pdb)
			return apierrors.IsNotFound(err)
		}).Should(BeTrue())
	})

	It("should reconcile backup related resources", func() {
		cluster := testNewMySQLCluster("test")
		cluster.Spec.BackupPolicyName = ptr.To[string]("test-policy")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		By("creating a backup policy")
		bp := &mocov1beta2.BackupPolicy{}
		bp.Namespace = "test"
		bp.Name = "test-policy"
		bp.Spec.ActiveDeadlineSeconds = ptr.To[int64](100)
		bp.Spec.BackoffLimit = ptr.To[int32](1)
		bp.Spec.ConcurrencyPolicy = batchv1.ForbidConcurrent
		bp.Spec.StartingDeadlineSeconds = ptr.To[int64](10)
		bp.Spec.Schedule = "*/5 * * * *"
		bp.Spec.SuccessfulJobsHistoryLimit = ptr.To[int32](1)
		bp.Spec.FailedJobsHistoryLimit = ptr.To[int32](2)
		jc := &bp.Spec.JobConfig
		jc.Threads = 3
		jc.ServiceAccountName = "foo"
		jc.CPU = resource.NewQuantity(1, resource.DecimalSI)
		jc.MaxCPU = resource.NewQuantity(4, resource.DecimalSI)
		jc.Memory = resource.NewQuantity(1<<30, resource.DecimalSI)
		jc.MaxMemory = resource.NewQuantity(10<<30, resource.DecimalSI)
		jc.Env = []mocov1beta2.EnvVarApplyConfiguration{{Name: ptr.To[string]("TEST"), Value: ptr.To[string]("123")}}
		jc.EnvFrom = []mocov1beta2.EnvFromSourceApplyConfiguration{
			{
				ConfigMapRef: &corev1ac.ConfigMapEnvSourceApplyConfiguration{
					LocalObjectReferenceApplyConfiguration: corev1ac.LocalObjectReferenceApplyConfiguration{
						Name: ptr.To[string]("bucket-config"),
					},
				},
			},
		}
		jc.WorkVolume = mocov1beta2.VolumeSourceApplyConfiguration{
			EmptyDir: &corev1ac.EmptyDirVolumeSourceApplyConfiguration{},
		}
		jc.Volumes = []mocov1beta2.VolumeApplyConfiguration{
			{
				Name: ptr.To[string]("test"),
				VolumeSourceApplyConfiguration: corev1ac.VolumeSourceApplyConfiguration{
					EmptyDir: &corev1ac.EmptyDirVolumeSourceApplyConfiguration{},
				},
			},
		}
		jc.VolumeMounts = []mocov1beta2.VolumeMountApplyConfiguration{
			{
				Name:      ptr.To[string]("test"),
				MountPath: ptr.To[string]("/path/to/dir"),
			},
		}
		jc.BucketConfig.BucketName = "mybucket"
		jc.BucketConfig.EndpointURL = "https://foo.bar.baz"
		jc.BucketConfig.Region = "us-east-1"
		jc.BucketConfig.UsePathStyle = true
		err = k8sClient.Create(ctx, bp)
		Expect(err).NotTo(HaveOccurred())

		var cj *batchv1.CronJob
		var role *rbacv1.Role
		var roleBinding *rbacv1.RoleBinding
		Eventually(func() error {
			cj = &batchv1.CronJob{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.BackupCronJobName()}, cj); err != nil {
				return err
			}
			role = &rbacv1.Role{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.BackupRoleName()}, role); err != nil {
				return err
			}
			roleBinding = &rbacv1.RoleBinding{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.BackupRoleName()}, roleBinding); err != nil {
				return err
			}
			return nil
		}).Should(Succeed())

		Expect(cj.Labels).NotTo(BeEmpty())
		Expect(cj.OwnerReferences).NotTo(BeEmpty())
		Expect(cj.Spec.Schedule).To(Equal("*/5 * * * *"))
		Expect(cj.Spec.StartingDeadlineSeconds).To(Equal(ptr.To[int64](10)))
		Expect(cj.Spec.ConcurrencyPolicy).To(Equal(batchv1.ForbidConcurrent))
		Expect(cj.Spec.SuccessfulJobsHistoryLimit).To(Equal(ptr.To[int32](1)))
		Expect(cj.Spec.FailedJobsHistoryLimit).To(Equal(ptr.To[int32](2)))
		Expect(cj.Spec.JobTemplate.Labels).NotTo(BeEmpty())
		js := &cj.Spec.JobTemplate.Spec
		Expect(js.ActiveDeadlineSeconds).To(Equal(ptr.To[int64](100)))
		Expect(js.BackoffLimit).To(Equal(ptr.To[int32](1)))
		Expect(js.Template.Labels).NotTo(BeEmpty())
		Expect(js.Template.Spec.Affinity).NotTo(BeNil())
		Expect(js.Template.Spec.RestartPolicy).To(Equal(corev1.RestartPolicyNever))
		Expect(js.Template.Spec.ServiceAccountName).To(Equal("foo"))
		Expect(js.Template.Spec.Volumes).To(HaveLen(2))
		Expect(js.Template.Spec.Volumes[0].EmptyDir).NotTo(BeNil())
		Expect(js.Template.Spec.Volumes[1].EmptyDir).NotTo(BeNil())
		Expect(js.Template.Spec.Containers).To(HaveLen(1))
		c := &js.Template.Spec.Containers[0]
		Expect(c.Name).To(Equal("backup"))
		Expect(c.Image).To(Equal(testBackupImage))
		Expect(c.Args).To(Equal([]string{
			"backup",
			"--threads=3",
			"--region=us-east-1",
			"--endpoint=https://foo.bar.baz",
			"--use-path-style",
			"--backend-type=s3",
			"mybucket",
			"test",
			"test",
		}))
		Expect(c.EnvFrom).To(HaveLen(1))
		Expect(c.Env).To(HaveLen(2))
		Expect(c.VolumeMounts).To(HaveLen(2))
		cpuReq := c.Resources.Requests[corev1.ResourceCPU]
		Expect(cpuReq.Value()).To(BeNumerically("==", 1))
		cpuLim := c.Resources.Limits[corev1.ResourceCPU]
		Expect(cpuLim.Value()).To(BeNumerically("==", 4))
		memReq := c.Resources.Requests[corev1.ResourceMemory]
		Expect(memReq.Value()).To(BeNumerically("==", 1<<30))
		memLim := c.Resources.Limits[corev1.ResourceMemory]
		Expect(memLim.Value()).To(BeNumerically("==", 10<<30))

		Expect(role.Labels).NotTo(BeEmpty())
		Expect(role.OwnerReferences).NotTo(BeEmpty())
		Expect(role.Rules).NotTo(BeEmpty())
		Expect(roleBinding.Labels).NotTo(BeEmpty())
		Expect(roleBinding.OwnerReferences).NotTo(BeEmpty())
		Expect(roleBinding.RoleRef.Name).To(Equal(role.Name))
		Expect(roleBinding.Subjects).To(HaveLen(1))
		Expect(roleBinding.Subjects[0].Name).To(Equal("foo"))

		By("updating a backup policy")
		bp = &mocov1beta2.BackupPolicy{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test-policy"}, bp)
		Expect(err).NotTo(HaveOccurred())
		bp.Spec.ActiveDeadlineSeconds = nil
		bp.Spec.BackoffLimit = nil
		bp.Spec.ConcurrencyPolicy = batchv1.AllowConcurrent
		bp.Spec.StartingDeadlineSeconds = nil
		bp.Spec.Schedule = "*/5 1 * * *"
		bp.Spec.SuccessfulJobsHistoryLimit = nil
		bp.Spec.FailedJobsHistoryLimit = nil
		jc = &bp.Spec.JobConfig
		jc.Threads = 1
		jc.ServiceAccountName = "oof"
		jc.CPU = nil
		jc.MaxCPU = nil
		jc.Memory = nil
		jc.MaxMemory = nil
		jc.Env = nil
		jc.EnvFrom = nil
		jc.WorkVolume = mocov1beta2.VolumeSourceApplyConfiguration{
			HostPath: &corev1ac.HostPathVolumeSourceApplyConfiguration{
				Path: ptr.To[string]("/host"),
			},
		}
		jc.BucketConfig.BucketName = "mybucket2"
		jc.BucketConfig.EndpointURL = ""
		jc.BucketConfig.Region = ""
		jc.BucketConfig.UsePathStyle = false
		err = k8sClient.Update(ctx, bp)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			cj = &batchv1.CronJob{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.BackupCronJobName()}, cj); err != nil {
				return err
			}
			if cj.Spec.Schedule != "*/5 1 * * *" {
				return errors.New("CronJob is not updated")
			}
			return nil
		}).Should(Succeed())

		Expect(cj.Spec.StartingDeadlineSeconds).To(BeNil())
		Expect(cj.Spec.ConcurrencyPolicy).To(Equal(batchv1.AllowConcurrent))
		Expect(cj.Spec.SuccessfulJobsHistoryLimit).To(Equal(ptr.To[int32](3)))
		Expect(cj.Spec.FailedJobsHistoryLimit).To(Equal(ptr.To[int32](1)))
		js = &cj.Spec.JobTemplate.Spec
		Expect(js.ActiveDeadlineSeconds).To(BeNil())
		Expect(js.BackoffLimit).To(BeNil())
		Expect(js.Template.Spec.ServiceAccountName).To(Equal("oof"))
		Expect(js.Template.Spec.Volumes).To(HaveLen(2))
		Expect(js.Template.Spec.Volumes[0].EmptyDir).To(BeNil())
		Expect(js.Template.Spec.Volumes[0].HostPath).NotTo(BeNil())
		Expect(js.Template.Spec.Containers).To(HaveLen(1))
		c = &js.Template.Spec.Containers[0]
		Expect(c.Args).To(Equal([]string{
			"backup",
			"--threads=1",
			"--backend-type=s3",
			"mybucket2",
			"test",
			"test",
		}))
		Expect(c.EnvFrom).To(BeEmpty())
		Expect(c.Env).To(HaveLen(1))
		cpuReq = c.Resources.Requests[corev1.ResourceCPU]
		Expect(cpuReq.Value()).To(BeNumerically("==", 4))
		Expect(c.Resources.Limits).NotTo(HaveKey(corev1.ResourceCPU))
		memReq = c.Resources.Requests[corev1.ResourceMemory]
		Expect(memReq.Value()).To(BeNumerically("==", 4<<30))
		Expect(c.Resources.Limits).NotTo(HaveKey(corev1.ResourceMemory))

		Eventually(func() error {
			roleBinding = &rbacv1.RoleBinding{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.BackupRoleName()}, roleBinding); err != nil {
				return err
			}
			if len(roleBinding.Subjects) != 1 {
				return errors.New("empty subject")
			}
			if roleBinding.Subjects[0].Name != "oof" {
				return errors.New("RoleBinding is not updated")
			}
			return nil
		}).Should(Succeed())

		By("disabling backup")
		cluster = &mocov1beta2.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
		Expect(err).NotTo(HaveOccurred())
		cluster.Spec.BackupPolicyName = nil
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() bool {
			cj = &batchv1.CronJob{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test-policy"}, cj)
			return apierrors.IsNotFound(err)
		}).Should(BeTrue())

		Eventually(func() bool {
			role = &rbacv1.Role{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.BackupRoleName()}, role)
			return apierrors.IsNotFound(err)
		}).Should(BeTrue())

		Eventually(func() bool {
			roleBinding = &rbacv1.RoleBinding{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.BackupRoleName()}, roleBinding)
			return apierrors.IsNotFound(err)
		}).Should(BeTrue())
	})

	It("should reconcile restore related resources", func() {
		By("creating a MySQLCluster with restore spec")
		now := metav1.Now()
		cluster := testNewMySQLCluster("test")
		cluster.Spec.Restore = &mocov1beta2.RestoreSpec{
			SourceName:      "single",
			SourceNamespace: "ns",
			RestorePoint:    now,
		}
		jc := &cluster.Spec.Restore.JobConfig
		jc.Threads = 3
		jc.ServiceAccountName = "foo"
		jc.CPU = resource.NewQuantity(1, resource.DecimalSI)
		jc.MaxCPU = resource.NewQuantity(4, resource.DecimalSI)
		jc.Memory = resource.NewQuantity(1<<30, resource.DecimalSI)
		jc.MaxMemory = resource.NewQuantity(10<<30, resource.DecimalSI)
		jc.Env = []mocov1beta2.EnvVarApplyConfiguration{{Name: ptr.To[string]("TEST"), Value: ptr.To[string]("123")}}
		jc.EnvFrom = []mocov1beta2.EnvFromSourceApplyConfiguration{
			{
				ConfigMapRef: &corev1ac.ConfigMapEnvSourceApplyConfiguration{
					LocalObjectReferenceApplyConfiguration: corev1ac.LocalObjectReferenceApplyConfiguration{
						Name: ptr.To[string]("bucket-config"),
					},
				},
			},
		}
		jc.WorkVolume = mocov1beta2.VolumeSourceApplyConfiguration{
			EmptyDir: &corev1ac.EmptyDirVolumeSourceApplyConfiguration{},
		}
		jc.Volumes = []mocov1beta2.VolumeApplyConfiguration{
			{
				Name: ptr.To[string]("test"),
				VolumeSourceApplyConfiguration: corev1ac.VolumeSourceApplyConfiguration{
					EmptyDir: &corev1ac.EmptyDirVolumeSourceApplyConfiguration{},
				},
			},
		}
		jc.VolumeMounts = []mocov1beta2.VolumeMountApplyConfiguration{
			{
				Name:      ptr.To[string]("test"),
				MountPath: ptr.To[string]("/path/to/dir"),
			},
		}
		jc.BucketConfig.BucketName = "mybucket"
		jc.BucketConfig.EndpointURL = "https://foo.bar.baz"
		jc.BucketConfig.Region = "us-east-1"
		jc.BucketConfig.UsePathStyle = true
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		var job *batchv1.Job
		var role *rbacv1.Role
		var roleBinding *rbacv1.RoleBinding
		Eventually(func() error {
			job = &batchv1.Job{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.RestoreJobName()}, job); err != nil {
				return err
			}
			role = &rbacv1.Role{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.RestoreRoleName()}, role); err != nil {
				return err
			}
			roleBinding = &rbacv1.RoleBinding{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.RestoreRoleName()}, roleBinding); err != nil {
				return err
			}
			return nil
		}).Should(Succeed())

		Expect(job.Labels).NotTo(BeEmpty())
		Expect(job.OwnerReferences).NotTo(BeEmpty())
		js := &job.Spec
		Expect(js.BackoffLimit).To(Equal(ptr.To[int32](0)))
		Expect(js.Template.Labels).NotTo(BeEmpty())
		Expect(js.Template.Spec.RestartPolicy).To(Equal(corev1.RestartPolicyNever))
		Expect(js.Template.Spec.ServiceAccountName).To(Equal("foo"))
		Expect(js.Template.Spec.Volumes).To(HaveLen(2))
		Expect(js.Template.Spec.Volumes[0].EmptyDir).NotTo(BeNil())
		Expect(js.Template.Spec.Volumes[1].EmptyDir).NotTo(BeNil())
		Expect(js.Template.Spec.Containers).To(HaveLen(1))
		c := &js.Template.Spec.Containers[0]
		Expect(c.Name).To(Equal("restore"))
		Expect(c.Image).To(Equal(testBackupImage))
		Expect(c.Args).To(Equal([]string{
			"restore",
			"--threads=3",
			"--region=us-east-1",
			"--endpoint=https://foo.bar.baz",
			"--use-path-style",
			"--backend-type=s3",
			"mybucket",
			"ns",
			"single",
			"test",
			"test",
			now.UTC().Format(constants.BackupTimeFormat),
		}))
		Expect(c.EnvFrom).To(HaveLen(1))
		Expect(c.Env).To(HaveLen(2))
		Expect(c.VolumeMounts).To(HaveLen(2))
		cpuReq := c.Resources.Requests[corev1.ResourceCPU]
		Expect(cpuReq.Value()).To(BeNumerically("==", 1))
		cpuLim := c.Resources.Limits[corev1.ResourceCPU]
		Expect(cpuLim.Value()).To(BeNumerically("==", 4))
		memReq := c.Resources.Requests[corev1.ResourceMemory]
		Expect(memReq.Value()).To(BeNumerically("==", 1<<30))
		memLim := c.Resources.Limits[corev1.ResourceMemory]
		Expect(memLim.Value()).To(BeNumerically("==", 10<<30))

		Expect(role.Labels).NotTo(BeEmpty())
		Expect(role.OwnerReferences).NotTo(BeEmpty())
		Expect(role.Rules).NotTo(BeEmpty())
		Expect(roleBinding.Labels).NotTo(BeEmpty())
		Expect(roleBinding.OwnerReferences).NotTo(BeEmpty())
		Expect(roleBinding.RoleRef.Name).To(Equal(role.Name))
		Expect(roleBinding.Subjects).To(HaveLen(1))
		Expect(roleBinding.Subjects[0].Name).To(Equal("foo"))

		By("changing cluster status")
		Eventually(func() error {
			cluster := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster); err != nil {
				return err
			}
			t := metav1.Now()
			cluster.Status.RestoredTime = &t
			return k8sClient.Status().Update(ctx, cluster)
		}).Should(Succeed())

		time.Sleep(5 * time.Second)

		err = k8sClient.Delete(ctx, role)
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Delete(ctx, roleBinding)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() bool {
			role = &rbacv1.Role{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.RestoreRoleName()}, role); err == nil {
				return false
			}
			roleBinding = &rbacv1.RoleBinding{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.RestoreRoleName()}, roleBinding); err == nil {
				return false
			}
			return true
		}).Should(BeTrue())

		Consistently(func() bool {
			role = &rbacv1.Role{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.RestoreRoleName()}, role); err == nil {
				return false
			}
			roleBinding = &rbacv1.RoleBinding{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.RestoreRoleName()}, roleBinding); err == nil {
				return false
			}
			return true
		}, 5).Should(BeTrue())
	})

	It("should reconcile a pod disruption budget when backup cron job is running", func() {
		cluster := testNewMySQLCluster("test")
		// use existing backup policy
		cluster.Spec.BackupPolicyName = ptr.To[string]("test-policy")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		var cj *batchv1.CronJob
		var pdb *policyv1.PodDisruptionBudget
		Eventually(func() error {
			cj = &batchv1.CronJob{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.BackupCronJobName()}, cj); err != nil {
				return err
			}
			pdb = &policyv1.PodDisruptionBudget{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.PrefixedName()}, pdb); err != nil {
				return err
			}
			return nil
		}).Should(Succeed())

		By("update cronJob Active status, backup cronJob started")
		cj = &batchv1.CronJob{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.BackupCronJobName()}, cj)
		Expect(err).NotTo(HaveOccurred())

		cj.Status.Active = []corev1.ObjectReference{{Name: "test"}}

		err = k8sClient.Status().Update(ctx, cj)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			cj = &batchv1.CronJob{}
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.BackupCronJobName()}, cj)
			if err != nil {
				return err
			}
			if cj.Status.Active == nil {
				return fmt.Errorf("backup cronJob is not started")
			}

			pdb = &policyv1.PodDisruptionBudget{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.PrefixedName()}, pdb)
			if err != nil {
				return err
			}
			if pdb.Spec.MaxUnavailable.IntVal != 0 {
				return fmt.Errorf("PodDisruptionBudget MaxUnavailable is not 0.")
			}
			return nil
		}).Should(Succeed())

		By("update cronJob Active status to nil, backup cronJob exited")
		cj = &batchv1.CronJob{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.BackupCronJobName()}, cj)
		Expect(err).NotTo(HaveOccurred())

		cj.Status.Active = nil

		err = k8sClient.Status().Update(ctx, cj)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			cj = &batchv1.CronJob{}
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.BackupCronJobName()}, cj)
			if err != nil {
				return err
			}
			if cj.Status.Active != nil {
				return fmt.Errorf("backup cronJob is started")
			}

			pdb = &policyv1.PodDisruptionBudget{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.PrefixedName()}, pdb)
			if err != nil {
				return err
			}
			if pdb.Spec.MaxUnavailable.IntVal == 0 {
				return fmt.Errorf("PodDisruptionBudget MaxUnavailable is equal 0.")
			}
			return nil
		}).Should(Succeed())
	})

	It("should have a correct status.reconcileInfo value", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			cluster2 := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster2); err != nil {
				return err
			}
			if cluster2.Status.ReconcileInfo.ReconcileVersion != 1 {
				return fmt.Errorf("reconcile version is not 1: %d", cluster2.Status.ReconcileInfo.ReconcileVersion)
			}
			if cluster2.Status.ReconcileInfo.Generation != 1 {
				return fmt.Errorf("generation is not 1: %d", cluster2.Status.ReconcileInfo.Generation)
			}
			return nil
		}).Should(Succeed())

		cluster = &mocov1beta2.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
		Expect(err).NotTo(HaveOccurred())

		cluster.Annotations = map[string]string{"foo": "bar"}
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Consistently(func() error {
			cluster2 := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster2); err != nil {
				return err
			}
			if cluster2.Status.ReconcileInfo.ReconcileVersion != 1 {
				return fmt.Errorf("reconcile version is not 1: %d", cluster2.Status.ReconcileInfo.ReconcileVersion)
			}
			if cluster2.Status.ReconcileInfo.Generation != 1 {
				return fmt.Errorf("generation is not 1: %d", cluster2.Status.ReconcileInfo.Generation)
			}
			return nil
		}).Should(Succeed())

		cluster = &mocov1beta2.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
		Expect(err).NotTo(HaveOccurred())

		cluster.Spec.Replicas = 5
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			cluster2 := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster2); err != nil {
				return err
			}
			if cluster2.Status.ReconcileInfo.ReconcileVersion != 1 {
				return fmt.Errorf("reconcile version is not 1: %d", cluster2.Status.ReconcileInfo.ReconcileVersion)
			}
			if cluster2.Status.ReconcileInfo.Generation != 2 {
				return fmt.Errorf("generation is not 2: %d", cluster2.Status.ReconcileInfo.Generation)
			}
			return nil
		}).Should(Succeed())
	})

	It("should call manager methods properly", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		cluster = testNewMySQLCluster("test")
		cluster.Name = "test2"
		err = k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() bool {
			keys := mockMgr.getKeys()
			return keys["test/test"] && keys["test/test2"]
		}).Should(BeTrue())

		testDeleteMySQLCluster(ctx, "test", "test2")

		Eventually(func() bool {
			return mockMgr.getKeys()["test/test2"]
		}).Should(BeFalse())

		Expect(mockMgr.getKeys()["test/test"]).To(BeTrue())
	})

	It("should delete all related resources", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			secret := &corev1.Secret{}
			key := client.ObjectKey{Namespace: testMocoSystemNamespace, Name: "mysql-test.test"}
			return k8sClient.Get(ctx, key, secret)
		}).Should(Succeed())

		testDeleteMySQLCluster(ctx, "test", "test")

		Eventually(func() error {
			secret := &corev1.Secret{}
			key := client.ObjectKey{Namespace: testMocoSystemNamespace, Name: "mysql-test.test"}
			err := k8sClient.Get(ctx, key, secret)
			if err == nil {
				return fmt.Errorf("the secret in controller namespace still exists")
			}
			if !apierrors.IsNotFound(err) {
				return err
			}

			cert := certificateObj.DeepCopy()
			key = client.ObjectKey{Namespace: testMocoSystemNamespace, Name: "moco-agent-test.test"}
			err = k8sClient.Get(ctx, key, cert)
			if err == nil {
				return fmt.Errorf("the certificate in controller namespace still exists")
			}
			if !apierrors.IsNotFound(err) {
				return err
			}

			return nil
		}).Should(Succeed())
	})

	It("should sets ConditionStatefulSetReady to be true when StatefulSet is ready", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		var sts *appsv1.StatefulSet
		Eventually(func() error {
			sts = &appsv1.StatefulSet{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test"}, sts); err != nil {
				return err
			}
			return nil
		}).Should(Succeed())

		By("setting sts status to be ready")
		sts.Status.Replicas = 3
		sts.Status.ReadyReplicas = 3
		sts.Status.AvailableReplicas = 3
		sts.Status.CurrentRevision = "hoge"
		sts.Status.UpdateRevision = "hoge"
		sts.Status.CurrentReplicas = 3
		sts.Status.UpdatedReplicas = 3
		sts.Status.ObservedGeneration = sts.Generation
		err = k8sClient.Status().Update(ctx, sts)
		Expect(err).NotTo(HaveOccurred())

		By("checking condition is true")
		Eventually(func() error {
			cluster2 := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster2); err != nil {
				return err
			}
			conditionStatefulSetReady := meta.FindStatusCondition(cluster2.Status.Conditions, mocov1beta2.ConditionStatefulSetReady)
			if conditionStatefulSetReady == nil {
				return fmt.Errorf("condition does not exists")
			}
			if conditionStatefulSetReady.Status != metav1.ConditionTrue {
				return fmt.Errorf("condition is not false")
			}
			return nil
		}).Should(Succeed())
	})

	It("should sets ConditionStatefulSetReady to be false when status of StatefulSet is empty", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		var sts *appsv1.StatefulSet
		Eventually(func() error {
			sts = &appsv1.StatefulSet{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test"}, sts); err != nil {
				return err
			}
			return nil
		}).Should(Succeed())

		By("setting sts status to be ready")
		sts.Status.Replicas = 3
		sts.Status.ReadyReplicas = 3
		sts.Status.AvailableReplicas = 3
		sts.Status.CurrentRevision = "hoge"
		sts.Status.UpdateRevision = "hoge"
		sts.Status.CurrentReplicas = 3
		sts.Status.UpdatedReplicas = 3
		sts.Status.ObservedGeneration = sts.Generation
		err = k8sClient.Status().Update(ctx, sts)
		Expect(err).NotTo(HaveOccurred())

		By("waiting condition to be updated")
		Eventually(func() error {
			cluster2 := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster2); err != nil {
				return err
			}
			conditionStatefulSetReady := meta.FindStatusCondition(cluster2.Status.Conditions, mocov1beta2.ConditionStatefulSetReady)
			if conditionStatefulSetReady == nil {
				return fmt.Errorf("condition does not exists")
			}
			if conditionStatefulSetReady.Status != metav1.ConditionTrue {
				return fmt.Errorf("condition is not true")
			}
			return nil
		}).Should(Succeed())

		By("setting sts status to be empty")
		sts.Status = appsv1.StatefulSetStatus{}
		err = k8sClient.Status().Update(ctx, sts)
		Expect(err).NotTo(HaveOccurred())

		By("checking condition is false")
		Eventually(func() error {
			cluster2 := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster2); err != nil {
				return err
			}
			conditionStatefulSetReady := meta.FindStatusCondition(cluster2.Status.Conditions, mocov1beta2.ConditionStatefulSetReady)
			if conditionStatefulSetReady == nil {
				return fmt.Errorf("condition does not exists")
			}
			if conditionStatefulSetReady.Status != metav1.ConditionFalse {
				return fmt.Errorf("condition is not false")
			}
			return nil
		}).Should(Succeed())
	})

	It("should sets ConditionStatefulSetReady to be false when status of StatefulSet does not found", func() {
		cluster := testNewMySQLCluster("test")
		cluster.Spec.MySQLConfigMapName = ptr.To[string]("foobarhoge")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		By("checking condition is false")
		Eventually(func() error {
			cluster2 := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster2); err != nil {
				return err
			}
			conditionStatefulSetReady := meta.FindStatusCondition(cluster2.Status.Conditions, mocov1beta2.ConditionStatefulSetReady)
			if conditionStatefulSetReady == nil {
				return fmt.Errorf("condition does not exists")
			}
			if conditionStatefulSetReady.Status != metav1.ConditionFalse {
				return fmt.Errorf("condition is not false")
			}
			if conditionStatefulSetReady.Reason != "StatefulSetNotFound" {
				return fmt.Errorf("reason is not expected")
			}
			return nil
		}).Should(Succeed())
	})

	It("should sets ConditionStatefulSetReady to be false when StatefulSet is not ready", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		var sts *appsv1.StatefulSet
		Eventually(func() error {
			sts = &appsv1.StatefulSet{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test"}, sts); err != nil {
				return err
			}
			return nil
		}).Should(Succeed())

		By("setting sts status to be ready")
		sts.Status.Replicas = 3
		sts.Status.ReadyReplicas = 3
		sts.Status.AvailableReplicas = 3
		sts.Status.CurrentRevision = "hoge"
		sts.Status.UpdateRevision = "hoge"
		sts.Status.CurrentReplicas = 3
		sts.Status.UpdatedReplicas = 3
		sts.Status.ObservedGeneration = sts.Generation
		err = k8sClient.Status().Update(ctx, sts)
		Expect(err).NotTo(HaveOccurred())

		By("waiting condition to be updated")
		Eventually(func() error {
			cluster2 := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster2); err != nil {
				return err
			}
			conditionStatefulSetReady := meta.FindStatusCondition(cluster2.Status.Conditions, mocov1beta2.ConditionStatefulSetReady)
			if conditionStatefulSetReady == nil {
				return fmt.Errorf("condition does not exists")
			}
			if conditionStatefulSetReady.Status != metav1.ConditionTrue {
				return fmt.Errorf("condition is not true")
			}
			return nil
		}).Should(Succeed())

		By("setting sts status to be not ready")
		sts.Status.Replicas = 3
		sts.Status.ReadyReplicas = 3
		sts.Status.AvailableReplicas = 3
		sts.Status.CurrentRevision = "hoge"
		sts.Status.UpdateRevision = "fuga"
		sts.Status.CurrentReplicas = 2
		sts.Status.UpdatedReplicas = 1
		sts.Status.ObservedGeneration = sts.Generation
		err = k8sClient.Status().Update(ctx, sts)
		Expect(err).NotTo(HaveOccurred())

		By("checking condition is false")
		Eventually(func() error {
			cluster2 := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster2); err != nil {
				return err
			}
			conditionStatefulSetReady := meta.FindStatusCondition(cluster2.Status.Conditions, mocov1beta2.ConditionStatefulSetReady)
			if conditionStatefulSetReady == nil {
				return fmt.Errorf("condition does not exists")
			}
			if conditionStatefulSetReady.Status != metav1.ConditionFalse {
				return fmt.Errorf("condition is not false")
			}
			return nil
		}).Should(Succeed())

		By("setting sts status to be ready")
		sts.Status.Replicas = 3
		sts.Status.ReadyReplicas = 3
		sts.Status.AvailableReplicas = 3
		sts.Status.CurrentRevision = "hoge"
		sts.Status.UpdateRevision = "hoge"
		sts.Status.CurrentReplicas = 3
		sts.Status.UpdatedReplicas = 3
		sts.Status.ObservedGeneration = sts.Generation
		err = k8sClient.Status().Update(ctx, sts)
		Expect(err).NotTo(HaveOccurred())

		By("waiting condition to be updated")
		Eventually(func() error {
			cluster2 := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster2); err != nil {
				return err
			}
			conditionStatefulSetReady := meta.FindStatusCondition(cluster2.Status.Conditions, mocov1beta2.ConditionStatefulSetReady)
			if conditionStatefulSetReady == nil {
				return fmt.Errorf("condition does not exists")
			}
			if conditionStatefulSetReady.Status != metav1.ConditionTrue {
				return fmt.Errorf("condition is not true")
			}
			return nil
		}).Should(Succeed())

		By("setting sts status to be not reconciled yet")
		sts.Status.Replicas = 3
		sts.Status.ReadyReplicas = 3
		sts.Status.AvailableReplicas = 3
		sts.Status.CurrentRevision = "hoge"
		sts.Status.UpdateRevision = "hoge"
		sts.Status.CurrentReplicas = 3
		sts.Status.UpdatedReplicas = 3
		sts.Status.ObservedGeneration = sts.Generation - 1
		err = k8sClient.Status().Update(ctx, sts)
		Expect(err).NotTo(HaveOccurred())

		By("checking condition is false")
		Eventually(func() error {
			cluster2 := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster2); err != nil {
				return err
			}
			conditionStatefulSetReady := meta.FindStatusCondition(cluster2.Status.Conditions, mocov1beta2.ConditionStatefulSetReady)
			if conditionStatefulSetReady == nil {
				return fmt.Errorf("condition does not exists")
			}
			if conditionStatefulSetReady.Status != metav1.ConditionFalse {
				return fmt.Errorf("condition is not false")
			}
			return nil
		}).Should(Succeed())
	})

	It("should sets reconcile status condition true when success", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		By("checking condition is true")
		Eventually(func() error {
			cluster = &mocov1beta2.MySQLCluster{}
			if err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster); err != nil {
				return err
			}
			conditionReconcileSuccess := meta.FindStatusCondition(cluster.Status.Conditions, mocov1beta2.ConditionReconcileSuccess)
			if conditionReconcileSuccess == nil {
				return fmt.Errorf("condition does not exists")

			}
			if conditionReconcileSuccess.Status != metav1.ConditionTrue {
				return fmt.Errorf("condition is not true")
			}
			return nil
		}).Should(Succeed())
	})

	It("should sets reconcile status condition false when faild", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		By("setting configmap name to be invalid")
		cluster.Spec.MySQLConfigMapName = ptr.To[string]("foobarhoge")
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		By("checking condition is false")
		Eventually(func() error {
			cluster = &mocov1beta2.MySQLCluster{}
			if err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster); err != nil {
				return err
			}
			conditionReconcileSuccess := meta.FindStatusCondition(cluster.Status.Conditions, mocov1beta2.ConditionReconcileSuccess)
			if conditionReconcileSuccess == nil {
				return fmt.Errorf("condition does not exists")
			}
			if conditionReconcileSuccess.Status != metav1.ConditionFalse {
				return fmt.Errorf("condition is not false")
			}
			return nil
		}).Should(Succeed())
	})

	It("should reconciliation stopped", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		By("setting reconcile stop annotation")
		if cluster.Annotations == nil {
			cluster.Annotations = map[string]string{}
		}
		cluster.Annotations[constants.AnnReconciliationStopped] = "true"
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		By("checking condition is false")
		Eventually(func() error {
			cluster = &mocov1beta2.MySQLCluster{}
			if err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster); err != nil {
				return err
			}
			cond := meta.FindStatusCondition(cluster.Status.Conditions, mocov1beta2.ConditionReconciliationActive)
			if cond == nil {
				return fmt.Errorf("condition does not exists")
			}
			if cond.Status != metav1.ConditionFalse {
				return fmt.Errorf("condition is not false")
			}
			return nil
		}).Should(Succeed())
	})

	It("should clustering stopped", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		By("setting clustering stop annotation")
		if cluster.Annotations == nil {
			cluster.Annotations = map[string]string{}
		}
		cluster.Annotations[constants.AnnClusteringStopped] = "true"
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		By("checking condition is false")
		Eventually(func() error {
			cluster = &mocov1beta2.MySQLCluster{}
			if err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster); err != nil {
				return err
			}
			cond := meta.FindStatusCondition(cluster.Status.Conditions, mocov1beta2.ConditionClusteringActive)
			if cond == nil {
				return fmt.Errorf("condition does not exists")
			}
			if cond.Status != metav1.ConditionFalse {
				return fmt.Errorf("condition is not false")
			}
			return nil
		}).Should(Succeed())
	})
})
