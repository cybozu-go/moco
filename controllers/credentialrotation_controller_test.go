package controllers

import (
	"context"
	"fmt"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/password"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// crStep returns the CR's derived workflow step as a string. Used by tests
// to assert workflow progression without depending on the (no-longer-stored)
// Reason of a single condition.
func crStep(cr *mocov1beta2.CredentialRotation) string {
	return string(cr.Step())
}

// simulateRetainDone mimics the side effect of ClusterManager finishing
// RETAIN: DualPassword flips to True, which moves Step from
// ApplyingRetain to DistributingPassword.
func simulateRetainDone(cr *mocov1beta2.CredentialRotation) {
	cr.SetDualPassword(metav1.ConditionTrue, mocov1beta2.ReasonRetained, "test simulate RETAIN done")
}

// simulateDiscardDone mimics the side effect of ClusterManager finishing
// DISCARD: DualPassword flips back to NotRetained, which moves
// Step from ApplyingDiscard to Finalizing.
func simulateDiscardDone(cr *mocov1beta2.CredentialRotation) {
	cr.SetDualPassword(metav1.ConditionFalse, mocov1beta2.ReasonNotRetained, "test simulate DISCARD done")
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
		err = mysqlr.SetupWithManager(mgr)
		Expect(err).ToNot(HaveOccurred())

		credRotr := &CredentialRotationReconciler{
			Client:          mgr.GetClient(),
			Scheme:          scheme,
			Recorder:        mgr.GetEventRecorderFor("moco-credential-rotation"),
			SystemNamespace: testMocoSystemNamespace,
		}
		err = credRotr.SetupWithManager(mgr)
		Expect(err).ToNot(HaveOccurred())

		ctx2, cancel := context.WithCancel(ctx)
		stopFunc = cancel
		go func() {
			err := mgr.Start(ctx2)
			if err != nil {
				panic(err)
			}
		}()
		time.Sleep(100 * time.Millisecond)
	})

	AfterEach(func() {
		stopFunc()
		time.Sleep(100 * time.Millisecond)
	})

	It("should transition from empty to Rotating phase", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		// Wait for the controller secret to be created.
		Eventually(func() error {
			secret := &corev1.Secret{}
			return k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testMocoSystemNamespace,
				Name:      cluster.ControllerSecretName(),
			}, secret)
		}).Should(Succeed())

		cr := &mocov1beta2.CredentialRotation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "test",
			},
			Spec: mocov1beta2.CredentialRotationSpec{
				RotationGeneration: 1,
			},
		}
		err = k8sClient.Create(ctx, cr)
		Expect(err).NotTo(HaveOccurred())

		// The reconciler should generate pending passwords and set phase to Rotating.
		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepApplyingRetain)))

		// Verify rotationID is set.
		cr = &mocov1beta2.CredentialRotation{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr)
		Expect(err).NotTo(HaveOccurred())
		Expect(cr.Status.RotationID).NotTo(BeEmpty())

		// Verify pending passwords are in the source secret.
		sourceSecret := &corev1.Secret{}
		err = k8sClient.Get(ctx, client.ObjectKey{
			Namespace: testMocoSystemNamespace,
			Name:      cluster.ControllerSecretName(),
		}, sourceSecret)
		Expect(err).NotTo(HaveOccurred())
		Expect(sourceSecret.Data).To(HaveKey(password.AdminPasswordPendingKey))
		Expect(sourceSecret.Data).To(HaveKey(password.RotationIDKey))
		Expect(string(sourceSecret.Data[password.RotationIDKey])).To(Equal(cr.Status.RotationID))

		// Verify ownerReference is set.
		Expect(cr.OwnerReferences).NotTo(BeEmpty())
		Expect(cr.OwnerReferences[0].Name).To(Equal(cluster.Name))
	})

	It("should distribute secrets and set Rotated when phase is Retained", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		// Wait for controller secret.
		Eventually(func() error {
			secret := &corev1.Secret{}
			return k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testMocoSystemNamespace,
				Name:      cluster.ControllerSecretName(),
			}, secret)
		}).Should(Succeed())

		// Wait for the StatefulSet to be created.
		Eventually(func() error {
			sts := &appsv1.StatefulSet{}
			return k8sClient.Get(ctx, client.ObjectKey{
				Namespace: "test",
				Name:      cluster.PrefixedName(),
			}, sts)
		}).Should(Succeed())

		// Create the CR and wait for Rotating phase.
		cr := &mocov1beta2.CredentialRotation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "test",
			},
			Spec: mocov1beta2.CredentialRotationSpec{
				RotationGeneration: 1,
			},
		}
		err = k8sClient.Create(ctx, cr)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepApplyingRetain)))

		// Simulate ClusterManager advancing phase to Retained.
		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			simulateRetainDone(cr)
			return k8sClient.Status().Update(ctx, cr)
		}).Should(Succeed())

		// The reconciler should distribute secrets and enter
		// AwaitingRollout (DiscardReady will flip to True only after
		// the StatefulSet rollout settles).
		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepAwaitingRollout)))

		// Verify user secret has pending passwords.
		userSecret := &corev1.Secret{}
		err = k8sClient.Get(ctx, client.ObjectKey{
			Namespace: "test",
			Name:      cluster.UserSecretName(),
		}, userSecret)
		Expect(err).NotTo(HaveOccurred())
		Expect(userSecret.Data).To(HaveKey("ADMIN_PASSWORD"))

		// Verify my.cnf secret exists.
		mycnfSecret := &corev1.Secret{}
		err = k8sClient.Get(ctx, client.ObjectKey{
			Namespace: "test",
			Name:      cluster.MyCnfSecretName(),
		}, mycnfSecret)
		Expect(err).NotTo(HaveOccurred())

		// Verify restart annotation on StatefulSet.
		sts := &appsv1.StatefulSet{}
		err = k8sClient.Get(ctx, client.ObjectKey{
			Namespace: "test",
			Name:      cluster.PrefixedName(),
		}, sts)
		Expect(err).NotTo(HaveOccurred())
		Expect(sts.Spec.Template.Annotations).To(HaveKey(constants.AnnPasswordRotationRestart))
	})

	It("should advance to Discarding when rollout is complete", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		// Wait for controller secret and StatefulSet.
		Eventually(func() error {
			secret := &corev1.Secret{}
			return k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testMocoSystemNamespace,
				Name:      cluster.ControllerSecretName(),
			}, secret)
		}).Should(Succeed())

		Eventually(func() error {
			sts := &appsv1.StatefulSet{}
			return k8sClient.Get(ctx, client.ObjectKey{
				Namespace: "test",
				Name:      cluster.PrefixedName(),
			}, sts)
		}).Should(Succeed())

		// Create CR, wait for Rotating, simulate to Retained, wait for Rotated.
		cr := &mocov1beta2.CredentialRotation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "test",
			},
			Spec: mocov1beta2.CredentialRotationSpec{
				RotationGeneration: 1,
			},
		}
		err = k8sClient.Create(ctx, cr)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepApplyingRetain)))

		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			simulateRetainDone(cr)
			return k8sClient.Status().Update(ctx, cr)
		}).Should(Succeed())

		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepAwaitingRollout)))

		// Simulate StatefulSet rollout complete so handleAwaitingRollout
		// flips DiscardReady=True and the CR enters AwaitingDiscard.
		Eventually(func() error {
			sts := &appsv1.StatefulSet{}
			if err := k8sClient.Get(ctx, client.ObjectKey{
				Namespace: "test",
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
		}).Should(Succeed())

		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepAwaitingDiscard)))

		// Bump discardGeneration to request discard.
		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			cr.Spec.DiscardGeneration = cr.Spec.RotationGeneration
			return k8sClient.Update(ctx, cr)
		}).Should(Succeed())

		// The reconciler should advance to Discarding.
		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepApplyingDiscard)))
	})

	It("should complete the full rotation cycle", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		// Wait for controller secret and StatefulSet.
		Eventually(func() error {
			secret := &corev1.Secret{}
			return k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testMocoSystemNamespace,
				Name:      cluster.ControllerSecretName(),
			}, secret)
		}).Should(Succeed())

		Eventually(func() error {
			sts := &appsv1.StatefulSet{}
			return k8sClient.Get(ctx, client.ObjectKey{
				Namespace: "test",
				Name:      cluster.PrefixedName(),
			}, sts)
		}).Should(Succeed())

		// Create the CredentialRotation CR.
		cr := &mocov1beta2.CredentialRotation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "test",
			},
			Spec: mocov1beta2.CredentialRotationSpec{
				RotationGeneration: 1,
			},
		}
		err = k8sClient.Create(ctx, cr)
		Expect(err).NotTo(HaveOccurred())

		// Phase 1: → Rotating
		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepApplyingRetain)))

		// Capture the pending passwords for later comparison.
		sourceSecret := &corev1.Secret{}
		err = k8sClient.Get(ctx, client.ObjectKey{
			Namespace: testMocoSystemNamespace,
			Name:      cluster.ControllerSecretName(),
		}, sourceSecret)
		Expect(err).NotTo(HaveOccurred())
		pendingAdmin := string(sourceSecret.Data[password.AdminPasswordPendingKey])
		Expect(pendingAdmin).NotTo(BeEmpty())

		// Phase 2: Simulate ClusterManager → Retained
		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			simulateRetainDone(cr)
			return k8sClient.Status().Update(ctx, cr)
		}).Should(Succeed())

		// Phase 3: → AwaitingRollout (reconciler distributes secrets,
		// promotes observedRotationGeneration, waits for the post-distribute
		// rolling restart to settle before opening the verification window).
		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepAwaitingRollout)))

		// Phase 3b: Simulate the StatefulSet rollout completing →
		// AwaitingDiscard (reconciler flips DiscardReady=True).
		Eventually(func() error {
			sts := &appsv1.StatefulSet{}
			if err := k8sClient.Get(ctx, client.ObjectKey{
				Namespace: "test",
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
		}).Should(Succeed())

		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepAwaitingDiscard)))

		// Phase 4: Bump discardGeneration → ApplyingDiscard.
		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			cr.Spec.DiscardGeneration = cr.Spec.RotationGeneration
			return k8sClient.Update(ctx, cr)
		}).Should(Succeed())

		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepApplyingDiscard)))

		// Phase 5: Simulate ClusterManager → Discarded
		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			simulateDiscardDone(cr)
			return k8sClient.Status().Update(ctx, cr)
		}).Should(Succeed())

		// Phase 6: → Completed (reconciler confirms passwords)
		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepIdle)))

		// Verify observed generations are updated.
		cr = &mocov1beta2.CredentialRotation{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr)
		Expect(err).NotTo(HaveOccurred())
		Expect(cr.Status.ObservedRotationGeneration).To(Equal(int64(1)))
		Expect(cr.Status.ObservedDiscardGeneration).To(Equal(int64(1)))

		// Verify pending passwords have been promoted in the source secret.
		sourceSecret = &corev1.Secret{}
		err = k8sClient.Get(ctx, client.ObjectKey{
			Namespace: testMocoSystemNamespace,
			Name:      cluster.ControllerSecretName(),
		}, sourceSecret)
		Expect(err).NotTo(HaveOccurred())
		Expect(sourceSecret.Data).NotTo(HaveKey(password.AdminPasswordPendingKey))
		Expect(sourceSecret.Data).NotTo(HaveKey(password.RotationIDKey))
		Expect(string(sourceSecret.Data["ADMIN_PASSWORD"])).To(Equal(pendingAdmin))

		// Phase 7: second cycle — bumping rotationGeneration on an
		// already-completed CR must still trigger handleStartRotation
		// (the previous cycle's RotationReady=True is stale).
		cycle1Admin := string(sourceSecret.Data["ADMIN_PASSWORD"])

		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			cr.Spec.RotationGeneration = 2
			return k8sClient.Update(ctx, cr)
		}).Should(Succeed())

		// The Reconciler must seed the new cycle: pending passwords
		// appear in the source Secret, RotationReady flips to
		// False/Pending, and the derived step settles on ApplyingRetain
		// waiting for ClusterManager.
		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepApplyingRetain)))

		Eventually(func() bool {
			s := &corev1.Secret{}
			if err := k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testMocoSystemNamespace,
				Name:      cluster.ControllerSecretName(),
			}, s); err != nil {
				return false
			}
			return len(s.Data[password.AdminPasswordPendingKey]) > 0
		}).Should(BeTrue(), "pending passwords for the second cycle were never written")

		// Sanity: the second cycle's pending password is different from
		// the first cycle's now-current password, and the new rotationID
		// is set on status.
		sourceSecret = &corev1.Secret{}
		err = k8sClient.Get(ctx, client.ObjectKey{
			Namespace: testMocoSystemNamespace,
			Name:      cluster.ControllerSecretName(),
		}, sourceSecret)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(sourceSecret.Data[password.AdminPasswordPendingKey])).NotTo(Equal(cycle1Admin))

		cr = &mocov1beta2.CredentialRotation{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr)
		Expect(err).NotTo(HaveOccurred())
		Expect(cr.Status.RotationID).NotTo(BeEmpty())
		Expect(cr.Status.ObservedRotationGeneration).To(Equal(int64(1)),
			"observedRotationGeneration must not advance until the cycle completes")
	})

	It("should refuse rotation when replicas is 0", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		// Wait for controller secret.
		Eventually(func() error {
			secret := &corev1.Secret{}
			return k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testMocoSystemNamespace,
				Name:      cluster.ControllerSecretName(),
			}, secret)
		}).Should(Succeed())

		// Patch replicas to 0 using a merge patch to bypass omitempty and CRD defaulting.
		patch := client.RawPatch(types.MergePatchType, []byte(`{"spec":{"replicas":0}}`))
		err = k8sClient.Patch(ctx, cluster, patch)
		Expect(err).NotTo(HaveOccurred())

		cr := &mocov1beta2.CredentialRotation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "test",
			},
			Spec: mocov1beta2.CredentialRotationSpec{
				RotationGeneration: 1,
			},
		}
		err = k8sClient.Create(ctx, cr)
		Expect(err).NotTo(HaveOccurred())

		// The reconciler should set Rotating=False/RotationRefused and stop
		// advancing. The CR must NOT enter any in-flight sub-step.
		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepRotationRefused)))

		Consistently(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepRotationRefused)))
	})

	It("should not advance past Rotated without bumping discardGeneration", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		// Wait for controller secret and StatefulSet.
		Eventually(func() error {
			secret := &corev1.Secret{}
			return k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testMocoSystemNamespace,
				Name:      cluster.ControllerSecretName(),
			}, secret)
		}).Should(Succeed())

		Eventually(func() error {
			sts := &appsv1.StatefulSet{}
			return k8sClient.Get(ctx, client.ObjectKey{
				Namespace: "test",
				Name:      cluster.PrefixedName(),
			}, sts)
		}).Should(Succeed())

		cr := &mocov1beta2.CredentialRotation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "test",
			},
			Spec: mocov1beta2.CredentialRotationSpec{
				RotationGeneration: 1,
			},
		}
		err = k8sClient.Create(ctx, cr)
		Expect(err).NotTo(HaveOccurred())

		// Advance to Rotated.
		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepApplyingRetain)))

		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			simulateRetainDone(cr)
			return k8sClient.Status().Update(ctx, cr)
		}).Should(Succeed())

		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepAwaitingRollout)))

		// Without simulating rollout completion or bumping
		// discardGeneration, the CR should stay at AwaitingRollout —
		// DiscardReady stays False until the Reconciler observes the
		// rolling restart settle.
		Consistently(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepAwaitingRollout)))
	})

	It("should refuse discard when replicas is 0", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		// Wait for controller secret and StatefulSet.
		Eventually(func() error {
			secret := &corev1.Secret{}
			return k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testMocoSystemNamespace,
				Name:      cluster.ControllerSecretName(),
			}, secret)
		}).Should(Succeed())

		Eventually(func() error {
			sts := &appsv1.StatefulSet{}
			return k8sClient.Get(ctx, client.ObjectKey{
				Namespace: "test",
				Name:      cluster.PrefixedName(),
			}, sts)
		}).Should(Succeed())

		cr := &mocov1beta2.CredentialRotation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "test",
			},
			Spec: mocov1beta2.CredentialRotationSpec{
				RotationGeneration: 1,
			},
		}
		err = k8sClient.Create(ctx, cr)
		Expect(err).NotTo(HaveOccurred())

		// Advance to Rotated.
		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepApplyingRetain)))

		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			simulateRetainDone(cr)
			return k8sClient.Status().Update(ctx, cr)
		}).Should(Succeed())

		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepAwaitingRollout)))

		// Simulate StatefulSet rollout complete so the CR reaches
		// AwaitingDiscard (the webhook requires this to accept a
		// discardGeneration bump).
		Eventually(func() error {
			sts := &appsv1.StatefulSet{}
			if err := k8sClient.Get(ctx, client.ObjectKey{
				Namespace: "test",
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
		}).Should(Succeed())

		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepAwaitingDiscard)))

		// Scale cluster down to 0 (merge patch bypasses CRD defaulting).
		patch := client.RawPatch(types.MergePatchType, []byte(`{"spec":{"replicas":0}}`))
		err = k8sClient.Patch(ctx, cluster, patch)
		Expect(err).NotTo(HaveOccurred())

		// Wait for the apiserver/cache to observe replicas=0 before bumping
		// discardGeneration. Otherwise the reconciler may still see the old
		// replica count and legitimately advance to Discarding.
		Eventually(func() int32 {
			c := &mocov1beta2.MySQLCluster{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), c); err != nil {
				return -1
			}
			return c.Spec.Replicas
		}).Should(Equal(int32(0)))

		// Bump discardGeneration to request discard.
		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			cr.Spec.DiscardGeneration = cr.Spec.RotationGeneration
			return k8sClient.Update(ctx, cr)
		}).Should(Succeed())

		// The discard phase must surface as Refused (DiscardReady=False
		// with Reason=Refused) and never advance to ApplyingDiscard
		// while replicas=0.
		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepDiscardRefused)))
		Consistently(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).ShouldNot(Equal(string(mocov1beta2.StepApplyingDiscard)))
	})

	It("should self-heal user secret with pending passwords after Retained", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		// Wait for the initial user secret created with current passwords.
		var oldAdminPwd string
		Eventually(func() error {
			secret := &corev1.Secret{}
			if err := k8sClient.Get(ctx, client.ObjectKey{
				Namespace: "test",
				Name:      cluster.UserSecretName(),
			}, secret); err != nil {
				return err
			}
			if len(secret.Data["ADMIN_PASSWORD"]) == 0 {
				return fmt.Errorf("admin password not set yet")
			}
			oldAdminPwd = string(secret.Data["ADMIN_PASSWORD"])
			return nil
		}).Should(Succeed())

		// Create a CredentialRotation CR and advance through to Rotated
		// (where pending passwords have been distributed to user Secret).
		cr := &mocov1beta2.CredentialRotation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "test",
			},
			Spec: mocov1beta2.CredentialRotationSpec{
				RotationGeneration: 1,
			},
		}
		err = k8sClient.Create(ctx, cr)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepApplyingRetain)))

		// Simulate ClusterManager → Retained, then let reconciler advance to Rotated.
		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			simulateRetainDone(cr)
			return k8sClient.Status().Update(ctx, cr)
		}).Should(Succeed())

		Eventually(func() string {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return crStep(cr)
		}).Should(Equal(string(mocov1beta2.StepAwaitingRollout)))

		// Capture the pending (new) password from the source Secret — this is what
		// the user Secret should hold after Retained phase distributes it.
		var pendingAdminPwd string
		Eventually(func() error {
			source := &corev1.Secret{}
			if err := k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testMocoSystemNamespace,
				Name:      cluster.ControllerSecretName(),
			}, source); err != nil {
				return err
			}
			if v := source.Data[password.AdminPasswordPendingKey]; len(v) > 0 {
				pendingAdminPwd = string(v)
				return nil
			}
			return fmt.Errorf("pending admin password not set")
		}).Should(Succeed())
		Expect(pendingAdminPwd).NotTo(Equal(oldAdminPwd))

		// Tamper the user Secret. The MySQLClusterReconciler must self-heal it
		// back to the pending (new) password — NOT the old current password.
		Eventually(func() error {
			secret := &corev1.Secret{}
			if err := k8sClient.Get(ctx, client.ObjectKey{
				Namespace: "test",
				Name:      cluster.UserSecretName(),
			}, secret); err != nil {
				return err
			}
			secret.Data["ADMIN_PASSWORD"] = []byte("tampered")
			return k8sClient.Update(ctx, secret)
		}).Should(Succeed())

		Eventually(func() string {
			secret := &corev1.Secret{}
			if err := k8sClient.Get(ctx, client.ObjectKey{
				Namespace: "test",
				Name:      cluster.UserSecretName(),
			}, secret); err != nil {
				return ""
			}
			return string(secret.Data["ADMIN_PASSWORD"])
		}).Should(Equal(pendingAdminPwd))
	})
})
