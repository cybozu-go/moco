package clustering

import (
	"context"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/dbop"
	"github.com/cybozu-go/moco/pkg/password"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// handlePasswordRotation dispatches to the appropriate handler based on the
// CredentialRotation CR's phase. It returns (true, nil) when progress was made and
// the caller should redo the loop, or (false, nil) when no rotation work is needed.
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

	switch cr.Status.Phase {
	case mocov1beta2.RotationPhaseRotating:
		return p.handleRotatingPhase(ctx, ss, cr)
	case mocov1beta2.RotationPhaseDiscarding:
		return p.handleDiscardingPhase(ctx, ss, cr)
	default:
		return false, nil
	}
}

// handleRotatingPhase executes ALTER USER RETAIN CURRENT PASSWORD on all
// instances, then transitions Phase from Rotating to Retained.
func (p *managerProcess) handleRotatingPhase(ctx context.Context, ss *StatusSet, cr *mocov1beta2.CredentialRotation) (bool, error) {
	log := logFromContext(ctx)
	cluster := ss.Cluster

	// Read the source Secret from the system namespace.
	sourceSecret := &corev1.Secret{}
	if err := p.reader.Get(ctx, client.ObjectKey{
		Namespace: p.systemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, sourceSecret); err != nil {
		return false, fmt.Errorf("failed to get source secret for rotation: %w", err)
	}

	// Wait for the controller to generate pending passwords.
	hasPending, err := password.HasPendingPasswords(sourceSecret, cr.Status.RotationID)
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
		return false, nil
	}

	currentPasswd, err := password.NewMySQLPasswordFromSecret(sourceSecret)
	if err != nil {
		return false, err
	}

	// Pre-check: verify no instance has stale dual passwords from outside
	// this rotation cycle. Skip if RETAIN_STARTED marker is present (crash
	// recovery after partial RETAIN — per-user HasDualPassword in
	// rotateInstanceUsers provides idempotency).
	retainStarted := string(sourceSecret.Data[password.RetainStartedKey]) == cr.Status.RotationID
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
				p.recorder.Eventf(cluster, corev1.EventTypeWarning, "DualPasswordExists",
					"Cannot proceed with RETAIN: instance %d user %s already has a dual password. "+
						"See MOCO documentation for recovery procedures.",
					idx, dualUser)
				return false, nil
			}
		}

		// Mark that pre-check passed and RETAIN is about to start.
		sourceSecret.Data[password.RetainStartedKey] = []byte(cr.Status.RotationID)
		if err := p.client.Update(ctx, sourceSecret); err != nil {
			return false, fmt.Errorf("failed to set RETAIN_STARTED marker: %w", err)
		}
	}

	// Execute ALTER USER RETAIN on all instances.
	pendingMap, err := password.PendingKeyMap(sourceSecret)
	if err != nil {
		return false, err
	}
	primaryIndex := cluster.Status.CurrentPrimaryIndex

	for idx := range replicas {
		isReplica := idx != primaryIndex
		op, err := p.dbf.New(ctx, cluster, currentPasswd, idx)
		if err != nil {
			return false, err
		}

		if err := rotateInstanceUsers(ctx, op, pendingMap, idx, isReplica); err != nil {
			_ = op.Close()
			return false, err
		}
		_ = op.Close()
		log.Info("completed ALTER USER RETAIN for instance", "instance", idx, "rotationID", cr.Status.RotationID)
	}

	log.Info("applied ALTER USER RETAIN for all instances", "rotationID", cr.Status.RotationID)

	// Transition Phase: Rotating → Retained
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cr := &mocov1beta2.CredentialRotation{}
		if err := p.reader.Get(ctx, client.ObjectKey{
			Namespace: p.name.Namespace,
			Name:      p.name.Name,
		}, cr); err != nil {
			return err
		}
		cr.Status.Phase = mocov1beta2.RotationPhaseRetained
		return p.client.Status().Update(ctx, cr)
	}); err != nil {
		return false, fmt.Errorf("failed to persist Retained status: %w", err)
	}

	p.recorder.Eventf(ss.Cluster, corev1.EventTypeNormal, "RetainApplied",
		"Applied ALTER USER RETAIN for all %d instances (rotationID: %s)", replicas, cr.Status.RotationID)

	return true, nil
}

// handleDiscardingPhase executes DISCARD OLD PASSWORD and auth plugin migration
// on all instances, then transitions Phase from Discarding to Discarded.
func (p *managerProcess) handleDiscardingPhase(ctx context.Context, ss *StatusSet, cr *mocov1beta2.CredentialRotation) (bool, error) {
	log := logFromContext(ctx)
	cluster := ss.Cluster

	// Read the source Secret from the system namespace.
	sourceSecret := &corev1.Secret{}
	if err := p.reader.Get(ctx, client.ObjectKey{
		Namespace: p.systemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, sourceSecret); err != nil {
		return false, fmt.Errorf("failed to get source secret for discard: %w", err)
	}

	hasPending, err := password.HasPendingPasswords(sourceSecret, cr.Status.RotationID)
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
		p.recorder.Eventf(cluster, corev1.EventTypeWarning, "DiscardRefused",
			"Cannot proceed with DISCARD: cluster has 0 replicas. Scale the cluster up first.")
		return false, nil
	}

	// Connect with the pending (new) password.
	pendingPasswd, err := password.NewMySQLPasswordFromPending(sourceSecret)
	if err != nil {
		return false, err
	}

	pendingMap, err := password.PendingKeyMap(sourceSecret)
	if err != nil {
		return false, err
	}

	// Determine the target auth plugin from the primary.
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

	// Execute DISCARD OLD PASSWORD + auth plugin migration on all instances.
	for idx := range replicas {
		isReplica := idx != primaryIndex
		op, err := p.dbf.New(ctx, cluster, pendingPasswd, idx)
		if err != nil {
			return false, err
		}

		if err := discardInstanceUsers(ctx, op, pendingMap, idx, isReplica, authPlugin); err != nil {
			_ = op.Close()
			return false, err
		}
		_ = op.Close()
		log.Info("applied DISCARD OLD PASSWORD and auth plugin migration for instance", "instance", idx, "rotationID", cr.Status.RotationID)
	}

	log.Info("applied DISCARD OLD PASSWORD for all instances", "rotationID", cr.Status.RotationID)

	// Transition Phase: Discarding → Discarded
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cr := &mocov1beta2.CredentialRotation{}
		if err := p.reader.Get(ctx, client.ObjectKey{
			Namespace: p.name.Namespace,
			Name:      p.name.Name,
		}, cr); err != nil {
			return err
		}
		cr.Status.Phase = mocov1beta2.RotationPhaseDiscarded
		return p.client.Status().Update(ctx, cr)
	}); err != nil {
		return false, fmt.Errorf("failed to persist Discarded status: %w", err)
	}

	p.recorder.Eventf(ss.Cluster, corev1.EventTypeNormal, "DiscardApplied",
		"Applied DISCARD OLD PASSWORD and migrated auth plugin to %s for all %d instances (rotationID: %s)", authPlugin, replicas, cr.Status.RotationID)

	return true, nil
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

// rotateInstanceUsers executes ALTER USER RETAIN for users on a single instance.
func rotateInstanceUsers(
	ctx context.Context,
	op dbop.Operator,
	pendingMap map[string]string,
	instanceIndex int,
	isReplica bool,
) error {
	log := logFromContext(ctx)

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

// discardInstanceUsers executes DISCARD OLD PASSWORD and auth plugin migration for all users on a single instance.
func discardInstanceUsers(
	ctx context.Context,
	op dbop.Operator,
	pendingMap map[string]string,
	instanceIndex int,
	isReplica bool,
	authPlugin string,
) error {
	log := logFromContext(ctx)

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
