package v1beta2_test

import (
	"context"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
		r.Spec.BackoffLimit = new(int32(-1))
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny BackupPolicy with invalid successfulJobsHistoryLimit", func() {
		r := makeBackupPolicy()
		r.Spec.SuccessfulJobsHistoryLimit = new(int32(-1))
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should deny BackupPolicy with invalid failedJobsHistoryLimit", func() {
		r := makeBackupPolicy()
		r.Spec.FailedJobsHistoryLimit = new(int32(-1))
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should create BackupPolicy with a customer key for each backend", func() {
		for _, backendType := range []string{"s3", "gcs", "azure"} {
			r := makeBackupPolicy()
			r.Spec.JobConfig.BucketConfig.BackendType = backendType
			r.Spec.JobConfig.BucketConfig.CustomerKeySecret = &mocov1beta2.SecretKeyRef{Name: "key", Key: "customer-key"}
			Expect(k8sClient.Create(ctx, r)).To(Succeed(), backendType)
			Expect(k8sClient.Delete(ctx, r)).To(Succeed())
		}
	})

	It("should deny BackupPolicy with an incomplete customer key reference", func() {
		r := makeBackupPolicy()
		r.Spec.JobConfig.BucketConfig.CustomerKeySecret = &mocov1beta2.SecretKeyRef{Name: "key"}
		err := k8sClient.Create(ctx, r)
		Expect(err).To(HaveOccurred())
	})

	It("should create BackupPolicy with age recipients", func() {
		r := makeBackupPolicy()
		r.Spec.JobConfig.BucketConfig.Age = &mocov1beta2.AgeConfig{
			Recipients: []string{testAgeRecipient, testAgeRecipient2},
		}
		Expect(k8sClient.Create(ctx, r)).To(Succeed())
		Expect(k8sClient.Delete(ctx, r)).To(Succeed())
	})

	It("should deny BackupPolicy with invalid age settings", func() {
		testCases := []struct {
			name    string
			bc      func(*mocov1beta2.BucketConfig)
			wantErr string
		}{
			{"no recipients", func(bc *mocov1beta2.BucketConfig) {
				bc.Age = &mocov1beta2.AgeConfig{IdentitySecret: &mocov1beta2.SecretKeyRef{Name: "key", Key: "identity"}}
			}, "spec.jobConfig.bucketConfig.age.recipients: Required value"},
			{"invalid recipient", func(bc *mocov1beta2.BucketConfig) {
				bc.Age = &mocov1beta2.AgeConfig{Recipients: []string{testAgeRecipient, "age1invalid"}}
			}, "spec.jobConfig.bucketConfig.age.recipients[1]: Invalid value"},
			{"private key as recipient", func(bc *mocov1beta2.BucketConfig) {
				bc.Age = &mocov1beta2.AgeConfig{Recipients: []string{testAgeIdentity}}
			}, "must be a public key (age1...), not a private key"},
			{"several recipients in one entry", func(bc *mocov1beta2.BucketConfig) {
				bc.Age = &mocov1beta2.AgeConfig{Recipients: []string{testAgeRecipient + "\n" + testAgeRecipient2}}
			}, "must be a single recipient"},
			{"combined with a customer key", func(bc *mocov1beta2.BucketConfig) {
				bc.Age = &mocov1beta2.AgeConfig{Recipients: []string{testAgeRecipient}}
				bc.CustomerKeySecret = &mocov1beta2.SecretKeyRef{Name: "key", Key: "customer-key"}
			}, "age cannot be combined with customerKeySecret"},
		}
		for _, tc := range testCases {
			r := makeBackupPolicy()
			tc.bc(&r.Spec.JobConfig.BucketConfig)
			err := k8sClient.Create(ctx, r)
			Expect(err).To(HaveOccurred(), tc.name)
			Expect(err.Error()).To(ContainSubstring(tc.wantErr), tc.name)
			Expect(err.Error()).NotTo(ContainSubstring(testAgeIdentity), tc.name)
		}
	})

	It("should delete BackupPolicy", func() {
		cluster := makeMySQLCluster()
		cluster.Spec.BackupPolicyName = new("no-test")
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
		cluster.Spec.BackupPolicyName = new("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		backup := makeBackupPolicy()
		err = k8sClient.Create(ctx, backup)
		Expect(err).NotTo(HaveOccurred())

		err = k8sClient.Delete(ctx, backup)
		Expect(err).To(HaveOccurred())
	})

})

// Keys for tests only.
const (
	testAgeRecipient  = "age1gf7j7dym5taxxud8js8q3j3ez8c67zyyvmzxcns6ua082h3flu7qvzcf95"
	testAgeRecipient2 = "age15mqcxuusrs0cwtv7mm5c0xcl9j4lmza6ussceemmzls3ns7jjv0qrrzvrm"
	testAgeIdentity   = "AGE-SECRET-KEY-16WN63TMFRPQWXSED882F6EMLHE6PJQEFNPY4ST02G7MQE7RKL34Q75X5W7"
)
