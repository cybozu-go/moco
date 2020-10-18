package controllers

import (
	"context"
	"errors"
	"fmt"
	mathrand "math/rand"
	"os"
	"time"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serror "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	systemNamespace = "test-moco-system"
	namespace       = "controllers-test"

	clusterName = "mysqlcluster"
)

func mysqlClusterResource() *mocov1alpha1.MySQLCluster {
	cluster := &mocov1alpha1.MySQLCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "MySQLCluster",
			APIVersion: mocov1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: namespace,
		},
		Spec: mocov1alpha1.MySQLClusterSpec{
			Replicas: 3,
			PodTemplate: mocov1alpha1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "mysqld",
							Image: "mysql:dev",
						},
					},
				},
			},
			DataVolumeClaimTemplateSpec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: *resource.NewQuantity(1<<10, resource.BinarySI),
					},
				},
			},
		},
	}
	return cluster
}

var _ = Describe("MySQLCluster controller", func() {

	ctx := context.Background()
	cluster := &mocov1alpha1.MySQLCluster{}

	BeforeEach(func() {
		sysNs := corev1.Namespace{}
		sysNs.Name = systemNamespace
		_, err := ctrl.CreateOrUpdate(ctx, k8sClient, &sysNs, func() error {
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())
		ns := corev1.Namespace{}
		ns.Name = namespace
		_, err = ctrl.CreateOrUpdate(ctx, k8sClient, &ns, func() error {
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())

		cluster = mysqlClusterResource()
		_, err = ctrl.CreateOrUpdate(ctx, k8sClient, cluster, func() error {
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())

		err = os.Setenv("POD_NAMESPACE", systemNamespace)
		Expect(err).ShouldNot(HaveOccurred())
	})

	Context("ServerIDBase", func() {
		It("should set ServerIDBase", func() {
			isUpdated, err := reconciler.setServerIDBaseIfNotAssigned(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			Eventually(func() error {
				var actual mocov1alpha1.MySQLCluster
				err = k8sClient.Get(ctx, client.ObjectKey{Name: clusterName, Namespace: namespace}, &actual)
				if err != nil {
					return err
				}

				if actual.Status.ServerIDBase == nil {
					return errors.New("status.ServerIDBase is not yet assigned")
				}

				return nil
			}, 5*time.Second).Should(Succeed())

			isUpdated, err = reconciler.setServerIDBaseIfNotAssigned(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})
	})

	Context("Secrets", func() {
		It("should create secrets", func() {
			isUpdated, err := reconciler.createSecretIfNotExist(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			ctrlSecretNS, ctrlSecretName := moco.GetSecretNameForController(cluster)
			initSecretNS := cluster.Namespace
			initSecretName := rootPasswordSecretPrefix + moco.UniqueName(cluster)

			initSecret := &corev1.Secret{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: initSecretNS, Name: initSecretName}, initSecret)
			Expect(err).ShouldNot(HaveOccurred())

			Expect(initSecret.Data).Should(HaveKey(moco.RootPasswordKey))
			Expect(initSecret.Data).Should(HaveKey(moco.OperatorPasswordKey))
			Expect(initSecret.Data).Should(HaveKey(moco.ReplicationPasswordKey))
			Expect(initSecret.Data).Should(HaveKey(moco.DonorPasswordKey))
			Expect(initSecret.Data).Should(HaveKey(moco.MiscPasswordKey))

			ctrlSecret := &corev1.Secret{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: ctrlSecretNS, Name: ctrlSecretName}, ctrlSecret)
			Expect(err).ShouldNot(HaveOccurred())

			Expect(ctrlSecret.Data).Should(HaveKey(moco.OperatorPasswordKey))
			Expect(ctrlSecret.Data).Should(HaveKey(moco.ReplicationPasswordKey))
			Expect(ctrlSecret.Data).Should(HaveKey(moco.DonorPasswordKey))

			isUpdated, err = reconciler.createSecretIfNotExist(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})

		It("should not recreate secret if init secret does not exist", func() {
			initSecretNS := cluster.Namespace
			initSecretName := rootPasswordSecretPrefix + moco.UniqueName(cluster)
			initSecret := &corev1.Secret{}
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: initSecretNS, Name: initSecretName}, initSecret)
			Expect(err).ShouldNot(HaveOccurred())

			err = k8sClient.Delete(ctx, initSecret)
			Expect(err).ShouldNot(HaveOccurred())

			isUpdated, err := reconciler.createSecretIfNotExist(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())

			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: initSecretNS, Name: initSecretName}, initSecret)
			reason := k8serror.ReasonForError(err)
			Expect(reason).Should(Equal(metav1.StatusReasonNotFound))
		})
	})

	Context("ConfigMaps", func() {
		It("should create configmap", func() {
			isUpdated, err := reconciler.createOrUpdateConfigMap(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			cm := &corev1.ConfigMap{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, cm)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(cm.Data).Should(HaveKey(moco.MySQLConfName))

			isUpdated, err = reconciler.createOrUpdateConfigMap(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})

		It("should merge with user defined configuration", func() {
			userDefinedConfName := "user-defined-my.cnf"
			cluster.Spec.MySQLConfigMapName = &userDefinedConfName

			userDefinedConf := &corev1.ConfigMap{}
			userDefinedConf.Namespace = cluster.Namespace
			userDefinedConf.Name = userDefinedConfName
			userDefinedConf.Data = map[string]string{
				"max_connections": "5000",
			}
			err := k8sClient.Create(ctx, userDefinedConf)
			Expect(err).ShouldNot(HaveOccurred())

			isUpdated, err := reconciler.createOrUpdateConfigMap(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			cm := &corev1.ConfigMap{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, cm)
			Expect(err).ShouldNot(HaveOccurred())

			Expect(cm.Data).Should(HaveKey(moco.MySQLConfName))
			conf := cm.Data[moco.MySQLConfName]
			Expect(conf).Should(ContainSubstring("max_connections = 5000"))
		})

		It("should set innodb_buffer_pool_size", func() {
			By("using default value if resource request is empty", func() {
				cm := &corev1.ConfigMap{}
				err := k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, cm)
				Expect(err).ShouldNot(HaveOccurred())

				Expect(cm.Data).Should(HaveKey(moco.MySQLConfName))
				conf := cm.Data[moco.MySQLConfName]
				Expect(conf).ShouldNot(ContainSubstring("innodb_buffer_pool_size"))
			})

			By("using default value if the container has less memory than the default", func() {
				cluster.Spec.PodTemplate.Spec.Containers[0].Resources = corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: *resource.NewQuantity(100<<20, resource.BinarySI),
					},
				}
				cm := &corev1.ConfigMap{}
				isUpdated, err := reconciler.createOrUpdateConfigMap(ctx, reconciler.Log, cluster)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(isUpdated).Should(BeTrue())

				err = k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, cm)
				Expect(err).ShouldNot(HaveOccurred())

				Expect(cm.Data).Should(HaveKey(moco.MySQLConfName))
				conf := cm.Data[moco.MySQLConfName]
				Expect(conf).ShouldNot(ContainSubstring("innodb_buffer_pool_size"))
			})

			By("setting the size of 70% of the request", func() {
				cm := &corev1.ConfigMap{}
				cluster.Spec.PodTemplate.Spec.Containers[0].Resources = corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: *resource.NewQuantity(256<<20, resource.BinarySI),
					},
				}

				isUpdated, err := reconciler.createOrUpdateConfigMap(ctx, reconciler.Log, cluster)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(isUpdated).Should(BeTrue())

				err = k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, cm)
				Expect(err).ShouldNot(HaveOccurred())

				Expect(cm.Data).Should(HaveKey(moco.MySQLConfName))
				conf := cm.Data[moco.MySQLConfName]
				Expect(conf).Should(ContainSubstring("innodb_buffer_pool_size = 179M")) // 256*0.7=179
			})

		})
	})

	Context("Headless service", func() {
		It("should create services", func() {
			isUpdated, err := reconciler.createOrUpdateHeadlessService(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			svc := &corev1.Service{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, svc)
			Expect(err).ShouldNot(HaveOccurred())

			isUpdated, err = reconciler.createOrUpdateHeadlessService(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})
	})

	Context("RBAC", func() {
		It("should not create service account if service account is given", func() {
			cluster.Spec.PodTemplate.Spec.ServiceAccountName = "test"
			isUpdated, err := reconciler.createOrUpdateRBAC(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})

		It("should create service account", func() {
			isUpdated, err := reconciler.createOrUpdateRBAC(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			sa := &corev1.ServiceAccount{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: serviceAccountPrefix + moco.UniqueName(cluster), Namespace: cluster.Namespace}, sa)
			Expect(err).ShouldNot(HaveOccurred())

			isUpdated, err = reconciler.createOrUpdateRBAC(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})
	})

	Context("Agent token", func() {
		It("should create agent token", func() {
			isUpdated, err := reconciler.generateAgentToken(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			err = k8sClient.Get(ctx, client.ObjectKey{Name: clusterName, Namespace: namespace}, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(cluster.Status.AgentToken).ShouldNot(BeEmpty())

			isUpdated, err = reconciler.generateAgentToken(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})
	})

	Context("StatefulSet", func() {
		It("should create statefulset", func() {
			serverIDBase := mathrand.Uint32()
			cluster.Status.ServerIDBase = &serverIDBase

			isUpdated, err := reconciler.createOrUpdateStatefulSet(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			sts := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, sts)
			Expect(err).ShouldNot(HaveOccurred())

			var mysqldContainer *corev1.Container
			var agentContainer *corev1.Container
			for i, c := range sts.Spec.Template.Spec.Containers {
				if c.Name == "mysqld" {
					mysqldContainer = &sts.Spec.Template.Spec.Containers[i]
				} else if c.Name == "agent" {
					agentContainer = &sts.Spec.Template.Spec.Containers[i]
				}
			}
			Expect(mysqldContainer).ShouldNot(BeNil())
			Expect(agentContainer).ShouldNot(BeNil())

			isUpdated, err = reconciler.createOrUpdateStatefulSet(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})

		It("should return error, when template does not contain mysqld container", func() {
			serverIDBase := mathrand.Uint32()
			cluster.Status.ServerIDBase = &serverIDBase
			cluster.Spec.PodTemplate = mocov1alpha1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "unknown",
							Image: "mysql:dev",
						},
					},
				},
			}

			_, err := reconciler.createOrUpdateStatefulSet(ctx, reconciler.Log, cluster)
			Expect(err).Should(HaveOccurred())
		})

		It("should return error, when template contains agent container", func() {
			serverIDBase := mathrand.Uint32()
			cluster.Status.ServerIDBase = &serverIDBase
			cluster.Spec.PodTemplate = mocov1alpha1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "mysqld",
							Image: "mysql:dev",
						},
						{
							Name:  "agent",
							Image: "mysql:dev",
						},
					},
				},
			}
			_, err := reconciler.createOrUpdateStatefulSet(ctx, reconciler.Log, cluster)
			Expect(err).Should(HaveOccurred())
		})

		It("update podTemplate", func() {

		})

		It("update volumeTemplate", func() {

		})

		It("update dataVolumeTemplate", func() {

		})
	})

	Context("Services", func() {
		It("should create services", func() {
			isUpdated, err := reconciler.createOrUpdateServices(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			createdPrimaryService := &corev1.Service{}
			createdReplicaService := &corev1.Service{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-primary", moco.UniqueName(cluster)), Namespace: namespace}, createdPrimaryService)
			Expect(err).ShouldNot(HaveOccurred())
			err = k8sClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-replica", moco.UniqueName(cluster)), Namespace: namespace}, createdReplicaService)
			Expect(err).ShouldNot(HaveOccurred())

			Expect(createdPrimaryService.Spec.Ports).Should(HaveLen(2))
			Expect(createdPrimaryService.Spec.Ports[0].Name).Should(Equal("mysql"))
			Expect(createdPrimaryService.Spec.Ports[0].Port).Should(BeNumerically("==", moco.MySQLPort))

			Expect(createdReplicaService.Spec.Ports).Should(HaveLen(2))
			Expect(createdPrimaryService.Spec.Ports[1].Name).Should(Equal("mysqlx"))
			Expect(createdPrimaryService.Spec.Ports[1].Port).Should(BeNumerically("==", moco.MySQLXPort))

			isUpdated, err = reconciler.createOrUpdateServices(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})

		It("should use serviceTemplate", func() {
			newCluster := &mocov1alpha1.MySQLCluster{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: clusterName, Namespace: namespace}, newCluster)
			Expect(err).ShouldNot(HaveOccurred())
			newCluster.Spec.ServiceTemplate = &corev1.ServiceSpec{
				Type: corev1.ServiceTypeLoadBalancer,
				Ports: []corev1.ServicePort{
					{
						Name:       "mysql",
						Protocol:   corev1.ProtocolTCP,
						Port:       8888,
						TargetPort: intstr.FromInt(8888),
					},
				},
			}
			err = k8sClient.Update(ctx, newCluster)
			Expect(err).ShouldNot(HaveOccurred())

			isUpdated, err := reconciler.createOrUpdateServices(ctx, reconciler.Log, newCluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			createdPrimaryService := &corev1.Service{}
			createdReplicaService := &corev1.Service{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-primary", moco.UniqueName(newCluster)), Namespace: namespace}, createdPrimaryService)
			Expect(err).ShouldNot(HaveOccurred())
			err = k8sClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-replica", moco.UniqueName(newCluster)), Namespace: namespace}, createdReplicaService)
			Expect(err).ShouldNot(HaveOccurred())

			Expect(createdPrimaryService.Spec.Type).Should(Equal(corev1.ServiceTypeLoadBalancer))
			Expect(createdReplicaService.Spec.Type).Should(Equal(corev1.ServiceTypeLoadBalancer))

			Expect(createdPrimaryService.Spec.Ports).Should(HaveLen(2))
			Expect(createdPrimaryService.Spec.Ports[0].Name).Should(Equal("mysql"))
			Expect(createdPrimaryService.Spec.Ports[0].Port).Should(BeNumerically("==", moco.MySQLPort))

			Expect(createdReplicaService.Spec.Ports).Should(HaveLen(2))
			Expect(createdPrimaryService.Spec.Ports[1].Name).Should(Equal("mysqlx"))
			Expect(createdPrimaryService.Spec.Ports[1].Port).Should(BeNumerically("==", moco.MySQLXPort))

			isUpdated, err = reconciler.createOrUpdateServices(ctx, reconciler.Log, newCluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})
	})
})
