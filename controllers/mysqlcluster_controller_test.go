package controllers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/pkg/constants"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	testMocoSystemNamespace = "moco-system"
	testAgentImage          = "foobar:123"
	testBackupImage         = "backup:123"
	testFluentBitImage      = "fluent-hoge:134"
	testExporterImage       = "mysqld_exporter:111"
)

func testNewMySQLCluster(ns string) *mocov1beta1.MySQLCluster {
	cluster := &mocov1beta1.MySQLCluster{}
	cluster.Namespace = ns
	cluster.Name = "test"
	cluster.Finalizers = []string{constants.MySQLClusterFinalizer}
	cluster.Spec.Replicas = 3
	cluster.Spec.PodTemplate.Spec.Containers = []corev1.Container{
		{Name: "mysqld", Image: "moco-mysql:latest"},
	}
	cluster.Spec.VolumeClaimTemplates = []mocov1beta1.PersistentVolumeClaim{
		{
			ObjectMeta: mocov1beta1.ObjectMeta{Name: "mysql-data"},
			Spec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: pointer.String("hoge"),
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: *resource.NewQuantity(1<<30, resource.BinarySI),
					},
				},
			},
		},
	}
	return cluster
}

func testDeleteMySQLCluster(ctx context.Context, ns, name string) {
	cluster := &mocov1beta1.MySQLCluster{}
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
		cs := &mocov1beta1.MySQLClusterList{}
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
		err = k8sClient.DeleteAllOf(ctx, &mocov1beta1.MySQLCluster{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &appsv1.StatefulSet{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &corev1.Secret{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &corev1.ConfigMap{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &corev1.ServiceAccount{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &policyv1beta1.PodDisruptionBudget{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:             scheme,
			LeaderElection:     false,
			MetricsBindAddress: "0",
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

		cluster = &mocov1beta1.MySQLCluster{}
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
		cluster.Spec.PodTemplate.Spec.Containers[0].Resources.Limits = make(corev1.ResourceList)
		cluster.Spec.PodTemplate.Spec.Containers[0].Resources.Limits[corev1.ResourceMemory] = resource.MustParse("1000Mi")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			c := &mocov1beta1.MySQLCluster{}
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
			"foo": "bar",
		}

		err = k8sClient.Create(ctx, userCM)
		Expect(err).NotTo(HaveOccurred())

		cluster = &mocov1beta1.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
		Expect(err).NotTo(HaveOccurred())
		cluster.Spec.MySQLConfigMapName = pointer.String(userCM.Name)
		cluster.Spec.PodTemplate.Spec.Containers[0].Resources.Requests = make(corev1.ResourceList)
		cluster.Spec.PodTemplate.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = resource.MustParse("500Mi")
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
			cluster = &mocov1beta1.MySQLCluster{}
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
			if err != nil {
				return err
			}
			cluster.Spec.ServiceTemplate = &mocov1beta1.ServiceTemplate{
				ObjectMeta: mocov1beta1.ObjectMeta{
					Annotations: map[string]string{"foo": "bar"},
					Labels:      map[string]string{"foo": "baz"},
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
			return nil
		}).Should(Succeed())

		Eventually(func() error {
			cluster = &mocov1beta1.MySQLCluster{}
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
			if err != nil {
				return err
			}
			cluster.Spec.ServiceTemplate = &mocov1beta1.ServiceTemplate{
				Spec: &corev1.ServiceSpec{
					Type:                  corev1.ServiceTypeLoadBalancer,
					ExternalTrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal,
				},
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
			return nil
		}).Should(Succeed())

		headless = &corev1.Service{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test"}, headless)
		Expect(err).NotTo(HaveOccurred())
		Expect(headless.Spec.ExternalTrafficPolicy).NotTo(Equal(corev1.ServiceExternalTrafficPolicyTypeLocal))

		// Edit Service again should succeed
		Eventually(func() error {
			cluster = &mocov1beta1.MySQLCluster{}
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
			if err != nil {
				return err
			}
			cluster.Spec.ServiceTemplate = &mocov1beta1.ServiceTemplate{
				ObjectMeta: mocov1beta1.ObjectMeta{
					Annotations: map[string]string{"foo": "bar"},
				},
				Spec: &corev1.ServiceSpec{
					Type:                  corev1.ServiceTypeLoadBalancer,
					ExternalTrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal,
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
				return errors.New("service does not have annotation foo")
			}
			return nil
		}).Should(Succeed())
	})

	It("should reconcile statefulset", func() {
		cluster := testNewMySQLCluster("test")
		cluster.Spec.ReplicationSourceSecretName = pointer.String("source-secret")
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

			cluster = &mocov1beta1.MySQLCluster{}
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
			case constants.AgentContainerName:
				foundAgent = true
				Expect(c.Image).To(Equal(testAgentImage))
			case constants.SlowQueryLogAgentContainerName:
				foundSlowLogAgent = true
				Expect(c.Image).To(Equal(testFluentBitImage))
			case constants.ExporterContainerName:
				foundExporter = true
			}
		}
		Expect(foundMysqld).To(BeTrue())
		Expect(foundAgent).To(BeTrue())
		Expect(foundSlowLogAgent).To(BeTrue())
		Expect(foundExporter).To(BeFalse())

		Expect(sts.Spec.Template.Spec.InitContainers).To(HaveLen(1))
		initContainer := &sts.Spec.Template.Spec.InitContainers[0]
		Expect(initContainer.Name).To(Equal(constants.InitContainerName))
		Expect(initContainer.Image).To(Equal("moco-mysql:latest"))
		Expect(initContainer.Command).To(ContainElement(fmt.Sprintf("%d", cluster.Spec.ServerIDBase)))
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
		cluster = &mocov1beta1.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
		Expect(err).NotTo(HaveOccurred())

		cluster.Spec.Replicas = 5
		cluster.Spec.ReplicationSourceSecretName = nil
		cluster.Spec.Collectors = []string{"engine_innodb_status", "info_schema.innodb_metrics"}
		cluster.Spec.MaxDelaySeconds = 20
		cluster.Spec.StartupWaitSeconds = 3
		cluster.Spec.LogRotationSchedule = "0 * * * *"
		cluster.Spec.DisableSlowQueryLogContainer = true
		cluster.Spec.PodTemplate.Spec.TerminationGracePeriodSeconds = pointer.Int64(512)
		cluster.Spec.PodTemplate.Spec.PriorityClassName = "hoge"
		cluster.Spec.PodTemplate.Spec.Containers = append(cluster.Spec.PodTemplate.Spec.Containers,
			corev1.Container{Name: "dummy", Image: "dummy:latest"})
		cluster.Spec.PodTemplate.Spec.InitContainers = append(cluster.Spec.PodTemplate.Spec.InitContainers,
			corev1.Container{Name: "init-dummy", Image: "init-dummy:latest"})
		cluster.Spec.PodTemplate.Spec.Volumes = append(cluster.Spec.PodTemplate.Spec.Volumes,
			corev1.Volume{Name: "dummy-vol", VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			}})
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			c := &mocov1beta1.MySQLCluster{}
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

		foundDummyContainer := false
		for _, c := range sts.Spec.Template.Spec.Containers {
			Expect(c.Name).NotTo(Equal(constants.SlowQueryLogAgentContainerName))
			switch c.Name {
			case constants.MysqldContainerName:
				Expect(c.StartupProbe).NotTo(BeNil())
				Expect(c.StartupProbe.FailureThreshold).To(Equal(int32(1)))
			case constants.AgentContainerName:
				Expect(c.Args).To(ContainElement("20s"))
				Expect(c.Args).To(ContainElement("0 * * * *"))
			case constants.ExporterContainerName:
				foundExporter = true
				Expect(c.Image).To(Equal(testExporterImage))
				Expect(c.Args).To(HaveLen(3))
			case "dummy":
				foundDummyContainer = true
			}
		}
		Expect(foundExporter).To(BeTrue())
		Expect(foundDummyContainer).To(BeTrue())

		foundInitDummyContainer := false
		for _, c := range sts.Spec.Template.Spec.InitContainers {
			switch c.Name {
			case "init-dummy":
				foundInitDummyContainer = true
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
	})

	It("should reconcile a pod disruption budget", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			cluster = &mocov1beta1.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster); err != nil {
				return err
			}
			if cluster.Status.ReconcileInfo.Generation != cluster.Generation {
				return fmt.Errorf("not yet reconciled")
			}
			return nil
		}).Should(Succeed())

		var pdb *policyv1beta1.PodDisruptionBudget
		Eventually(func() error {
			pdb = &policyv1beta1.PodDisruptionBudget{}
			return k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.PrefixedName()}, pdb)
		}).Should(Succeed())

		Expect(pdb.Spec.MaxUnavailable).NotTo(BeNil())
		Expect(pdb.Spec.MaxUnavailable.IntVal).To(Equal(int32(1)))

		Eventually(func() error {
			cluster = &mocov1beta1.MySQLCluster{}
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
			pdb = &policyv1beta1.PodDisruptionBudget{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.PrefixedName()}, pdb)
			return apierrors.IsNotFound(err)
		}).Should(BeTrue())
	})

	It("should reconcile backup related resources", func() {
		cluster := testNewMySQLCluster("test")
		cluster.Spec.BackupPolicyName = pointer.String("test-policy")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		By("creating a backup policy")
		bp := &mocov1beta1.BackupPolicy{}
		bp.Namespace = "test"
		bp.Name = "test-policy"
		bp.Spec.ActiveDeadlineSeconds = pointer.Int64(100)
		bp.Spec.BackoffLimit = pointer.Int32(1)
		bp.Spec.ConcurrencyPolicy = batchv1beta1.ForbidConcurrent
		bp.Spec.StartingDeadlineSeconds = pointer.Int64(10)
		bp.Spec.Schedule = "*/5 * * * *"
		bp.Spec.SuccessfulJobsHistoryLimit = pointer.Int32(1)
		bp.Spec.FailedJobsHistoryLimit = pointer.Int32(2)
		jc := &bp.Spec.JobConfig
		jc.Threads = 3
		jc.ServiceAccountName = "foo"
		jc.Memory = resource.NewQuantity(1<<30, resource.DecimalSI)
		jc.MaxMemory = resource.NewQuantity(10<<30, resource.DecimalSI)
		jc.Env = []corev1.EnvVar{{Name: "TEST", Value: "123"}}
		jc.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: "bucket-config",
			},
		}}}
		jc.WorkVolume = corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}
		jc.BucketConfig.BucketName = "mybucket"
		jc.BucketConfig.EndpointURL = "https://foo.bar.baz"
		jc.BucketConfig.Region = "us-east-1"
		jc.BucketConfig.UsePathStyle = true
		err = k8sClient.Create(ctx, bp)
		Expect(err).NotTo(HaveOccurred())

		var cj *batchv1beta1.CronJob
		var role *rbacv1.Role
		var roleBinding *rbacv1.RoleBinding
		Eventually(func() error {
			cj = &batchv1beta1.CronJob{}
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
		Expect(cj.Spec.StartingDeadlineSeconds).To(Equal(pointer.Int64(10)))
		Expect(cj.Spec.ConcurrencyPolicy).To(Equal(batchv1beta1.ForbidConcurrent))
		Expect(cj.Spec.SuccessfulJobsHistoryLimit).To(Equal(pointer.Int32(1)))
		Expect(cj.Spec.FailedJobsHistoryLimit).To(Equal(pointer.Int32(2)))
		Expect(cj.Spec.JobTemplate.Labels).NotTo(BeEmpty())
		js := &cj.Spec.JobTemplate.Spec
		Expect(js.ActiveDeadlineSeconds).To(Equal(pointer.Int64(100)))
		Expect(js.BackoffLimit).To(Equal(pointer.Int32(1)))
		Expect(js.Template.Labels).NotTo(BeEmpty())
		Expect(js.Template.Spec.RestartPolicy).To(Equal(corev1.RestartPolicyNever))
		Expect(js.Template.Spec.ServiceAccountName).To(Equal("foo"))
		Expect(js.Template.Spec.Volumes).To(HaveLen(1))
		Expect(js.Template.Spec.Volumes[0].EmptyDir).NotTo(BeNil())
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
			"mybucket",
			"test",
			"test",
		}))
		Expect(c.EnvFrom).To(HaveLen(1))
		Expect(c.Env).To(HaveLen(2))
		Expect(c.VolumeMounts).To(HaveLen(1))
		cpuReq := c.Resources.Requests[corev1.ResourceCPU]
		Expect(cpuReq.Value()).To(BeNumerically("==", 3))
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
		bp = &mocov1beta1.BackupPolicy{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test-policy"}, bp)
		Expect(err).NotTo(HaveOccurred())
		bp.Spec.ActiveDeadlineSeconds = nil
		bp.Spec.BackoffLimit = nil
		bp.Spec.ConcurrencyPolicy = batchv1beta1.AllowConcurrent
		bp.Spec.StartingDeadlineSeconds = nil
		bp.Spec.Schedule = "*/5 1 * * *"
		bp.Spec.SuccessfulJobsHistoryLimit = nil
		bp.Spec.FailedJobsHistoryLimit = nil
		jc = &bp.Spec.JobConfig
		jc.Threads = 1
		jc.ServiceAccountName = "oof"
		jc.Memory = nil
		jc.MaxMemory = nil
		jc.Env = nil
		jc.EnvFrom = nil
		jc.WorkVolume = corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/host"}}
		jc.BucketConfig.BucketName = "mybucket2"
		jc.BucketConfig.EndpointURL = ""
		jc.BucketConfig.Region = ""
		jc.BucketConfig.UsePathStyle = false
		err = k8sClient.Update(ctx, bp)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			cj = &batchv1beta1.CronJob{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.BackupCronJobName()}, cj); err != nil {
				return err
			}
			if cj.Spec.Schedule != "*/5 1 * * *" {
				return errors.New("CronJob is not updated")
			}
			return nil
		}).Should(Succeed())

		Expect(cj.Spec.StartingDeadlineSeconds).To(BeNil())
		Expect(cj.Spec.ConcurrencyPolicy).To(Equal(batchv1beta1.AllowConcurrent))
		Expect(cj.Spec.SuccessfulJobsHistoryLimit).To(Equal(pointer.Int32(3)))
		Expect(cj.Spec.FailedJobsHistoryLimit).To(Equal(pointer.Int32(1)))
		js = &cj.Spec.JobTemplate.Spec
		Expect(js.ActiveDeadlineSeconds).To(BeNil())
		Expect(js.BackoffLimit).To(BeNil())
		Expect(js.Template.Spec.ServiceAccountName).To(Equal("oof"))
		Expect(js.Template.Spec.Volumes).To(HaveLen(1))
		Expect(js.Template.Spec.Volumes[0].EmptyDir).To(BeNil())
		Expect(js.Template.Spec.Volumes[0].HostPath).NotTo(BeNil())
		Expect(js.Template.Spec.Containers).To(HaveLen(1))
		c = &js.Template.Spec.Containers[0]
		Expect(c.Args).To(Equal([]string{
			"backup",
			"--threads=1",
			"mybucket2",
			"test",
			"test",
		}))
		Expect(c.EnvFrom).To(BeEmpty())
		Expect(c.Env).To(HaveLen(1))
		cpuReq = c.Resources.Requests[corev1.ResourceCPU]
		Expect(cpuReq.Value()).To(BeNumerically("==", 1))
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
		cluster = &mocov1beta1.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
		Expect(err).NotTo(HaveOccurred())
		cluster.Spec.BackupPolicyName = nil
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() bool {
			cj = &batchv1beta1.CronJob{}
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
		cluster.Spec.Restore = &mocov1beta1.RestoreSpec{
			SourceName:      "single",
			SourceNamespace: "ns",
			RestorePoint:    now,
		}
		jc := &cluster.Spec.Restore.JobConfig
		jc.Threads = 3
		jc.ServiceAccountName = "foo"
		jc.Memory = resource.NewQuantity(1<<30, resource.DecimalSI)
		jc.MaxMemory = resource.NewQuantity(10<<30, resource.DecimalSI)
		jc.Env = []corev1.EnvVar{{Name: "TEST", Value: "123"}}
		jc.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: "bucket-config",
			},
		}}}
		jc.WorkVolume = corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}
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
		Expect(js.BackoffLimit).To(Equal(pointer.Int32(0)))
		Expect(js.Template.Labels).NotTo(BeEmpty())
		Expect(js.Template.Spec.RestartPolicy).To(Equal(corev1.RestartPolicyNever))
		Expect(js.Template.Spec.ServiceAccountName).To(Equal("foo"))
		Expect(js.Template.Spec.Volumes).To(HaveLen(1))
		Expect(js.Template.Spec.Volumes[0].EmptyDir).NotTo(BeNil())
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
			"mybucket",
			"ns",
			"single",
			"test",
			"test",
			now.UTC().Format(constants.BackupTimeFormat),
		}))
		Expect(c.EnvFrom).To(HaveLen(1))
		Expect(c.Env).To(HaveLen(2))
		Expect(c.VolumeMounts).To(HaveLen(1))
		cpuReq := c.Resources.Requests[corev1.ResourceCPU]
		Expect(cpuReq.Value()).To(BeNumerically("==", 3))
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
			cluster := &mocov1beta1.MySQLCluster{}
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

	It("should have a correct status.reconcileInfo value", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			cluster2 := &mocov1beta1.MySQLCluster{}
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

		cluster = &mocov1beta1.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
		Expect(err).NotTo(HaveOccurred())

		cluster.Annotations = map[string]string{"foo": "bar"}
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Consistently(func() error {
			cluster2 := &mocov1beta1.MySQLCluster{}
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

		cluster = &mocov1beta1.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
		Expect(err).NotTo(HaveOccurred())

		cluster.Spec.Replicas = 5
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			cluster2 := &mocov1beta1.MySQLCluster{}
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
})
