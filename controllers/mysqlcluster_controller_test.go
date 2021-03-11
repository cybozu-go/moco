package controllers

import (
	"context"
	"errors"
	"fmt"
	mathrand "math/rand"
	"time"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	systemNamespace  = "test-moco-system"
	clusterNamespace = "controllers-test"
	clusterName      = "mysqlcluster"
)

var replicationSourceSecretName = "replication-source-secret"

func mysqlClusterResource() *mocov1alpha1.MySQLCluster {
	cluster := &mocov1alpha1.MySQLCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "MySQLCluster",
			APIVersion: mocov1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: clusterNamespace,
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
						corev1.ResourceStorage: *resource.NewQuantity(1<<30, resource.BinarySI),
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
		ns.Name = clusterNamespace
		_, err = ctrl.CreateOrUpdate(ctx, k8sClient, &ns, func() error {
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())

		cluster = mysqlClusterResource()
		_, err = ctrl.CreateOrUpdate(ctx, k8sClient, cluster, func() error {
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())
	})

	Context("ServerIDBase", func() {
		It("should set ServerIDBase", func() {
			isUpdated, err := reconciler.setServerIDBaseIfNotAssigned(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			Eventually(func() error {
				var actual mocov1alpha1.MySQLCluster
				err = k8sClient.Get(ctx, client.ObjectKey{Name: clusterName, Namespace: clusterNamespace}, &actual)
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
			isUpdated, err := reconciler.createControllerSecretIfNotExist(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			isUpdated, err = reconciler.createOrUpdateSecret(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			ctrlSecretName := moco.GetControllerSecretName(cluster)
			clusterSecretNS := cluster.Namespace
			clusterSecretName := moco.GetClusterSecretName(cluster.Name)
			myCnfSecretName := moco.GetMyCnfSecretName(cluster.Name)
			myCnfSecretNS := cluster.Namespace

			ctrlSecret := &corev1.Secret{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: systemNamespace, Name: ctrlSecretName}, ctrlSecret)
			Expect(err).ShouldNot(HaveOccurred())

			Expect(ctrlSecret.Data).Should(HaveKey(moco.AdminPasswordKey))
			Expect(ctrlSecret.Data).Should(HaveKey(moco.ReplicationPasswordKey))
			Expect(ctrlSecret.Data).Should(HaveKey(moco.CloneDonorPasswordKey))
			Expect(ctrlSecret.Data).Should(HaveKey(moco.AgentPasswordKey))
			Expect(ctrlSecret.Data).Should(HaveKey(moco.ReadOnlyPasswordKey))
			Expect(ctrlSecret.Data).Should(HaveKey(moco.WritablePasswordKey))

			clusterSecret := &corev1.Secret{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: clusterSecretNS, Name: clusterSecretName}, clusterSecret)
			Expect(err).ShouldNot(HaveOccurred())

			Expect(clusterSecret.Data).Should(HaveKey(moco.AdminPasswordKey))
			Expect(clusterSecret.Data).Should(HaveKey(moco.ReplicationPasswordKey))
			Expect(clusterSecret.Data).Should(HaveKey(moco.CloneDonorPasswordKey))
			Expect(clusterSecret.Data).Should(HaveKey(moco.AgentPasswordKey))
			Expect(clusterSecret.Data).Should(HaveKey(moco.ReadOnlyPasswordKey))
			Expect(clusterSecret.Data).Should(HaveKey(moco.WritablePasswordKey))

			myCnfSecret := &corev1.Secret{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: myCnfSecretNS, Name: myCnfSecretName}, myCnfSecret)
			Expect(err).ShouldNot(HaveOccurred())

			Expect(myCnfSecret.Data).Should(HaveKey(moco.ReadOnlyMyCnfKey))
			Expect(myCnfSecret.Data).Should(HaveKey(moco.WritableMyCnfKey))

			isUpdated, err = reconciler.createOrUpdateSecret(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})

		It("if delete clusterSecret and myConfSecret, they will be regenerated", func() {
			ctrlSecretName := moco.GetControllerSecretName(cluster)
			ctrlSecret := &corev1.Secret{}
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: systemNamespace, Name: ctrlSecretName}, ctrlSecret)
			Expect(err).ShouldNot(HaveOccurred())

			clusterSecretNS := cluster.Namespace
			clusterSecretName := moco.GetClusterSecretName(cluster.Name)
			clusterSecret := &corev1.Secret{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: clusterSecretNS, Name: clusterSecretName}, clusterSecret)
			Expect(err).ShouldNot(HaveOccurred())

			err = k8sClient.Delete(ctx, clusterSecret)
			Expect(err).ShouldNot(HaveOccurred())

			myCnfSecretNS := cluster.Namespace
			myCnfSecretName := moco.GetMyCnfSecretName(cluster.Name)
			myCnfSecret := &corev1.Secret{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: myCnfSecretNS, Name: myCnfSecretName}, myCnfSecret)
			Expect(err).ShouldNot(HaveOccurred())

			err = k8sClient.Delete(ctx, myCnfSecret)
			Expect(err).ShouldNot(HaveOccurred())

			isUpdated, err := reconciler.createOrUpdateSecret(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: clusterSecretNS, Name: clusterSecretName}, clusterSecret)
			Expect(err).ShouldNot(HaveOccurred())
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: myCnfSecretNS, Name: myCnfSecretName}, myCnfSecret)
			Expect(err).ShouldNot(HaveOccurred())
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
			err = k8sClient.Get(ctx, client.ObjectKey{Name: moco.GetServiceAccountName(cluster.Name), Namespace: cluster.Namespace}, sa)
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

			err = k8sClient.Get(ctx, client.ObjectKey{Name: clusterName, Namespace: clusterNamespace}, cluster)
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

			var binaryCopyInitContainer *corev1.Container
			var entrypointInitContainer *corev1.Container
			for i, c := range sts.Spec.Template.Spec.InitContainers {
				if c.Name == binaryCopyContainerName {
					binaryCopyInitContainer = &sts.Spec.Template.Spec.InitContainers[i]
				} else if c.Name == entrypointInitContainerName {
					entrypointInitContainer = &sts.Spec.Template.Spec.InitContainers[i]
				}
			}

			Expect(binaryCopyInitContainer).ShouldNot(BeNil())
			Expect(entrypointInitContainer).ShouldNot(BeNil())
			Expect(len(binaryCopyInitContainer.VolumeMounts)).Should(Equal(1))
			Expect(len(entrypointInitContainer.VolumeMounts)).Should(Equal(8))
			Expect(entrypointInitContainer.Command).Should(Equal([]string{
				"/moco-bin/moco-agent", "init", fmt.Sprintf("--server-id-base=%d", *cluster.Status.ServerIDBase),
			}))

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
			Expect(mysqldContainer.LivenessProbe).ShouldNot(BeNil())
			Expect(mysqldContainer.LivenessProbe.Exec.Command).Should(Equal([]string{"/moco-bin/moco-agent", "ping"}))
			Expect(mysqldContainer.LivenessProbe.InitialDelaySeconds).Should(BeNumerically("==", 5))
			Expect(mysqldContainer.LivenessProbe.PeriodSeconds).Should(BeNumerically("==", 5))
			Expect(mysqldContainer.LivenessProbe.TimeoutSeconds).Should(BeNumerically("==", 1))
			Expect(mysqldContainer.LivenessProbe.SuccessThreshold).Should(BeNumerically("==", 1))
			Expect(mysqldContainer.LivenessProbe.FailureThreshold).Should(BeNumerically("==", 3))
			Expect(mysqldContainer.ReadinessProbe).ShouldNot(BeNil())
			Expect(mysqldContainer.ReadinessProbe.Exec.Command).Should(Equal([]string{"/moco-bin/grpc-health-probe", "-addr=localhost:9080"}))
			Expect(mysqldContainer.ReadinessProbe.InitialDelaySeconds).Should(BeNumerically("==", 10))
			Expect(mysqldContainer.ReadinessProbe.PeriodSeconds).Should(BeNumerically("==", 5))
			Expect(mysqldContainer.ReadinessProbe.TimeoutSeconds).Should(BeNumerically("==", 1))
			Expect(mysqldContainer.ReadinessProbe.SuccessThreshold).Should(BeNumerically("==", 1))
			Expect(mysqldContainer.ReadinessProbe.FailureThreshold).Should(BeNumerically("==", 3))

			Expect(agentContainer).ShouldNot(BeNil())
			Expect(len(agentContainer.VolumeMounts)).Should(Equal(5))
			Expect(agentContainer.Command).Should(Equal([]string{
				"/moco-bin/moco-agent", "server", "--log-rotation-schedule", cluster.Spec.LogRotationSchedule,
			}))

			var claim *corev1.PersistentVolumeClaim
			for i, v := range sts.Spec.VolumeClaimTemplates {
				if v.Name == mysqlDataVolumeName {
					claim = &sts.Spec.VolumeClaimTemplates[i]
				}
			}
			Expect(claim).ShouldNot(BeNil())
			Expect(claim.Spec.Resources.Requests.Storage().Value()).Should(BeNumerically("==", 1<<30))

			isUpdated, err = reconciler.createOrUpdateStatefulSet(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})

		It("should mount volumes of MyCnfSecret", func() {
			serverIDBase := mathrand.Uint32()
			cluster.Status.ServerIDBase = &serverIDBase
			cluster.Spec.ReplicationSourceSecretName = &replicationSourceSecretName

			isUpdated, err := reconciler.createOrUpdateStatefulSet(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			sts := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, sts)
			Expect(err).ShouldNot(HaveOccurred())

			var mysqldContainer *corev1.Container
			for i, c := range sts.Spec.Template.Spec.Containers {
				if c.Name == "mysqld" {
					mysqldContainer = &sts.Spec.Template.Spec.Containers[i]
				}
			}
			Expect(mysqldContainer).ShouldNot(BeNil())
			Expect(len(mysqldContainer.VolumeMounts)).Should(Equal(8))
			Expect(mysqldContainer.VolumeMounts).Should(ContainElement(corev1.VolumeMount{
				MountPath: moco.MyCnfSecretPath,
				Name:      myCnfSecretVolumeName,
			}))
			defaultMode := corev1.SecretVolumeSourceDefaultMode
			Expect(sts.Spec.Template.Spec.Volumes).Should(ContainElement(corev1.Volume{
				Name: myCnfSecretVolumeName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  moco.GetMyCnfSecretName(cluster.Name),
						DefaultMode: &defaultMode,
					},
				},
			}))
		})

		It("should mount volumes of ReplicationSourceSecret", func() {
			serverIDBase := mathrand.Uint32()
			cluster.Status.ServerIDBase = &serverIDBase
			cluster.Spec.ReplicationSourceSecretName = &replicationSourceSecretName

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: replicationSourceSecretName, Namespace: cluster.Namespace},
				Data:       make(map[string][]byte),
			}
			_, err := ctrl.CreateOrUpdate(ctx, k8sClient, secret, func() error {
				return nil
			})
			Expect(err).ShouldNot(HaveOccurred())

			isUpdated, err := reconciler.createOrUpdateStatefulSet(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			sts := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, sts)
			Expect(err).ShouldNot(HaveOccurred())

			var agentContainer *corev1.Container
			for i, c := range sts.Spec.Template.Spec.Containers {
				if c.Name == "agent" {
					agentContainer = &sts.Spec.Template.Spec.Containers[i]
				}
			}
			Expect(agentContainer).ShouldNot(BeNil())
			Expect(len(agentContainer.VolumeMounts)).Should(Equal(6))
			Expect(agentContainer.VolumeMounts).Should(ContainElement(corev1.VolumeMount{
				MountPath: moco.ReplicationSourceSecretPath,
				Name:      replicationSourceSecretVolumeName,
			}))
			defaultMode := corev1.SecretVolumeSourceDefaultMode
			Expect(sts.Spec.Template.Spec.Volumes).Should(ContainElement(corev1.Volume{
				Name: replicationSourceSecretVolumeName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  replicationSourceSecretName,
						DefaultMode: &defaultMode,
					},
				},
			}))
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

		It("should overwrite probes in mysqld container", func() {
			serverIDBase := mathrand.Uint32()
			cluster.Status.ServerIDBase = &serverIDBase
			cluster.Spec.PodTemplate = mocov1alpha1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "mysqld",
							Image: "mysql:dev",
							LivenessProbe: &corev1.Probe{
								Handler: corev1.Handler{
									Exec: &corev1.ExecAction{
										Command: []string{"/dummy/liveness"},
									},
								},
								InitialDelaySeconds: 999,
								PeriodSeconds:       999,
							},
							ReadinessProbe: &corev1.Probe{
								Handler: corev1.Handler{
									Exec: &corev1.ExecAction{
										Command: []string{"/dummy/readiness"},
									},
								},
								InitialDelaySeconds: 999,
								PeriodSeconds:       999,
							},
						},
					},
				},
			}
			isUpdated, err := reconciler.createOrUpdateStatefulSet(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			sts := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, sts)
			Expect(err).ShouldNot(HaveOccurred())

			var mysqldContainer *corev1.Container
			for i, c := range sts.Spec.Template.Spec.Containers {
				if c.Name == "mysqld" {
					mysqldContainer = &sts.Spec.Template.Spec.Containers[i]
				}
			}
			Expect(mysqldContainer).ShouldNot(BeNil())
			Expect(mysqldContainer.LivenessProbe).ShouldNot(BeNil())
			Expect(mysqldContainer.LivenessProbe.Exec.Command).Should(Equal([]string{"/moco-bin/moco-agent", "ping"}))
			Expect(mysqldContainer.LivenessProbe.InitialDelaySeconds).Should(BeNumerically("==", 5))
			Expect(mysqldContainer.LivenessProbe.PeriodSeconds).Should(BeNumerically("==", 5))
			Expect(mysqldContainer.LivenessProbe.TimeoutSeconds).Should(BeNumerically("==", 1))
			Expect(mysqldContainer.LivenessProbe.SuccessThreshold).Should(BeNumerically("==", 1))
			Expect(mysqldContainer.LivenessProbe.FailureThreshold).Should(BeNumerically("==", 3))
			Expect(mysqldContainer.ReadinessProbe).ShouldNot(BeNil())
			Expect(mysqldContainer.ReadinessProbe.Exec.Command).Should(Equal([]string{"/moco-bin/grpc-health-probe", "-addr=localhost:9080"}))
			Expect(mysqldContainer.ReadinessProbe.InitialDelaySeconds).Should(BeNumerically("==", 10))
			Expect(mysqldContainer.ReadinessProbe.PeriodSeconds).Should(BeNumerically("==", 5))
			Expect(mysqldContainer.ReadinessProbe.TimeoutSeconds).Should(BeNumerically("==", 1))
			Expect(mysqldContainer.ReadinessProbe.SuccessThreshold).Should(BeNumerically("==", 1))
			Expect(mysqldContainer.ReadinessProbe.FailureThreshold).Should(BeNumerically("==", 3))
		})

		It("update podTemplate", func() {
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
							Name:  "fluent-bit",
							Image: "fluent-bit:dev",
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "config",
									ReadOnly:  true,
									MountPath: "/fluent-bit/etc/fluent-bit.conf",
									SubPath:   "fluent-bit.conf",
								},
							},
						},
					},
				},
			}
			isUpdated, err := reconciler.createOrUpdateStatefulSet(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			sts := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, sts)
			Expect(err).ShouldNot(HaveOccurred())

			var mysqldContainer *corev1.Container
			var fluentBitContainer *corev1.Container
			for i, c := range sts.Spec.Template.Spec.Containers {
				if c.Name == "mysqld" {
					mysqldContainer = &sts.Spec.Template.Spec.Containers[i]
				} else if c.Name == "fluent-bit" {
					fluentBitContainer = &sts.Spec.Template.Spec.Containers[i]
				}
			}
			Expect(mysqldContainer).ShouldNot(BeNil())
			Expect(fluentBitContainer).ShouldNot(BeNil())
			Expect(fluentBitContainer.VolumeMounts).Should(HaveLen(1))

			isUpdated, err = reconciler.createOrUpdateStatefulSet(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})

		It("should use volumeTemplate", func() {
			oldSts := &appsv1.StatefulSet{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, oldSts)
			Expect(err).ShouldNot(HaveOccurred())
			err = k8sClient.Delete(ctx, oldSts)
			Expect(err).ShouldNot(HaveOccurred())

			serverIDBase := mathrand.Uint32()
			cluster.Status.ServerIDBase = &serverIDBase
			cluster.Spec.VolumeClaimTemplates = []mocov1alpha1.PersistentVolumeClaim{
				{
					ObjectMeta: mocov1alpha1.ObjectMeta{
						Name: "test-volume",
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: *resource.NewQuantity(1<<10, resource.BinarySI),
							},
						},
					},
				},
			}

			isUpdated, err := reconciler.createOrUpdateStatefulSet(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			sts := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, sts)
			Expect(err).ShouldNot(HaveOccurred())

			var testClaim *corev1.PersistentVolumeClaim
			var dataClaim *corev1.PersistentVolumeClaim
			for i, v := range sts.Spec.VolumeClaimTemplates {
				if v.Name == "test-volume" {
					testClaim = &sts.Spec.VolumeClaimTemplates[i]
				}
				if v.Name == mysqlDataVolumeName {
					dataClaim = &sts.Spec.VolumeClaimTemplates[i]
				}
			}
			Expect(testClaim).ShouldNot(BeNil())
			Expect(dataClaim).ShouldNot(BeNil())

			isUpdated, err = reconciler.createOrUpdateStatefulSet(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})

		It("should return error, when volumeTemplate contains mysql-data", func() {
			oldSts := &appsv1.StatefulSet{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, oldSts)
			Expect(err).ShouldNot(HaveOccurred())
			err = k8sClient.Delete(ctx, oldSts)
			Expect(err).ShouldNot(HaveOccurred())

			serverIDBase := mathrand.Uint32()
			cluster.Status.ServerIDBase = &serverIDBase
			cluster.Spec.VolumeClaimTemplates = []mocov1alpha1.PersistentVolumeClaim{
				{
					ObjectMeta: mocov1alpha1.ObjectMeta{
						Name: mysqlDataVolumeName,
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: *resource.NewQuantity(1<<10, resource.BinarySI),
							},
						},
					},
				},
			}

			_, err = reconciler.createOrUpdateStatefulSet(ctx, reconciler.Log, cluster)
			Expect(err).Should(HaveOccurred())
		})

	})

	Context("Services", func() {
		It("should create services", func() {
			isUpdated, err := reconciler.createOrUpdateServices(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			createdPrimaryService := &corev1.Service{}
			createdReplicaService := &corev1.Service{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-primary", moco.UniqueName(cluster)), Namespace: clusterNamespace}, createdPrimaryService)
			Expect(err).ShouldNot(HaveOccurred())
			err = k8sClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-replica", moco.UniqueName(cluster)), Namespace: clusterNamespace}, createdReplicaService)
			Expect(err).ShouldNot(HaveOccurred())

			Expect(createdPrimaryService.Spec.Type).Should(Equal(corev1.ServiceTypeClusterIP))
			Expect(createdReplicaService.Spec.Type).Should(Equal(corev1.ServiceTypeClusterIP))

			Expect(createdPrimaryService.Spec.Ports).Should(HaveLen(2))
			Expect(createdPrimaryService.Spec.Ports[0].Name).Should(Equal("mysql"))
			Expect(createdPrimaryService.Spec.Ports[0].Port).Should(BeNumerically("==", moco.MySQLPort))
			Expect(createdPrimaryService.Spec.Ports[1].Name).Should(Equal("mysqlx"))
			Expect(createdPrimaryService.Spec.Ports[1].Port).Should(BeNumerically("==", moco.MySQLXPort))

			Expect(createdReplicaService.Spec.Ports).Should(HaveLen(2))
			Expect(createdReplicaService.Spec.Ports[0].Name).Should(Equal("mysql"))
			Expect(createdReplicaService.Spec.Ports[0].Port).Should(BeNumerically("==", moco.MySQLPort))
			Expect(createdReplicaService.Spec.Ports[1].Name).Should(Equal("mysqlx"))
			Expect(createdReplicaService.Spec.Ports[1].Port).Should(BeNumerically("==", moco.MySQLXPort))

			isUpdated, err = reconciler.createOrUpdateServices(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})

		It("should use serviceTemplate", func() {
			newCluster := &mocov1alpha1.MySQLCluster{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: clusterName, Namespace: clusterNamespace}, newCluster)
			Expect(err).ShouldNot(HaveOccurred())
			newCluster.Spec.ServiceTemplate = &mocov1alpha1.ServiceTemplate{}
			annotation := map[string]string{
				"annotation-key": "annotation-value",
			}
			newCluster.Spec.ServiceTemplate.Annotations = annotation
			labelKey := "label-key"
			labelValue := "label-value"
			newCluster.Spec.ServiceTemplate.Labels = map[string]string{
				labelKey: labelValue,
			}
			newCluster.Spec.ServiceTemplate.Spec = &corev1.ServiceSpec{
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
			err = k8sClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-primary", moco.UniqueName(newCluster)), Namespace: clusterNamespace}, createdPrimaryService)
			Expect(err).ShouldNot(HaveOccurred())
			err = k8sClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-replica", moco.UniqueName(newCluster)), Namespace: clusterNamespace}, createdReplicaService)
			Expect(err).ShouldNot(HaveOccurred())

			Expect(createdPrimaryService.ObjectMeta.Annotations).Should(Equal(annotation))
			Expect(createdReplicaService.ObjectMeta.Annotations).Should(Equal(annotation))

			Expect(createdPrimaryService.ObjectMeta.Labels[labelKey]).Should(Equal(labelValue))
			Expect(createdReplicaService.ObjectMeta.Labels[labelKey]).Should(Equal(labelValue))

			Expect(createdPrimaryService.Spec.Type).Should(Equal(corev1.ServiceTypeLoadBalancer))
			Expect(createdReplicaService.Spec.Type).Should(Equal(corev1.ServiceTypeLoadBalancer))

			Expect(createdPrimaryService.Spec.Ports).Should(HaveLen(2))
			Expect(createdPrimaryService.Spec.Ports[0].Name).Should(Equal("mysql"))
			Expect(createdPrimaryService.Spec.Ports[0].Port).Should(BeNumerically("==", moco.MySQLPort))
			Expect(createdPrimaryService.Spec.Ports[1].Name).Should(Equal("mysqlx"))
			Expect(createdPrimaryService.Spec.Ports[1].Port).Should(BeNumerically("==", moco.MySQLXPort))

			Expect(createdReplicaService.Spec.Ports).Should(HaveLen(2))
			Expect(createdReplicaService.Spec.Ports[0].Name).Should(Equal("mysql"))
			Expect(createdReplicaService.Spec.Ports[0].Port).Should(BeNumerically("==", moco.MySQLPort))
			Expect(createdReplicaService.Spec.Ports[1].Name).Should(Equal("mysqlx"))
			Expect(createdReplicaService.Spec.Ports[1].Port).Should(BeNumerically("==", moco.MySQLXPort))

			isUpdated, err = reconciler.createOrUpdateServices(ctx, reconciler.Log, newCluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})
	})

	Context("PodDisruptionBudget", func() {
		It("should create pod disruption budget", func() {
			expectedSpec := policyv1beta1.PodDisruptionBudgetSpec{
				MaxUnavailable: &intstr.IntOrString{
					Type:   intstr.Int,
					IntVal: 1,
				},
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						moco.ClusterKey:   moco.UniqueName(cluster),
						moco.AppNameKey:   moco.AppName,
						moco.ManagedByKey: moco.MyName,
					},
				},
			}

			cluster.Spec.Replicas = 1

			isUpdated, err := reconciler.createOrUpdatePodDisruptionBudget(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			pdb := &policyv1beta1.PodDisruptionBudget{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, pdb)
			Expect(err).ShouldNot(HaveOccurred())

			Expect(pdb.Spec).Should(Equal(expectedSpec))

			isUpdated, err = reconciler.createOrUpdatePodDisruptionBudget(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})

		It("should fill appropriate MaxUnavailable", func() {
			expectedSpec := policyv1beta1.PodDisruptionBudgetSpec{
				MaxUnavailable: &intstr.IntOrString{
					Type:   intstr.Int,
					IntVal: 1,
				},
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						moco.ClusterKey:   moco.UniqueName(cluster),
						moco.AppNameKey:   moco.AppName,
						moco.ManagedByKey: moco.MyName,
					},
				},
			}

			By("checking in case of 5 replicas")
			cluster.Spec.Replicas = 5
			isUpdated, err := reconciler.createOrUpdatePodDisruptionBudget(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			pdb := &policyv1beta1.PodDisruptionBudget{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, pdb)
			Expect(err).ShouldNot(HaveOccurred())
			expectedSpec.MaxUnavailable.IntVal = 2
			Expect(pdb.Spec).Should(Equal(expectedSpec))

			By("checking in case of 3 replicas")
			cluster.Spec.Replicas = 3
			isUpdated, err = reconciler.createOrUpdatePodDisruptionBudget(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			pdb = &policyv1beta1.PodDisruptionBudget{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: moco.UniqueName(cluster), Namespace: cluster.Namespace}, pdb)
			Expect(err).ShouldNot(HaveOccurred())
			expectedSpec.MaxUnavailable.IntVal = 1
			Expect(pdb.Spec).Should(Equal(expectedSpec))
		})
	})
})
