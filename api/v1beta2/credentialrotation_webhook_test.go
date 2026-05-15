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
		},
	}
}

func deleteCredentialRotation(name string) error {
	cr := &mocov1beta2.CredentialRotation{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: name}, cr); err != nil {
		return client.IgnoreNotFound(err)
	}
	// Reset phase to Completed so the validating webhook allows deletion
	// regardless of the test's intermediate phase state.
	if cr.Status.Phase != "" && cr.Status.Phase != mocov1beta2.RotationPhaseCompleted {
		cr.Status.Phase = mocov1beta2.RotationPhaseCompleted
		if err := k8sClient.Status().Update(ctx, cr); err != nil {
			return client.IgnoreNotFound(err)
		}
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

		It("should reject when discardGeneration > rotationGeneration", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			cr.Spec.DiscardGeneration = 2
			err = k8sClient.Create(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should reject when discardGeneration is negative", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			cr.Spec.DiscardGeneration = -1
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

		It("should accept create with discardGeneration == rotationGeneration", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			cr.Spec.DiscardGeneration = 1
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

		It("should reject discardGeneration > rotationGeneration on update", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.DiscardGeneration = 2
			err = k8sClient.Update(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should reject decreasing discardGeneration", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			cr.Spec.DiscardGeneration = 1
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.DiscardGeneration = 0
			err = k8sClient.Update(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should reject bumping discardGeneration when phase is not Rotated", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.DiscardGeneration = 1
			err = k8sClient.Update(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should accept bumping discardGeneration when phase is Rotated", func() {
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

			cr.Spec.DiscardGeneration = 1
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
			err = k8sClient.Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("ValidateDelete", func() {
		It("should allow deletion when phase is empty", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Delete(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should allow deletion when phase is Completed", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Status.Phase = mocov1beta2.RotationPhaseCompleted
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Delete(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should allow deletion when the owning MySQLCluster is gone (GC)", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Status.Phase = mocov1beta2.RotationPhaseRotating
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			// Delete the MySQLCluster first to simulate GC ordering after owner
			// removal. The MySQLCluster mutating webhook adds a finalizer on
			// create, and no controller runs in this envtest suite to remove
			// it, so we clear it manually before Delete actually reaps the
			// object.
			Eventually(func() error {
				c := &mocov1beta2.MySQLCluster{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), c); err != nil {
					return err
				}
				c.Finalizers = nil
				return k8sClient.Update(ctx, c)
			}).Should(Succeed())
			err = k8sClient.Delete(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() bool {
				c := &mocov1beta2.MySQLCluster{}
				return k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), c) != nil
			}).Should(BeTrue())

			err = k8sClient.Delete(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should allow deletion when the owning cluster is terminating (GC unblock)", func() {
			cluster := makeMySQLCluster()
			cluster.Finalizers = []string{"moco.cybozu.com/test-block-delete"}
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Status.Phase = mocov1beta2.RotationPhaseRotating
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			// Delete the cluster — finalizer keeps it in Terminating state.
			err = k8sClient.Delete(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() bool {
				c := &mocov1beta2.MySQLCluster{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), c); err != nil {
					return false
				}
				return c.DeletionTimestamp != nil
			}).Should(BeTrue())

			// GC delete of the CR must be allowed so the cluster can finish terminating.
			err = k8sClient.Delete(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			// Clean up: remove the finalizer so the cluster can be deleted.
			Eventually(func() error {
				c := &mocov1beta2.MySQLCluster{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), c); err != nil {
					return err
				}
				c.Finalizers = nil
				return k8sClient.Update(ctx, c)
			}).Should(Succeed())
		})

		It("should allow deletion when ownerReference points to a recreated cluster (different UID)", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			// Stale ownerRef pointing at a UID that no live cluster has.
			cr.OwnerReferences = []metav1.OwnerReference{{
				APIVersion: mocov1beta2.GroupVersion.String(),
				Kind:       "MySQLCluster",
				Name:       cluster.Name,
				UID:        "stale-uid",
				Controller: new(true),
			}}
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Status.Phase = mocov1beta2.RotationPhaseRotating
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Delete(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should reject deletion when phase is active", func() {
			for _, phase := range []mocov1beta2.RotationPhase{
				mocov1beta2.RotationPhaseRotating,
				mocov1beta2.RotationPhaseRetained,
				mocov1beta2.RotationPhaseRotated,
				mocov1beta2.RotationPhaseDiscarding,
				mocov1beta2.RotationPhaseDiscarded,
			} {
				cluster := makeMySQLCluster()
				err := k8sClient.Create(ctx, cluster)
				Expect(err).NotTo(HaveOccurred())

				cr := makeCredentialRotation("test", 1)
				err = k8sClient.Create(ctx, cr)
				Expect(err).NotTo(HaveOccurred())

				cr.Status.Phase = phase
				err = k8sClient.Status().Update(ctx, cr)
				Expect(err).NotTo(HaveOccurred())

				err = k8sClient.Delete(ctx, cr)
				Expect(err).To(HaveOccurred(), "phase=%s should reject delete", phase)

				// Force-cleanup via status reset for next iteration.
				err = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), cr)
				Expect(err).NotTo(HaveOccurred())
				cr.Status.Phase = mocov1beta2.RotationPhaseCompleted
				err = k8sClient.Status().Update(ctx, cr)
				Expect(err).NotTo(HaveOccurred())
				err = k8sClient.Delete(ctx, cr)
				Expect(err).NotTo(HaveOccurred())
				err = deleteMySQLCluster()
				Expect(err).NotTo(HaveOccurred())
			}
		})
	})
})
