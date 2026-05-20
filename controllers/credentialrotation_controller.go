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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	credRotationRequeueInterval = 15 * time.Second

	// credRotationFieldManager is the field manager used for Server-Side Apply
	// writes by the CredentialRotation reconciler. It is intentionally distinct
	// from MySQLClusterReconciler's "moco-controller" so that fields written
	// here (notably the rolling-restart annotation on the StatefulSet pod
	// template) are not removed when MySQLClusterReconciler re-applies its own
	// view of the StatefulSet, which does not declare the rotation annotation.
	credRotationFieldManager = "moco-credential-rotation"
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

	// Refuse to adopt a stale CR (one whose MySQLCluster ownerReference points
	// at a different UID than the live cluster — i.e., the original cluster
	// was deleted and another was recreated under the same name). Reparenting
	// it would let leftover rotation state poison the new cluster. Such a CR
	// must be deleted (the validating webhook permits this) before a fresh
	// rotation is started on the new cluster.
	if hasStaleClusterOwnerRef(cr, cluster, r.Scheme) {
		log.Info("ignoring stale CredentialRotation (ownerReference UID differs from live cluster)")
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "StaleCredentialRotation",
			"CredentialRotation is owned by a different MySQLCluster UID than the live cluster; delete this CR before starting a new rotation")
		return ctrl.Result{}, nil
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

	// Check if a new rotation or discard is needed
	newRotation := cr.Spec.RotationGeneration > cr.Status.ObservedRotationGeneration
	newDiscard := cr.Spec.DiscardGeneration > cr.Status.ObservedDiscardGeneration

	switch step := cr.CurrentStep(); step {
	case "":
		// Idle or terminal-non-progressing (Rotating absent, or
		// Status=False with Reason ∈ {NotStarted, Completed,
		// RotationRefused}). Start a new rotation if requested.
		if newRotation {
			return r.handleStartRotation(ctx, cr, cluster)
		}
		return ctrl.Result{}, nil

	case mocov1beta2.ReasonApplyingRetain:
		// ClusterManager owns the RETAIN sub-step.
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil

	case mocov1beta2.ReasonDistributingPassword:
		return r.handleDistributingPassword(ctx, cr, cluster)

	case mocov1beta2.ReasonAwaitingDiscard:
		if newDiscard {
			return r.handleStartDiscard(ctx, cr, cluster)
		}
		// Waiting for the operator to bump discardGeneration.
		return ctrl.Result{}, nil

	case mocov1beta2.ReasonWaitingForRollout:
		return r.handleWaitingForRollout(ctx, cr, cluster)

	case mocov1beta2.ReasonApplyingDiscard:
		// ClusterManager owns the DISCARD sub-step.
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil

	case mocov1beta2.ReasonFinalizing:
		return r.handleFinalize(ctx, cr, cluster)

	case mocov1beta2.ReasonRotationBlocked:
		// Try to resume if the cluster is healthy again. Otherwise
		// stay blocked and wait for the operator to act.
		if cluster.Spec.Replicas > 0 {
			return r.handleStartRotation(ctx, cr, cluster)
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil

	case mocov1beta2.ReasonStalePending:
		// Stuck on inconsistent source Secret. The transition into
		// StalePending already emitted a Warning Event with the diagnostic
		// detail in the condition message; just log here to avoid event
		// spam while waiting for manual recovery.
		log.Info("CR is stuck in StalePending; manual recovery required",
			"rotationID", cr.Status.RotationID)
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil

	default:
		log.Info("unrecognized Rotating reason; ignoring", "reason", step)
		return ctrl.Result{}, nil
	}
}

// handleStartRotation: idle (or RotationBlocked resume) → ApplyingRetain
func (r *CredentialRotationReconciler) handleStartRotation(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	if cluster.Spec.Replicas <= 0 {
		// Emit the Warning Event only when the CR transitions into
		// RotationRefused, not on every requeue while it stays refused.
		if cr.RotatingReason() != mocov1beta2.ReasonRotationRefused {
			r.Recorder.Eventf(cr, corev1.EventTypeWarning, "RotationRefused",
				"Cannot start rotation: MySQLCluster replicas is 0")
		}
		cr.SetRotating(metav1.ConditionFalse, mocov1beta2.ReasonRotationRefused,
			"MySQLCluster replicas is 0; nothing has been mutated.")
		cr.SetReady(metav1.ConditionFalse, mocov1beta2.ReasonInProgress,
			"Rotation requested but refused; cluster has 0 replicas.")
		if err := r.Status().Update(ctx, cr); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to RotationRefused: %w", err)
		}
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
		cr.SetRotating(metav1.ConditionTrue, mocov1beta2.ReasonStalePending,
			fmt.Sprintf("Failed to set pending passwords: %v", err))
		if statusErr := r.Status().Update(ctx, cr); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to StalePending: %w", statusErr)
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	// Update the source Secret
	if err := r.Update(ctx, sourceSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update source secret with pending passwords: %w", err)
	}

	// Transition all three conditions to their in-flight initial values.
	cr.Status.RotationID = rotationID
	cr.SetRotating(metav1.ConditionTrue, mocov1beta2.ReasonApplyingRetain,
		"Awaiting ClusterManager to apply RETAIN on all instances.")
	cr.SetOldPasswordRetained(metav1.ConditionFalse, mocov1beta2.ReasonNotRetained,
		"No RETAIN has been issued in the current cycle yet.")
	cr.SetReady(metav1.ConditionFalse, mocov1beta2.ReasonInProgress,
		"Rotation cycle in progress.")
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status to ApplyingRetain: %w", err)
	}

	log.Info("started rotation", "rotationID", rotationID, "rotationGeneration", cr.Spec.RotationGeneration)
	r.Recorder.Eventf(cr, corev1.EventTypeNormal, "RotationStarted",
		"Started rotation cycle (rotationID: %s, generation: %d)", rotationID, cr.Spec.RotationGeneration)

	return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
}

// handleDistributingPassword: DistributingPassword → AwaitingDiscard
func (r *CredentialRotationReconciler) handleDistributingPassword(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
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
		cr.SetRotating(metav1.ConditionTrue, mocov1beta2.ReasonStalePending,
			fmt.Sprintf("Pending password state inconsistency: %v", err))
		if statusErr := r.Status().Update(ctx, cr); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to StalePending: %w", statusErr)
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	} else if !hasPending {
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "MissingRotationPending",
			"Pending passwords not found in source secret for rotationID %s. Manual cleanup required: "+
				"See MOCO documentation for recovery procedures", cr.Status.RotationID)
		cr.SetRotating(metav1.ConditionTrue, mocov1beta2.ReasonStalePending,
			fmt.Sprintf("Pending passwords not found for rotationID %s", cr.Status.RotationID))
		if statusErr := r.Status().Update(ctx, cr); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to StalePending: %w", statusErr)
		}
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

	// Add restart annotation to StatefulSet Pod template via Server-Side Apply
	// under a dedicated field manager. MySQLClusterReconciler reconstructs the
	// pod template annotations from cluster.Spec.PodTemplate.Annotations only
	// and applies under "moco-controller" with ForceOwnership; using a separate
	// field manager here keeps ownership of the rotation annotation key
	// isolated, so the restart trigger is not silently removed by the next
	// MySQLCluster reconcile.
	stsName := cluster.PrefixedName()
	stsAC := appsv1ac.StatefulSet(stsName, cluster.Namespace).
		WithSpec(appsv1ac.StatefulSetSpec().
			WithTemplate(corev1ac.PodTemplateSpec().
				WithAnnotations(map[string]string{
					constants.AnnPasswordRotationRestart: cr.Status.RotationID,
				})))
	if err := r.Apply(ctx, stsAC, client.FieldOwner(credRotationFieldManager), client.ForceOwnership); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply rotation annotation to StatefulSet: %w", err)
	}

	// Transition to AwaitingDiscard.
	cr.SetRotating(metav1.ConditionTrue, mocov1beta2.ReasonAwaitingDiscard,
		"Pending passwords distributed; awaiting discardGeneration bump.")
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status to AwaitingDiscard: %w", err)
	}

	log.Info("distributed pending passwords and triggered rolling restart", "rotationID", cr.Status.RotationID)
	r.Recorder.Eventf(cr, corev1.EventTypeNormal, "SecretsDistributed",
		"Distributed new passwords and triggered rolling restart (rotationID: %s)", cr.Status.RotationID)

	return ctrl.Result{}, nil
}

// handleStartDiscard: AwaitingDiscard → WaitingForRollout
func (r *CredentialRotationReconciler) handleStartDiscard(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	// Refuse to advance when the cluster is scaled to 0. ClusterManager
	// cannot run DISCARD on 0 instances, and the webhook forbids reverting
	// discardGeneration, so the CR would otherwise be stuck.
	if cluster.Spec.Replicas <= 0 {
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "DiscardRefused",
			"Cannot start discard: MySQLCluster replicas is 0. Scale the cluster up first.")
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	cr.SetRotating(metav1.ConditionTrue, mocov1beta2.ReasonWaitingForRollout,
		"Discard requested; waiting for StatefulSet rollout to complete.")
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status to WaitingForRollout: %w", err)
	}

	log.Info("discard requested, waiting for rollout", "rotationID", cr.Status.RotationID)
	r.Recorder.Eventf(cr, corev1.EventTypeNormal, "DiscardStarted",
		"Discard requested; waiting for StatefulSet rollout to complete (rotationID: %s)", cr.Status.RotationID)

	return ctrl.Result{Requeue: true}, nil
}

// handleWaitingForRollout: WaitingForRollout → ApplyingDiscard
func (r *CredentialRotationReconciler) handleWaitingForRollout(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	if cluster.Spec.Replicas <= 0 {
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "DiscardRefused",
			"Cannot run discard: MySQLCluster replicas is 0. Scale the cluster up first.")
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

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

	cr.SetRotating(metav1.ConditionTrue, mocov1beta2.ReasonApplyingDiscard,
		"Awaiting ClusterManager to execute DISCARD OLD PASSWORD on all instances.")
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status to ApplyingDiscard: %w", err)
	}

	log.Info("rollout complete, handing off to ClusterManager for DISCARD", "rotationID", cr.Status.RotationID)
	return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
}

// handleFinalize: Finalizing → Completed
func (r *CredentialRotationReconciler) handleFinalize(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	// Get the source Secret and confirm pending passwords
	sourceSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: r.SystemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, sourceSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get source secret for confirm: %w", err)
	}

	// ConfirmPendingPasswords is idempotent: if pending keys are already gone
	// (crash recovery after Secret update but before status update), it's a no-op.
	hasPending, pendingErr := password.HasPendingPasswords(sourceSecret, cr.Status.RotationID)
	if pendingErr != nil {
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "RotationPendingError",
			"Pending password state inconsistency during confirm: %v. Manual cleanup required: "+
				"See MOCO documentation for recovery procedures", pendingErr)
		cr.SetRotating(metav1.ConditionTrue, mocov1beta2.ReasonStalePending,
			fmt.Sprintf("Pending password state inconsistency during confirm: %v", pendingErr))
		if statusErr := r.Status().Update(ctx, cr); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to StalePending: %w", statusErr)
		}
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
			cr.SetRotating(metav1.ConditionTrue, mocov1beta2.ReasonStalePending,
				fmt.Sprintf("Pending keys lost without promotion for rotationID %s", cr.Status.RotationID))
			if statusErr := r.Status().Update(ctx, cr); statusErr != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update status to StalePending: %w", statusErr)
			}
			return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
		}

		log.Info("no pending passwords found, verified crash recovery (already promoted)",
			"rotationID", cr.Status.RotationID)
		r.Recorder.Eventf(cr, corev1.EventTypeNormal, "CrashRecovery",
			"No pending passwords found for rotationID %s; confirmed prior promotion via user Secret match. Proceeding to Completed.",
			cr.Status.RotationID)
	}

	// Mark cycle complete.
	cr.Status.ObservedRotationGeneration = cr.Spec.RotationGeneration
	cr.Status.ObservedDiscardGeneration = cr.Spec.DiscardGeneration
	cr.SetRotating(metav1.ConditionFalse, mocov1beta2.ReasonCompleted,
		"Rotation cycle completed successfully.")
	cr.SetReady(metav1.ConditionTrue, mocov1beta2.ReasonCompleted,
		"observedRotationGeneration and observedDiscardGeneration match spec.")
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status to Completed: %w", err)
	}

	log.Info("rotation completed",
		"rotationID", cr.Status.RotationID,
		"observedRotationGeneration", cr.Status.ObservedRotationGeneration,
		"observedDiscardGeneration", cr.Status.ObservedDiscardGeneration)
	r.Recorder.Eventf(cr, corev1.EventTypeNormal, "RotationCompleted",
		"Rotation completed (rotationID: %s, rotationGeneration: %d, discardGeneration: %d)",
		cr.Status.RotationID, cr.Spec.RotationGeneration, cr.Spec.DiscardGeneration)

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

// hasStaleClusterOwnerRef reports whether cr carries a MySQLCluster owner
// reference that points at a different UID than the live cluster, with no
// matching reference. That signals a CR left over after a cluster was deleted
// and another recreated under the same name; such CRs must NOT be adopted.
func hasStaleClusterOwnerRef(cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster, scheme *runtime.Scheme) bool {
	gvk, err := apiutil.GVKForObject(cluster, scheme)
	if err != nil {
		return false
	}
	hasStale := false
	for _, ref := range cr.OwnerReferences {
		if ref.Kind != gvk.Kind {
			continue
		}
		if ref.UID == cluster.UID {
			return false
		}
		hasStale = true
	}
	return hasStale
}

func (r *CredentialRotationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mocov1beta2.CredentialRotation{}).
		WithOptions(
			controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles},
		).
		Complete(r)
}
