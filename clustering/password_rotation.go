package clustering

import (
	"context"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/dbop"
	"github.com/cybozu-go/moco/pkg/event"
	"github.com/cybozu-go/moco/pkg/password"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// handlePasswordRotation dispatches to the appropriate handler based on the
// CredentialRotation CR's derived workflow step. It returns (true, nil) when
// progress was made and the caller should redo the loop, or (false, nil)
// when no rotation work is needed.
func (p *managerProcess) handlePasswordRotation(ctx context.Context, ss *StatusSet) (bool, error) {
	cr := &mocov1beta2.CredentialRotation{}
	err := p.client.Get(ctx, client.ObjectKey{
		Namespace: p.name.Namespace,
		Name:      p.name.Name,
	}, cr)
	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	// Ignore a CR that belongs to a previously deleted cluster (same name,
	// different UID). Running RETAIN/DISCARD against the new cluster based on
	// leftover state would corrupt its credentials.
	if !crBelongsToCluster(cr, ss.Cluster) {
		return false, nil
	}

	switch cr.Step() {
	case mocov1beta2.StepApplyingRetain:
		return p.handleApplyingRetain(ctx, ss, cr)
	case mocov1beta2.StepApplyingDiscard:
		return p.handleApplyingDiscard(ctx, ss, cr)
	default:
		return false, nil
	}
}

// crBelongsToCluster reports whether cr carries an ownerReference whose UID
// matches the given MySQLCluster.
func crBelongsToCluster(cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) bool {
	for _, ref := range cr.OwnerReferences {
		if ref.UID == cluster.UID {
			return true
		}
	}
	return false
}

// handleApplyingRetain executes ALTER USER RETAIN CURRENT PASSWORD on all
// instances, then flips DualPassword to True so the derived step
// transitions from ApplyingRetain to DistributingPassword.
func (p *managerProcess) handleApplyingRetain(ctx context.Context, ss *StatusSet, cr *mocov1beta2.CredentialRotation) (bool, error) {
	log := logFromContext(ctx)
	cluster := ss.Cluster

	controllerSecret := &corev1.Secret{}
	if err := p.reader.Get(ctx, client.ObjectKey{
		Namespace: p.systemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, controllerSecret); err != nil {
		return false, fmt.Errorf("failed to get controller secret for rotation: %w", err)
	}

	// Wait for the controller to generate pending passwords.
	hasPending, err := password.HasPendingPasswords(controllerSecret, cr.Status.RotationID)
	if err != nil {
		return false, fmt.Errorf("failed to verify pending passwords: %w", err)
	}
	if !hasPending {
		log.Info("waiting for controller to generate pending passwords", "rotationID", cr.Status.RotationID)
		return false, nil
	}

	replicas := int(cluster.Spec.Replicas)
	if replicas == 0 {
		log.Info("waiting for replicas to be scaled up before RETAIN", "rotationID", cr.Status.RotationID)
		event.RotationBlocked.Emit(cluster, p.recorder)
		if err := p.setRotationReady(ctx, metav1.ConditionFalse, mocov1beta2.ReasonBlocked,
			"Cluster scaled to 0 replicas mid-cycle; cannot proceed with RETAIN."); err != nil {
			return false, err
		}
		return false, nil
	}

	currentPasswd, err := password.NewMySQLPasswordFromSecret(controllerSecret)
	if err != nil {
		return false, err
	}

	// Pre-check: verify no instance has stale dual passwords from outside
	// this rotation cycle. Skip if RETAIN_STARTED marker is present (crash
	// recovery after partial RETAIN — per-user HasDualPassword in
	// rotateInstanceUsers provides idempotency).
	retainStarted := string(controllerSecret.Data[password.RetainStartedKey]) == cr.Status.RotationID
	if !retainStarted {
		for idx := range replicas {
			op, err := p.dbf.New(ctx, cluster, currentPasswd, idx)
			if err != nil {
				return false, err
			}
			dualFound, dualUser, checkErr := checkInstanceDualPasswords(ctx, op, idx)
			_ = op.Close()
			if checkErr != nil {
				return false, checkErr
			}
			if dualFound {
				log.Info("waiting: instance has pre-existing dual password",
					"instance", idx, "user", dualUser)
				event.DualPasswordExists.Emit(cluster, p.recorder, idx, dualUser)
				return false, nil
			}
		}

		// Mark that pre-check passed and RETAIN is about to start.
		controllerSecret.Data[password.RetainStartedKey] = []byte(cr.Status.RotationID)
		if err := p.client.Update(ctx, controllerSecret); err != nil {
			return false, fmt.Errorf("failed to set RETAIN_STARTED marker: %w", err)
		}
	}

	// Execute ALTER USER RETAIN on all instances.
	pendingMap, err := password.PendingKeyMap(controllerSecret)
	if err != nil {
		return false, err
	}
	primaryIndex := cluster.Status.CurrentPrimaryIndex

	for idx := range replicas {
		op, err := p.dbf.New(ctx, cluster, currentPasswd, idx)
		if err != nil {
			return false, err
		}

		if err := rotateInstanceUsers(ctx, op, pendingMap, idx, needsSuperReadOnlyOff(idx, primaryIndex, cluster)); err != nil {
			_ = op.Close()
			return false, err
		}
		_ = op.Close()
		log.Info("completed ALTER USER RETAIN for instance", "instance", idx, "rotationID", cr.Status.RotationID)
	}

	log.Info("applied ALTER USER RETAIN for all instances", "rotationID", cr.Status.RotationID)

	// Flip DualPassword to True. The derived step transitions
	// from ApplyingRetain to DistributingPassword and the Reconciler
	// picks up from there.
	if err := p.updateCRCondition(ctx, func(fresh *mocov1beta2.CredentialRotation) {
		fresh.SetDualPassword(metav1.ConditionTrue, mocov1beta2.ReasonRetained,
			"All instances now hold a dual-password set.")
		// Clear any prior Blocked reason if we're resuming after a
		// scale-up.
		fresh.SetRotationReady(metav1.ConditionFalse, mocov1beta2.ReasonPending,
			"Rotation phase in progress.")
	}); err != nil {
		return false, fmt.Errorf("failed to persist RETAIN status: %w", err)
	}

	event.RetainApplied.Emit(ss.Cluster, p.recorder, replicas, cr.Status.RotationID)

	return true, nil
}

// handleApplyingDiscard executes DISCARD OLD PASSWORD and auth plugin
// migration on all instances, then flips DualPassword back to False so
// the derived step transitions from ApplyingDiscard to Finalizing.
//
// The post-distribute rollout wait is owned by the Reconciler
// (handleAwaitingRollout) and is what gates DiscardReady=True; by the
// time we reach this handler every Pod is already running with the new
// password.
func (p *managerProcess) handleApplyingDiscard(ctx context.Context, ss *StatusSet, cr *mocov1beta2.CredentialRotation) (bool, error) {
	log := logFromContext(ctx)
	cluster := ss.Cluster

	controllerSecret := &corev1.Secret{}
	if err := p.reader.Get(ctx, client.ObjectKey{
		Namespace: p.systemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, controllerSecret); err != nil {
		return false, fmt.Errorf("failed to get controller secret for discard: %w", err)
	}

	hasPending, err := password.HasPendingPasswords(controllerSecret, cr.Status.RotationID)
	if err != nil {
		return false, fmt.Errorf("failed to verify pending passwords for discard: %w", err)
	}
	if !hasPending {
		log.Info("waiting for pending passwords for discard", "rotationID", cr.Status.RotationID)
		return false, nil
	}

	replicas := int(cluster.Spec.Replicas)
	if replicas == 0 {
		log.Info("waiting for replicas to be scaled up before DISCARD", "rotationID", cr.Status.RotationID)
		event.DiscardBlocked.Emit(cluster, p.recorder)
		if err := p.setDiscardReady(ctx, metav1.ConditionFalse, mocov1beta2.ReasonBlocked,
			"Cluster scaled to 0 replicas mid-discard; cannot proceed with DISCARD."); err != nil {
			return false, err
		}
		return false, nil
	}

	// Defer DISCARD until the Reconciler has flipped DiscardReady to
	// False/Pending. The Reconciler owns the initial state-set after the
	// operator bumps discardGeneration (it emits the DiscardStarted Event
	// and records Refused/Blocked transitions); blocking here keeps that
	// transition observable and prevents a race where ClusterManager
	// would DISCARD before DiscardReady reflects the in-flight state.
	discardReadyCond := meta.FindStatusCondition(cr.Status.Conditions, mocov1beta2.ConditionDiscardReady)
	if discardReadyCond == nil ||
		discardReadyCond.Status != metav1.ConditionFalse ||
		discardReadyCond.Reason != mocov1beta2.ReasonPending {
		log.Info("waiting for Reconciler to initialise the discard phase",
			"rotationID", cr.Status.RotationID)
		return false, nil
	}

	// The post-distribute rollout wait is owned by the Reconciler
	// (handleAwaitingRollout) and is what gates DiscardReady=True in
	// the first place. By the time the operator can bump
	// discardGeneration and reach this handler, rollout has settled
	// and every Pod is running with the new password.

	// Connect with the pending (new) password.
	pendingPasswd, err := password.MySQLPasswordFromPending(controllerSecret)
	if err != nil {
		return false, err
	}

	pendingMap, err := password.PendingKeyMap(controllerSecret)
	if err != nil {
		return false, err
	}

	primaryIndex := cluster.Status.CurrentPrimaryIndex
	authPlugin, err := func() (string, error) {
		op, err := p.dbf.New(ctx, cluster, pendingPasswd, primaryIndex)
		if err != nil {
			return "", err
		}
		defer func() { _ = op.Close() }()
		return op.GetAuthPlugin(ctx)
	}()
	if err != nil {
		return false, err
	}
	log.Info("determined target auth plugin for migration", "authPlugin", authPlugin, "rotationID", cr.Status.RotationID)

	for idx := range replicas {
		op, err := p.dbf.New(ctx, cluster, pendingPasswd, idx)
		if err != nil {
			return false, err
		}

		if err := discardInstanceUsers(ctx, op, pendingMap, idx, needsSuperReadOnlyOff(idx, primaryIndex, cluster), authPlugin); err != nil {
			_ = op.Close()
			return false, err
		}
		_ = op.Close()
		log.Info("applied DISCARD OLD PASSWORD and auth plugin migration for instance", "instance", idx, "rotationID", cr.Status.RotationID)
	}

	log.Info("applied DISCARD OLD PASSWORD for all instances", "rotationID", cr.Status.RotationID)

	if err := p.updateCRCondition(ctx, func(fresh *mocov1beta2.CredentialRotation) {
		fresh.SetDualPassword(metav1.ConditionFalse, mocov1beta2.ReasonNotRetained,
			"DISCARD OLD PASSWORD succeeded on all instances.")
	}); err != nil {
		return false, fmt.Errorf("failed to persist DISCARD status: %w", err)
	}

	event.DiscardApplied.Emit(ss.Cluster, p.recorder, authPlugin, replicas, cr.Status.RotationID)

	return true, nil
}

// setRotationReady updates the RotationReady condition with retry-on-conflict.
func (p *managerProcess) setRotationReady(ctx context.Context, status metav1.ConditionStatus, reason, message string) error {
	return p.updateCRCondition(ctx, func(cr *mocov1beta2.CredentialRotation) {
		cr.SetRotationReady(status, reason, message)
	})
}

// setDiscardReady updates the DiscardReady condition with retry-on-conflict.
func (p *managerProcess) setDiscardReady(ctx context.Context, status metav1.ConditionStatus, reason, message string) error {
	return p.updateCRCondition(ctx, func(cr *mocov1beta2.CredentialRotation) {
		cr.SetDiscardReady(status, reason, message)
	})
}

// updateCRCondition fetches the CR fresh, applies the mutator, stamps
// ObservedGeneration, and writes the status with retry-on-conflict.
// The mutator must be idempotent because RetryOnConflict may call it
// repeatedly on a freshly-fetched copy.
func (p *managerProcess) updateCRCondition(ctx context.Context, mutate func(*mocov1beta2.CredentialRotation)) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		fresh := &mocov1beta2.CredentialRotation{}
		if err := p.reader.Get(ctx, client.ObjectKey{
			Namespace: p.name.Namespace,
			Name:      p.name.Name,
		}, fresh); err != nil {
			return err
		}
		mutate(fresh)
		fresh.StampObservedGeneration()
		return p.client.Status().Update(ctx, fresh)
	})
}

// checkInstanceDualPasswords checks whether any MOCO user on the given instance
// already has a dual password.
func checkInstanceDualPasswords(
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

// needsSuperReadOnlyOff reports whether the given instance runs with
// super_read_only=1 during steady-state operation and therefore needs the
// flag temporarily lowered before RETAIN / DISCARD DDL. The primary of a
// normal cluster is writable, but the primary of an intermediate-primary
// cluster (spec.replicationSourceSecretName != nil) is itself a replica of
// an external cluster and runs with super_read_only=1. Without the toggle
// the DDL fails with ER_OPTION_PREVENTS_STATEMENT (1290).
func needsSuperReadOnlyOff(idx, primaryIndex int, cluster *mocov1beta2.MySQLCluster) bool {
	if idx != primaryIndex {
		return true
	}
	return cluster.Spec.ReplicationSourceSecretName != nil
}

// rotateInstanceUsers executes ALTER USER RETAIN for users on a single instance.
// See needsSuperReadOnlyOff for the semantics of the last argument.
func rotateInstanceUsers(
	ctx context.Context,
	op dbop.Operator,
	pendingMap map[string]string,
	instanceIndex int,
	needsSuperReadOnlyOff bool,
) error {
	log := logFromContext(ctx)

	if needsSuperReadOnlyOff {
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

// discardInstanceUsers executes DISCARD OLD PASSWORD and auth plugin migration for all users on a single instance.
// See needsSuperReadOnlyOff for the semantics of the fifth argument.
func discardInstanceUsers(
	ctx context.Context,
	op dbop.Operator,
	pendingMap map[string]string,
	instanceIndex int,
	needsSuperReadOnlyOff bool,
	authPlugin string,
) error {
	log := logFromContext(ctx)

	if needsSuperReadOnlyOff {
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
		// Idempotency: skip DISCARD if the user no longer has a retained old
		// password (e.g., a partial retry after controller restart or transient
		// DB error). MySQL rejects DISCARD OLD PASSWORD once there is no
		// retained password, which would otherwise wedge the rotation.
		hasDual, err := op.HasDualPassword(ctx, user)
		if err != nil {
			return fmt.Errorf("failed to check dual password for %s on instance %d: %w", user, instanceIndex, err)
		}
		if !hasDual {
			log.Info("skipping DISCARD OLD PASSWORD (no retained password)", "user", user, "instance", instanceIndex)
			continue
		}
		if err := op.DiscardOldPassword(ctx, user); err != nil {
			return fmt.Errorf("failed to discard old password for %s on instance %d: %w", user, instanceIndex, err)
		}
	}

	for _, user := range constants.MocoUsers {
		currentPlugin, err := op.GetUserAuthPlugin(ctx, user)
		if err != nil {
			return fmt.Errorf("failed to get current auth plugin for %s on instance %d: %w", user, instanceIndex, err)
		}
		if currentPlugin == authPlugin {
			log.Info("skipping auth plugin migration (already using target plugin)", "user", user, "instance", instanceIndex, "authPlugin", authPlugin)
			continue
		}
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
