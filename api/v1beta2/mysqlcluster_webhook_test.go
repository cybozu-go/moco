package v1beta2_test

import (
	"context"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func makeMySQLCluster() *mocov1beta2.MySQLCluster {
	return &mocov1beta2.MySQLCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: mocov1beta2.MySQLClusterSpec{
			Replicas: 1,
			PodTemplate: mocov1beta2.PodTemplateSpec{
				Spec: (mocov1beta2.PodSpecApplyConfiguration)(*corev1ac.PodSpec().WithContainers(corev1ac.Container().WithName("mysqld"))),
			},
			VolumeClaimTemplates: []mocov1beta2.PersistentVolumeClaim{
				{
					ObjectMeta: mocov1beta2.ObjectMeta{
						Name: "mysql-data",
					},
					Spec: mocov1beta2.PersistentVolumeClaimSpecApplyConfiguration(*corev1ac.PersistentVolumeClaimSpec().
						WithStorageClassName("default").
						WithResources(
							corev1ac.VolumeResourceRequirements().WithRequests(corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")})),
					),
				},
			},
		},
	}
}

func deleteMySQLCluster() error {
	r := &mocov1beta2.MySQLCluster{}
	err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "test"}, r)
	if apierrors.IsNotFound(err) {
		return nil
	}

	if err != nil {
		return err
	}

	r.Finalizers = nil
	if err := k8sClient.Update(ctx, r); err != nil {
		return err
	}

	if err := k8sClient.Delete(ctx, r); err != nil {
		return err
	}

	return nil
}

func createStorageClass() error {
	defaultSC := storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
			Annotations: map[string]string{
				"storageclass.kubernetes.io/is-default-class": "true",
			},
		},
		Provisioner:          "dummy",
		AllowVolumeExpansion: new(true),
	}

	if err := k8sClient.Create(ctx, &defaultSC); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	notSupportVolumeExpansionSC := storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "not-support-volume-expansion",
		},
		Provisioner:          "dummy",
		AllowVolumeExpansion: new(false),
	}

	if err := k8sClient.Create(ctx, &notSupportVolumeExpansionSC); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	return nil
}

var _ = Describe("MySQLCluster Webhook", func() {
	ctx := context.TODO()

	BeforeEach(func() {
		err := createStorageClass()
		Expect(err).NotTo(HaveOccurred())
		err = deleteMySQLCluster()
		Expect(err).NotTo(HaveOccurred())
	})

	It("should create MySQL cluster with the sane defaults", func() {
		r := makeMySQLCluster()
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())

		Expect(r.Finalizers).To(ContainElement(constants.MySQLClusterFinalizer))
	})

	It("should deny without mysqld-data volume claim template", func() {
		r := makeMySQLCluster()
		r.Spec.VolumeClaimTemplates = nil
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny without storage size in volume claim template", func() {
		r := makeMySQLCluster()
		r.Spec.VolumeClaimTemplates[0].Spec.Resources = nil
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should set a valid serverIDBase", func() {
		r := makeMySQLCluster()
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Spec.ServerIDBase).To(BeNumerically(">", 0))
	})

	It("should deny negative values for serverIDBase", func() {
		r := makeMySQLCluster()
		r.Spec.ServerIDBase = -3
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should allow a valid logRotationSchedule", func() {
		r := makeMySQLCluster()
		r.Spec.LogRotationSchedule = "@every 30s"
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should deny an invalid logRotationSchedule", func() {
		r := makeMySQLCluster()
		r.Spec.LogRotationSchedule = "hoge fuga"
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should allow a valid logRotationSize", func() {
		r := makeMySQLCluster()
		r.Spec.LogRotationSize = 1024
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should deny an invalid logRotationSize", func() {
		r := makeMySQLCluster()
		r.Spec.LogRotationSize = -1
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny without mysqld container", func() {
		r := makeMySQLCluster()
		r.Spec.PodTemplate.Spec.Containers = nil
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny without container name", func() {
		r := makeMySQLCluster()
		spec := (corev1ac.PodSpecApplyConfiguration)(r.Spec.PodTemplate.Spec)
		spec.WithContainers(corev1ac.Container().WithImage("image:dev"))
		r.Spec.PodTemplate.Spec = (mocov1beta2.PodSpecApplyConfiguration)(spec)
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny without init container name", func() {
		r := makeMySQLCluster()
		spec := (corev1ac.PodSpecApplyConfiguration)(r.Spec.PodTemplate.Spec)
		spec.WithInitContainers(corev1ac.Container().WithImage("image:dev"))
		r.Spec.PodTemplate.Spec = (mocov1beta2.PodSpecApplyConfiguration)(spec)
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny mysqld container using reserved port", func() {
		for _, port := range []*corev1ac.ContainerPortApplyConfiguration{
			corev1ac.ContainerPort().WithContainerPort(constants.MySQLPort),
			corev1ac.ContainerPort().WithName(constants.MySQLPortName),
			corev1ac.ContainerPort().WithContainerPort(constants.MySQLAdminPort),
			corev1ac.ContainerPort().WithName(constants.MySQLAdminPortName),
			corev1ac.ContainerPort().WithContainerPort(constants.MySQLXPort),
			corev1ac.ContainerPort().WithName(constants.MySQLXPortName),
			corev1ac.ContainerPort().WithContainerPort(constants.MySQLHealthPort),
			corev1ac.ContainerPort().WithName(constants.MySQLHealthPortName),
		} {
			r := makeMySQLCluster()
			r.Spec.PodTemplate.Spec.Containers[0].WithPorts(port)
			err := k8sClient.Create(ctx, r)
			Expect(err).To(HaveOccurred())
		}
	})

	It("should deny reserved volume names", func() {
		for _, volname := range []string{
			constants.TmpVolumeName, constants.RunVolumeName, constants.VarLogVolumeName,
			constants.MySQLConfVolumeName, constants.MySQLInitConfVolumeName,
			constants.MySQLConfSecretVolumeName, constants.SlowQueryLogAgentConfigVolumeName,
		} {
			r := makeMySQLCluster()
			spec := (corev1ac.PodSpecApplyConfiguration)(r.Spec.PodTemplate.Spec)
			spec.WithVolumes(corev1ac.Volume().WithName(volname))
			r.Spec.PodTemplate.Spec = (mocov1beta2.PodSpecApplyConfiguration)(spec)
			err := k8sClient.Create(ctx, r)
			Expect(err).To(HaveOccurred())
		}
	})

	It("should deny the reserved password-rotation-restart pod template annotation", func() {
		r := makeMySQLCluster()
		r.Spec.PodTemplate.Annotations = map[string]string{
			constants.AnnPasswordRotationRestart: "user-set-value",
		}
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny agent container", func() {
		r := makeMySQLCluster()
		spec := (corev1ac.PodSpecApplyConfiguration)(r.Spec.PodTemplate.Spec)
		spec.WithContainers(corev1ac.Container().WithName("agent"))
		r.Spec.PodTemplate.Spec = (mocov1beta2.PodSpecApplyConfiguration)(spec)
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny slow query log container if not disabled", func() {
		r := makeMySQLCluster()
		spec := (corev1ac.PodSpecApplyConfiguration)(r.Spec.PodTemplate.Spec)
		spec.WithContainers(corev1ac.Container().WithName(constants.SlowQueryLogAgentContainerName))
		r.Spec.PodTemplate.Spec = (mocov1beta2.PodSpecApplyConfiguration)(spec)
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should allow slow log container if disabled", func() {
		r := makeMySQLCluster()
		spec := (corev1ac.PodSpecApplyConfiguration)(r.Spec.PodTemplate.Spec)
		spec.WithContainers(corev1ac.Container().WithName(constants.SlowQueryLogAgentContainerName))
		r.Spec.PodTemplate.Spec = (mocov1beta2.PodSpecApplyConfiguration)(spec)
		r.Spec.DisableSlowQueryLogContainer = true
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should deny mysqld_exporter container if enabled", func() {
		r := makeMySQLCluster()
		r.Spec.Collectors = []string{"engine_innodb_status", "info_schema.innodb_metrics"}
		spec := (corev1ac.PodSpecApplyConfiguration)(r.Spec.PodTemplate.Spec)
		spec.WithContainers(corev1ac.Container().WithName(constants.ExporterContainerName))
		r.Spec.PodTemplate.Spec = (mocov1beta2.PodSpecApplyConfiguration)(spec)
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should allow mysqld_exporter container if not enabled", func() {
		r := makeMySQLCluster()
		spec := (corev1ac.PodSpecApplyConfiguration)(r.Spec.PodTemplate.Spec)
		spec.WithContainers(corev1ac.Container().WithName(constants.ExporterContainerName))
		r.Spec.PodTemplate.Spec = (mocov1beta2.PodSpecApplyConfiguration)(spec)
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should allow non-reserved init containers", func() {
		r := makeMySQLCluster()
		spec := (corev1ac.PodSpecApplyConfiguration)(r.Spec.PodTemplate.Spec)
		spec.WithInitContainers(corev1ac.Container().WithName("foobar"))
		r.Spec.PodTemplate.Spec = (mocov1beta2.PodSpecApplyConfiguration)(spec)
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should deny moco-init init containers", func() {
		r := makeMySQLCluster()
		spec := (corev1ac.PodSpecApplyConfiguration)(r.Spec.PodTemplate.Spec)
		spec.WithInitContainers(corev1ac.Container().WithName("moco-init"))
		r.Spec.PodTemplate.Spec = (mocov1beta2.PodSpecApplyConfiguration)(spec)
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny decreasing replicas", func() {
		r := makeMySQLCluster()
		r.Spec.Replicas = 3
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())

		r.Spec.Replicas = 1
		err = k8sClient.Update(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny negative values for replicas", func() {
		r := makeMySQLCluster()
		r.Spec.Replicas = 4
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())

		r.Spec.Replicas = -1
		err = k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny adding replication source secret", func() {
		r := makeMySQLCluster()
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())

		r.Spec.ReplicationSourceSecretName = new("fuga")
		err = k8sClient.Update(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny changing replication source secret name", func() {
		r := makeMySQLCluster()
		r.Spec.ReplicationSourceSecretName = new("foo")
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())

		r.Spec.ReplicationSourceSecretName = new("bar")
		err = k8sClient.Update(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny invalid restore spec", func() {
		r := makeMySQLCluster()
		r.Spec.Restore = &mocov1beta2.RestoreSpec{
			SourceNamespace: "test",
			RestorePoint:    metav1.Now(),
			JobConfig: mocov1beta2.JobConfig{
				ServiceAccountName: "foo",
				BucketConfig: mocov1beta2.BucketConfig{
					BucketName: "mybucket",
				},
			},
		}
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())

		r = makeMySQLCluster()
		r.Spec.Restore = &mocov1beta2.RestoreSpec{
			SourceName:   "test",
			RestorePoint: metav1.Now(),
			JobConfig: mocov1beta2.JobConfig{
				ServiceAccountName: "foo",
				BucketConfig: mocov1beta2.BucketConfig{
					BucketName: "mybucket",
				},
			},
		}
		err = k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())

		r = makeMySQLCluster()
		r.Spec.Restore = &mocov1beta2.RestoreSpec{
			SourceName:      "test",
			SourceNamespace: "test",
			JobConfig: mocov1beta2.JobConfig{
				ServiceAccountName: "foo",
				BucketConfig: mocov1beta2.BucketConfig{
					BucketName: "mybucket",
				},
			},
		}
		err = k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())

		r = makeMySQLCluster()
		r.Spec.Restore = &mocov1beta2.RestoreSpec{
			SourceName:      "test",
			SourceNamespace: "test",
			RestorePoint:    metav1.Now(),
			JobConfig: mocov1beta2.JobConfig{
				BucketConfig: mocov1beta2.BucketConfig{
					BucketName: "mybucket",
				},
			},
		}
		err = k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())

		r = makeMySQLCluster()
		r.Spec.Restore = &mocov1beta2.RestoreSpec{
			SourceName:      "test",
			SourceNamespace: "test",
			RestorePoint:    metav1.Now(),
			JobConfig: mocov1beta2.JobConfig{
				ServiceAccountName: "foo",
			},
		}
		err = k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())

		r = makeMySQLCluster()
		r.Spec.Restore = &mocov1beta2.RestoreSpec{
			SourceName:      "test",
			SourceNamespace: "test",
			RestorePoint:    metav1.Now(),
			JobConfig: mocov1beta2.JobConfig{
				ServiceAccountName: "foo",
				BucketConfig: mocov1beta2.BucketConfig{
					BucketName:  "mybucket",
					EndpointURL: "hoge",
				},
			},
		}
		err = k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should allow valid restore spec", func() {
		r := makeMySQLCluster()
		r.Spec.Restore = &mocov1beta2.RestoreSpec{
			SourceName:      "test",
			SourceNamespace: "test",
			RestorePoint:    metav1.Now(),
			JobConfig: mocov1beta2.JobConfig{
				ServiceAccountName: "foo",
				BucketConfig: mocov1beta2.BucketConfig{
					BucketName:  "mybucket",
					EndpointURL: "https://foo.bar.svc:9000",
				},
			},
			Schema: "db1",
			Users:  "db1",
		}
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should deny editing restore spec", func() {
		r := makeMySQLCluster()
		r.Spec.Restore = &mocov1beta2.RestoreSpec{
			SourceName:      "test",
			SourceNamespace: "test",
			RestorePoint:    metav1.Now(),
			JobConfig: mocov1beta2.JobConfig{
				ServiceAccountName: "foo",
				BucketConfig: mocov1beta2.BucketConfig{
					BucketName: "mybucket",
				},
			},
		}
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())

		r.Spec.Restore = nil
		err = k8sClient.Update(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should allow storage size expansion", func() {
		r := makeMySQLCluster()
		r.Spec.VolumeClaimTemplates = make([]mocov1beta2.PersistentVolumeClaim, 2)

		r.Spec.VolumeClaimTemplates[0] = mocov1beta2.PersistentVolumeClaim{
			ObjectMeta: mocov1beta2.ObjectMeta{
				Name: "mysql-data",
			},
			Spec: mocov1beta2.PersistentVolumeClaimSpecApplyConfiguration(
				*corev1ac.PersistentVolumeClaimSpec().
					WithStorageClassName("default").
					WithResources(corev1ac.VolumeResourceRequirements().
						WithRequests(corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						}),
					),
			),
		}

		r.Spec.VolumeClaimTemplates[1] = mocov1beta2.PersistentVolumeClaim{
			ObjectMeta: mocov1beta2.ObjectMeta{
				Name: "foo",
			},
			Spec: mocov1beta2.PersistentVolumeClaimSpecApplyConfiguration(
				*corev1ac.PersistentVolumeClaimSpec().
					WithStorageClassName("not-support-volume-expansion").
					WithResources(corev1ac.VolumeResourceRequirements().
						WithRequests(corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						}),
					),
			),
		}

		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())

		r.Spec.VolumeClaimTemplates[0].Spec.Resources.WithRequests(
			corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse("10Gi"),
			},
		)

		err = k8sClient.Update(ctx, r)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should deny storage size expansion for not support volume expansion storage class", func() {
		r := makeMySQLCluster()
		r.Spec.VolumeClaimTemplates = make([]mocov1beta2.PersistentVolumeClaim, 2)

		r.Spec.VolumeClaimTemplates[0] = mocov1beta2.PersistentVolumeClaim{
			ObjectMeta: mocov1beta2.ObjectMeta{
				Name: "mysql-data",
			},
			Spec: mocov1beta2.PersistentVolumeClaimSpecApplyConfiguration(
				*corev1ac.PersistentVolumeClaimSpec().
					WithStorageClassName("default").
					WithResources(corev1ac.VolumeResourceRequirements().
						WithRequests(corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						}),
					),
			),
		}

		r.Spec.VolumeClaimTemplates[1] = mocov1beta2.PersistentVolumeClaim{
			ObjectMeta: mocov1beta2.ObjectMeta{
				Name: "foo",
			},
			Spec: mocov1beta2.PersistentVolumeClaimSpecApplyConfiguration(
				*corev1ac.PersistentVolumeClaimSpec().
					WithStorageClassName("not-support-volume-expansion").
					WithResources(corev1ac.VolumeResourceRequirements().
						WithRequests(corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						}),
					),
			),
		}

		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())

		r.Spec.VolumeClaimTemplates[1].Spec.Resources.WithRequests(
			corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse("10Gi"),
			},
		)

		err = k8sClient.Update(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny long name", func() {
		r := makeMySQLCluster()
		r.Name = "mycluster-1234567890123456789012345678901" // 41 characters
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())

		r.Name = "mycluster-123456789012345678901234567890" // 40 characters
		err = k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())
	})

	Context("with a CredentialRotation targeting the cluster", func() {
		BeforeEach(func() {
			Expect(deleteCredentialRotation("test")).To(Succeed())
		})

		// createClusterAndCR creates the cluster and an adopted CR, then
		// drives the CR's status to the given phase. An empty phase leaves
		// the CR as freshly created (adopted, no status).
		createClusterAndCR := func(phase mocov1beta2.RotationPhase) (*mocov1beta2.MySQLCluster, *mocov1beta2.CredentialRotation) {
			cluster := makeMySQLCluster()
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			cr := makeCredentialRotation("test")
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
			Expect(adoptCR(cr, cluster)).To(Succeed())
			if phase != "" {
				cr.SetPhase(phase, "test")
				Expect(k8sClient.Status().Update(ctx, cr)).To(Succeed())
			}
			return cluster, cr
		}

		It("should deny changing replicas while a rotation is active", func() {
			cluster, _ := createClusterAndCR(mocov1beta2.PhaseApplyingRetain)
			cluster.Spec.Replicas = 3
			err := k8sClient.Update(ctx, cluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("CredentialRotation"))
		})

		It("should deny taking the cluster offline while a rotation is active", func() {
			cluster, _ := createClusterAndCR(mocov1beta2.PhaseAwaitingRollout)
			cluster.Spec.Offline = true
			err := k8sClient.Update(ctx, cluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("CredentialRotation"))
		})

		It("should deny the change even before the CR's first reconcile", func() {
			cluster := makeMySQLCluster()
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			cr := makeCredentialRotation("test")
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			cluster.Spec.Replicas = 3
			err := k8sClient.Update(ctx, cluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("CredentialRotation"))
		})

		It("should allow other spec changes while a rotation is active", func() {
			cluster, _ := createClusterAndCR(mocov1beta2.PhaseApplyingRetain)
			name := "mycnf"
			cluster.Spec.MySQLConfigMapName = &name
			Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
		})

		It("should allow replicas and offline changes when the rotation is terminal", func() {
			cluster, _ := createClusterAndCR(mocov1beta2.PhaseSucceeded)
			cluster.Spec.Replicas = 3
			Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
			cluster.Spec.Offline = true
			Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
		})

		It("should allow the resume changes when the rotation is Blocked", func() {
			cluster, _ := createClusterAndCR(mocov1beta2.PhaseBlocked)
			cluster.Spec.Offline = true
			Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
			cluster.Spec.Replicas = 3
			Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
		})

		It("should always allow bringing the cluster back online", func() {
			cluster, cr := createClusterAndCR(mocov1beta2.PhaseBlocked)
			cluster.Spec.Offline = true
			Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

			// Even with the rotation active again (the freeze's race window
			// can leave an offline cluster with a non-Blocked phase),
			// clearing spec.offline must not be rejected: it is the only
			// way back to a running cluster short of deleting the CR.
			cr.SetPhase(mocov1beta2.PhaseApplyingRetain, "test")
			Expect(k8sClient.Status().Update(ctx, cr)).To(Succeed())
			cluster.Spec.Offline = false
			Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
		})

		It("should ignore a CredentialRotation left over from a previous cluster", func() {
			_, _ = createClusterAndCR(mocov1beta2.PhaseApplyingRetain)
			// Recreate the cluster under the same name: the CR's
			// ownerReference now carries the old UID, so it is stale.
			// (envtest runs no garbage collector, so the CR survives.)
			Expect(deleteMySQLCluster()).To(Succeed())
			cluster := makeMySQLCluster()
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			cluster.Spec.Replicas = 3
			Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
		})
	})
})
