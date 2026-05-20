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
	// Reset the Rotating condition to a terminal idle state so the
	// validating webhook allows deletion regardless of the test's
	// intermediate condition state.
	if !cr.IsIdle() {
		cr.SetRotating(metav1.ConditionFalse, mocov1beta2.ReasonCompleted, "test cleanup")
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

		It("should reject increasing generation when a rotation cycle is in flight", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.SetRotating(metav1.ConditionTrue, mocov1beta2.ReasonApplyingRetain, "in flight")
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

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

		It("should reject bumping discardGeneration when Rotating.Reason is not AwaitingDiscard", func() {
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

		It("should accept bumping discardGeneration when Rotating.Reason is AwaitingDiscard", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.SetRotating(metav1.ConditionTrue, mocov1beta2.ReasonAwaitingDiscard, "ready for discard")
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.DiscardGeneration = 1
			err = k8sClient.Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should accept increasing rotationGeneration when the CR is idle (Completed)", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.SetRotating(metav1.ConditionFalse, mocov1beta2.ReasonCompleted, "previous cycle complete")
			cr.Status.ObservedRotationGeneration = 1
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.RotationGeneration = 2
			err = k8sClient.Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("ValidateDelete", func() {
		It("should allow deletion when Rotating condition is absent", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Delete(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should allow deletion when Rotating.Reason is Completed", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.SetRotating(metav1.ConditionFalse, mocov1beta2.ReasonCompleted, "cycle complete")
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Delete(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should allow deletion when Rotating.Reason is RotationBlocked (recovery escape hatch)", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.SetRotating(metav1.ConditionTrue, mocov1beta2.ReasonRotationBlocked, "scaled to 0")
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Delete(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should allow deletion when Rotating.Reason is StalePending (recovery escape hatch)", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.SetRotating(metav1.ConditionTrue, mocov1beta2.ReasonStalePending, "inconsistent secret")
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

			cr.SetRotating(metav1.ConditionTrue, mocov1beta2.ReasonApplyingRetain, "in flight")
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

			cr.SetRotating(metav1.ConditionTrue, mocov1beta2.ReasonApplyingRetain, "in flight")
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

			cr.SetRotating(metav1.ConditionTrue, mocov1beta2.ReasonApplyingRetain, "in flight")
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Delete(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should reject deletion while an actively progressing cycle is in flight", func() {
			for _, reason := range []string{
				mocov1beta2.ReasonApplyingRetain,
				mocov1beta2.ReasonDistributingPassword,
				mocov1beta2.ReasonAwaitingDiscard,
				mocov1beta2.ReasonWaitingForRollout,
				mocov1beta2.ReasonApplyingDiscard,
				mocov1beta2.ReasonFinalizing,
			} {
				cluster := makeMySQLCluster()
				err := k8sClient.Create(ctx, cluster)
				Expect(err).NotTo(HaveOccurred())

				cr := makeCredentialRotation("test", 1)
				err = k8sClient.Create(ctx, cr)
				Expect(err).NotTo(HaveOccurred())

				cr.SetRotating(metav1.ConditionTrue, reason, "test")
				err = k8sClient.Status().Update(ctx, cr)
				Expect(err).NotTo(HaveOccurred())

				err = k8sClient.Delete(ctx, cr)
				Expect(err).To(HaveOccurred(), "reason=%s should reject delete", reason)

				// Force-cleanup for next iteration.
				err = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), cr)
				Expect(err).NotTo(HaveOccurred())
				cr.SetRotating(metav1.ConditionFalse, mocov1beta2.ReasonCompleted, "test cleanup")
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
