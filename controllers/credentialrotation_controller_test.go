package controllers

import (
	"context"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/password"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// testRotationTTL keeps Succeeded CRs short-lived so the TTL deletion is
// observable within a test.
const testRotationTTL = 3 * time.Second

func getCR(ctx context.Context) (*mocov1beta2.CredentialRotation, error) {
	cr := &mocov1beta2.CredentialRotation{}
	err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr)
	return cr, err
}

func crPhase(ctx context.Context) string {
	cr, err := getCR(ctx)
	if err != nil {
		return "<error: " + err.Error() + ">"
	}
	return string(cr.Status.Phase)
}

// simulateRetainDone mimics ClusterManager finishing RETAIN: DualPassword
// flips to True and the phase moves to Promoting in the same update.
func simulateRetainDone(ctx context.Context) error {
	cr, err := getCR(ctx)
	if err != nil {
		return err
	}
	cr.SetDualPassword(metav1.ConditionTrue, mocov1beta2.ReasonRetained, "test simulate RETAIN done")
	cr.SetPhase(mocov1beta2.PhasePromoting, "test simulate RETAIN done")
	return k8sClient.Status().Update(ctx, cr)
}

// simulateDiscardDone mimics ClusterManager finishing DISCARD: DualPassword
// flips back to False and the phase moves to Finalizing.
func simulateDiscardDone(ctx context.Context) error {
	cr, err := getCR(ctx)
	if err != nil {
		return err
	}
	cr.SetDualPassword(metav1.ConditionFalse, mocov1beta2.ReasonNotRetained, "test simulate DISCARD done")
	cr.SetPhase(mocov1beta2.PhaseFinalizing, "test simulate DISCARD done")
	return k8sClient.Status().Update(ctx, cr)
}

// syncStatefulSetRolloutComplete makes the StatefulSet status report a
// settled rollout for its current generation. No StatefulSet controller
// runs in envtest, and handleAwaitingRollout bumps the pod template
// (restart annotation) after its distribution catch-up check, so a single
// status write can become stale; call this inside an Eventually together
// with the phase assertion so the status keeps tracking the generation.
func syncStatefulSetRolloutComplete(ctx context.Context, cluster *mocov1beta2.MySQLCluster) error {
	sts := &appsv1.StatefulSet{}
	if err := k8sClient.Get(ctx, client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      cluster.PrefixedName(),
	}, sts); err != nil {
		return err
	}
	sts.Status.ObservedGeneration = sts.Generation
	sts.Status.Replicas = *sts.Spec.Replicas
	sts.Status.CurrentRevision = "rev-1"
	sts.Status.UpdateRevision = "rev-1"
	sts.Status.UpdatedReplicas = *sts.Spec.Replicas
	sts.Status.ReadyReplicas = *sts.Spec.Replicas
	return k8sClient.Status().Update(ctx, sts)
}

// startRotationToApplyingRetain creates the cluster and the CR, and waits
// until the seed handler has moved the CR to ApplyingRetain.
func startRotationToApplyingRetain(ctx context.Context) *mocov1beta2.MySQLCluster {
	cluster := testNewMySQLCluster("test")
	Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

	Eventually(func() error {
		secret := &corev1.Secret{}
		return k8sClient.Get(ctx, client.ObjectKey{
			Namespace: testMocoSystemNamespace,
			Name:      cluster.ControllerSecretName(),
		}, secret)
	}).Should(Succeed())

	cr := &mocov1beta2.CredentialRotation{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"},
	}
	Expect(k8sClient.Create(ctx, cr)).To(Succeed())

	Eventually(func() string { return crPhase(ctx) }).Should(Equal(string(mocov1beta2.PhaseApplyingRetain)))
	return cluster
}

var _ = Describe("CredentialRotation reconciler", func() {
	ctx := context.Background()
	var stopFunc func()

	BeforeEach(func() {
		// Clean up any existing resources.
		cs := &mocov1beta2.MySQLClusterList{}
		err := k8sClient.List(ctx, cs, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		for _, cluster := range cs.Items {
			cluster.Finalizers = nil
			err := k8sClient.Update(ctx, &cluster)
			Expect(err).NotTo(HaveOccurred())
		}
		err = k8sClient.DeleteAllOf(ctx, &mocov1beta2.CredentialRotation{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &mocov1beta2.MySQLCluster{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		// Clear finalizers left on StatefulSets (e.g. the orphan finalizer
		// from a DeletePropagationOrphan delete, which envtest's missing
		// garbage collector never removes) so DeleteAllOf actually works.
		stss := &appsv1.StatefulSetList{}
		err = k8sClient.List(ctx, stss, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		for i := range stss.Items {
			sts := &stss.Items[i]
			if len(sts.Finalizers) == 0 {
				continue
			}
			sts.Finalizers = nil
			err := k8sClient.Update(ctx, sts)
			Expect(err).NotTo(HaveOccurred())
		}
		err = k8sClient.DeleteAllOf(ctx, &appsv1.StatefulSet{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &corev1.Secret{}, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &corev1.Secret{}, client.InNamespace(testMocoSystemNamespace))
		Expect(err).NotTo(HaveOccurred())

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:         scheme,
			LeaderElection: false,
			Metrics: metricsserver.Options{
				BindAddress: "0",
			},
			Controller: config.Controller{
				SkipNameValidation: new(true),
			},
		})
		Expect(err).ToNot(HaveOccurred())

		mockMgr := &mockManager{
			clusters: make(map[string]struct{}),
		}
		mysqlr := &MySQLClusterReconciler{
			Client:                     mgr.GetClient(),
			Scheme:                     scheme,
			Recorder:                   mgr.GetEventRecorderFor("moco-controller"),
			SystemNamespace:            testMocoSystemNamespace,
			ClusterManager:             mockMgr,
			AgentImage:                 testAgentImage,
			BackupImage:                testBackupImage,
			FluentBitImage:             testFluentBitImage,
			ExporterImage:              testExporterImage,
			MySQLConfigMapHistoryLimit: 2,
		}
		err = mysqlr.SetupWithManager(ctx, mgr)
		Expect(err).ToNot(HaveOccurred())

		credRotr := &CredentialRotationReconciler{
			Client:          mgr.GetClient(),
			Scheme:          scheme,
			Recorder:        mgr.GetEventRecorderFor("moco-credential-rotation"),
			SystemNamespace: testMocoSystemNamespace,
			TTL:             testRotationTTL,
		}
		err = credRotr.SetupWithManager(mgr)
		Expect(err).ToNot(HaveOccurred())

		ctx2, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			err := mgr.Start(ctx2)
			if err != nil {
				panic(err)
			}
		}()
		// Stop synchronously: wait until Start returns, so this spec's
		// manager can never keep reconciling into the next spec and fight
		// the next manager over the same objects.
		stopFunc = func() {
			cancel()
			<-done
		}
		time.Sleep(100 * time.Millisecond)
	})

	AfterEach(func() {
		stopFunc()
	})

	It("should seed the pending passwords and reach ApplyingRetain on creation", func() {
		cluster := startRotationToApplyingRetain(ctx)

		cr, err := getCR(ctx)
		Expect(err).NotTo(HaveOccurred())

		// rotationID and the full condition set are written with the phase.
		Expect(cr.Status.RotationID).NotTo(BeEmpty())
		Expect(apimeta.IsStatusConditionFalse(cr.Status.Conditions, mocov1beta2.ConditionDiscardReady)).To(BeTrue())
		Expect(apimeta.IsStatusConditionFalse(cr.Status.Conditions, mocov1beta2.ConditionDualPassword)).To(BeTrue())
		finished := apimeta.FindStatusCondition(cr.Status.Conditions, mocov1beta2.ConditionFinished)
		Expect(finished).NotTo(BeNil())
		Expect(finished.Status).To(Equal(metav1.ConditionFalse))
		Expect(finished.Reason).To(Equal(mocov1beta2.ReasonRunning))

		// The ownerReference was added before any status write (adoption).
		Expect(cr.OwnerReferences).NotTo(BeEmpty())
		Expect(cr.OwnerReferences[0].Name).To(Equal(cluster.Name))

		// The pending passwords are staged in the controller Secret.
		controllerSecret := &corev1.Secret{}
		err = k8sClient.Get(ctx, client.ObjectKey{
			Namespace: testMocoSystemNamespace,
			Name:      cluster.ControllerSecretName(),
		}, controllerSecret)
		Expect(err).NotTo(HaveOccurred())
		Expect(controllerSecret.Data).To(HaveKey(password.AdminPasswordPendingKey))
		Expect(string(controllerSecret.Data[password.RotationIDKey])).To(Equal(cr.Status.RotationID))
	})

	It("should promote after RETAIN and open the verification window after the rollout", func() {
		cluster := startRotationToApplyingRetain(ctx)

		// Capture the staged pending password before promotion consumes it.
		controllerSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Namespace: testMocoSystemNamespace,
			Name:      cluster.ControllerSecretName(),
		}, controllerSecret)).To(Succeed())
		pendingAdmin := string(controllerSecret.Data[password.AdminPasswordPendingKey])
		oldAdmin := string(controllerSecret.Data["ADMIN_PASSWORD"])

		Eventually(func() error { return simulateRetainDone(ctx) }).Should(Succeed())

		// The reconciler promotes and then waits for distribution + rollout;
		// keep the StatefulSet status reporting a settled rollout while it
		// converges.
		Eventually(func() string {
			_ = syncStatefulSetRolloutComplete(ctx, cluster)
			return crPhase(ctx)
		}).Should(Equal(string(mocov1beta2.PhaseAwaitingDiscard)))

		// Promotion happened as one atomic key rename.
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Namespace: testMocoSystemNamespace,
			Name:      cluster.ControllerSecretName(),
		}, controllerSecret)).To(Succeed())
		Expect(string(controllerSecret.Data["ADMIN_PASSWORD"])).To(Equal(pendingAdmin))
		Expect(string(controllerSecret.Data[password.AdminPasswordOldKey])).To(Equal(oldAdmin))
		Expect(controllerSecret.Data).NotTo(HaveKey(password.AdminPasswordPendingKey))
		Expect(controllerSecret.Data).NotTo(HaveKey(password.RetainStartedKey))
		Expect(controllerSecret.Data).To(HaveKey(password.RotationIDKey))

		cr, err := getCR(ctx)
		Expect(err).NotTo(HaveOccurred())

		// The verification window is open.
		ready := apimeta.FindStatusCondition(cr.Status.Conditions, mocov1beta2.ConditionDiscardReady)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Status).To(Equal(metav1.ConditionTrue))
		Expect(ready.Reason).To(Equal(mocov1beta2.ReasonRolloutSettled))

		// The restart annotation carries this rotation's ID.
		sts := &appsv1.StatefulSet{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Namespace: "test",
			Name:      cluster.PrefixedName(),
		}, sts)).To(Succeed())
		Expect(sts.Spec.Template.Annotations[constants.AnnPasswordRotationRestart]).To(Equal(cr.Status.RotationID))

		// The distributed user Secret carries the promoted passwords.
		userSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Namespace: "test",
			Name:      cluster.UserSecretName(),
		}, userSecret)).To(Succeed())
		Expect(password.CurrentPasswordsMatch(controllerSecret, userSecret)).To(BeTrue())
	})

	It("should complete the cycle on discard and TTL-delete the CR", func() {
		cluster := startRotationToApplyingRetain(ctx)
		Eventually(func() error { return simulateRetainDone(ctx) }).Should(Succeed())
		Eventually(func() string {
			_ = syncStatefulSetRolloutComplete(ctx, cluster)
			return crPhase(ctx)
		}).Should(Equal(string(mocov1beta2.PhaseAwaitingDiscard)))

		// Request the discard (the webhook is not registered in this suite;
		// its gate is tested in the api package).
		Eventually(func() error {
			cr, err := getCR(ctx)
			if err != nil {
				return err
			}
			cr.Spec.Discard = true
			return k8sClient.Update(ctx, cr)
		}).Should(Succeed())

		Eventually(func() string { return crPhase(ctx) }).Should(Equal(string(mocov1beta2.PhaseApplyingDiscard)))

		// The discard request closed the window atomically with the phase.
		cr, err := getCR(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(mocov1beta2.IsConditionFalseWithReason(cr, mocov1beta2.ConditionDiscardReady, mocov1beta2.ReasonPending)).To(BeTrue())
		rotationID := cr.Status.RotationID

		Eventually(func() error { return simulateDiscardDone(ctx) }).Should(Succeed())

		Eventually(func() string { return crPhase(ctx) }).Should(Equal(string(mocov1beta2.PhaseSucceeded)))

		cr, err = getCR(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(cr.Status.CompletionTime).NotTo(BeNil())
		finished := apimeta.FindStatusCondition(cr.Status.Conditions, mocov1beta2.ConditionFinished)
		Expect(finished).NotTo(BeNil())
		Expect(finished.Status).To(Equal(metav1.ConditionTrue))
		Expect(finished.Reason).To(Equal(mocov1beta2.ReasonSucceeded))
		Expect(cr.Status.Message).To(ContainSubstring("deleted automatically"))

		// The bookkeeping keys are gone; the promoted passwords stay current.
		controllerSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Namespace: testMocoSystemNamespace,
			Name:      cluster.ControllerSecretName(),
		}, controllerSecret)).To(Succeed())
		Expect(controllerSecret.Data).NotTo(HaveKey(password.AdminPasswordOldKey))
		Expect(controllerSecret.Data).NotTo(HaveKey(password.RotationIDKey))
		Expect(controllerSecret.Data).NotTo(HaveKey(password.RetainStartedKey))
		_ = rotationID

		// The TTL deletes the CR.
		Eventually(func() bool {
			_, err := getCR(ctx)
			return apierrors.IsNotFound(err)
		}, 30*time.Second).Should(BeTrue(), "the Succeeded CR must be TTL-deleted")

		// A new rotation can start from scratch.
		cr2 := &mocov1beta2.CredentialRotation{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"},
		}
		Expect(k8sClient.Create(ctx, cr2)).To(Succeed())
		Eventually(func() string { return crPhase(ctx) }).Should(Equal(string(mocov1beta2.PhaseApplyingRetain)))
		cr2, err = getCR(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(cr2.Status.RotationID).NotTo(BeEmpty())
		Expect(cr2.Status.RotationID).NotTo(Equal(rotationID))
	})

	It("should block at seed while the cluster has 0 replicas and resume on scale-up", func() {
		cluster := testNewMySQLCluster("test")
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		Eventually(func() error {
			secret := &corev1.Secret{}
			return k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testMocoSystemNamespace,
				Name:      cluster.ControllerSecretName(),
			}, secret)
		}).Should(Succeed())

		// Scale to 0 before creating the CR (the create webhook that would
		// reject this is not registered in this suite). Use a raw merge
		// patch with an explicit 0 to bypass omitempty and CRD defaulting.
		Eventually(func() error {
			fresh := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, fresh); err != nil {
				return err
			}
			patch := client.RawPatch(types.MergePatchType, []byte(`{"spec":{"replicas":0}}`))
			return k8sClient.Patch(ctx, fresh, patch)
		}).Should(Succeed())

		cr := &mocov1beta2.CredentialRotation{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"},
		}
		Expect(k8sClient.Create(ctx, cr)).To(Succeed())

		Eventually(func() string { return crPhase(ctx) }).Should(Equal(string(mocov1beta2.PhaseBlocked)))

		// Nothing was mutated: no pending keys were staged.
		controllerSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Namespace: testMocoSystemNamespace,
			Name:      cluster.ControllerSecretName(),
		}, controllerSecret)).To(Succeed())
		Expect(controllerSecret.Data).NotTo(HaveKey(password.AdminPasswordPendingKey))

		// Scale up; the Reconciler resumes by re-running the seed.
		Eventually(func() error {
			fresh := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, fresh); err != nil {
				return err
			}
			fresh.Spec.Replicas = 3
			return k8sClient.Update(ctx, fresh)
		}).Should(Succeed())

		Eventually(func() string { return crPhase(ctx) }).Should(Equal(string(mocov1beta2.PhaseApplyingRetain)))
	})

	It("should block at seed while the cluster is offline and resume when it is back online", func() {
		cluster := testNewMySQLCluster("test")
		cluster.Spec.Offline = true
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		Eventually(func() error {
			secret := &corev1.Secret{}
			return k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testMocoSystemNamespace,
				Name:      cluster.ControllerSecretName(),
			}, secret)
		}).Should(Succeed())

		// The create webhook that rejects this is not registered in this
		// suite; this exercises the runtime defense in depth.
		cr := &mocov1beta2.CredentialRotation{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"},
		}
		Expect(k8sClient.Create(ctx, cr)).To(Succeed())

		Eventually(func() string { return crPhase(ctx) }).Should(Equal(string(mocov1beta2.PhaseBlocked)))

		// Nothing was mutated: no pending keys were staged.
		controllerSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Namespace: testMocoSystemNamespace,
			Name:      cluster.ControllerSecretName(),
		}, controllerSecret)).To(Succeed())
		Expect(controllerSecret.Data).NotTo(HaveKey(password.AdminPasswordPendingKey))

		// Back online, the Reconciler resumes by re-running the seed.
		Eventually(func() error {
			fresh := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, fresh); err != nil {
				return err
			}
			fresh.Spec.Offline = false
			return k8sClient.Update(ctx, fresh)
		}).Should(Succeed())

		Eventually(func() string { return crPhase(ctx) }).Should(Equal(string(mocov1beta2.PhaseApplyingRetain)))
	})

	It("should hold AwaitingRollout while the cluster is offline instead of opening the window", func() {
		cluster := startRotationToApplyingRetain(ctx)
		Eventually(func() error { return simulateRetainDone(ctx) }).Should(Succeed())

		// Take the cluster offline before the rollout settles.
		Eventually(func() error {
			fresh := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, fresh); err != nil {
				return err
			}
			fresh.Spec.Offline = true
			return k8sClient.Update(ctx, fresh)
		}).Should(Succeed())

		// Offline scales the StatefulSet to 0 replicas, and a 0-replica
		// StatefulSet passes every rollout check trivially. Keep its
		// status "settled" and verify the guard still holds the phase.
		Eventually(func() string {
			_ = syncStatefulSetRolloutComplete(ctx, cluster)
			cr, err := getCR(ctx)
			if err != nil {
				return err.Error()
			}
			return cr.Status.Message
		}).Should(ContainSubstring("Paused"), "the offline hold must be surfaced in status.message")

		Consistently(func() string {
			_ = syncStatefulSetRolloutComplete(ctx, cluster)
			return crPhase(ctx)
		}, 3*time.Second, 500*time.Millisecond).Should(Equal(string(mocov1beta2.PhaseAwaitingRollout)),
			"the verification window must not open while the cluster is offline")

		cr, err := getCR(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(apimeta.IsStatusConditionFalse(cr.Status.Conditions, mocov1beta2.ConditionDiscardReady)).To(BeTrue())

		// Back online, the rollout settles for real and the window opens.
		Eventually(func() error {
			fresh := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, fresh); err != nil {
				return err
			}
			fresh.Spec.Offline = false
			return k8sClient.Update(ctx, fresh)
		}).Should(Succeed())

		Eventually(func() string {
			_ = syncStatefulSetRolloutComplete(ctx, cluster)
			return crPhase(ctx)
		}).Should(Equal(string(mocov1beta2.PhaseAwaitingDiscard)))
	})

	It("should fail in AwaitingRollout when the controller Secret loses a current password key", func() {
		cluster := startRotationToApplyingRetain(ctx)
		Eventually(func() error { return simulateRetainDone(ctx) }).Should(Succeed())

		// Wait until the promotion moved the CR past Promoting.
		Eventually(func() string { return crPhase(ctx) }).Should(Equal(string(mocov1beta2.PhaseAwaitingRollout)))

		// Hand-edit the controller Secret: drop a current password key.
		// Retrying cannot repair this, so the CR must turn Failed instead
		// of requeuing forever.
		Eventually(func() error {
			controllerSecret := &corev1.Secret{}
			if err := k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testMocoSystemNamespace,
				Name:      cluster.ControllerSecretName(),
			}, controllerSecret); err != nil {
				return err
			}
			delete(controllerSecret.Data, password.AdminPasswordKey)
			return k8sClient.Update(ctx, controllerSecret)
		}).Should(Succeed())

		Eventually(func() string { return crPhase(ctx) }).Should(Equal(string(mocov1beta2.PhaseFailed)))
		cr, err := getCR(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(cr.Status.Message).To(ContainSubstring("Inconsistent Controller Secret"))
	})

	It("should fail at seed on *_OLD residue in the controller secret", func() {
		cluster := testNewMySQLCluster("test")
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		Eventually(func() error {
			secret := &corev1.Secret{}
			return k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testMocoSystemNamespace,
				Name:      cluster.ControllerSecretName(),
			}, secret)
		}).Should(Succeed())

		// Inject residue from an abandoned cycle: a partial *_OLD group.
		Eventually(func() error {
			controllerSecret := &corev1.Secret{}
			if err := k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testMocoSystemNamespace,
				Name:      cluster.ControllerSecretName(),
			}, controllerSecret); err != nil {
				return err
			}
			controllerSecret.Data[password.AdminPasswordOldKey] = []byte("leftover")
			controllerSecret.Data[password.RotationIDKey] = []byte("adf914e2-4c3f-45b6-90b8-2f8bbd4b0e6f")
			return k8sClient.Update(ctx, controllerSecret)
		}).Should(Succeed())

		cr := &mocov1beta2.CredentialRotation{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"},
		}
		Expect(k8sClient.Create(ctx, cr)).To(Succeed())

		Eventually(func() string { return crPhase(ctx) }).Should(Equal(string(mocov1beta2.PhaseFailed)))

		cr, err := getCR(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(cr.Status.Message).To(ContainSubstring("recovery procedure"))
		Expect(cr.Status.CompletionTime).NotTo(BeNil())
		finished := apimeta.FindStatusCondition(cr.Status.Conditions, mocov1beta2.ConditionFinished)
		Expect(finished).NotTo(BeNil())
		Expect(finished.Status).To(Equal(metav1.ConditionTrue))
		Expect(finished.Reason).To(Equal(mocov1beta2.ReasonFailed))

		// A Failed CR is not TTL-deleted.
		Consistently(func() bool {
			_, err := getCR(ctx)
			return err == nil
		}, 2*testRotationTTL, time.Second).Should(BeTrue(), "a Failed CR must stay")
	})

	It("should fail when discard is true before the window ever opened", func() {
		cluster := testNewMySQLCluster("test")
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		Eventually(func() error {
			secret := &corev1.Secret{}
			return k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testMocoSystemNamespace,
				Name:      cluster.ControllerSecretName(),
			}, secret)
		}).Should(Succeed())

		// The create webhook (which rejects this) is not registered in this
		// suite; this exercises the runtime defense in depth.
		cr := &mocov1beta2.CredentialRotation{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"},
			Spec:       mocov1beta2.CredentialRotationSpec{Discard: true},
		}
		Expect(k8sClient.Create(ctx, cr)).To(Succeed())

		Eventually(func() string { return crPhase(ctx) }).Should(Equal(string(mocov1beta2.PhaseFailed)))
		cr, err := getCR(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(cr.Status.Message).To(ContainSubstring("verification window"))
	})

	It("should fail a CR whose cluster disappeared before adoption", func() {
		// No MySQLCluster exists (the create webhook that would reject this
		// is not registered in this suite). The reconciler must not leave
		// the CR pending forever: without an ownerReference, garbage
		// collection never deletes it, and a cluster created later under
		// the same name would adopt it and start an unrequested rotation.
		cr := &mocov1beta2.CredentialRotation{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"},
		}
		Expect(k8sClient.Create(ctx, cr)).To(Succeed())

		Eventually(func() string { return crPhase(ctx) }).Should(Equal(string(mocov1beta2.PhaseFailed)))

		cr, err := getCR(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(cr.OwnerReferences).To(BeEmpty(), "a terminal write must not adopt the CR")
		Expect(cr.Status.Message).To(ContainSubstring("before the rotation started"))
	})

	It("should mark a leftover CR from a recreated cluster as Failed", func() {
		cluster := startRotationToApplyingRetain(ctx)

		// Delete the cluster. envtest runs no garbage collector, so the CR
		// survives with the old cluster's UID in its ownerReference.
		Eventually(func() error {
			fresh := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, fresh); err != nil {
				return err
			}
			return k8sClient.Delete(ctx, fresh)
		}).Should(Succeed())
		Eventually(func() bool {
			fresh := &mocov1beta2.MySQLCluster{}
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, fresh)
			return apierrors.IsNotFound(err)
		}).Should(BeTrue())

		// Do what the garbage collector would do in a real cluster: delete
		// the StatefulSet owned by the deleted cluster. Without this, the
		// reconciler for the recreated cluster takes the volumeClaimTemplates
		// re-creation path and deletes the StatefulSet with
		// DeletePropagationOrphan; envtest runs no garbage collector to
		// remove the orphan finalizer, so the StatefulSet would remain
		// terminating forever and poison every subsequent spec.
		sts := &appsv1.StatefulSet{}
		if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test"}, sts); err == nil {
			Expect(k8sClient.Delete(ctx, sts)).To(Succeed())
		}
		Eventually(func() bool {
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "moco-test"}, &appsv1.StatefulSet{})
			return apierrors.IsNotFound(err)
		}).Should(BeTrue())

		// Recreate the cluster under the same name.
		cluster2 := testNewMySQLCluster("test")
		Expect(k8sClient.Create(ctx, cluster2)).To(Succeed())

		Eventually(func() string { return crPhase(ctx) }).Should(Equal(string(mocov1beta2.PhaseFailed)))
		cr, err := getCR(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(cr.Status.Message).To(ContainSubstring("previous MySQLCluster"))

		_ = cluster
	})
})
