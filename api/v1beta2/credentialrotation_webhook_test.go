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

// setCRIdle drives cr's status to the "fully reconciled, no dual passwords"
// state — the cycle has completed and a new rotation may be requested.
func setCRIdle(cr *mocov1beta2.CredentialRotation) {
	cr.Status.ObservedRotationGeneration = cr.Spec.RotationGeneration
	cr.Status.ObservedDiscardGeneration = cr.Spec.DiscardGeneration
	cr.SetRotationReady(metav1.ConditionTrue, mocov1beta2.ReasonReconciled, "test idle")
	cr.SetDiscardReady(metav1.ConditionFalse, mocov1beta2.ReasonPending, "test idle")
	cr.SetDualPassword(metav1.ConditionFalse, mocov1beta2.ReasonNotRetained, "test idle")
}

// setCRInFlight drives cr's status to an actively-progressing rotation
// phase (RETAIN done; conditions match Step=Promoting).
func setCRInFlight(cr *mocov1beta2.CredentialRotation) {
	cr.SetRotationReady(metav1.ConditionFalse, mocov1beta2.ReasonPending, "test in flight")
	cr.SetDiscardReady(metav1.ConditionFalse, mocov1beta2.ReasonPending, "test in flight")
	cr.SetDualPassword(metav1.ConditionTrue, mocov1beta2.ReasonRetained, "test in flight")
}

// setCRAwaitingDiscard drives cr's status to the steady state that
// follows a completed rotation phase, ready for the operator to bump
// discardGeneration. Conditions: RotationReady=False/Pending,
// DiscardReady=True/Reconciled, DualPassword=True/Retained.
func setCRAwaitingDiscard(cr *mocov1beta2.CredentialRotation) {
	cr.Status.ObservedRotationGeneration = cr.Spec.RotationGeneration
	cr.Status.ObservedDiscardGeneration = cr.Spec.DiscardGeneration
	cr.SetRotationReady(metav1.ConditionFalse, mocov1beta2.ReasonPending, "test awaiting discard")
	cr.SetDiscardReady(metav1.ConditionTrue, mocov1beta2.ReasonReconciled, "test awaiting discard")
	cr.SetDualPassword(metav1.ConditionTrue, mocov1beta2.ReasonRetained, "test awaiting discard")
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

		It("should reject when rotationGeneration is greater than 1", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 2)
			err = k8sClient.Create(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should reject when discardGeneration is non-zero", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			cr.Spec.DiscardGeneration = 1
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
	})

	Context("ValidateUpdate", func() {
		It("should reject decreasing rotationGeneration", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			// Drive to idle and bump to 2 so we can attempt a decrease back to 1.
			setCRIdle(cr)
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.RotationGeneration = 2
			err = k8sClient.Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.RotationGeneration = 1
			err = k8sClient.Update(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should reject increasing rotationGeneration when a rotation cycle is in flight", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			setCRInFlight(cr)
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
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			// Drive to awaiting-discard and bump discardGeneration so we can
			// attempt to decrease it.
			setCRAwaitingDiscard(cr)
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.DiscardGeneration = 1
			err = k8sClient.Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.DiscardGeneration = 0
			err = k8sClient.Update(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should reject bumping discardGeneration when the CR is not awaiting discard", func() {
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

		It("should reject bumping rotationGeneration and discardGeneration in the same update", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			setCRIdle(cr)
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), cr)
			Expect(err).NotTo(HaveOccurred())

			// A combined bump would skip the AwaitingRollout gate and the
			// verification window; discardGeneration may only increase
			// from the awaiting-discard steady state.
			cr.Spec.RotationGeneration = 2
			cr.Spec.DiscardGeneration = 2
			err = k8sClient.Update(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should accept bumping discardGeneration when the CR is awaiting discard", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			setCRAwaitingDiscard(cr)
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), cr)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.DiscardGeneration = 1
			err = k8sClient.Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should accept increasing rotationGeneration when the CR is idle (cycle completed)", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			setCRIdle(cr)
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
		// Deletion is always allowed: the webhook is not registered for
		// the delete verb. The core invariant (the controller Secret's
		// current passwords always authenticate) makes deletion
		// non-destructive; mid-cycle deletion only leaves residue that
		// blocks the next cycle until cleaned up.
		It("should allow deletion when conditions are absent (fresh CR)", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Delete(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should allow deletion when the previous cycle has completed (idle)", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			setCRIdle(cr)
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Delete(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should allow deletion while an actively progressing cycle is in flight", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			// In-flight rotation phase: DualPassword=True with
			// observedRotationGeneration still 0 — Step() returns
			// Promoting.
			setCRInFlight(cr)
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Delete(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should allow deletion when the CR is awaiting discard (dual passwords held)", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test", 1)
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			setCRAwaitingDiscard(cr)
			err = k8sClient.Status().Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Delete(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
