package v1beta2_test

import (
	"context"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func makeBackupPolicy() *mocov1beta2.BackupPolicy {
	r := &mocov1beta2.BackupPolicy{}
	r.Namespace = "default"
	r.Name = "test"
	r.Spec.Schedule = "*/5 * * * *"
	r.Spec.JobConfig.ServiceAccountName = "foo"
	r.Spec.JobConfig.BucketConfig.BucketName = "mybucket"
	return r
}

var _ = Describe("BackupPolicy Webhook", func() {
	ctx := context.TODO()

	BeforeEach(func() {
		err := deleteMySQLCluster()
		Expect(err).NotTo(HaveOccurred())

		r := &mocov1beta2.BackupPolicy{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "test"}, r)
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Delete(ctx, r)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should create BackupPolicy with the sane defaults", func() {
		r := makeBackupPolicy()
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())

		Expect(r.Spec.JobConfig.Threads).To(Equal(4))
		Expect(r.Spec.JobConfig.CPU).NotTo(BeNil())
		Expect(r.Spec.JobConfig.CPU.Value()).To(Equal(int64(4)))
		Expect(r.Spec.JobConfig.MaxCPU).To(BeNil())
		Expect(r.Spec.JobConfig.Memory).NotTo(BeNil())
		Expect(r.Spec.JobConfig.Memory.Value()).To(Equal(int64(4) << 30))
		Expect(r.Spec.JobConfig.MaxMemory).To(BeNil())
		Expect(r.Spec.ConcurrencyPolicy).To(Equal(batchv1.AllowConcurrent))
	})

	It("should create BackupPolicy with concurrencyPolicy=Forbid", func() {
		r := makeBackupPolicy()
		r.Spec.ConcurrencyPolicy = batchv1.ForbidConcurrent
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should create BackupPolicy with concurrencyPolicy=Replace", func() {
		r := makeBackupPolicy()
		r.Spec.ConcurrencyPolicy = batchv1.ReplaceConcurrent
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should deny BackupPolicy with invalid concurrencyPolicy", func() {
		r := makeBackupPolicy()
		r.Spec.ConcurrencyPolicy = batchv1.ConcurrencyPolicy("invalid")
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny BackupPolicy with invalid backoffLimit", func() {
		r := makeBackupPolicy()
		r.Spec.BackoffLimit = pointer.Int32(-1)
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny BackupPolicy with invalid successfulJobsHistoryLimit", func() {
		r := makeBackupPolicy()
		r.Spec.SuccessfulJobsHistoryLimit = pointer.Int32(-1)
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny BackupPolicy with invalid failedJobsHistoryLimit", func() {
		r := makeBackupPolicy()
		r.Spec.FailedJobsHistoryLimit = pointer.Int32(-1)
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should delete BackupPolicy", func() {
		cluster := makeMySQLCluster()
		cluster.Spec.BackupPolicyName = pointer.String("no-test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		backup := makeBackupPolicy()
		err = k8sClient.Create(ctx, backup)
		Expect(err).NotTo(HaveOccurred())

		err = k8sClient.Delete(ctx, backup)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should NOT delete BackupPolicy which is referenced by MySQLCluster", func() {
		cluster := makeMySQLCluster()
		cluster.Spec.BackupPolicyName = pointer.String("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		backup := makeBackupPolicy()
		err = k8sClient.Create(ctx, backup)
		Expect(err).NotTo(HaveOccurred())

		err = k8sClient.Delete(ctx, backup)
		Expect(err).To(HaveOccurred())
	})

})
