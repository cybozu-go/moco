package controllers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/clustering"
	"github.com/cybozu-go/moco/pkg/constants"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

type mockManager struct {
	mu       sync.Mutex
	clusters map[string]struct{}
}

var _ clustering.ClusterManager = &mockManager{}

func (m *mockManager) Update(ctx context.Context, key types.NamespacedName) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.clusters[key.String()] = struct{}{}
}

func (m *mockManager) Stop(key types.NamespacedName) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.clusters, key.String())
}

func (m *mockManager) getKeys() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	keys := make(map[string]bool)
	for k := range m.clusters {
		keys[k] = true
	}
	return keys
}

const (
	testMocoSystemNamespace = "moco-system"
	testAgentImage          = "foobar:123"
	testFluentBitImage      = "fluent-hoge:134"
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
			Client:              mgr.GetClient(),
			Scheme:              scheme,
			SystemNamespace:     testMocoSystemNamespace,
			ClusterManager:      mockMgr,
			AgentContainerImage: testAgentImage,
			FluentBitImage:      testFluentBitImage,
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

		cluster = &mocov1beta1.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
		Expect(err).NotTo(HaveOccurred())
		cluster.Spec.ServiceTemplate = &mocov1beta1.ServiceTemplate{
			ObjectMeta: mocov1beta1.ObjectMeta{
				Annotations: map[string]string{"foo": "bar"},
				Labels:      map[string]string{"foo": "baz"},
			},
		}
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			headless = &corev1.Service{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test"}, headless); err != nil {
				return err
			}
			if headless.Annotations["foo"] != "bar" {
				return errors.New("no annotation")
			}
			if headless.Labels["foo"] != "baz" {
				return errors.New("no label")
			}
			return nil
		}).Should(Succeed())

		newPrimary := &corev1.Service{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test-primary"}, newPrimary)
		Expect(err).NotTo(HaveOccurred())
		Expect(newPrimary.Spec.ClusterIP).To(Equal(primary.Spec.ClusterIP))

		cluster = &mocov1beta1.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cluster)
		Expect(err).NotTo(HaveOccurred())
		cluster.Spec.ServiceTemplate = &mocov1beta1.ServiceTemplate{
			Spec: &corev1.ServiceSpec{
				Type: corev1.ServiceTypeLoadBalancer,
			},
		}
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

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
			return k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test"}, sts)
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
		for _, c := range sts.Spec.Template.Spec.Containers {
			switch c.Name {
			case constants.MysqldContainerName:
				foundMysqld = true
				Expect(c.Image).To(Equal("moco-mysql:latest"))
			case constants.AgentContainerName:
				foundAgent = true
				Expect(c.Image).To(Equal(testAgentImage))
			case constants.SlowQueryLogAgentContainerName:
				foundSlowLogAgent = true
				Expect(c.Image).To(Equal(testFluentBitImage))
			}
		}
		Expect(foundMysqld).To(BeTrue())
		Expect(foundAgent).To(BeTrue())
		Expect(foundSlowLogAgent).To(BeTrue())

		Expect(sts.Spec.Template.Spec.InitContainers).To(HaveLen(1))
		initContainer := &sts.Spec.Template.Spec.InitContainers[0]
		Expect(initContainer.Name).To(Equal(constants.InitContainerName))
		Expect(initContainer.Image).To(Equal("moco-mysql:latest"))
		Expect(initContainer.Command).To(ContainElement(fmt.Sprintf("%d", cluster.Spec.ServerIDBase)))

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
		cluster.Spec.MaxDelaySeconds = 20
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
			case constants.AgentContainerName:
				Expect(c.Args).To(ContainElement("20s"))
				Expect(c.Args).To(ContainElement("0 * * * *"))
			case "dummy":
				foundDummyContainer = true
			}
		}
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
			return nil
		}).Should(Succeed())
	})
})
