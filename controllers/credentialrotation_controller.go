package controllers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/password"
	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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

	cluster := &mocov1beta2.MySQLCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("MySQLCluster not found, skipping")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if hasStaleClusterOwnerRef(cr, cluster, r.Scheme) {
		log.Info("ignoring stale CredentialRotation (ownerReference UID differs from live cluster)")
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "StaleCredentialRotation",
			"CredentialRotation is owned by a different MySQLCluster UID than the live cluster; delete this CR before starting a new rotation")
		return ctrl.Result{}, nil
	}

	if !hasOwnerReference(cr, cluster) {
		if err := controllerutil.SetOwnerReference(cluster, cr, r.Scheme); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set ownerReference: %w", err)
		}
		if err := r.Update(ctx, cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	newRotation := cr.Spec.RotationGeneration > cr.Status.ObservedRotationGeneration
	newDiscard := cr.Spec.DiscardGeneration > cr.Status.ObservedDiscardGeneration

	switch step := cr.Step(); step {
	case mocov1beta2.StepIdle:
		if newRotation {
			return r.handleStartRotation(ctx, cr, cluster)
		}
		return ctrl.Result{}, nil

	case mocov1beta2.StepApplyingRetain:
		// ClusterManager owns RETAIN.
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil

	case mocov1beta2.StepDistributingPassword:
		return r.handleDistributingPassword(ctx, cr, cluster)

	case mocov1beta2.StepAwaitingRollout:
		// Reconciler waits for the post-distribute StatefulSet rollout
		// to settle, then flips DiscardReady=True so the verification
		// window opens.
		return r.handleAwaitingRollout(ctx, cr, cluster)

	case mocov1beta2.StepAwaitingDiscard:
		// Wait for the operator to bump discardGeneration.
		return ctrl.Result{}, nil

	case mocov1beta2.StepApplyingDiscard:
		// Reconciler handles the first transition after discardGeneration
		// is bumped (Refused detection + DiscardReady→Pending + event).
		// ClusterManager owns the DISCARD SQL execution; by the time we
		// reach this step DiscardReady was True, so the rollout already
		// settled in StepAwaitingRollout.
		return r.handleApplyingDiscard(ctx, cr, cluster)

	case mocov1beta2.StepFinalizing:
		return r.handleFinalize(ctx, cr, cluster)

	case mocov1beta2.StepRotationRefused, mocov1beta2.StepRotationBlocked:
		// Retry the rotation phase when the cluster becomes healthy.
		// (newRotation is implied by the step; re-checked defensively.)
		if cluster.Spec.Replicas > 0 && newRotation {
			return r.handleStartRotation(ctx, cr, cluster)
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil

	case mocov1beta2.StepDiscardRefused, mocov1beta2.StepDiscardBlocked:
		// Retry the discard phase when the cluster becomes healthy.
		// (newDiscard is implied by the step; re-checked defensively.)
		if cluster.Spec.Replicas > 0 && newDiscard {
			return r.handleApplyingDiscard(ctx, cr, cluster)
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil

	case mocov1beta2.StepStalePending:
		// Stuck on inconsistent source Secret. The transition into
		// Stale already emitted a Warning Event with the diagnostic
		// detail in the condition Message; just log here to avoid event
		// spam while waiting for manual recovery.
		log.Info("CR is stuck in Stale state; manual recovery required",
			"rotationID", cr.Status.RotationID)
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil

	default:
		log.Info("unrecognized rotation step; ignoring", "step", step)
		return ctrl.Result{}, nil
	}
}

// handleStartRotation begins a new rotation cycle, or resumes one that
// previously transitioned to RotationRefused or RotationBlocked because
// the cluster had 0 replicas. It writes pending passwords into the source
// Secret, initialises the three Conditions to their "in flight" defaults,
// and hands off to the ClusterManager for the RETAIN step.
//
// Callers must ensure the CR's step warrants a start/retry: the
// Reconcile switch dispatches here only from StepIdle, StepRotationRefused,
// or StepRotationBlocked, and only when newRotation is true and the
// cluster has been scaled back up (for the Refused/Blocked cases).
func (r *CredentialRotationReconciler) handleStartRotation(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	if cluster.Spec.Replicas <= 0 {
		// Skip the no-op Status().Update when the CR is already at
		// False/Refused. Without this guard, every 15s requeue while
		// the cluster stays scaled to 0 would re-issue an API write
		// (apimeta.SetStatusCondition preserves LastTransitionTime,
		// but Status().Update itself still hits the apiserver).
		if mocov1beta2.ConditionFalseWithReason(cr, mocov1beta2.ConditionRotationReady, mocov1beta2.ReasonRefused) {
			return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
		}
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "RotationRefused",
			"Cannot start rotation: MySQLCluster replicas is 0")
		initCycleConditionsIfAbsent(cr)
		cr.SetRotationReady(metav1.ConditionFalse, mocov1beta2.ReasonRefused,
			"MySQLCluster replicas is 0; nothing has been mutated.")
		if err := r.updateStatus(ctx, cr); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to RotationRefused: %w", err)
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	sourceSecret := &corev1.Secret{}
	secretName := cluster.ControllerSecretName()
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: r.SystemNamespace,
		Name:      secretName,
	}, sourceSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get source secret: %w", err)
	}

	rotationID := password.GetRotationID(sourceSecret)
	if rotationID == "" {
		rotationID = uuid.New().String()
	}

	if _, err := password.SetPendingPasswords(sourceSecret, rotationID); err != nil {
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "RotationPendingError",
			"Failed to set pending passwords: %v. Manual cleanup required: "+
				"See MOCO documentation for recovery procedures", err)
		initCycleConditionsIfAbsent(cr)
		cr.SetRotationReady(metav1.ConditionFalse, mocov1beta2.ReasonStale,
			fmt.Sprintf("Failed to set pending passwords: %v", err))
		if statusErr := r.updateStatus(ctx, cr); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to Stale: %w", statusErr)
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	if err := r.Update(ctx, sourceSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update source secret with pending passwords: %w", err)
	}

	cr.Status.RotationID = rotationID
	cr.SetDualPassword(metav1.ConditionFalse, mocov1beta2.ReasonNotRetained,
		"No RETAIN has been issued in the current cycle yet.")
	cr.SetRotationReady(metav1.ConditionFalse, mocov1beta2.ReasonPending,
		"Rotation cycle in flight; idle (rotate) is not currently allowed.")
	cr.SetDiscardReady(metav1.ConditionFalse, mocov1beta2.ReasonPending,
		"Rotation cycle in flight; awaiting-discard (discard) is not currently allowed.")
	if err := r.updateStatus(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status to ApplyingRetain: %w", err)
	}

	log.Info("started rotation", "rotationID", rotationID, "rotationGeneration", cr.Spec.RotationGeneration)
	r.Recorder.Eventf(cr, corev1.EventTypeNormal, "RotationStarted",
		"Started rotation cycle (rotationID: %s, generation: %d)", rotationID, cr.Spec.RotationGeneration)

	return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
}

// handleDistributingPassword: distributes pending passwords to per-namespace
// Secrets, triggers the rolling restart, and promotes
// observedRotationGeneration. DiscardReady stays False/Pending — the next
// step (StepAwaitingRollout) waits for the StatefulSet rollout to settle
// before flipping DiscardReady=True and opening the verification window.
func (r *CredentialRotationReconciler) handleDistributingPassword(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	sourceSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: r.SystemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, sourceSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get source secret: %w", err)
	}

	hasPending, err := password.HasPendingPasswords(sourceSecret, cr.Status.RotationID)
	if err != nil {
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "RotationPendingError",
			"Pending password state inconsistency: %v. Manual cleanup required: "+
				"See MOCO documentation for recovery procedures", err)
		cr.SetRotationReady(metav1.ConditionFalse, mocov1beta2.ReasonStale,
			fmt.Sprintf("Pending password state inconsistency: %v", err))
		if statusErr := r.updateStatus(ctx, cr); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to Stale: %w", statusErr)
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}
	if !hasPending {
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "MissingRotationPending",
			"Pending passwords not found in source secret for rotationID %s. Manual cleanup required: "+
				"See MOCO documentation for recovery procedures", cr.Status.RotationID)
		cr.SetRotationReady(metav1.ConditionFalse, mocov1beta2.ReasonStale,
			fmt.Sprintf("Pending passwords not found for rotationID %s", cr.Status.RotationID))
		if statusErr := r.updateStatus(ctx, cr); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to Stale: %w", statusErr)
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	pendingPasswd, err := password.NewMySQLPasswordFromPending(sourceSecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to read pending passwords: %w", err)
	}

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
	// under a dedicated field manager so the rotation annotation key is not
	// silently removed by the next MySQLCluster reconcile.
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

	// Enter AwaitingRollout. observedRotationGeneration is promoted
	// here, but DiscardReady stays False/Pending — handleAwaitingRollout
	// will flip it to True once the rolling restart settles.
	cr.Status.ObservedRotationGeneration = cr.Spec.RotationGeneration
	if err := r.updateStatus(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status after distribution: %w", err)
	}

	log.Info("distributed pending passwords and triggered rolling restart", "rotationID", cr.Status.RotationID)
	r.Recorder.Eventf(cr, corev1.EventTypeNormal, "SecretsDistributed",
		"Distributed new passwords and triggered rolling restart (rotationID: %s)", cr.Status.RotationID)

	return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
}

// handleAwaitingRollout waits for the post-distribute StatefulSet rollout
// to settle. While the rollout is in flight, some Pods may still be
// running with the old password loaded via EnvFrom; flipping
// DiscardReady=True before every Pod has picked up the new password
// would let an operator-initiated DISCARD strip the secondary password
// out from under them. Once the rollout is complete, all Pods are using
// the new password and the verification window is genuinely open.
func (r *CredentialRotationReconciler) handleAwaitingRollout(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      cluster.PrefixedName(),
	}, sts); err != nil {
		if apierrors.IsNotFound(err) {
			// The StatefulSet has not been created yet (or was deleted);
			// requeue and try again.
			return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get StatefulSet for rollout check: %w", err)
	}
	if !isStatefulSetRolloutComplete(sts) {
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	cr.SetDiscardReady(metav1.ConditionTrue, mocov1beta2.ReasonReconciled,
		"Rotation phase finished and post-distribute rollout settled; awaiting-discard steady state — discard is now allowed.")
	if err := r.updateStatus(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status after rollout: %w", err)
	}

	log.Info("post-distribute rollout settled; awaiting-discard window open",
		"rotationID", cr.Status.RotationID)
	r.Recorder.Eventf(cr, corev1.EventTypeNormal, "AwaitingDiscard",
		"Post-distribute StatefulSet rollout settled; verification window open (rotationID: %s)",
		cr.Status.RotationID)
	return ctrl.Result{}, nil
}

// isStatefulSetRolloutComplete reports whether the StatefulSet has fully
// rolled out the latest revision (mirrors the helper in clustering/).
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

// handleApplyingDiscard is the Reconciler-side handler for the discard
// phase. It owns the K8s-state transitions into DiscardReady=False/Pending
// (handling the initial bump as well as recovery from Refused / Blocked /
// stale Reconciled), and emits the DiscardStarted Event. ClusterManager
// then runs the DISCARD SQL — by the time the operator can bump
// discardGeneration the post-distribute rollout has already settled
// (handleAwaitingRollout is what flipped DiscardReady=True in the first
// place), so ClusterManager does not need to repeat the rollout wait.
func (r *CredentialRotationReconciler) handleApplyingDiscard(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	discardReadyCond := apimeta.FindStatusCondition(cr.Status.Conditions, mocov1beta2.ConditionDiscardReady)
	isPending := discardReadyCond != nil &&
		discardReadyCond.Status == metav1.ConditionFalse &&
		discardReadyCond.Reason == mocov1beta2.ReasonPending
	wasRefused := discardReadyCond != nil &&
		discardReadyCond.Status == metav1.ConditionFalse &&
		discardReadyCond.Reason == mocov1beta2.ReasonRefused

	if cluster.Spec.Replicas <= 0 {
		// Skip the no-op Status().Update when the CR is already at
		// False/Refused. See the matching guard in handleStartRotation
		// for the rationale.
		if wasRefused {
			return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
		}
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "DiscardRefused",
			"Cannot start discard: MySQLCluster replicas is 0. Scale the cluster up first.")
		cr.SetDiscardReady(metav1.ConditionFalse, mocov1beta2.ReasonRefused,
			"Cannot start discard: MySQLCluster replicas is 0.")
		if err := r.updateStatus(ctx, cr); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to DiscardReady=Refused: %w", err)
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	if !isPending {
		// Initial bump (DiscardReady=True/absent) or recovery from
		// Refused/Blocked — transition to Pending so ClusterManager
		// is unblocked.
		cr.SetDiscardReady(metav1.ConditionFalse, mocov1beta2.ReasonPending,
			"Discard requested; awaiting StatefulSet rollout and DISCARD on all instances.")
		if err := r.updateStatus(ctx, cr); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to initialise discard phase: %w", err)
		}
		log.Info("discard requested; ClusterManager will wait for rollout and run DISCARD",
			"rotationID", cr.Status.RotationID)
		r.Recorder.Eventf(cr, corev1.EventTypeNormal, "DiscardStarted",
			"Discard requested; ClusterManager will wait for StatefulSet rollout before DISCARD (rotationID: %s)",
			cr.Status.RotationID)
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	// DiscardReady=False/Pending already; ClusterManager owns from here.
	return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
}

// handleFinalize promotes the pending passwords to current in the source
// Secret, marks the discard phase complete, and flips DualPassword
// to NotRetained for the next cycle.
func (r *CredentialRotationReconciler) handleFinalize(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	sourceSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: r.SystemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, sourceSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get source secret for confirm: %w", err)
	}

	hasPending, pendingErr := password.HasPendingPasswords(sourceSecret, cr.Status.RotationID)
	if pendingErr != nil {
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "RotationPendingError",
			"Pending password state inconsistency during confirm: %v. Manual cleanup required: "+
				"See MOCO documentation for recovery procedures", pendingErr)
		cr.SetDiscardReady(metav1.ConditionFalse, mocov1beta2.ReasonStale,
			fmt.Sprintf("Pending password state inconsistency during confirm: %v", pendingErr))
		if statusErr := r.updateStatus(ctx, cr); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to Stale: %w", statusErr)
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
			cr.SetDiscardReady(metav1.ConditionFalse, mocov1beta2.ReasonStale,
				fmt.Sprintf("Pending keys lost without promotion for rotationID %s", cr.Status.RotationID))
			if statusErr := r.updateStatus(ctx, cr); statusErr != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update status to Stale: %w", statusErr)
			}
			return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
		}

		log.Info("no pending passwords found, verified crash recovery (already promoted)",
			"rotationID", cr.Status.RotationID)
		r.Recorder.Eventf(cr, corev1.EventTypeNormal, "CrashRecovery",
			"No pending passwords found for rotationID %s; confirmed prior promotion via user Secret match. Proceeding to Completed.",
			cr.Status.RotationID)
	}

	// Return to Idle steady state.
	cr.Status.ObservedDiscardGeneration = cr.Spec.DiscardGeneration
	cr.SetRotationReady(metav1.ConditionTrue, mocov1beta2.ReasonReconciled,
		"Cycle complete; idle steady state — rotate is now allowed.")
	cr.SetDiscardReady(metav1.ConditionFalse, mocov1beta2.ReasonPending,
		"Idle steady state; no dual-password set to discard.")
	if err := r.updateStatus(ctx, cr); err != nil {
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

// initCycleConditionsIfAbsent seeds DualPassword and DiscardReady to
// their default values on a freshly created CR so handleStartRotation's
// Refused/Stale path can leave RotationReady as the only condition with
// a non-default Reason. Both seeded conditions reflect "not currently
// applicable": no dual password is held and the cycle has not yet
// reached the awaiting-discard window.
func initCycleConditionsIfAbsent(cr *mocov1beta2.CredentialRotation) {
	if apimeta.FindStatusCondition(cr.Status.Conditions, mocov1beta2.ConditionDualPassword) == nil {
		cr.SetDualPassword(metav1.ConditionFalse, mocov1beta2.ReasonNotRetained,
			"No RETAIN has been issued in the current cycle yet.")
	}
	if apimeta.FindStatusCondition(cr.Status.Conditions, mocov1beta2.ConditionDiscardReady) == nil {
		cr.SetDiscardReady(metav1.ConditionFalse, mocov1beta2.ReasonPending,
			"No rotation has reached the awaiting-discard window yet.")
	}
}

// updateStatus stamps the current metadata.generation into
// status.observedGeneration before sending the Status().Update so that
// external tooling (kstatus, ArgoCD, Flux) can detect that the controller
// has caught up with the latest spec change.
func (r *CredentialRotationReconciler) updateStatus(ctx context.Context, cr *mocov1beta2.CredentialRotation) error {
	cr.StampObservedGeneration()
	return r.Status().Update(ctx, cr)
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
		// Wake the reconciler when the target MySQLCluster changes — most
		// importantly when Spec.Replicas transitions to or from 0, so a
		// Refused / Blocked state resumes immediately on scale-up instead
		// of waiting for the periodic requeue.
		Watches(
			&mocov1beta2.MySQLCluster{},
			handler.EnqueueRequestsFromMapFunc(mapClusterToCR),
			builder.WithPredicates(clusterReplicasChangedPredicate{}),
		).
		// Wake the reconciler when the controller-namespace source Secret
		// changes — this lets the operator clean up a Stale source Secret
		// (or restore one) without waiting for the periodic requeue.
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.mapSourceSecretToCR),
			builder.WithPredicates(sourceSecretPredicate(r.SystemNamespace)),
		).
		WithOptions(
			controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles},
		).
		Complete(r)
}

// mapClusterToCR maps a MySQLCluster event to a CredentialRotation
// reconcile request. A CredentialRotation always shares its
// namespace/name with the cluster it manages.
func mapClusterToCR(_ context.Context, obj client.Object) []reconcile.Request {
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
		},
	}}
}

// mapSourceSecretToCR reverse-parses a controller-namespace Secret name
// ("mysql-<ns>.<name>") back to the CredentialRotation that may be
// interested in it. The reverse parse is best-effort: if the name does
// not match the expected pattern, no request is enqueued.
func (r *CredentialRotationReconciler) mapSourceSecretToCR(_ context.Context, obj client.Object) []reconcile.Request {
	if obj.GetNamespace() != r.SystemNamespace {
		return nil
	}
	const prefix = "mysql-"
	name := obj.GetName()
	if !strings.HasPrefix(name, prefix) {
		return nil
	}
	rest := name[len(prefix):]
	idx := strings.Index(rest, ".")
	if idx <= 0 || idx == len(rest)-1 {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: rest[:idx],
			Name:      rest[idx+1:],
		},
	}}
}

// clusterReplicasChangedPredicate filters MySQLCluster events down to
// those that may unblock or refresh a rotation: creation, deletion, or
// Spec.Replicas changes.
type clusterReplicasChangedPredicate struct {
	predicate.Funcs
}

func (clusterReplicasChangedPredicate) Update(e event.UpdateEvent) bool {
	oldCluster, ok := e.ObjectOld.(*mocov1beta2.MySQLCluster)
	if !ok {
		return false
	}
	newCluster, ok := e.ObjectNew.(*mocov1beta2.MySQLCluster)
	if !ok {
		return false
	}
	if oldCluster.Spec.Replicas != newCluster.Spec.Replicas {
		return true
	}
	return (oldCluster.DeletionTimestamp == nil) != (newCluster.DeletionTimestamp == nil)
}

func (clusterReplicasChangedPredicate) Generic(_ event.GenericEvent) bool { return false }

// sourceSecretPredicate filters Secret events to only those that live in
// the controller's system namespace. This avoids reconciling for every
// secret change cluster-wide.
func sourceSecretPredicate(systemNamespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == systemNamespace
	})
}
