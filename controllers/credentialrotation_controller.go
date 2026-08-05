package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	mocoevent "github.com/cybozu-go/moco/pkg/event"
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
		mocoevent.StaleCredentialRotation.Emit(cr, r.Recorder)
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
		// ClusterManager owns RETAIN. If clustering is stopped, the
		// ClusterManager loop for this cluster is paused and RETAIN will
		// not run; surface that instead of waiting silently.
		if isClusteringStopped(cluster) {
			log.Info("rotation is paused: clustering is stopped", "rotationID", cr.Status.RotationID)
			mocoevent.RotationPaused.Emit(cr, r.Recorder,
				fmt.Sprintf("clustering is stopped (annotation %s=true), so RETAIN cannot run", constants.AnnClusteringStopped))
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil

	case mocov1beta2.StepPromoting:
		return r.handlePromoting(ctx, cr, cluster)

	case mocov1beta2.StepAwaitingRollout:
		// Reconciler waits for MySQLClusterReconciler to distribute the
		// promoted current passwords, triggers the rolling restart, and
		// waits for the StatefulSet rollout to settle, then flips
		// DiscardReady=True so the verification window opens.
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
		return r.handleStalePending(ctx, cr, cluster)

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
		if mocov1beta2.IsConditionFalseWithReason(cr, mocov1beta2.ConditionRotationReady, mocov1beta2.ReasonRefused) {
			return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
		}
		mocoevent.RotationRefused.Emit(cr, r.Recorder)
		initCycleConditionsIfAbsent(cr)
		cr.SetRotationReady(metav1.ConditionFalse, mocov1beta2.ReasonRefused,
			"MySQLCluster replicas is 0; nothing has been mutated.")
		if err := r.updateStatus(ctx, cr); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to RotationRefused: %w", err)
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	controllerSecret := &corev1.Secret{}
	secretName := cluster.ControllerSecretName()
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: r.SystemNamespace,
		Name:      secretName,
	}, controllerSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get controller secret: %w", err)
	}

	rotationID := password.GetRotationID(controllerSecret)
	if rotationID == "" {
		rotationID = uuid.New().String()
	}

	if _, err := password.SetPendingPasswords(controllerSecret, rotationID); err != nil {
		mocoevent.RotationPendingSetFailed.Emit(cr, r.Recorder, err)
		initCycleConditionsIfAbsent(cr)
		cr.SetRotationReady(metav1.ConditionFalse, mocov1beta2.ReasonStale,
			fmt.Sprintf("Failed to set pending passwords: %v", err))
		if statusErr := r.updateStatus(ctx, cr); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to Stale: %w", statusErr)
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	if err := r.Update(ctx, controllerSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update controller secret with pending passwords: %w", err)
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
	mocoevent.RotationStarted.Emit(cr, r.Recorder, rotationID, cr.Spec.RotationGeneration)

	return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
}

// handlePromoting promotes the pending passwords to current in the
// controller Secret and records the rotation phase as complete by
// promoting observedRotationGeneration.
//
// This is the point where the core invariant flips from "current = old"
// to "current = new": Step()==StepPromoting implies DualPassword=True,
// i.e. ALTER USER ... RETAIN succeeded on every instance, so both
// password sets authenticate everywhere and making the new set canonical
// is safe. Distribution to per-namespace Secrets is NOT done here — it is
// MySQLClusterReconciler's normal job (it always distributes current).
func (r *CredentialRotationReconciler) handlePromoting(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	controllerSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: r.SystemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, controllerSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get controller secret: %w", err)
	}

	state, err := password.RotationState(controllerSecret, cr.Status.RotationID)
	if err != nil {
		mocoevent.RotationPendingInconsistent.Emit(cr, r.Recorder, err)
		cr.SetRotationReady(metav1.ConditionFalse, mocov1beta2.ReasonStale,
			fmt.Sprintf("Rotation state inconsistency: %v", err))
		if statusErr := r.updateStatus(ctx, cr); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to Stale: %w", statusErr)
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	switch state {
	case password.RotationSecretPending:
		// One atomic Secret update: current → *_OLD, *_PENDING → current,
		// delete the pending keys and RETAIN_STARTED. ROTATION_ID stays
		// until the cycle completes.
		if err := password.PromotePendingPasswords(controllerSecret, cr.Status.RotationID); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to promote pending passwords: %w", err)
		}
		if err := r.Update(ctx, controllerSecret); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update controller secret after promote: %w", err)
		}
	case password.RotationSecretPromoted:
		// Crash recovery: the Secret update succeeded but the status
		// update below did not. The *_OLD key group with a matching
		// ROTATION_ID is the "promotion done" marker; just re-run the
		// status update.
		log.Info("controller Secret already promoted (crash recovery)",
			"rotationID", cr.Status.RotationID)
	case password.RotationSecretClean:
		// The pending keys disappeared without promotion (manual edit,
		// restore from backup, ...). The staged passwords are gone, but
		// the canonical current (old) passwords still authenticate
		// everywhere — nothing is broken; the cycle just cannot proceed.
		mocoevent.InconsistentRotationState.Emit(cr, r.Recorder, cr.Status.RotationID)
		cr.SetRotationReady(metav1.ConditionFalse, mocov1beta2.ReasonStale,
			fmt.Sprintf("Pending passwords lost without promotion for rotationID %s", cr.Status.RotationID))
		if statusErr := r.updateStatus(ctx, cr); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to Stale: %w", statusErr)
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	// Enter AwaitingRollout. observedRotationGeneration is promoted here,
	// but DiscardReady stays False/Pending — handleAwaitingRollout flips
	// it to True once distribution catches up and the rolling restart
	// settles.
	cr.Status.ObservedRotationGeneration = cr.Spec.RotationGeneration
	if err := r.updateStatus(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status after promotion: %w", err)
	}

	log.Info("promoted pending passwords to current in the controller Secret", "rotationID", cr.Status.RotationID)
	mocoevent.PasswordsPromoted.Emit(cr, r.Recorder, cr.Status.RotationID)

	return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
}

// handleAwaitingRollout gates the verification window on two things, in
// order:
//
//  1. Distribution catch-up: the per-namespace user Secret and my.cnf
//     Secret must be derived from the controller Secret's current
//     (promoted) passwords. Distribution itself is MySQLClusterReconciler's
//     normal job; this handler only observes the result. The rolling
//     restart annotation must not be applied before this point — a Pod
//     that restarts early still works (the old password authenticates
//     during the dual window), but the rollout could settle with Pods on
//     the old password, and a subsequent DISCARD would remove the very
//     password those Pods use.
//  2. Rollout completion: while the rollout is in flight, some Pods may
//     still be running with the old password loaded via EnvFrom; flipping
//     DiscardReady=True before every Pod has picked up the new password
//     would let an operator-initiated DISCARD strip the secondary password
//     out from under them.
//
// Once both have settled, all Pods are using the new password and the
// verification window is genuinely open.
func (r *CredentialRotationReconciler) handleAwaitingRollout(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	caughtUp, err := r.distributionCaughtUp(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !caughtUp {
		// Distribution is MySQLClusterReconciler's job; if reconciliation
		// is stopped, the promoted passwords will never propagate and the
		// rotation stays parked here. Surface that instead of waiting
		// silently. The wait itself is safe: the current passwords keep
		// authenticating everywhere.
		if isReconciliationStopped(cluster) {
			log.Info("rotation is paused: reconciliation is stopped", "rotationID", cr.Status.RotationID)
			mocoevent.RotationPaused.Emit(cr, r.Recorder,
				fmt.Sprintf("reconciliation is stopped (annotation %s=true), so the promoted passwords are not distributed", constants.AnnReconciliationStopped))
		}
		log.Info("waiting for per-namespace Secrets to catch up with the promoted passwords",
			"rotationID", cr.Status.RotationID)
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	stsName := cluster.PrefixedName()
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      stsName,
	}, sts); err != nil {
		if apierrors.IsNotFound(err) {
			// The StatefulSet has not been created yet (or was deleted);
			// requeue and try again.
			return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get StatefulSet for rollout check: %w", err)
	}

	// Add the restart annotation to the StatefulSet Pod template via
	// Server-Side Apply under a dedicated field manager so the rotation
	// annotation key is not silently removed by the next MySQLCluster
	// reconcile.
	//
	// Apply ONLY when the pod template does not already carry this
	// rotation's annotation. A content-no-op re-apply is NOT harmless
	// here: every StatefulSet update that is not a pure partition change
	// passes the StatefulSetDefaulter mutating webhook, which resets
	// spec.updateStrategy.rollingUpdate.partition to replicas to guard
	// rollouts. Re-applying on each reconcile would keep resetting the
	// partition that StatefulSetPartitionReconciler walks down, and the
	// rollout would never complete. (MySQLClusterReconciler avoids the
	// same trap with its ExtractStatefulSet + DeepEqual gate.)
	//
	// The Get above is served from the informer cache, but reconciles are
	// triggered after the cache has stored the update, so at most one
	// extra apply can occur — and it also checks the rollout only against
	// an annotated template, never against a stale pre-annotation object.
	if sts.Spec.Template.Annotations[constants.AnnPasswordRotationRestart] != cr.Status.RotationID {
		stsAC := appsv1ac.StatefulSet(stsName, cluster.Namespace).
			WithSpec(appsv1ac.StatefulSetSpec().
				WithTemplate(corev1ac.PodTemplateSpec().
					WithAnnotations(map[string]string{
						constants.AnnPasswordRotationRestart: cr.Status.RotationID,
					})))
		if err := r.Apply(ctx, stsAC, client.FieldOwner(credRotationFieldManager), client.ForceOwnership); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to apply rotation annotation to StatefulSet: %w", err)
		}
		log.Info("applied the rotation restart annotation to the StatefulSet",
			"rotationID", cr.Status.RotationID)
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	if !isStatefulSetRolloutComplete(sts) {
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	cr.SetDiscardReady(metav1.ConditionTrue, mocov1beta2.ReasonReconciled,
		"Rotation phase finished and post-promotion rollout settled; awaiting-discard steady state — discard is now allowed.")
	if err := r.updateStatus(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status after rollout: %w", err)
	}

	log.Info("post-promotion rollout settled; awaiting-discard window open",
		"rotationID", cr.Status.RotationID)
	mocoevent.AwaitingDiscard.Emit(cr, r.Recorder, cr.Status.RotationID)
	return ctrl.Result{}, nil
}

// distributionCaughtUp reports whether the per-namespace user Secret and
// my.cnf Secret are derived from the controller Secret's current
// passwords. It never writes — MySQLClusterReconciler is the only writer
// of the per-namespace Secrets, and its watch on the controller Secret
// triggers redistribution promptly after promotion.
func (r *CredentialRotationReconciler) distributionCaughtUp(ctx context.Context, cluster *mocov1beta2.MySQLCluster) (bool, error) {
	controllerSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: r.SystemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, controllerSecret); err != nil {
		return false, fmt.Errorf("failed to get controller secret: %w", err)
	}

	userSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      cluster.UserSecretName(),
	}, userSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get user secret: %w", err)
	}
	if !password.CurrentPasswordsMatch(controllerSecret, userSecret) {
		return false, nil
	}

	passwd, err := password.NewMySQLPasswordFromSecret(controllerSecret)
	if err != nil {
		return false, fmt.Errorf("failed to read current passwords: %w", err)
	}
	expectedMyCnf := passwd.ToMyCnfSecret()
	mycnfSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      cluster.MyCnfSecretName(),
	}, mycnfSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get my.cnf secret: %w", err)
	}
	for key, expected := range expectedMyCnf.Data {
		actual, ok := mycnfSecret.Data[key]
		if !ok || string(actual) != string(expected) {
			return false, nil
		}
	}
	return true, nil
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
		mocoevent.DiscardRefused.Emit(cr, r.Recorder)
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
		mocoevent.DiscardStarted.Emit(cr, r.Recorder, cr.Status.RotationID)
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	// DiscardReady=False (reason=Pending) already; ClusterManager owns
	// from here. If clustering is stopped, its loop is paused and DISCARD
	// will not run; surface that instead of waiting silently.
	if isClusteringStopped(cluster) {
		log.Info("discard is paused: clustering is stopped", "rotationID", cr.Status.RotationID)
		mocoevent.RotationPaused.Emit(cr, r.Recorder,
			fmt.Sprintf("clustering is stopped (annotation %s=true), so DISCARD cannot run", constants.AnnClusteringStopped))
	}
	return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
}

// handleFinalize cleans up the rotation bookkeeping keys (*_OLD,
// ROTATION_ID, RETAIN_STARTED) from the controller Secret, marks the
// discard phase complete, and returns the CR to the Idle steady state.
// No password values move — the canonical current passwords were promoted
// before distribution (handlePromoting). Every action here is idempotent,
// so a crash at any point simply re-runs.
func (r *CredentialRotationReconciler) handleFinalize(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	controllerSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: r.SystemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, controllerSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get controller secret for cleanup: %w", err)
	}

	if err := password.CleanupRotationKeys(controllerSecret, cr.Status.RotationID); err != nil {
		// An inconsistent secret at Finalizing (unpromoted pending keys,
		// partial *_OLD group, or a ROTATION_ID from a different cycle)
		// means manual tampering — refuse to hide that by deleting the
		// bookkeeping around it.
		mocoevent.RotationPendingInconsistent.Emit(cr, r.Recorder, err)
		cr.SetDiscardReady(metav1.ConditionFalse, mocov1beta2.ReasonStale,
			fmt.Sprintf("Rotation state inconsistency during cleanup: %v", err))
		if statusErr := r.updateStatus(ctx, cr); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to Stale: %w", statusErr)
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}
	if err := r.Update(ctx, controllerSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update controller secret after cleanup: %w", err)
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
	mocoevent.RotationCompleted.Emit(cr, r.Recorder, cr.Status.RotationID, cr.Spec.RotationGeneration, cr.Spec.DiscardGeneration)

	return ctrl.Result{}, nil
}

// handleStalePending waits for manual recovery of an inconsistent
// controller Secret. While the rotation bookkeeping keys are present, it
// only logs — the transition into Stale already emitted a Warning Event
// with the diagnostic detail in the condition Message. Once the operator
// has removed all bookkeeping keys (the Secret is Clean again), the
// wedged cycle is aborted: the observed generations catch up to spec so
// the cycle does not restart by itself, and the conditions return to the
// idle steady state. The operator then requests a fresh rotation with a
// new generation bump. Leftover dual passwords on MySQL instances (if the
// operator skipped the reset step) are caught by the DualPasswordExists
// pre-check of that next cycle.
func (r *CredentialRotationReconciler) handleStalePending(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	controllerSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: r.SystemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, controllerSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get controller secret: %w", err)
	}
	if state, err := password.RotationState(controllerSecret, ""); err != nil || state != password.RotationSecretClean {
		log.Info("CR is stuck in Stale state; manual recovery required",
			"rotationID", cr.Status.RotationID)
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	abortedRotationID := cr.Status.RotationID
	cr.Status.RotationID = ""
	cr.Status.ObservedRotationGeneration = cr.Spec.RotationGeneration
	cr.Status.ObservedDiscardGeneration = cr.Spec.DiscardGeneration
	cr.SetRotationReady(metav1.ConditionTrue, mocov1beta2.ReasonReconciled,
		"Recovered after manual cleanup of the controller Secret; the wedged cycle was aborted. Idle steady state — rotate is now allowed.")
	cr.SetDiscardReady(metav1.ConditionFalse, mocov1beta2.ReasonPending,
		"Idle steady state; no dual-password set to discard.")
	cr.SetDualPassword(metav1.ConditionFalse, mocov1beta2.ReasonNotRetained,
		"No RETAIN has been issued in the current cycle yet.")
	if err := r.updateStatus(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status after Stale recovery: %w", err)
	}

	log.Info("recovered from Stale state after manual cleanup of the controller secret",
		"abortedRotationID", abortedRotationID)
	mocoevent.RotationRecovered.Emit(cr, r.Recorder, abortedRotationID)

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
		// Wake the reconciler when the controller Secret in the system
		// namespace changes — this lets the operator clean up a Stale
		// controller Secret (or restore one) without waiting for the
		// periodic requeue.
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.mapControllerSecretToCR),
			builder.WithPredicates(controllerSecretPredicate(r.SystemNamespace)),
		).
		// Wake the reconciler when a per-namespace user Secret changes —
		// handleAwaitingRollout waits for MySQLClusterReconciler to
		// distribute the promoted passwords, and this watch lets it
		// observe the catch-up without waiting for the periodic requeue.
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.mapUserSecretToCR),
		).
		// Wake the reconciler when the cluster's StatefulSet changes —
		// handleAwaitingRollout waits for the rolling restart to settle,
		// and this watch lets it flip DiscardReady=True as soon as the
		// rollout completes.
		Watches(
			&appsv1.StatefulSet{},
			handler.EnqueueRequestsFromMapFunc(mapStatefulSetToCR),
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

// mapControllerSecretToCR reverse-parses a controller-namespace Secret name
// ("mysql-<ns>.<name>") back to the CredentialRotation that may be
// interested in it. Splitting at the first "." is unambiguous: namespace
// names are RFC 1123 DNS labels and can never contain a dot, while any
// dots in the cluster name are preserved on the right-hand side. The
// reverse parse is best-effort: if the name does not match the expected
// pattern, no request is enqueued.
func (r *CredentialRotationReconciler) mapControllerSecretToCR(_ context.Context, obj client.Object) []reconcile.Request {
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

// mapUserSecretToCR maps a per-namespace user Secret ("moco-<cluster>")
// or my.cnf Secret ("moco-my-cnf-<cluster>") to the CredentialRotation of
// the same cluster, so handleAwaitingRollout observes distribution
// catch-up of both Secrets without waiting for the periodic requeue.
// System-namespace Secrets are handled by mapControllerSecretToCR
// instead. Best-effort: a non-matching name enqueues nothing; a false
// positive (another Secret that happens to carry the prefix) only causes
// a cheap no-op reconcile.
func (r *CredentialRotationReconciler) mapUserSecretToCR(_ context.Context, obj client.Object) []reconcile.Request {
	if obj.GetNamespace() == r.SystemNamespace {
		return nil
	}
	name := obj.GetName()
	const prefix = "moco-"
	if !strings.HasPrefix(name, prefix) {
		return nil
	}
	// "moco-my-cnf-<x>" is ambiguous: it is the my.cnf Secret of cluster
	// "<x>" but also the user Secret of a cluster literally named
	// "my-cnf-<x>". Enqueue both interpretations rather than guessing —
	// the extra request is a cheap no-op when no such CR exists.
	reqs := []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: obj.GetNamespace(),
			Name:      name[len(prefix):],
		},
	}}
	const mycnfPrefix = "moco-my-cnf-"
	if strings.HasPrefix(name, mycnfPrefix) {
		reqs = append(reqs, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: obj.GetNamespace(),
				Name:      name[len(mycnfPrefix):],
			},
		})
	}
	return reqs
}

// mapStatefulSetToCR maps a cluster StatefulSet ("moco-<cluster>") to the
// CredentialRotation of the same cluster.
func mapStatefulSetToCR(_ context.Context, obj client.Object) []reconcile.Request {
	const prefix = "moco-"
	name := obj.GetName()
	if !strings.HasPrefix(name, prefix) {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: obj.GetNamespace(),
			Name:      name[len(prefix):],
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

// controllerSecretPredicate filters Secret events to only those that live in
// the controller's system namespace. This avoids reconciling for every
// secret change cluster-wide.
func controllerSecretPredicate(systemNamespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == systemNamespace
	})
}
