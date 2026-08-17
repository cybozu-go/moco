package clustering

import (
	"context"
	"errors"
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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// maxDiscardReverifyRounds bounds the re-verification loop after DISCARD.
// Each round only re-runs instances that still hold a dual password (e.g.
// an instance added by a scale-up mid-discard, which cloned its donor's
// dual-password state), so two rounds converge unless instances keep
// appearing faster than they can be discarded.
const maxDiscardReverifyRounds = 3

// maxRetainReverifyRounds bounds the scale-up catch-up loop of RETAIN in
// the same way: each round retains only the instances added since the
// previous one, so two rounds converge unless the cluster keeps growing.
const maxRetainReverifyRounds = 3

// handlePasswordRotation dispatches to the appropriate handler based on the
// CredentialRotation CR's phase. It returns (true, nil) when progress was
// made and the caller should redo the loop, or (false, nil) when no
// rotation work is needed.
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

	// Act only on a CR that the Reconciler has adopted onto THIS cluster
	// incarnation. This is stricter than the stale rule used elsewhere:
	// a CR with no ownerReference yet is skipped too (the Reconciler
	// adopts before any phase can reach us, so this only skips brand-new
	// objects for one tick).
	if !crBelongsToCluster(cr, ss.Cluster) {
		return false, nil
	}

	var redo bool
	switch cr.Status.Phase {
	case mocov1beta2.PhaseApplyingRetain:
		redo, err = p.handleApplyingRetain(ctx, ss, cr)
	case mocov1beta2.PhaseApplyingDiscard:
		redo, err = p.handleApplyingDiscard(ctx, ss, cr)
	default:
		// Blocked resumes are the Reconciler's job; every other phase is
		// not ours either.
		return false, nil
	}
	if err != nil {
		// Mirror the error into THIS CR's status.message (the UID guard in
		// updateCRStatus prevents writing an old cycle's error onto a CR
		// recreated under the same name), so the stall stays visible after
		// the Events expire. Best-effort.
		p.mirrorCRMessage(ctx, cr, fmt.Sprintf(
			"Rotation cannot progress and retries every tick: %v. See the PasswordRotationError Events on the MySQLCluster for details.", err))
	}
	return redo, err
}

// staleCachedCR re-reads the CR uncached and reports whether the cached
// object this tick works on is stale: the live CR is a different object
// (UID) or its phase has already moved on. The RETAIN and DISCARD handlers
// diagnose "Secret state contradicts the phase" from a cached CR plus an
// uncached Secret read; when the Reconciler advanced the live phase and
// rewrote the Secret in between, that contradiction is an artifact of the
// lag, not an inconsistency — so they rule it out before failing the
// rotation.
func (p *managerProcess) staleCachedCR(ctx context.Context, cr *mocov1beta2.CredentialRotation, phase mocov1beta2.RotationPhase) (bool, error) {
	freshCR := &mocov1beta2.CredentialRotation{}
	if err := p.reader.Get(ctx, client.ObjectKeyFromObject(cr), freshCR); err != nil {
		return false, fmt.Errorf("failed to re-read the CredentialRotation: %w", err)
	}
	return freshCR.UID != cr.UID || freshCR.Status.Phase != phase, nil
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
// instances, then moves the CR to Promoting (writing DualPassword=True in
// the same status update).
//
// RETAIN is all-or-nothing: this handler aborts on the first instance
// that cannot be reached or fails ALTER USER, and the transition is only
// written after every instance holds the dual-password set. The
// Reconciler's promotion step (which makes the new passwords canonical
// current) depends on this — skipping an unreachable instance here would
// let promotion create an instance where the canonical current password
// does not authenticate.
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

	// Wait for the Reconciler to stage the pending passwords; fail the CR
	// on any state that contradicts the phase.
	state, err := password.RotationState(controllerSecret, cr.Status.RotationID)
	if err != nil {
		return false, p.failRotation(ctx, cr, fmt.Sprintf(
			"inconsistent controller Secret during RETAIN: %v; follow the 'Inconsistent Controller Secret' recovery procedure, then delete this CR and create a new one", err))
	}
	switch state {
	case password.RotationSecretClean:
		// The seed handler stages the pending passwords BEFORE it sets
		// ApplyingRetain, and the Secret above was read uncached — so a
		// clean Secret is not an in-flight state of this phase: the
		// staged passwords were lost (manual edit or a restore from
		// backup), and waiting can never make progress. Rule out the one
		// benign explanation — a stale cached CR whose live phase has
		// already moved past Finalizing's key cleanup — before failing.
		stale, err := p.staleCachedCR(ctx, cr, mocov1beta2.PhaseApplyingRetain)
		if err != nil {
			return false, err
		}
		if stale {
			log.Info("cached CredentialRotation was stale; skipping this tick")
			return false, nil
		}
		return false, p.failRotation(ctx, cr, fmt.Sprintf(
			"the staged pending passwords for rotationID %s were lost (the controller Secret is clean); follow the 'Inconsistent Controller Secret' recovery procedure, then delete this CR and create a new one", cr.Status.RotationID))
	case password.RotationSecretPromoted:
		// Promotion already happened for this rotationID, yet the phase is
		// still ApplyingRetain. This combination is inconsistent — the
		// Reconciler only promotes from the Promoting phase — unless the
		// cached CR is stale and the live phase has already advanced to
		// Promoting or beyond; rule that out before failing.
		stale, err := p.staleCachedCR(ctx, cr, mocov1beta2.PhaseApplyingRetain)
		if err != nil {
			return false, err
		}
		if stale {
			log.Info("cached CredentialRotation was stale; skipping this tick")
			return false, nil
		}
		return false, p.failRotation(ctx, cr, fmt.Sprintf(
			"the controller Secret is already promoted for rotationID %s but the phase is still ApplyingRetain; follow the 'Inconsistent Controller Secret' recovery procedure, then delete this CR and create a new one", cr.Status.RotationID))
	}

	// spec.offline scales the StatefulSet down to zero Pods while
	// spec.replicas stays positive, so both fields must block here:
	// RETAIN would otherwise retry forever against Pods that do not exist.
	replicas := int(cluster.Spec.Replicas)
	if cluster.IsOfflineOrZeroReplicas() {
		log.Info("blocked: cluster has 0 replicas or is offline before RETAIN", "rotationID", cr.Status.RotationID)
		event.RotationBlocked.Emit(cluster, p.recorder)
		if err := p.updateCRStatus(ctx, cr.UID, phaseIs(mocov1beta2.PhaseApplyingRetain), func(fresh *mocov1beta2.CredentialRotation) {
			fresh.SetPhase(mocov1beta2.PhaseBlocked,
				"MySQLCluster has 0 replicas or is offline; RETAIN resumes when the cluster is running again.")
		}); err != nil {
			return false, err
		}
		return false, nil
	}

	// An unreadable current password set cannot be repaired by retrying
	// (manual edit or a restore from backup), so fail the CR like every
	// other controller-Secret inconsistency. NewMySQLPasswordFromSecret
	// validates only the version annotation; CurrentKeyMap validates that
	// every current key is present.
	currentPasswd, err := password.NewMySQLPasswordFromSecret(controllerSecret)
	if err == nil {
		_, err = password.CurrentKeyMap(controllerSecret)
	}
	if err != nil {
		return false, p.failRotation(ctx, cr, fmt.Sprintf(
			"the controller Secret's current passwords are unreadable during RETAIN: %v; follow the 'Inconsistent Controller Secret' recovery procedure, then delete this CR and create a new one", err))
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
				p.mirrorCRMessage(ctx, cr, fmt.Sprintf(
					"Waiting: instance %d user %s already has a dual password from outside this cycle. See the 'Dual Password Exists Outside the Current Cycle' recovery procedure.", idx, dualUser))
				return false, nil
			}
		}

		// Mark that pre-check passed and RETAIN is about to start.
		controllerSecret.Data[password.RetainStartedKey] = []byte(cr.Status.RotationID)
		if err := p.client.Update(ctx, controllerSecret); err != nil {
			return false, fmt.Errorf("failed to set RETAIN_STARTED marker: %w", err)
		}
	}

	// Execute ALTER USER RETAIN on all instances. The replica count is
	// re-read from the live cluster after every pass: a scale-up while
	// this loop runs can add an instance whose clone finished before its
	// donor was retained, and promoting without retaining that instance
	// would break the core invariant on it. An instance that is added but
	// not yet ready simply fails the connection, which aborts this tick
	// and retries later — all-or-nothing is preserved.
	pendingMap, err := password.PendingKeyMap(controllerSecret)
	if err != nil {
		return false, err
	}
	primaryIndex := cluster.Status.CurrentPrimaryIndex

	retained := 0
	for round := 0; ; round++ {
		if round >= maxRetainReverifyRounds {
			return false, fmt.Errorf("the cluster kept growing during RETAIN for %d rounds", maxRetainReverifyRounds)
		}

		for idx := retained; idx < replicas; idx++ {
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
		retained = replicas

		freshCluster := &mocov1beta2.MySQLCluster{}
		if err := p.client.Get(ctx, client.ObjectKey{
			Namespace: p.name.Namespace,
			Name:      p.name.Name,
		}, freshCluster); err != nil {
			return false, fmt.Errorf("failed to re-read the cluster after RETAIN: %w", err)
		}
		if int(freshCluster.Spec.Replicas) <= retained {
			break
		}
		log.Info("cluster was scaled up during RETAIN; retaining the new instances",
			"from", retained, "to", freshCluster.Spec.Replicas, "rotationID", cr.Status.RotationID)
		replicas = int(freshCluster.Spec.Replicas)
	}

	log.Info("applied ALTER USER RETAIN for all instances", "rotationID", cr.Status.RotationID)

	// Move to Promoting, writing DualPassword=True in the same update so a
	// single read never sees the phase and the condition disagree.
	if err := p.updateCRStatus(ctx, cr.UID, phaseIs(mocov1beta2.PhaseApplyingRetain), func(fresh *mocov1beta2.CredentialRotation) {
		fresh.SetDualPassword(metav1.ConditionTrue, mocov1beta2.ReasonRetained,
			"All instances now hold a dual-password set.")
		fresh.SetPhase(mocov1beta2.PhasePromoting,
			"Promoting the pending passwords to current in the controller Secret.")
	}); err != nil {
		return false, fmt.Errorf("failed to persist RETAIN status: %w", err)
	}

	event.RetainApplied.Emit(ss.Cluster, p.recorder, replicas, cr.Status.RotationID)

	return true, nil
}

// handleApplyingDiscard executes DISCARD OLD PASSWORD and auth plugin
// migration on all instances, re-verifies that no dual password remains,
// then moves the CR to Finalizing (writing DualPassword=False in the same
// status update).
//
// The post-distribute rollout wait is owned by the Reconciler
// (handleAwaitingRollout) and is what gated the discard request; by the
// time we reach this handler every Pod is already running with the new
// password. The DiscardStarted Event and DiscardReady=False were recorded
// in the same atomic status update that set the phase, so no extra
// handshake is needed here.
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

	// The *_OLD group with a matching ROTATION_ID stays in the Secret
	// until Finalizing and is the only proof that the current keys hold
	// the promoted passwords. Anything else is inconsistent:
	//   - Pending means current is still the OLD password, and DISCARD
	//     would remove the very secondary that current matches.
	//   - Clean means the bookkeeping keys were lost mid-cycle (manual
	//     edit, restore from backup, ...), so nothing proves that current
	//     holds the promoted passwords; running DISCARD and auth plugin
	//     migration against unverified values could break the core
	//     invariant.
	state, err := password.RotationState(controllerSecret, cr.Status.RotationID)
	if err != nil {
		return false, p.failRotation(ctx, cr, fmt.Sprintf(
			"inconsistent controller Secret during DISCARD: %v; follow the 'Inconsistent Controller Secret' recovery procedure, then delete this CR and create a new one", err))
	}
	if state != password.RotationSecretPromoted {
		// A clean Secret also appears when the cached CR is stale and the
		// live phase has already reached Finalizing, whose key cleanup ran
		// between our two reads; rule that out before failing.
		stale, err := p.staleCachedCR(ctx, cr, mocov1beta2.PhaseApplyingDiscard)
		if err != nil {
			return false, err
		}
		if stale {
			log.Info("cached CredentialRotation was stale; skipping this tick")
			return false, nil
		}
		return false, p.failRotation(ctx, cr, fmt.Sprintf(
			"the controller Secret is not in the promoted state for rotationID %s (state=%v); it cannot be proven that the current keys hold the promoted passwords. Follow the 'Inconsistent Controller Secret' recovery procedure, then delete this CR and create a new one", cr.Status.RotationID, state))
	}

	// See the RETAIN handler: spec.offline must block like 0 replicas.
	if cluster.IsOfflineOrZeroReplicas() {
		log.Info("blocked: cluster has 0 replicas or is offline before DISCARD", "rotationID", cr.Status.RotationID)
		event.DiscardBlocked.Emit(cluster, p.recorder)
		if err := p.updateCRStatus(ctx, cr.UID, phaseIs(mocov1beta2.PhaseApplyingDiscard), func(fresh *mocov1beta2.CredentialRotation) {
			fresh.SetPhase(mocov1beta2.PhaseBlocked,
				"MySQLCluster has 0 replicas or is offline; DISCARD resumes when the cluster is running again.")
			fresh.SetDiscardReady(metav1.ConditionFalse, mocov1beta2.ReasonBlocked,
				"The discard cannot progress: the cluster has 0 replicas or is offline.")
		}); err != nil {
			return false, err
		}
		return false, nil
	}

	// Connect with the current (new) password. DISCARD removes the
	// secondary (old) password; the current password is the primary and
	// is unaffected before, during, and after DISCARD.
	// An unreadable current password set cannot be repaired by retrying
	// (manual edit or a restore from backup), so fail the CR like every
	// other controller-Secret inconsistency.
	currentPasswd, err := password.NewMySQLPasswordFromSecret(controllerSecret)
	if err != nil {
		return false, p.failRotation(ctx, cr, fmt.Sprintf(
			"the controller Secret's current passwords are unreadable during DISCARD: %v; follow the 'Inconsistent Controller Secret' recovery procedure, then delete this CR and create a new one", err))
	}

	currentMap, err := password.CurrentKeyMap(controllerSecret)
	if err != nil {
		return false, p.failRotation(ctx, cr, fmt.Sprintf(
			"the controller Secret's current passwords are unreadable during DISCARD: %v; follow the 'Inconsistent Controller Secret' recovery procedure, then delete this CR and create a new one", err))
	}

	primaryIndex := cluster.Status.CurrentPrimaryIndex
	authPlugin, err := func() (string, error) {
		op, err := p.dbf.New(ctx, cluster, currentPasswd, primaryIndex)
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

	// Run DISCARD + plugin migration, then re-verify until no instance
	// holds a dual password. The re-verification catches an instance
	// added by a scale-up during the loop, which cloned its donor's
	// dual-password state before the donor was discarded. The replica
	// count is re-read from the live cluster on every round — ss.Cluster
	// is a snapshot from the start of the tick and would hide a mid-tick
	// scale-up.
	var replicas int
	for round := 0; ; round++ {
		if round >= maxDiscardReverifyRounds {
			return false, fmt.Errorf("instances still hold dual passwords after %d discard rounds", maxDiscardReverifyRounds)
		}

		freshCluster := &mocov1beta2.MySQLCluster{}
		if err := p.client.Get(ctx, client.ObjectKey{
			Namespace: p.name.Namespace,
			Name:      p.name.Name,
		}, freshCluster); err != nil {
			return false, fmt.Errorf("failed to re-read the cluster for discard re-verification: %w", err)
		}
		replicas = int(freshCluster.Spec.Replicas)
		for idx := range replicas {
			op, err := p.dbf.New(ctx, cluster, currentPasswd, idx)
			if err != nil {
				return false, err
			}

			if err := discardInstanceUsers(ctx, op, currentMap, idx, needsSuperReadOnlyOff(idx, primaryIndex, cluster), authPlugin); err != nil {
				_ = op.Close()
				return false, err
			}
			_ = op.Close()
			log.Info("applied DISCARD OLD PASSWORD and auth plugin migration for instance", "instance", idx, "rotationID", cr.Status.RotationID)
		}

		clean := true
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
				log.Info("re-verification found a remaining dual password; re-running DISCARD",
					"instance", idx, "user", dualUser, "round", round)
				clean = false
				break
			}
		}
		if clean {
			break
		}
	}

	log.Info("applied DISCARD OLD PASSWORD for all instances", "rotationID", cr.Status.RotationID)

	// Move to Finalizing, writing DualPassword=False in the same update.
	if err := p.updateCRStatus(ctx, cr.UID, phaseIs(mocov1beta2.PhaseApplyingDiscard), func(fresh *mocov1beta2.CredentialRotation) {
		fresh.SetDualPassword(metav1.ConditionFalse, mocov1beta2.ReasonNotRetained,
			"DISCARD OLD PASSWORD succeeded on all instances.")
		fresh.SetPhase(mocov1beta2.PhaseFinalizing,
			"Removing the rotation bookkeeping keys from the controller Secret.")
	}); err != nil {
		return false, fmt.Errorf("failed to persist DISCARD status: %w", err)
	}

	event.DiscardApplied.Emit(ss.Cluster, p.recorder, authPlugin, replicas, cr.Status.RotationID)

	return true, nil
}

// failRotation moves the CR to the terminal Failed phase and emits the
// RotationFailed Warning Event on the CR. It returns nil so callers can
// `return false, p.failRotation(...)` — the failure is recorded on the CR,
// not surfaced as a clustering error.
func (p *managerProcess) failRotation(ctx context.Context, cr *mocov1beta2.CredentialRotation, message string) error {
	log := logFromContext(ctx)

	if err := p.updateCRStatus(ctx, cr.UID, notTerminal(), func(fresh *mocov1beta2.CredentialRotation) {
		fresh.Fail(message, metav1.Now())
	}); err != nil {
		return err
	}
	log.Info("rotation failed", "message", message)
	event.RotationFailed.Emit(cr, p.recorder, message)
	return nil
}

// mirrorCRMessage best-effort mirrors a wait reason into status.message so
// it stays visible after the Events expire. No-op when the message is
// already set.
func (p *managerProcess) mirrorCRMessage(ctx context.Context, cr *mocov1beta2.CredentialRotation, message string) {
	if cr.Status.Message == message {
		return
	}
	if err := p.updateCRStatus(ctx, cr.UID, notTerminal(), func(fresh *mocov1beta2.CredentialRotation) {
		fresh.Status.Message = message
	}); err != nil {
		logFromContext(ctx).Error(err, "failed to mirror the wait reason into status.message")
	}
}

// phaseIs returns a precondition that holds while the CR is still in the
// given phase.
func phaseIs(phase mocov1beta2.RotationPhase) func(*mocov1beta2.CredentialRotation) bool {
	return func(cr *mocov1beta2.CredentialRotation) bool {
		return cr.Status.Phase == phase
	}
}

// notTerminal matches any non-terminal CR.
func notTerminal() func(*mocov1beta2.CredentialRotation) bool {
	return func(cr *mocov1beta2.CredentialRotation) bool {
		return !cr.IsTerminal()
	}
}

// updateCRStatus fetches the CR fresh, re-verifies that it is still the
// same object incarnation (UID) and that the transition's precondition
// still holds, applies the mutator, stamps ObservedGeneration, and writes
// the status with retry-on-conflict. The re-verification runs after every
// fresh Get (including the first): a conflict retry must never apply a
// transition to a different CR incarnation (deleted and recreated
// mid-tick) or to a phase that has moved on. The mutator must be
// idempotent because RetryOnConflict may call it repeatedly.
func (p *managerProcess) updateCRStatus(ctx context.Context, uid types.UID, precondition func(*mocov1beta2.CredentialRotation) bool, mutate func(*mocov1beta2.CredentialRotation)) error {
	log := logFromContext(ctx)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		fresh := &mocov1beta2.CredentialRotation{}
		if err := p.reader.Get(ctx, client.ObjectKey{
			Namespace: p.name.Namespace,
			Name:      p.name.Name,
		}, fresh); err != nil {
			return err
		}
		if fresh.UID != uid {
			log.Info("skipping CR status update: the CredentialRotation was replaced mid-operation")
			return nil
		}
		if precondition != nil && !precondition(fresh) {
			log.Info("skipping CR status update: the phase moved on", "phase", fresh.Status.Phase)
			return nil
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
) (err error) {
	log := logFromContext(ctx)

	if needsSuperReadOnlyOff {
		if err := op.SetSuperReadOnly(ctx, false); err != nil {
			return fmt.Errorf("failed to disable super_read_only on instance %d: %w", instanceIndex, err)
		}
		defer func() {
			if restoreErr := op.SetSuperReadOnly(ctx, true); restoreErr != nil {
				err = errors.Join(err, fmt.Errorf("failed to re-enable super_read_only on instance %d: %w", instanceIndex, restoreErr))
			}
		}()
	}

	for _, user := range constants.MocoUsers {
		newPwd, ok := pendingMap[user]
		if !ok {
			return fmt.Errorf("pending password not found for user %s", user)
		}
		hasDual, err := op.HasDualPassword(ctx, user)
		if err != nil {
			return fmt.Errorf("failed to check dual password for %s on instance %d: %w", user, instanceIndex, err)
		}
		if hasDual {
			// This runs only on a crash retry (the RETAIN_STARTED marker is
			// set), so the dual password normally comes from this cycle's
			// own RETAIN. Verify that instead of assuming it: if the pending
			// password does not authenticate, the dual password was created
			// outside this cycle after the pre-check, and skipping would let
			// the promotion make passwords canonical that this user never
			// retained. Re-running RETAIN is no better (it would drop the
			// still-needed current password from the secondary slot), so
			// report it for the recovery procedure.
			verified, err := op.VerifyUserPassword(ctx, user, newPwd)
			if err != nil {
				return fmt.Errorf("failed to verify the pending password for %s on instance %d: %w", user, instanceIndex, err)
			}
			if !verified {
				return fmt.Errorf("user %s on instance %d holds a dual password that does not match this cycle's pending password. See the 'Dual Password Exists Outside the Current Cycle' recovery procedure", user, instanceIndex)
			}
			log.Info("skipping ALTER USER RETAIN (this cycle's RETAIN already applied)", "user", user, "instance", instanceIndex)
			continue
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
	currentMap map[string]string,
	instanceIndex int,
	needsSuperReadOnlyOff bool,
	authPlugin string,
) (err error) {
	log := logFromContext(ctx)

	if needsSuperReadOnlyOff {
		if err := op.SetSuperReadOnly(ctx, false); err != nil {
			return fmt.Errorf("failed to disable super_read_only on instance %d for discard: %w", instanceIndex, err)
		}
		defer func() {
			if restoreErr := op.SetSuperReadOnly(ctx, true); restoreErr != nil {
				err = errors.Join(err, fmt.Errorf("failed to re-enable super_read_only on instance %d: %w", instanceIndex, restoreErr))
			}
		}()
	}

	for _, user := range constants.MocoUsers {
		// Idempotency: skip DISCARD if the user no longer has a retained old
		// password (e.g., a partial retry after controller restart or transient
		// DB error). DISCARD OLD PASSWORD is a no-op in that case; skipping
		// makes the retry explicit in the logs.
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
		pwd, ok := currentMap[user]
		if !ok {
			return fmt.Errorf("current password not found for user %s during auth plugin migration", user)
		}
		if err := op.MigrateUserAuthPlugin(ctx, user, pwd, authPlugin); err != nil {
			return fmt.Errorf("failed to migrate auth plugin for %s on instance %d: %w", user, instanceIndex, err)
		}
		log.Info("migrated auth plugin", "user", user, "instance", instanceIndex, "authPlugin", authPlugin)
	}

	return nil
}
