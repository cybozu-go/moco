package controllers

import (
	"context"
	"fmt"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/password"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

// rotationRequeueInterval is the interval for requeuing during password rotation
// when waiting for external state to converge (rollout completion, template update, etc.).
const rotationRequeueInterval = 15 * time.Second

// reconcileV1PasswordRotation is the entry point for password rotation.
//
// Design principles:
//   - Annotations are one-shot triggers, not state. The controller consumes
//     (removes) them after processing. Actual state lives in status.systemUserRotation.
//   - DB operations (ALTER USER RETAIN / DISCARD) are handled by the clusterManager.
//     The controller only manages K8s resources (Phase transitions, Secret distribution,
//     annotation handling).
//   - Inconsistent Secret state (partial pending keys) is always treated as an
//     error. The controller never attempts automatic repair; manual cleanup is
//     required and documented in Event messages.
func (r *MySQLClusterReconciler) reconcileV1PasswordRotation(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)
	ann := cluster.Annotations
	rotateID := ann[constants.AnnPasswordRotate]
	discardID := ann[constants.AnnPasswordDiscard]
	wantRotate := rotateID != ""
	wantDiscard := discardID != ""

	if !wantRotate && !wantDiscard {
		return ctrl.Result{}, nil
	}

	rotation := &cluster.Status.SystemUserRotation

	// Stale annotation guards.
	// After a completed rotation cycle, best-effort annotation removal may fail,
	// leaving annotations from the previous cycle. Compare the annotation values
	// with LastRotationID to distinguish stale from fresh triggers.
	// If they match, the annotation is stale; remove it without starting new work.
	if rotation.Phase == mocov1beta2.RotationPhaseIdle {
		if wantRotate && rotateID == rotation.LastRotationID {
			log.Info("removing stale password-rotate annotation", "rotationID", rotateID)
			r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "StaleAnnotationRemoved",
				"Removed stale password-rotate annotation (rotationID %s matches completed LastRotationID); no new rotation started.", rotateID)
			r.removeAnnotation(ctx, cluster, constants.AnnPasswordRotate)
			wantRotate = false
		}
		if wantDiscard && discardID == rotation.LastRotationID {
			log.Info("removing stale password-discard annotation", "discardID", discardID)
			r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "StaleAnnotationRemoved",
				"Removed stale password-discard annotation (rotationID %s matches completed LastRotationID); no new discard started.", discardID)
			r.removeAnnotation(ctx, cluster, constants.AnnPasswordDiscard)
			wantDiscard = false
		}
		if !wantRotate && !wantDiscard {
			return ctrl.Result{}, nil
		}
	}

	// --- Rotate Phase ---
	// If the rotate annotation is present, handle rotate and return immediately.
	// Discard is evaluated on the next reconcile. This prevents the entire
	// rotate→discard cycle from completing in a single reconcile, preserving
	// the two-phase design that gives operators a verification window.
	if wantRotate {
		if err := r.handlePasswordRotate(ctx, cluster, rotation, rotateID); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// --- Discard Phase ---
	if wantDiscard {
		return r.handlePasswordDiscard(ctx, cluster, rotation, discardID)
	}

	return ctrl.Result{}, nil
}

// handlePasswordRotate drives the rotate operation of password rotation.
//
// Phase transitions (controller responsibilities only):
//
//	Idle ──(annotation)──▶ Rotating ──(clusterMgr RETAIN)──▶ Retained ──(distribute)──▶ Rotated
//
// The controller handles Idle→Rotating and Retained→Rotated transitions.
// The clusterManager handles Rotating→Retained (ALTER USER RETAIN on all instances).
//
// Re-reconcile behaviour at each state:
//
//	Phase=Idle:
//	  The rotationID is taken from the password-rotate annotation.
//	  Pending passwords are generated and Phase is set to Rotating.
//
//	Phase=Rotating:
//	  Ensure pending passwords exist. Then wait for clusterManager to transition to Retained.
//
//	Phase=Retained:
//	  Distribute pending passwords to per-namespace Secrets, then set Phase to Rotated.
//
//	Phase=Rotated:
//	  Stale annotation is silently removed.
func (r *MySQLClusterReconciler) handlePasswordRotate(ctx context.Context, cluster *mocov1beta2.MySQLCluster, rotation *mocov1beta2.SystemUserRotationStatus, rotateID string) error {
	log := crlog.FromContext(ctx)

	// === Phase: Idle → Rotating ===
	if rotation.Phase == mocov1beta2.RotationPhaseIdle {
		replicas := int(cluster.Spec.Replicas)
		if replicas == 0 {
			log.Info("refusing to start rotation: cluster is scaled down (replicas=0), scale up first")
			r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "RotateRefused",
				"Cannot rotate passwords while cluster is scaled down (replicas=0). "+
					"Scale the cluster up first so that ALTER USER can be executed on running instances.")
			return fmt.Errorf("cannot rotate passwords: cluster is scaled down (replicas=0)")
		}

		base := cluster.DeepCopy()
		rotation.RotationID = rotateID
		rotation.Phase = mocov1beta2.RotationPhaseRotating
		if err := r.Status().Patch(ctx, cluster, client.MergeFrom(base)); err != nil {
			return fmt.Errorf("failed to persist Rotating status: %w", err)
		}
		log.Info("started password rotation", "rotationID", rotation.RotationID)
		r.Recorder.Eventf(cluster, corev1.EventTypeNormal, "RotationStarted",
			"Started password rotation (rotationID: %s)", rotation.RotationID)
	}

	// === Phase: Rotated (already done) ===
	if rotation.Phase == mocov1beta2.RotationPhaseRotated {
		log.Info("removing stale password-rotate annotation in Rotated phase", "rotationID", rotation.RotationID)
		r.removeAnnotation(ctx, cluster, constants.AnnPasswordRotate)
		return nil
	}

	// === Phase: Rotating ===
	// Ensure pending passwords exist in the source Secret, then return.
	// The clusterManager will pick up Phase=Rotating, execute ALTER USER RETAIN,
	// and transition to Phase=Retained.
	if rotation.Phase == mocov1beta2.RotationPhaseRotating {
		sourceSecret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.SystemNamespace, Name: cluster.ControllerSecretName()}, sourceSecret); err != nil {
			return err
		}

		hasPending, err := password.HasPendingPasswords(sourceSecret, rotation.RotationID)
		if err != nil {
			r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "RotationPendingError",
				"Failed to verify pending passwords in source secret %s: %v. "+
					"If ALTER USER RETAIN was already applied on any instance, follow the full rollback procedure "+
					"in docs/designdoc/password_rotation.md (status → Secret → rollout → MySQL reset). "+
					"Do NOT recover by only deleting *_PENDING or ROTATION_ID keys from the Secret.",
				cluster.ControllerSecretName(), err)
			return fmt.Errorf("failed to verify pending passwords in source secret: %w", err)
		}
		if !hasPending {
			if _, err := password.SetPendingPasswords(sourceSecret, rotation.RotationID); err != nil {
				return err
			}
			if err := r.Update(ctx, sourceSecret); err != nil {
				return err
			}
			log.Info("generated pending passwords", "rotationID", rotation.RotationID)
		}

		// Return and wait for clusterManager to transition to Retained.
		return nil
	}

	// === Phase: Retained → Rotated ===
	// The clusterManager has completed ALTER USER RETAIN on all instances.
	// Distribute pending passwords to per-namespace Secrets.
	if rotation.Phase == mocov1beta2.RotationPhaseRetained {
		sourceSecret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.SystemNamespace, Name: cluster.ControllerSecretName()}, sourceSecret); err != nil {
			return err
		}

		pendingPasswd, err := password.NewMySQLPasswordFromPending(sourceSecret)
		if err != nil {
			return err
		}
		if err := r.distributeSecret(ctx, cluster, pendingPasswd, rotation.RotationID); err != nil {
			return err
		}

		// Persist Rotated phase immediately so that reconcileV1Secret no longer
		// overwrites the distributed Secrets with the old (current) passwords.
		base := cluster.DeepCopy()
		rotation.Phase = mocov1beta2.RotationPhaseRotated
		if err := r.Status().Patch(ctx, cluster, client.MergeFrom(base)); err != nil {
			return fmt.Errorf("failed to persist Rotated status: %w", err)
		}
		log.Info("distributed pending passwords", "rotationID", rotation.RotationID)
		r.Recorder.Eventf(cluster, corev1.EventTypeNormal, "PasswordRotated",
			"Rotate complete: pending passwords distributed to user secrets (rotationID: %s)", rotation.RotationID)

		// Remove annotation (best-effort).
		r.removeAnnotation(ctx, cluster, constants.AnnPasswordRotate)
	}

	return nil
}

// handlePasswordDiscard drives the discard operation of password rotation.
//
// Phase transitions (controller responsibilities only):
//
//	Rotated ──(rollout check)──▶ Discarding ──(clusterMgr DISCARD)──▶ Discarded ──(confirm)──▶ Idle
//
// The controller handles Rotated→Discarding and Discarded→Idle transitions.
// The clusterManager handles Discarding→Discarded (DISCARD OLD PASSWORD on all instances).
//
// Preconditions:
//   - Phase must be Rotated. If not met, the annotation is consumed (removed)
//     and a Warning Event tells the user what to do.
//   - Pending passwords must exist in the source Secret with matching rotationID.
//   - Replicas must be > 0 and rollout must be complete.
func (r *MySQLClusterReconciler) handlePasswordDiscard(ctx context.Context, cluster *mocov1beta2.MySQLCluster, rotation *mocov1beta2.SystemUserRotationStatus, discardID string) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	// Precondition: must be in Rotated phase (or a later discard-related phase).
	if rotation.Phase != mocov1beta2.RotationPhaseRotated &&
		rotation.Phase != mocov1beta2.RotationPhaseDiscarding &&
		rotation.Phase != mocov1beta2.RotationPhaseDiscarded {
		log.Info("discard skipped: rotation not in Rotated phase",
			"phase", rotation.Phase)
		r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "DiscardSkipped",
			"password-discard annotation was removed because preconditions are not met: "+
				"rotation phase is %q (expected Rotated). "+
				"Run password-rotate first, then password-discard.", rotation.Phase)
		r.removeAnnotation(ctx, cluster, constants.AnnPasswordDiscard)
		return ctrl.Result{}, nil
	}
	if discardID != rotation.RotationID {
		log.Info("discard skipped: rotationID mismatch",
			"discardID", discardID, "expected", rotation.RotationID)
		r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "DiscardSkipped",
			"password-discard annotation was removed because rotationID %q does not match "+
				"the active rotation %q.", discardID, rotation.RotationID)
		r.removeAnnotation(ctx, cluster, constants.AnnPasswordDiscard)
		return ctrl.Result{}, nil
	}

	// === Phase: Rotated → Discarding ===
	if rotation.Phase == mocov1beta2.RotationPhaseRotated {
		sourceSecret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.SystemNamespace, Name: cluster.ControllerSecretName()}, sourceSecret); err != nil {
			return ctrl.Result{}, err
		}

		hasPending, err := password.HasPendingPasswords(sourceSecret, rotation.RotationID)
		if err != nil {
			r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "RotationPendingCheckFailed",
				"Cannot discard: failed to validate pending passwords in source secret %s for rotationID %s: %v. "+
					"Follow the full rollback procedure in docs/designdoc/password_rotation.md "+
					"(status → Secret → rollout → MySQL reset). "+
					"Do NOT recover by only deleting *_PENDING or ROTATION_ID keys from the Secret.",
				cluster.ControllerSecretName(), rotation.RotationID, err)
			return ctrl.Result{}, fmt.Errorf("cannot discard: failed to validate pending passwords for rotationID %s: %w", rotation.RotationID, err)
		}
		if !hasPending {
			r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "MissingRotationPending",
				"Cannot discard: pending passwords not found in source secret %s for rotationID %s. "+
					"Follow the full rollback procedure in docs/designdoc/password_rotation.md "+
					"(status → Secret → rollout → MySQL reset).",
				cluster.ControllerSecretName(), rotation.RotationID)
			return ctrl.Result{}, fmt.Errorf("cannot discard: pending passwords not found for rotationID %s", rotation.RotationID)
		}

		// Gate: Wait for StatefulSet rollout to complete before discarding.
		sts := &appsv1.StatefulSet{}
		if err := r.Get(ctx, client.ObjectKey{
			Namespace: cluster.Namespace,
			Name:      cluster.PrefixedName(),
		}, sts); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get StatefulSet for rollout check: %w", err)
		}
		replicas := ptr.Deref(sts.Spec.Replicas, 1)

		if replicas == 0 {
			log.Info("refusing to discard: cluster is scaled down (replicas=0), scale up first",
				"rotationID", rotation.RotationID)
			r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "DiscardRefused",
				"Cannot discard old passwords while cluster is scaled down (replicas=0). "+
					"Scale the cluster up first so that Pods can verify the new passwords.")
			return ctrl.Result{RequeueAfter: rotationRequeueInterval}, nil
		}

		// Verify the StatefulSet template annotation matches the active rotation.
		var stsRotationID string
		if sts.Spec.Template.Annotations != nil {
			stsRotationID = sts.Spec.Template.Annotations[constants.AnnPasswordRotationRestart]
		}
		if stsRotationID != rotation.RotationID {
			log.Info("waiting for StatefulSet template to reflect rotation",
				"stsAnnotation", stsRotationID, "expected", rotation.RotationID)
			return ctrl.Result{RequeueAfter: rotationRequeueInterval}, nil
		}

		// Verify the user Secret contains the expected ROTATION_ID.
		userSecret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{
			Namespace: cluster.Namespace,
			Name:      cluster.UserSecretName(),
		}, userSecret); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get user Secret for rotation check: %w", err)
		}
		if string(userSecret.Data[password.RotationIDKey]) != rotation.RotationID {
			log.Info("waiting for user Secret to reflect rotation",
				"secretRotationID", string(userSecret.Data[password.RotationIDKey]),
				"expected", rotation.RotationID)
			return ctrl.Result{RequeueAfter: rotationRequeueInterval}, nil
		}

		rolloutDone := sts.Status.ObservedGeneration >= sts.Generation &&
			sts.Status.CurrentRevision == sts.Status.UpdateRevision &&
			sts.Status.UpdatedReplicas == replicas &&
			sts.Status.ReadyReplicas == replicas
		if !rolloutDone {
			log.Info("waiting for StatefulSet rollout to complete before discarding old passwords",
				"observedGeneration", sts.Status.ObservedGeneration,
				"generation", sts.Generation,
				"currentRevision", sts.Status.CurrentRevision,
				"updateRevision", sts.Status.UpdateRevision,
				"updatedReplicas", sts.Status.UpdatedReplicas,
				"readyReplicas", sts.Status.ReadyReplicas,
				"expectedReplicas", replicas,
				"rotationID", rotation.RotationID)
			return ctrl.Result{RequeueAfter: rotationRequeueInterval}, nil
		}

		// Transition Phase: Rotated → Discarding
		base := cluster.DeepCopy()
		rotation.Phase = mocov1beta2.RotationPhaseDiscarding
		if err := r.Status().Patch(ctx, cluster, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to persist Discarding status: %w", err)
		}
		log.Info("rollout complete, transitioning to Discarding phase", "rotationID", rotation.RotationID)
		// Return and wait for clusterManager to handle DISCARD.
		return ctrl.Result{}, nil
	}

	// === Phase: Discarding ===
	// Wait for clusterManager to execute DISCARD and transition to Discarded.
	if rotation.Phase == mocov1beta2.RotationPhaseDiscarding {
		return ctrl.Result{RequeueAfter: rotationRequeueInterval}, nil
	}

	// === Phase: Discarded → Idle ===
	// The clusterManager has completed DISCARD OLD PASSWORD on all instances.
	// Confirm the source secret and reset to Idle.
	if rotation.Phase == mocov1beta2.RotationPhaseDiscarded {
		sourceSecret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.SystemNamespace, Name: cluster.ControllerSecretName()}, sourceSecret); err != nil {
			return ctrl.Result{}, err
		}

		if err := password.ConfirmPendingPasswords(sourceSecret); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Update(ctx, sourceSecret); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("confirmed source secret", "rotationID", rotation.RotationID)

		// Reset status to Idle.
		base := cluster.DeepCopy()
		rotation.LastRotationID = rotation.RotationID
		rotation.RotationID = ""
		rotation.Phase = mocov1beta2.RotationPhaseIdle
		if err := r.Status().Patch(ctx, cluster, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to persist Idle status: %w", err)
		}

		// Remove both annotations (best-effort).
		r.removeAnnotation(ctx, cluster, constants.AnnPasswordRotate, constants.AnnPasswordDiscard)

		log.Info("password rotation completed")
		r.Recorder.Eventf(cluster, corev1.EventTypeNormal, "RotationCompleted",
			"Password rotation completed successfully (rotationID: %s)", rotation.LastRotationID)
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *MySQLClusterReconciler) distributeSecret(ctx context.Context, cluster *mocov1beta2.MySQLCluster, passwd *password.MySQLPassword, rotationID string) error {
	if err := r.reconcileUserSecretWith(ctx, cluster, passwd, rotationID); err != nil {
		return err
	}
	if err := r.reconcileMyCnfSecretWith(ctx, cluster, passwd); err != nil {
		return err
	}
	return nil
}

// removeAnnotation removes the given annotation keys from the cluster in a best-effort manner.
// Failure is logged but not returned, since the annotation will be removed on the next reconcile.
func (r *MySQLClusterReconciler) removeAnnotation(ctx context.Context, cluster *mocov1beta2.MySQLCluster, keys ...string) {
	log := crlog.FromContext(ctx)
	if cluster.Annotations == nil {
		return
	}
	needsPatch := false
	for _, key := range keys {
		if _, ok := cluster.Annotations[key]; ok {
			needsPatch = true
		}
	}
	if !needsPatch {
		return
	}
	base := cluster.DeepCopy()
	for _, key := range keys {
		delete(cluster.Annotations, key)
	}
	if err := r.Patch(ctx, cluster, client.MergeFrom(base)); err != nil {
		log.Error(err, "failed to remove annotation (will retry on next reconcile)", "keys", keys)
	}
}
