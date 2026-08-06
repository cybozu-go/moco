package v1beta2_test

import (
	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func makeCredentialRotation(name string) *mocov1beta2.CredentialRotation {
	return &mocov1beta2.CredentialRotation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: mocov1beta2.CredentialRotationSpec{
			Discard: false,
		},
	}
}

// adoptCR mimics the controller's adoption: it adds the MySQLCluster
// ownerReference before any status write. Without it, a CR with a status
// matches the orphan half of the stale rule and spec updates are rejected.
func adoptCR(cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) error {
	fresh := &mocov1beta2.MySQLCluster{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.Name}, fresh); err != nil {
		return err
	}
	cr.OwnerReferences = append(cr.OwnerReferences, metav1.OwnerReference{
		APIVersion: mocov1beta2.GroupVersion.String(),
		Kind:       "MySQLCluster",
		Name:       fresh.Name,
		UID:        fresh.UID,
	})
	return k8sClient.Update(ctx, cr)
}

// openVerificationWindow drives the CR's status to the state the webhook's
// discard gate requires: phase AwaitingDiscard with DiscardReady=True.
func openVerificationWindow(cr *mocov1beta2.CredentialRotation) error {
	cr.SetPhase(mocov1beta2.PhaseAwaitingDiscard, "test window open")
	cr.SetDiscardReady(metav1.ConditionTrue, mocov1beta2.ReasonRolloutSettled, "test window open")
	cr.SetDualPassword(metav1.ConditionTrue, mocov1beta2.ReasonRetained, "test window open")
	cr.SetFinished(metav1.ConditionFalse, mocov1beta2.ReasonRunning, "test window open")
	return k8sClient.Status().Update(ctx, cr)
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
			cr := makeCredentialRotation("test")
			err := k8sClient.Create(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should reject when discard is true at create time", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test")
			cr.Spec.Discard = true
			err = k8sClient.Create(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should reject when the MySQLCluster is terminating", func() {
			cluster := makeMySQLCluster()
			cluster.Finalizers = append(cluster.Finalizers, "test.moco.cybozu.com/hold")
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())
			err = k8sClient.Delete(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test")
			err = k8sClient.Create(ctx, cr)
			Expect(err).To(HaveOccurred())

			// Unblock deletion for the AfterEach cleanup.
			fresh := &mocov1beta2.MySQLCluster{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: cluster.Name}, fresh)
			Expect(err).NotTo(HaveOccurred())
			fresh.Finalizers = nil
			err = k8sClient.Update(ctx, fresh)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should accept a valid CR", func() {
			cluster := makeMySQLCluster()
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			cr := makeCredentialRotation("test")
			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("ValidateUpdate", func() {
		It("should reject the discard flip while the verification window is closed", func() {
			cluster := makeMySQLCluster()
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			cr := makeCredentialRotation("test")
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			cr.Spec.Discard = true
			err := k8sClient.Update(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should allow the discard flip while DiscardReady is True", func() {
			cluster := makeMySQLCluster()
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			cr := makeCredentialRotation("test")
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
			Expect(adoptCR(cr, cluster)).To(Succeed())
			Expect(openVerificationWindow(cr)).To(Succeed())

			cr.Spec.Discard = true
			err := k8sClient.Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should reject unsetting discard", func() {
			cluster := makeMySQLCluster()
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			cr := makeCredentialRotation("test")
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
			Expect(adoptCR(cr, cluster)).To(Succeed())
			Expect(openVerificationWindow(cr)).To(Succeed())
			cr.Spec.Discard = true
			Expect(k8sClient.Update(ctx, cr)).To(Succeed())

			cr.Spec.Discard = false
			err := k8sClient.Update(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should reject the discard flip on a stale CR", func() {
			cluster := makeMySQLCluster()
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			cr := makeCredentialRotation("test")
			cr.OwnerReferences = []metav1.OwnerReference{{
				APIVersion: mocov1beta2.GroupVersion.String(),
				Kind:       "MySQLCluster",
				Name:       cluster.Name,
				UID:        types.UID("00000000-0000-0000-0000-000000000000"),
			}}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
			Expect(openVerificationWindow(cr)).To(Succeed())

			cr.Spec.Discard = true
			err := k8sClient.Update(ctx, cr)
			Expect(err).To(HaveOccurred())
		})

		It("should allow metadata-only updates at any time", func() {
			cluster := makeMySQLCluster()
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			cr := makeCredentialRotation("test")
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			cr.Labels = map[string]string{"test": "label"}
			err := k8sClient.Update(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("ValidateDelete", func() {
		It("should allow deletion at any phase", func() {
			cluster := makeMySQLCluster()
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			cr := makeCredentialRotation("test")
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
			Expect(openVerificationWindow(cr)).To(Succeed())

			err := k8sClient.Delete(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
