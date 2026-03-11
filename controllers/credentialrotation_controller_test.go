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
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

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
				SkipNameValidation: ptr.To(true),
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
		Eventually(func() mocov1beta2.RotationPhase {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return cr.Status.Phase
		}).Should(Equal(mocov1beta2.RotationPhaseRotating))

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

		Eventually(func() mocov1beta2.RotationPhase {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return cr.Status.Phase
		}).Should(Equal(mocov1beta2.RotationPhaseRotating))

		// Simulate ClusterManager advancing phase to Retained.
		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			cr.Status.Phase = mocov1beta2.RotationPhaseRetained
			return k8sClient.Status().Update(ctx, cr)
		}).Should(Succeed())

		// The reconciler should distribute secrets and set phase to Rotated.
		Eventually(func() mocov1beta2.RotationPhase {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return cr.Status.Phase
		}).Should(Equal(mocov1beta2.RotationPhaseRotated))

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

		Eventually(func() mocov1beta2.RotationPhase {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return cr.Status.Phase
		}).Should(Equal(mocov1beta2.RotationPhaseRotating))

		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			cr.Status.Phase = mocov1beta2.RotationPhaseRetained
			return k8sClient.Status().Update(ctx, cr)
		}).Should(Succeed())

		Eventually(func() mocov1beta2.RotationPhase {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return cr.Status.Phase
		}).Should(Equal(mocov1beta2.RotationPhaseRotated))

		// Set discardOldPassword=true.
		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			cr.Spec.DiscardOldPassword = true
			return k8sClient.Update(ctx, cr)
		}).Should(Succeed())

		// Simulate StatefulSet rollout complete.
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

		// The reconciler should advance to Discarding.
		Eventually(func() mocov1beta2.RotationPhase {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return cr.Status.Phase
		}).Should(Equal(mocov1beta2.RotationPhaseDiscarding))
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
		Eventually(func() mocov1beta2.RotationPhase {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return cr.Status.Phase
		}).Should(Equal(mocov1beta2.RotationPhaseRotating))

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
			cr.Status.Phase = mocov1beta2.RotationPhaseRetained
			return k8sClient.Status().Update(ctx, cr)
		}).Should(Succeed())

		// Phase 3: → Rotated (reconciler distributes secrets)
		Eventually(func() mocov1beta2.RotationPhase {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return cr.Status.Phase
		}).Should(Equal(mocov1beta2.RotationPhaseRotated))

		// Phase 4: Set discardOldPassword + simulate rollout → Discarding
		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			cr.Spec.DiscardOldPassword = true
			return k8sClient.Update(ctx, cr)
		}).Should(Succeed())

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

		Eventually(func() mocov1beta2.RotationPhase {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return cr.Status.Phase
		}).Should(Equal(mocov1beta2.RotationPhaseDiscarding))

		// Phase 5: Simulate ClusterManager → Discarded
		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			cr.Status.Phase = mocov1beta2.RotationPhaseDiscarded
			return k8sClient.Status().Update(ctx, cr)
		}).Should(Succeed())

		// Phase 6: → Completed (reconciler confirms passwords)
		Eventually(func() mocov1beta2.RotationPhase {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return cr.Status.Phase
		}).Should(Equal(mocov1beta2.RotationPhaseCompleted))

		// Verify observedRotationGeneration is updated.
		cr = &mocov1beta2.CredentialRotation{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr)
		Expect(err).NotTo(HaveOccurred())
		Expect(cr.Status.ObservedRotationGeneration).To(Equal(int64(1)))

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

		// Phase should remain empty (reconciler refuses and requeues without advancing).
		Consistently(func() mocov1beta2.RotationPhase {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return cr.Status.Phase
		}).Should(Equal(mocov1beta2.RotationPhase("")))
	})

	It("should not advance past Rotated without discardOldPassword", func() {
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
		Eventually(func() mocov1beta2.RotationPhase {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return cr.Status.Phase
		}).Should(Equal(mocov1beta2.RotationPhaseRotating))

		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			cr.Status.Phase = mocov1beta2.RotationPhaseRetained
			return k8sClient.Status().Update(ctx, cr)
		}).Should(Succeed())

		Eventually(func() mocov1beta2.RotationPhase {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return cr.Status.Phase
		}).Should(Equal(mocov1beta2.RotationPhaseRotated))

		// Without discardOldPassword, phase should stay at Rotated.
		Consistently(func() mocov1beta2.RotationPhase {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return cr.Status.Phase
		}).Should(Equal(mocov1beta2.RotationPhaseRotated))
	})

	It("should skip secret distribution when rotation is past Retained", func() {
		cluster := testNewMySQLCluster("test")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		// Wait for user secret with old passwords.
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

		Eventually(func() mocov1beta2.RotationPhase {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return cr.Status.Phase
		}).Should(Equal(mocov1beta2.RotationPhaseRotating))

		// Simulate ClusterManager → Retained, then let reconciler advance to Rotated.
		Eventually(func() error {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return err
			}
			cr.Status.Phase = mocov1beta2.RotationPhaseRetained
			return k8sClient.Status().Update(ctx, cr)
		}).Should(Succeed())

		Eventually(func() mocov1beta2.RotationPhase {
			cr := &mocov1beta2.CredentialRotation{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, cr); err != nil {
				return ""
			}
			return cr.Status.Phase
		}).Should(Equal(mocov1beta2.RotationPhaseRotated))

		// Tamper user secret to verify MySQLClusterReconciler does NOT overwrite
		// it with old passwords during Rotated phase.
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

		// MySQLClusterReconciler should NOT overwrite the secret in Rotated phase.
		Consistently(func() string {
			secret := &corev1.Secret{}
			if err := k8sClient.Get(ctx, client.ObjectKey{
				Namespace: "test",
				Name:      cluster.UserSecretName(),
			}, secret); err != nil {
				return ""
			}
			return string(secret.Data["ADMIN_PASSWORD"])
		}).ShouldNot(Equal(oldAdminPwd))
	})
})
