package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/password"
	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	credRotationRequeueInterval = 15 * time.Second
)

// CredentialRotationReconciler reconciles a CredentialRotation object
type CredentialRotationReconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	Recorder                record.EventRecorder
	SystemNamespace         string
	MaxConcurrentReconciles int
}

//+kubebuilder:rbac:groups=moco.cybozu.com,resources=credentialrotations,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=moco.cybozu.com,resources=credentialrotations/status,verbs=get;update;patch

func (r *CredentialRotationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	cr := &mocov1beta2.CredentialRotation{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Look up the target MySQLCluster
	cluster := &mocov1beta2.MySQLCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("MySQLCluster not found, skipping")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Ensure ownerReference is set
	if !hasOwnerReference(cr, cluster) {
		if err := controllerutil.SetOwnerReference(cluster, cr, r.Scheme); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set ownerReference: %w", err)
		}
		if err := r.Update(ctx, cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Check if a new rotation is needed
	newRotation := cr.Spec.RotationGeneration > cr.Status.ObservedRotationGeneration

	switch {
	case newRotation && (cr.Status.Phase == "" || cr.Status.Phase == mocov1beta2.RotationPhaseCompleted):
		return r.handleStartRotation(ctx, cr, cluster)

	case cr.Status.Phase == mocov1beta2.RotationPhaseRotating:
		// Waiting for ClusterManager to advance to Retained
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil

	case cr.Status.Phase == mocov1beta2.RotationPhaseRetained:
		return r.handleRetainedPhase(ctx, cr, cluster)

	case cr.Status.Phase == mocov1beta2.RotationPhaseRotated:
		if cr.Spec.DiscardOldPassword {
			return r.handleStartDiscard(ctx, cr, cluster)
		}
		// Waiting for user to set discardOldPassword=true
		return ctrl.Result{}, nil

	case cr.Status.Phase == mocov1beta2.RotationPhaseDiscarding:
		// Waiting for ClusterManager to advance to Discarded
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil

	case cr.Status.Phase == mocov1beta2.RotationPhaseDiscarded:
		return r.handleDiscardedPhase(ctx, cr, cluster)

	default:
		return ctrl.Result{}, nil
	}
}

// handleStartRotation: ""/Completed → Rotating
func (r *CredentialRotationReconciler) handleStartRotation(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	if cluster.Spec.Replicas <= 0 {
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "RotationRefused",
			"Cannot start rotation: MySQLCluster replicas is 0")
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	// Get the source Secret
	sourceSecret := &corev1.Secret{}
	secretName := cluster.ControllerSecretName()
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: r.SystemNamespace,
		Name:      secretName,
	}, sourceSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get source secret: %w", err)
	}

	// Reuse existing rotationID if the Secret already has complete pending
	// passwords (crash recovery: Secret was updated but status was not).
	rotationID := password.GetRotationID(sourceSecret)
	if rotationID == "" {
		rotationID = uuid.New().String()
	}

	// Generate pending passwords (idempotent if rotationID matches)
	_, err := password.SetPendingPasswords(sourceSecret, rotationID)
	if err != nil {
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "RotationPendingError",
			"Failed to set pending passwords: %v. Manual cleanup required: "+
				"See MOCO documentation for recovery procedures", err)
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	// Update the source Secret
	if err := r.Update(ctx, sourceSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update source secret with pending passwords: %w", err)
	}

	// Update status to Rotating
	cr.Status.Phase = mocov1beta2.RotationPhaseRotating
	cr.Status.RotationID = rotationID
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status to Rotating: %w", err)
	}

	log.Info("started rotation", "rotationID", rotationID, "rotationGeneration", cr.Spec.RotationGeneration)
	r.Recorder.Eventf(cr, corev1.EventTypeNormal, "RotationStarted",
		"Started rotation cycle (rotationID: %s, generation: %d)", rotationID, cr.Spec.RotationGeneration)

	return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
}

// handleRetainedPhase: Retained → Rotated
func (r *CredentialRotationReconciler) handleRetainedPhase(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	// Get the source Secret
	sourceSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: r.SystemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, sourceSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get source secret: %w", err)
	}

	// Verify pending passwords belong to this rotation cycle.
	if hasPending, err := password.HasPendingPasswords(sourceSecret, cr.Status.RotationID); err != nil {
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "RotationPendingError",
			"Pending password state inconsistency: %v. Manual cleanup required: "+
				"See MOCO documentation for recovery procedures", err)
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	} else if !hasPending {
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "MissingRotationPending",
			"Pending passwords not found in source secret for rotationID %s. Manual cleanup required: "+
				"See MOCO documentation for recovery procedures", cr.Status.RotationID)
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	// Distribute pending passwords to per-namespace user Secret
	pendingPasswd, err := password.NewMySQLPasswordFromPending(sourceSecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to read pending passwords: %w", err)
	}

	// Apply user Secret with pending passwords
	newSecret := pendingPasswd.ToSecret()
	userSecretName := cluster.UserSecretName()
	userSecret := corev1ac.Secret(userSecretName, cluster.Namespace).
		WithAnnotations(newSecret.Annotations).
		WithLabels(labelSet(cluster, false)).
		WithData(newSecret.Data)
	if err := setControllerReference(cluster, userSecret, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set ownerReference to user Secret: %w", err)
	}
	userKey := client.ObjectKey{Namespace: cluster.Namespace, Name: userSecretName}
	if _, err := apply(ctx, r.Client, userKey, userSecret, corev1ac.ExtractSecret); err != nil {
		if !errors.Is(err, ErrApplyConfigurationNotChanged) {
			return ctrl.Result{}, fmt.Errorf("failed to apply user Secret: %w", err)
		}
	}

	// Apply my.cnf Secret with pending passwords
	mycnfSecret := pendingPasswd.ToMyCnfSecret()
	mycnfSecretName := cluster.MyCnfSecretName()
	mycnfSecretAC := corev1ac.Secret(mycnfSecretName, cluster.Namespace).
		WithAnnotations(mycnfSecret.Annotations).
		WithLabels(labelSet(cluster, false)).
		WithData(mycnfSecret.Data)
	if err := setControllerReference(cluster, mycnfSecretAC, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set ownerReference to my.cnf Secret: %w", err)
	}
	mycnfKey := client.ObjectKey{Namespace: cluster.Namespace, Name: mycnfSecretName}
	if _, err := apply(ctx, r.Client, mycnfKey, mycnfSecretAC, corev1ac.ExtractSecret); err != nil {
		if !errors.Is(err, ErrApplyConfigurationNotChanged) {
			return ctrl.Result{}, fmt.Errorf("failed to apply my.cnf Secret: %w", err)
		}
	}

	// Add restart annotation to StatefulSet Pod template
	sts := &appsv1.StatefulSet{}
	stsName := cluster.PrefixedName()
	if err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: stsName}, sts); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get StatefulSet: %w", err)
	}

	patch := client.MergeFrom(sts.DeepCopy())
	if sts.Spec.Template.Annotations == nil {
		sts.Spec.Template.Annotations = make(map[string]string)
	}
	sts.Spec.Template.Annotations[constants.AnnPasswordRotationRestart] = cr.Status.RotationID
	if err := r.Patch(ctx, sts, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch StatefulSet with restart annotation: %w", err)
	}

	// Update status to Rotated
	cr.Status.Phase = mocov1beta2.RotationPhaseRotated
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status to Rotated: %w", err)
	}

	log.Info("distributed pending passwords and triggered rolling restart", "rotationID", cr.Status.RotationID)
	r.Recorder.Eventf(cr, corev1.EventTypeNormal, "SecretsDistributed",
		"Distributed new passwords and triggered rolling restart (rotationID: %s)", cr.Status.RotationID)

	return ctrl.Result{}, nil
}

// handleStartDiscard: Rotated + discardOldPassword=true → Discarding
func (r *CredentialRotationReconciler) handleStartDiscard(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	// Wait for StatefulSet rollout to complete
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      cluster.PrefixedName(),
	}, sts); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get StatefulSet: %w", err)
	}

	if !isStatefulSetRolloutComplete(sts) {
		log.Info("waiting for StatefulSet rollout to complete before discard")
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	// Update status to Discarding
	cr.Status.Phase = mocov1beta2.RotationPhaseDiscarding
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status to Discarding: %w", err)
	}

	log.Info("StatefulSet rollout complete, moving to Discarding phase", "rotationID", cr.Status.RotationID)
	r.Recorder.Eventf(cr, corev1.EventTypeNormal, "DiscardStarted",
		"StatefulSet rollout complete, starting discard phase (rotationID: %s)", cr.Status.RotationID)

	return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
}

// handleDiscardedPhase: Discarded → Completed
func (r *CredentialRotationReconciler) handleDiscardedPhase(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	// Get the source Secret and confirm pending passwords
	sourceSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: r.SystemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, sourceSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get source secret for confirm: %w", err)
	}

	// Confirm pending passwords (promote pending → current, remove pending keys).
	// ConfirmPendingPasswords is idempotent: if pending keys are already gone
	// (crash recovery after Secret update but before status update), it's a no-op.
	hasPending, pendingErr := password.HasPendingPasswords(sourceSecret, cr.Status.RotationID)
	if pendingErr != nil {
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "RotationPendingError",
			"Pending password state inconsistency during confirm: %v. Manual cleanup required: "+
				"See MOCO documentation for recovery procedures", pendingErr)
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	if hasPending {
		if err := password.ConfirmPendingPasswords(sourceSecret); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to confirm pending passwords: %w", err)
		}
		if err := r.Update(ctx, sourceSecret); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update source secret after confirm: %w", err)
		}
	} else {
		// No pending keys found. Verify this is genuine crash recovery
		// (pending already promoted to current) by comparing the controller
		// Secret's current passwords with the per-namespace user Secret.
		// If they match, promotion succeeded before the status update.
		// If they differ, pending keys were lost without promotion —
		// an inconsistency that requires manual cleanup.
		userSecret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{
			Namespace: cluster.Namespace,
			Name:      cluster.UserSecretName(),
		}, userSecret); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get user secret for crash recovery verification: %w", err)
		}
		if !password.CurrentPasswordsMatch(sourceSecret, userSecret) {
			r.Recorder.Eventf(cr, corev1.EventTypeWarning, "InconsistentState",
				"No pending passwords found for rotationID %s and controller Secret does not match user Secret. "+
					"Manual cleanup required: See MOCO documentation for recovery procedures",
				cr.Status.RotationID)
			return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
		}

		log.Info("no pending passwords found, verified crash recovery (already promoted)",
			"rotationID", cr.Status.RotationID)
		r.Recorder.Eventf(cr, corev1.EventTypeNormal, "CrashRecovery",
			"No pending passwords found for rotationID %s; confirmed prior promotion via user Secret match. Proceeding to Completed.",
			cr.Status.RotationID)
	}

	// Update status to Completed
	cr.Status.Phase = mocov1beta2.RotationPhaseCompleted
	cr.Status.ObservedRotationGeneration = cr.Spec.RotationGeneration
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status to Completed: %w", err)
	}

	log.Info("rotation completed", "rotationID", cr.Status.RotationID, "observedGeneration", cr.Status.ObservedRotationGeneration)
	r.Recorder.Eventf(cr, corev1.EventTypeNormal, "RotationCompleted",
		"Rotation completed (rotationID: %s, generation: %d)", cr.Status.RotationID, cr.Spec.RotationGeneration)

	return ctrl.Result{}, nil
}

func isStatefulSetRolloutComplete(sts *appsv1.StatefulSet) bool {
	if sts.Status.ObservedGeneration < sts.Generation {
		return false
	}
	if sts.Status.CurrentRevision != sts.Status.UpdateRevision {
		return false
	}
	if sts.Spec.Replicas != nil && sts.Status.Replicas != *sts.Spec.Replicas {
		return false
	}
	if sts.Spec.Replicas != nil && sts.Status.UpdatedReplicas != *sts.Spec.Replicas {
		return false
	}
	if sts.Spec.Replicas != nil && sts.Status.ReadyReplicas != *sts.Spec.Replicas {
		return false
	}
	return true
}

func hasOwnerReference(cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) bool {
	for _, ref := range cr.OwnerReferences {
		if ref.UID == cluster.UID {
			return true
		}
	}
	return false
}

func (r *CredentialRotationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mocov1beta2.CredentialRotation{}).
		WithOptions(
			controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles},
		).
		Complete(r)
}
