package v1beta2_test

import (
	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func makeCredentialRotation(name string, gen int64) *mocov1beta2.CredentialRotation {
	return &mocov1beta2.CredentialRotation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: mocov1beta2.CredentialRotationSpec{
			RotationGeneration: gen,
			DiscardOldPassword: false,
		},
	}
}

func deleteCredentialRotation(name string) error {
	cr := &mocov1beta2.CredentialRotation{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: name}, cr); err != nil {
		return client.IgnoreNotFound(err)
	}
	return client.IgnoreNotFound(k8sClient.Delete(ctx, cr))
}

var _ = Describe("CredentialRotation Webhook", func() {
	BeforeEach(func() {
		// Delete CR first (before cluster) to avoid GC race
		err := deleteCredentialRotation("test")
		Expect(err).NotTo(HaveOccurred())
		err = deleteMySQLCluster()
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		err := deleteCredentialRotation("test")
		Expect(err).NotTo(HaveOccurred())
		err = deleteMySQLCluster()
		Expect(err).NotTo(HaveOccurred())
	})

	Context("ValidateCreate", func() {
		It("should reject when no MySQLCluster exists", func() {
			cr := makeCredentialRotation("test", 1)
			err := k8sClient.Create(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should reject when rotationGeneration is 0", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 0)
			err = k8sClient.Create(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should reject when discardOldPassword is true", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			cr.Spec.DiscardOldPassword = true
			err = k8sClient.Create(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should accept a valid CR", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("ValidateUpdate", func() {
		It("should reject decreasing rotationGeneration", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 2)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.RotationGeneration = 1
			err = k8sClient.Update(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should reject increasing generation when phase is not empty or Completed", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			// Simulate Rotating phase
			cr.Status.Phase = mocov1beta2.RotationPhaseRotating
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			// Re-fetch to get updated resource version
			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.RotationGeneration = 2
			err = k8sClient.Update(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should reject discardOldPassword=true when increasing generation", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.RotationGeneration = 2
			cr.Spec.DiscardOldPassword = true
			err = k8sClient.Update(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should reject setting discardOldPassword=true when phase is not Rotated", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.DiscardOldPassword = true
			err = k8sClient.Update(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should accept setting discardOldPassword=true when phase is Rotated", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			// Simulate Rotated phase
			cr.Status.Phase = mocov1beta2.RotationPhaseRotated
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			// Re-fetch to get updated resource version
			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.DiscardOldPassword = true
			err = k8sClient.Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should accept increasing generation when phase is Completed", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			// Simulate Completed phase
			cr.Status.Phase = mocov1beta2.RotationPhaseCompleted
			cr.Status.ObservedRotationGeneration = 1
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			// Re-fetch to get updated resource version
			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.RotationGeneration = 2
			cr.Spec.DiscardOldPassword = false
			err = k8sClient.Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should reject reverting discardOldPassword from true to false", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			// Set phase to Rotated and discardOldPassword=true
			cr.Status.Phase = mocov1beta2.RotationPhaseRotated
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.DiscardOldPassword = true
			err = k8sClient.Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			// Now try to revert
			cr.Spec.DiscardOldPassword = false
			err = k8sClient.Update(ctx, cr)
			Expect(err).To(HaveOccurred())
		})
	})
})
