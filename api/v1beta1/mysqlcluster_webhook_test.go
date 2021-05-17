package v1beta1

import (
	"context"

	"github.com/cybozu-go/moco/pkg/constants"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func makeMySQLCluster() *MySQLCluster {
	return &MySQLCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: MySQLClusterSpec{
			Replicas: 1,
			PodTemplate: PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "mysqld",
						},
					},
				},
			},
			VolumeClaimTemplates: []PersistentVolumeClaim{
				{
					ObjectMeta: ObjectMeta{
						Name: "mysql-data",
					},
				},
			},
		},
	}
}

var _ = Describe("MySQLCluster Webhook", func() {
	ctx := context.TODO()

	BeforeEach(func() {
		r := &MySQLCluster{}
		err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "test"}, r)
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		r.Finalizers = nil
		err = k8sClient.Update(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Delete(ctx, r)
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

	It("should deny without mysqld container", func() {
		r := makeMySQLCluster()
		r.Spec.PodTemplate.Spec.Containers = nil
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny mysqld container using reserved port", func() {
		for _, port := range []corev1.ContainerPort{
			{ContainerPort: constants.MySQLPort},
			{Name: constants.MySQLPortName},
			{ContainerPort: constants.MySQLAdminPort},
			{Name: constants.MySQLAdminPortName},
			{ContainerPort: constants.MySQLXPort},
			{Name: constants.MySQLXPortName},
			{ContainerPort: constants.MySQLHealthPort},
			{Name: constants.MySQLHealthPortName},
		} {
			r := makeMySQLCluster()
			r.Spec.PodTemplate.Spec.Containers[0].Ports = []corev1.ContainerPort{port}
			err := k8sClient.Create(ctx, r)
			Expect(err).To(HaveOccurred())
		}
	})

	It("should deny reserved volume names", func() {
		for _, volname := range []string{
			constants.TmpVolumeName, constants.RunVolumeName, constants.VarLogVolumeName,
			constants.MySQLConfVolumeName, constants.MySQLInitConfVolumeName,
			constants.MySQLConfSecretVolumeName, constants.SlowQueryLogAgentConfigVolumeName,
			constants.MOCOBinVolumeName,
		} {
			r := makeMySQLCluster()
			r.Spec.PodTemplate.Spec.Volumes = []corev1.Volume{{Name: volname}}
			err := k8sClient.Create(ctx, r)
			Expect(err).To(HaveOccurred())
		}
	})

	It("should deny agent container", func() {
		r := makeMySQLCluster()
		r.Spec.PodTemplate.Spec.Containers = append(r.Spec.PodTemplate.Spec.Containers, corev1.Container{
			Name: "agent",
		})
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny slow query log container if not disabled", func() {
		r := makeMySQLCluster()
		r.Spec.PodTemplate.Spec.Containers = append(r.Spec.PodTemplate.Spec.Containers, corev1.Container{
			Name: constants.SlowQueryLogAgentContainerName,
		})
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should allow slow log container if disabled", func() {
		r := makeMySQLCluster()
		r.Spec.PodTemplate.Spec.Containers = append(r.Spec.PodTemplate.Spec.Containers, corev1.Container{
			Name: constants.SlowQueryLogAgentContainerName,
		})
		r.Spec.DisableSlowQueryLogContainer = true
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should deny mysqld_exporter container if enabled", func() {
		r := makeMySQLCluster()
		r.Spec.Collectors = []string{"engine_innodb_status", "info_schema.innodb_metrics"}
		r.Spec.PodTemplate.Spec.Containers = append(r.Spec.PodTemplate.Spec.Containers, corev1.Container{
			Name: constants.ExporterContainerName,
		})
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should allow mysqld_exporter container if not enabled", func() {
		r := makeMySQLCluster()
		r.Spec.PodTemplate.Spec.Containers = append(r.Spec.PodTemplate.Spec.Containers, corev1.Container{
			Name: constants.ExporterContainerName,
		})
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should allow non-reserved init containers", func() {
		r := makeMySQLCluster()
		r.Spec.PodTemplate.Spec.InitContainers = []corev1.Container{
			{
				Name: "foobar",
			},
		}
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should deny moco-init init containers", func() {
		r := makeMySQLCluster()
		r.Spec.PodTemplate.Spec.InitContainers = []corev1.Container{
			{
				Name: "moco-init",
			},
		}
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

	It("should deny adding replication source secret", func() {
		r := makeMySQLCluster()
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())

		r.Spec.ReplicationSourceSecretName = pointer.String("fuga")
		err = k8sClient.Update(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny changing replication source secret name", func() {
		r := makeMySQLCluster()
		r.Spec.ReplicationSourceSecretName = pointer.String("foo")
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())

		r.Spec.ReplicationSourceSecretName = pointer.String("bar")
		err = k8sClient.Update(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny invalid restore spec", func() {
		r := makeMySQLCluster()
		r.Spec.Restore = &RestoreSpec{
			SourceNamespace: "test",
			RestorePoint:    metav1.Now(),
			JobConfig: JobConfig{
				ServiceAccountName: "foo",
				BucketConfig: BucketConfig{
					BucketName: "mybucket",
				},
			},
		}
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())

		r = makeMySQLCluster()
		r.Spec.Restore = &RestoreSpec{
			SourceName:   "test",
			RestorePoint: metav1.Now(),
			JobConfig: JobConfig{
				ServiceAccountName: "foo",
				BucketConfig: BucketConfig{
					BucketName: "mybucket",
				},
			},
		}
		err = k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())

		r = makeMySQLCluster()
		r.Spec.Restore = &RestoreSpec{
			SourceName:      "test",
			SourceNamespace: "test",
			JobConfig: JobConfig{
				ServiceAccountName: "foo",
				BucketConfig: BucketConfig{
					BucketName: "mybucket",
				},
			},
		}
		err = k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())

		r = makeMySQLCluster()
		r.Spec.Restore = &RestoreSpec{
			SourceName:      "test",
			SourceNamespace: "test",
			RestorePoint:    metav1.Now(),
			JobConfig: JobConfig{
				BucketConfig: BucketConfig{
					BucketName: "mybucket",
				},
			},
		}
		err = k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())

		r = makeMySQLCluster()
		r.Spec.Restore = &RestoreSpec{
			SourceName:      "test",
			SourceNamespace: "test",
			RestorePoint:    metav1.Now(),
			JobConfig: JobConfig{
				ServiceAccountName: "foo",
			},
		}
		err = k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())

		r = makeMySQLCluster()
		r.Spec.Restore = &RestoreSpec{
			SourceName:      "test",
			SourceNamespace: "test",
			RestorePoint:    metav1.Now(),
			JobConfig: JobConfig{
				ServiceAccountName: "foo",
				BucketConfig: BucketConfig{
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
		r.Spec.Restore = &RestoreSpec{
			SourceName:      "test",
			SourceNamespace: "test",
			RestorePoint:    metav1.Now(),
			JobConfig: JobConfig{
				ServiceAccountName: "foo",
				BucketConfig: BucketConfig{
					BucketName:  "mybucket",
					EndpointURL: "https://foo.bar.svc:9000",
				},
			},
		}
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should deny editing restore spec", func() {
		r := makeMySQLCluster()
		r.Spec.Restore = &RestoreSpec{
			SourceName:      "test",
			SourceNamespace: "test",
			RestorePoint:    metav1.Now(),
			JobConfig: JobConfig{
				ServiceAccountName: "foo",
				BucketConfig: BucketConfig{
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
})
