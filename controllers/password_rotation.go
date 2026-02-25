package controllers

import (
	"context"
	"fmt"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/dbop"
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
//   - Safety of destructive MySQL operations (ALTER USER RETAIN / DISCARD) is
//     enforced by the controller via status guards, not by the CLI.
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

// handlePasswordRotate drives the rotate phase (Phase 1) of password rotation.
//
// Phase transitions:
//
//	Idle ──(annotation)──▶ Rotating ──(RETAIN on all instances)──▶ Rotating(rotateApplied) ──(distribute)──▶ Rotated
//
// All ALTER USER statements use sql_log_bin=0 to prevent binlog propagation to
// cross-cluster replicas. Because binlog is disabled, RETAIN must be executed on
// every instance individually (primary + all replicas).
//
// Re-reconcile behaviour at each state:
//
//	Phase=Idle:
//	  The rotationID is taken from the password-rotate annotation (set by
//	  kubectl-moco or the user). Pre-checks (replicas>0, no stale dual
//	  passwords) run before Phase is persisted. On success, Phase is set to
//	  Rotating via Status.Patch. On re-reconcile the same rotationID is
//	  reused from status, not regenerated.
//
//	Phase=Rotating, RotateApplied=false:
//	  Instances are processed sequentially. For each instance, each user is
//	  checked via HasDualPassword (querying mysql.user for additional_password).
//	  If a user already has a dual password, RETAIN is skipped; otherwise it is
//	  executed. This makes MySQL the source of truth and eliminates per-user
//	  Status.Patch calls.
//
//	Phase=Rotating, RotateApplied=true:
//	  All instances completed. Distribution (Step 3) is idempotent.
//
//	Phase=Rotated:
//	  Stale annotation is silently removed.
func (r *MySQLClusterReconciler) handlePasswordRotate(ctx context.Context, cluster *mocov1beta2.MySQLCluster, rotation *mocov1beta2.SystemUserRotationStatus, rotateID string) error {
	log := crlog.FromContext(ctx)

	// === Phase: Idle → Rotating ===
	// Use the rotationID from the annotation and persist the Rotating phase
	// immediately. This must happen before any MySQL operation so that a crash
	// here does not leave us in Idle with ALTER USER already executed.
	if rotation.Phase == mocov1beta2.RotationPhaseIdle {
		// Refuse to start rotation when no replicas are running. ALTER USER
		// cannot be executed without instances. Checking here (before
		// Status.Patch) keeps the Phase at Idle and avoids creating *_PENDING
		// keys that would need manual cleanup.
		replicas := int(cluster.Spec.Replicas)
		if replicas == 0 {
			log.Info("refusing to start rotation: cluster is scaled down (replicas=0), scale up first")
			r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "RotateRefused",
				"Cannot rotate passwords while cluster is scaled down (replicas=0). "+
					"Scale the cluster up first so that ALTER USER can be executed on running instances.")
			return fmt.Errorf("cannot rotate passwords: cluster is scaled down (replicas=0)")
		}

		// Pre-check: verify no instance has stale dual passwords.
		// In Phase=Idle, no MOCO system user should have a dual password.
		// If one exists (e.g. from a manual ALTER USER RETAIN or incomplete
		// manual recovery), starting rotation would cause HasDualPassword=true
		// → RETAIN skipped → password inconsistency across instances.
		// This check runs before Status.Patch so the Phase stays Idle on failure.
		if replicas > 0 {
			sourceSecret := &corev1.Secret{}
			if err := r.Get(ctx, client.ObjectKey{Namespace: r.SystemNamespace, Name: cluster.ControllerSecretName()}, sourceSecret); err != nil {
				return err
			}
			currentPasswd, err := password.NewMySQLPasswordFromSecret(sourceSecret)
			if err != nil {
				return err
			}
			for idx := 0; idx < replicas; idx++ {
				op, err := r.OpFactory.New(ctx, cluster, currentPasswd, idx)
				if err != nil {
					return err
				}
				dualFound, dualUser, checkErr := r.checkInstanceDualPasswords(ctx, op, idx)
				op.Close()
				if checkErr != nil {
					return checkErr
				}
				if dualFound {
					log.Info("refusing to rotate: instance has pre-existing dual password",
						"instance", idx, "user", dualUser)
					r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "DualPasswordExists",
						"Cannot start rotation: instance %d user %s already has a dual password in Phase=Idle. "+
							"This indicates a previous rotation was not fully cleaned up. "+
							"Follow the recovery procedure in docs/designdoc/password_rotation.md "+
							"to rollback dual password state before retrying.",
						idx, dualUser)
					return fmt.Errorf("cannot start rotation: instance %d user %s has pre-existing dual password", idx, dualUser)
				}
			}
		}

		base := cluster.DeepCopy()
		rotation.RotationID = rotateID
		rotation.Phase = mocov1beta2.RotationPhaseRotating
		rotation.RotateApplied = false
		rotation.DiscardApplied = false
		if err := r.Status().Patch(ctx, cluster, client.MergeFrom(base)); err != nil {
			return fmt.Errorf("failed to persist Rotating status: %w", err)
		}
		log.Info("started password rotation", "rotationID", rotation.RotationID)
		r.Recorder.Eventf(cluster, corev1.EventTypeNormal, "RotationStarted",
			"Started password rotation (rotationID: %s)", rotation.RotationID)
	}

	// === Phase: Rotated (already done) ===
	// Phase 1 has already completed. The annotation is stale;
	// remove it silently without emitting an Event to avoid noise on every reconcile
	// if the annotation is not cleaned up promptly.
	if rotation.Phase == mocov1beta2.RotationPhaseRotated {
		log.Info("removing stale password-rotate annotation in Rotated phase", "rotationID", rotation.RotationID)
		r.removeAnnotation(ctx, cluster, constants.AnnPasswordRotate)
		return nil
	}

	// === Phase: Rotating ===

	// Step 1: Ensure pending passwords exist in the source Secret.
	// SetPendingPasswords is idempotent: if pending passwords already exist for the
	// current rotationID, they are reused without regeneration.
	sourceSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.SystemNamespace, Name: cluster.ControllerSecretName()}, sourceSecret); err != nil {
		return err
	}

	hasPending, err := password.HasPendingPasswords(sourceSecret, rotation.RotationID)
	if err != nil {
		r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "StaleRotationPending",
			"Failed to verify pending passwords in source secret %s: %v. "+
				"To recover, manually delete *_PENDING and ROTATION_ID keys from the secret: "+
				"kubectl -n %s edit secret %s",
			cluster.ControllerSecretName(), err, r.SystemNamespace, cluster.ControllerSecretName())
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

	// Step 2: Execute ALTER USER ... RETAIN CURRENT PASSWORD on ALL instances.
	// Guarded by RotateApplied: if true, this step is skipped entirely.
	//
	// Each ALTER USER is executed with sql_log_bin=0 to prevent binlog
	// propagation to cross-cluster replicas. Because binlog is disabled,
	// within-cluster replicas do not receive the change via replication,
	// so we must execute on every instance individually.
	//
	// For each instance, each user is checked via HasDualPassword. If a user
	// already has a dual password (from a previous partial run), RETAIN is
	// skipped. This makes MySQL the source of truth for crash recovery.
	// Phase advances only after ALL instances complete.
	//
	// replicas=0 (scaled-down mid-rotation): the Idle→Rotating transition
	// already refuses replicas=0, but the cluster may be scaled down after
	// the transition. Refuse here as well to prevent distributing passwords
	// that MySQL has not yet accepted.
	if !rotation.RotateApplied {
		replicas := int(cluster.Spec.Replicas)
		if replicas == 0 {
			log.Info("refusing to rotate: cluster is scaled down (replicas=0), scale up first",
				"rotationID", rotation.RotationID)
			r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "RotateRefused",
				"Cannot rotate passwords while cluster is scaled down (replicas=0). "+
					"Scale the cluster up first so that ALTER USER can be executed on running instances.")
			return fmt.Errorf("cannot rotate passwords: cluster is scaled down (replicas=0)")
		}

		pendingMap, err := password.PendingKeyMap(sourceSecret)
		if err != nil {
			return err
		}
		currentPasswd, err := password.NewMySQLPasswordFromSecret(sourceSecret)
		if err != nil {
			return err
		}

		primaryIndex := cluster.Status.CurrentPrimaryIndex

		for idx := 0; idx < replicas; idx++ {
			isReplica := idx != primaryIndex
			op, err := r.OpFactory.New(ctx, cluster, currentPasswd, idx)
			if err != nil {
				return err
			}

			if err := r.rotateInstanceUsers(ctx, op, pendingMap, idx, isReplica); err != nil {
				op.Close()
				return err
			}
			op.Close()
			log.Info("completed ALTER USER RETAIN for instance", "instance", idx, "rotationID", rotation.RotationID)
		}

		// All instances rotated. Set RotateApplied=true.
		base := cluster.DeepCopy()
		rotation.RotateApplied = true
		if err := r.Status().Patch(ctx, cluster, client.MergeFrom(base)); err != nil {
			return fmt.Errorf("failed to persist rotateApplied: %w", err)
		}
		log.Info("applied ALTER USER RETAIN for all instances", "rotationID", rotation.RotationID)
		r.Recorder.Eventf(cluster, corev1.EventTypeNormal, "RetainApplied",
			"Applied ALTER USER RETAIN for all %d instances (rotationID: %s)", replicas, rotation.RotationID)
	}

	// Step 3: Distribute the pending (new) passwords to per-namespace Secrets.
	// This is idempotent: the apply-based reconciliation only writes if the content
	// has actually changed.
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
		"Phase 1 complete: pending passwords distributed to user secrets (rotationID: %s)", rotation.RotationID)

	// Step 4: Remove annotation (best-effort).
	r.removeAnnotation(ctx, cluster, constants.AnnPasswordRotate)
	return nil
}

// handlePasswordDiscard drives the discard phase (Phase 2) of password rotation.
//
// Phase transitions:
//
//	Rotated ──(DISCARD OLD PASSWORD)──▶ Rotated(discardApplied) ──(confirm Secret)──▶ Idle
//
// Preconditions:
//   - Phase must be Rotated with RotateApplied=true. If not met, the annotation
//     is consumed (removed) and a Warning Event tells the user what to do.
//   - Pending passwords must exist in the source Secret with matching rotationID.
//     This is validated once at the top; subsequent steps proceed without re-checking
//     to avoid a mid-flow inconsistency ("DISCARD succeeded but Secret not confirmed").
//   - Replicas must be > 0. Discard is rejected when the cluster is scaled down because
//     we cannot verify that the new passwords work without running Pods.
//
// Re-reconcile behaviour at each state:
//
//	DiscardApplied=false:
//	  DISCARD OLD PASSWORD is executed. The connection uses the pending (new) password,
//	  which serves as an implicit verification that distribution succeeded. DISCARD is
//	  idempotent in MySQL, so re-execution on crash is safe.
//
//	DiscardApplied=true:
//	  DISCARD is skipped. ConfirmPendingPasswords (pending→current, delete pending keys)
//	  is called. This is idempotent: if a crash occurred after Secret.Update but before
//	  Status.Patch, the pending keys are already gone and Confirm is a no-op.
//
//	After reset to Idle:
//	  reconcileV1Secret resumes normal distribution using the (now-confirmed) current
//	  passwords. The source Secret's current passwords are already the new ones.
func (r *MySQLClusterReconciler) handlePasswordDiscard(ctx context.Context, cluster *mocov1beta2.MySQLCluster, rotation *mocov1beta2.SystemUserRotationStatus, discardID string) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	// Precondition: must be in Rotated phase with rotateApplied=true,
	// and the discard annotation's rotationID must match the active rotation.
	if !rotation.RotateApplied || rotation.Phase != mocov1beta2.RotationPhaseRotated {
		log.Info("discard skipped: rotation not in Rotated phase",
			"phase", rotation.Phase, "rotateApplied", rotation.RotateApplied)
		r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "DiscardSkipped",
			"password-discard annotation was removed because preconditions are not met: "+
				"rotation phase is %q (expected Rotated with rotateApplied=true). "+
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

	sourceSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.SystemNamespace, Name: cluster.ControllerSecretName()}, sourceSecret); err != nil {
		return ctrl.Result{}, err
	}

	// Validate pending passwords once at the top as a precondition.
	// After this check passes, all subsequent steps assume pending passwords are present
	// and proceed without re-checking. This avoids a dangerous mid-flow inconsistency
	// where "DISCARD succeeded but Secret was not confirmed" because a re-check failed.
	hasPending, err := password.HasPendingPasswords(sourceSecret, rotation.RotationID)
	if err != nil {
		r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "RotationPendingCheckFailed",
			"Cannot discard: failed to validate pending passwords in source secret %s for rotationID %s: %v. "+
				"To recover: (1) delete *_PENDING and ROTATION_ID keys from the secret: "+
				"kubectl -n %s edit secret %s  "+
				"(2) if needed, reset status.systemUserRotation as a last resort.",
			cluster.ControllerSecretName(), rotation.RotationID, err,
			r.SystemNamespace, cluster.ControllerSecretName())
		return ctrl.Result{}, fmt.Errorf("cannot discard: failed to validate pending passwords for rotationID %s: %w", rotation.RotationID, err)
	}
	if !hasPending {
		r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "MissingRotationPending",
			"Cannot discard: pending passwords not found in source secret %s for rotationID %s. "+
				"To recover: (1) delete *_PENDING and ROTATION_ID keys from the secret: "+
				"kubectl -n %s edit secret %s  "+
				"(2) if needed, reset status.systemUserRotation as a last resort.",
			cluster.ControllerSecretName(), rotation.RotationID,
			r.SystemNamespace, cluster.ControllerSecretName())
		return ctrl.Result{}, fmt.Errorf("cannot discard: pending passwords not found for rotationID %s", rotation.RotationID)
	}

	// Step 1: Execute DISCARD OLD PASSWORD using the pending (new) password.
	// We connect with the pending password to verify that distribution succeeded;
	// if the pending password does not work, the old password is not yet discardable.
	if !rotation.DiscardApplied {
		// Gate: Wait for StatefulSet rollout to complete before discarding.
		// The Pod template annotation change (from Phase 1) triggers a rolling
		// restart so that agents pick up the new passwords via EnvFrom. We must
		// not discard old passwords until all Pods are running the new template.
		sts := &appsv1.StatefulSet{}
		if err := r.Get(ctx, client.ObjectKey{
			Namespace: cluster.Namespace,
			Name:      cluster.PrefixedName(),
		}, sts); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get StatefulSet for rollout check: %w", err)
		}
		replicas := ptr.Deref(sts.Spec.Replicas, 1)

		// replicas=0 (scaled-down cluster): reject discard because we cannot
		// verify that the new passwords actually work without running Pods.
		// The operator should scale the cluster up first.
		if replicas == 0 {
			log.Info("refusing to discard: cluster is scaled down (replicas=0), scale up first",
				"rotationID", rotation.RotationID)
			r.Recorder.Eventf(cluster, corev1.EventTypeWarning, "DiscardRefused",
				"Cannot discard old passwords while cluster is scaled down (replicas=0). "+
					"Scale the cluster up first so that Pods can verify the new passwords.")
			return ctrl.Result{RequeueAfter: rotationRequeueInterval}, nil
		}

		// Verify the StatefulSet template annotation matches the active rotation.
		// This confirms that reconcileV1StatefulSet has applied the template change
		// that triggers the rolling restart. Without this check, the rollout
		// conditions below could pass vacuously if the template hasn't been
		// updated yet (no pending rollout → all conditions true).
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
		// distributeSecret writes {new passwords + ROTATION_ID} in a single SSA
		// apply, so matching ROTATION_ID proves the new passwords are present.
		// Combined with the rollout check below, this completes the guarantee:
		//   Secret has new passwords → template updated → all Pods restarted
		//   → all agents have new passwords via EnvFrom.
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

		pendingPasswd, err := password.NewMySQLPasswordFromPending(sourceSecret)
		if err != nil {
			return ctrl.Result{}, err
		}

		pendingMap, err := password.PendingKeyMap(sourceSecret)
		if err != nil {
			return ctrl.Result{}, err
		}

		// Determine the target authentication plugin from the primary instance.
		// authentication_policy is a global variable, so it is the same across
		// all instances in the cluster.
		primaryIndex := cluster.Status.CurrentPrimaryIndex
		authPlugin, err := func() (string, error) {
			op, err := r.OpFactory.New(ctx, cluster, pendingPasswd, primaryIndex)
			if err != nil {
				return "", err
			}
			defer op.Close()
			return op.GetAuthPlugin(ctx)
		}()
		if err != nil {
			return ctrl.Result{}, err
		}
		log.Info("determined target auth plugin for migration", "authPlugin", authPlugin, "rotationID", rotation.RotationID)

		// Execute DISCARD OLD PASSWORD + auth plugin migration on ALL instances
		// with sql_log_bin=0.
		// DISCARD is idempotent in MySQL (no-op when there is no secondary password).
		// MigrateUserAuthPlugin is also idempotent (re-hashes the same password).
		// Re-execution on crash is safe and per-user tracking is not needed.
		for idx := 0; idx < int(replicas); idx++ {
			isReplica := idx != primaryIndex
			op, err := r.OpFactory.New(ctx, cluster, pendingPasswd, idx)
			if err != nil {
				return ctrl.Result{}, err
			}

			if err := r.discardInstanceUsers(ctx, op, pendingMap, idx, isReplica, authPlugin); err != nil {
				op.Close()
				return ctrl.Result{}, err
			}
			op.Close()
			log.Info("applied DISCARD OLD PASSWORD and auth plugin migration for instance", "instance", idx, "rotationID", rotation.RotationID)
		}

		// Persist discardApplied=true immediately so that a crash here will not
		// re-execute DISCARD on the next reconcile.
		base := cluster.DeepCopy()
		rotation.DiscardApplied = true
		if err := r.Status().Patch(ctx, cluster, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to persist discardApplied: %w", err)
		}
		log.Info("applied DISCARD OLD PASSWORD for all instances", "rotationID", rotation.RotationID)
		r.Recorder.Eventf(cluster, corev1.EventTypeNormal, "DiscardApplied",
			"Applied DISCARD OLD PASSWORD and migrated auth plugin to %s for all %d instances (rotationID: %s)", authPlugin, replicas, rotation.RotationID)
	}

	// Step 2: Confirm the source secret (pending → current, delete pending keys).
	// Guarded by DiscardApplied to ensure DISCARD has completed before confirming.
	// ConfirmPendingPasswords is idempotent: if a crash occurred after Secret.Update
	// but before Status.Patch, the pending keys are already gone and Confirm is a no-op.
	if rotation.DiscardApplied {
		if err := password.ConfirmPendingPasswords(sourceSecret); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Update(ctx, sourceSecret); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("confirmed source secret", "rotationID", rotation.RotationID)
	}

	// Step 3: Reset status to Idle. Once Idle, reconcileV1Secret will resume normal
	// distribution using the (now-confirmed) current passwords.
	// Save the current RotationID as LastRotationID so that stale rotate annotations
	// (left over from failed best-effort removal) can be detected on the next reconcile.
	base := cluster.DeepCopy()
	rotation.LastRotationID = rotation.RotationID
	rotation.RotationID = ""
	rotation.Phase = mocov1beta2.RotationPhaseIdle
	rotation.RotateApplied = false
	rotation.DiscardApplied = false
	if err := r.Status().Patch(ctx, cluster, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to persist Idle status: %w", err)
	}

	// Step 4: Remove both annotations (best-effort).
	r.removeAnnotation(ctx, cluster, constants.AnnPasswordRotate, constants.AnnPasswordDiscard)

	log.Info("password rotation completed")
	r.Recorder.Eventf(cluster, corev1.EventTypeNormal, "RotationCompleted",
		"Password rotation completed successfully (rotationID: %s)", rotation.LastRotationID)
	return ctrl.Result{}, nil
}

// checkInstanceDualPasswords checks whether any MOCO user on the given instance
// already has a dual password. It returns (true, userName, nil) on the first user
// found with a dual password, or (false, "", nil) if none exist.
// The caller is responsible for closing op.
func (r *MySQLClusterReconciler) checkInstanceDualPasswords(
	ctx context.Context,
	op dbop.Operator,
	instanceIndex int,
) (bool, string, error) {
	for _, user := range constants.MocoUsers {
		hasDual, err := op.HasDualPassword(ctx, user)
		if err != nil {
			return false, "", fmt.Errorf("failed to check dual password for %s on instance %d: %w", user, instanceIndex, err)
		}
		if hasDual {
			return true, user, nil
		}
	}
	return false, "", nil
}

// rotateInstanceUsers executes ALTER USER RETAIN for users on a single instance
// that do not yet have a dual password in MySQL.
// For replicas, super_read_only is temporarily disabled to allow ALTER USER.
func (r *MySQLClusterReconciler) rotateInstanceUsers(
	ctx context.Context,
	op dbop.Operator,
	pendingMap map[string]string,
	instanceIndex int,
	isReplica bool,
) error {
	log := crlog.FromContext(ctx)

	if isReplica {
		if err := op.SetSuperReadOnly(ctx, false); err != nil {
			return fmt.Errorf("failed to disable super_read_only on instance %d: %w", instanceIndex, err)
		}
		defer func() {
			if err := op.SetSuperReadOnly(ctx, true); err != nil {
				log.Error(err, "failed to re-enable super_read_only (clustering loop will recover)",
					"instance", instanceIndex)
			}
		}()
	}

	for _, user := range constants.MocoUsers {
		hasDual, err := op.HasDualPassword(ctx, user)
		if err != nil {
			return fmt.Errorf("failed to check dual password for %s on instance %d: %w", user, instanceIndex, err)
		}
		if hasDual {
			log.Info("skipping ALTER USER RETAIN (dual password already exists)", "user", user, "instance", instanceIndex)
			continue
		}
		newPwd, ok := pendingMap[user]
		if !ok {
			return fmt.Errorf("pending password not found for user %s", user)
		}
		if err := op.RotateUserPassword(ctx, user, newPwd); err != nil {
			return fmt.Errorf("failed to rotate password for %s on instance %d: %w", user, instanceIndex, err)
		}
		log.Info("applied ALTER USER RETAIN", "user", user, "instance", instanceIndex)
	}

	return nil
}

// discardInstanceUsers executes DISCARD OLD PASSWORD for all users on a single instance,
// then migrates each user to the target authentication plugin.
// For replicas, super_read_only is temporarily disabled to allow ALTER USER.
//
// The auth plugin migration is done after DISCARD because MySQL Error 3894 prevents
// changing the authentication plugin in an ALTER USER ... RETAIN CURRENT PASSWORD statement.
// After DISCARD, the user has only a single password, so IDENTIFIED WITH can be used safely.
// Both DISCARD and MigrateUserAuthPlugin are idempotent, so re-execution on crash is safe.
func (r *MySQLClusterReconciler) discardInstanceUsers(
	ctx context.Context,
	op dbop.Operator,
	pendingMap map[string]string,
	instanceIndex int,
	isReplica bool,
	authPlugin string,
) error {
	log := crlog.FromContext(ctx)

	if isReplica {
		if err := op.SetSuperReadOnly(ctx, false); err != nil {
			return fmt.Errorf("failed to disable super_read_only on instance %d for discard: %w", instanceIndex, err)
		}
		defer func() {
			if err := op.SetSuperReadOnly(ctx, true); err != nil {
				log.Error(err, "failed to re-enable super_read_only (clustering loop will recover)",
					"instance", instanceIndex)
			}
		}()
	}

	for _, user := range constants.MocoUsers {
		if err := op.DiscardOldPassword(ctx, user); err != nil {
			return fmt.Errorf("failed to discard old password for %s on instance %d: %w", user, instanceIndex, err)
		}
	}

	// Migrate each user to the target authentication plugin.
	// This re-hashes the password under the new plugin (e.g. caching_sha2_password)
	// without RETAIN, which is safe because DISCARD has already cleared the secondary.
	for _, user := range constants.MocoUsers {
		pwd, ok := pendingMap[user]
		if !ok {
			return fmt.Errorf("pending password not found for user %s during auth plugin migration", user)
		}
		if err := op.MigrateUserAuthPlugin(ctx, user, pwd, authPlugin); err != nil {
			return fmt.Errorf("failed to migrate auth plugin for %s on instance %d: %w", user, instanceIndex, err)
		}
		log.Info("migrated auth plugin", "user", user, "instance", instanceIndex, "authPlugin", authPlugin)
	}

	return nil
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
