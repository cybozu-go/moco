package v1beta2

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RotationStep represents the internal workflow step of a rotation cycle.
// It is derived on the fly from the combination of conditions and the
// rotationGeneration / discardGeneration comparisons; it is NOT stored on
// the CR. Persisting a phase enum violates the Kubernetes API
// conventions, which is why the workflow is represented as a small set
// of orthogonal Conditions instead.
type RotationStep string

const (
	// StepIdle is the action-availability steady state for "rotate is
	// allowed": no work is in flight and no dual password is held.
	// Also covers a fresh CR with no conditions yet, and the transient
	// window between an operator's rotationGeneration bump and the next
	// reconcile (RotationReady=True is stale until handleStartRotation
	// seeds the cycle).
	StepIdle RotationStep = "Idle"

	// StepApplyingRetain means ClusterManager is executing or about to
	// execute ALTER USER ... RETAIN CURRENT PASSWORD on all instances.
	// Detected when newRotation is true and DualPassword is False
	// (RETAIN has not yet succeeded for this cycle).
	StepApplyingRetain RotationStep = "ApplyingRetain"

	// StepDistributingPassword means the Reconciler should distribute
	// pending passwords to per-namespace Secrets and trigger the
	// rolling restart. Detected when newRotation is true and
	// DualPassword is True (RETAIN succeeded; the Reconciler now needs
	// to publish the new password).
	StepDistributingPassword RotationStep = "DistributingPassword"

	// StepAwaitingRollout sits between StepDistributingPassword and
	// StepAwaitingDiscard. The Reconciler has distributed the pending
	// passwords and triggered the rolling restart; MySQL holds the
	// dual-password set. The CR is waiting for the StatefulSet rollout
	// to settle before the verification window (StepAwaitingDiscard)
	// opens — DiscardReady stays False/Pending until every Pod is
	// running with the new password.
	StepAwaitingRollout RotationStep = "AwaitingRollout"

	// StepAwaitingDiscard is the action-availability steady state for
	// "discard is allowed": the rotation phase finished, the
	// post-distribute rollout settled, MySQL is holding a dual-password
	// set, and the CR is waiting for the operator to bump
	// discardGeneration.
	StepAwaitingDiscard RotationStep = "AwaitingDiscard"

	// StepApplyingDiscard means the discard phase is in flight.
	// Subsumes the post-distribute StatefulSet rollout wait:
	// ClusterManager checks the StatefulSet rollout state itself and
	// defers DISCARD until the rollout has completed.
	StepApplyingDiscard RotationStep = "ApplyingDiscard"

	// StepFinalizing means DISCARD is complete and the Reconciler
	// should promote the pending passwords to current in the source
	// Secret. Detected when newDiscard is true and DualPassword has
	// flipped back to False.
	StepFinalizing RotationStep = "Finalizing"

	// StepRotationRefused means the rotation could not start (e.g.
	// MySQLCluster has 0 replicas). Nothing has been mutated yet, so
	// the CR is still effectively idle for the purposes of accepting a
	// new rotationGeneration.
	StepRotationRefused RotationStep = "RotationRefused"

	// StepRotationBlocked means the rotation phase started but cannot
	// progress (e.g. MySQLCluster scaled to 0 mid-RETAIN, after pending
	// passwords were written to the source Secret).
	StepRotationBlocked RotationStep = "RotationBlocked"

	// StepDiscardRefused means the discard phase could not start (e.g.
	// MySQLCluster scaled to 0 between AwaitingDiscard and the first
	// DISCARD). Dual passwords are still held; manual recovery is
	// required if the operator wants to abandon the cycle.
	StepDiscardRefused RotationStep = "DiscardRefused"

	// StepDiscardBlocked means the discard phase started but cannot
	// progress (e.g. MySQLCluster scaled to 0 mid-DISCARD). Partial
	// DISCARD may have completed; manual recovery is required.
	StepDiscardBlocked RotationStep = "DiscardBlocked"

	// StepStalePending means the source Secret is in an inconsistent
	// state; manual recovery is required.
	StepStalePending RotationStep = "StalePending"
)

// Step derives the current internal workflow step from spec, status, and
// conditions. Pure function of persisted state. See the condition godocs
// (ConditionRotationReady / ConditionDiscardReady / ConditionDualPassword)
// for the semantics each branch projects onto.
func (cr *CredentialRotation) Step() RotationStep {
	// Stale states are stuck and take priority — neither generation
	// comparison nor sub-step conditions are meaningful while the
	// source Secret is inconsistent.
	if ConditionFalseWithReason(cr, ConditionRotationReady, ReasonStale) ||
		ConditionFalseWithReason(cr, ConditionDiscardReady, ReasonStale) {
		return StepStalePending
	}

	// Conditions have never been written → the controller hasn't
	// reconciled yet. Treat as idle so the very first reconcile can
	// initialise the cycle via handleStartRotation.
	if apimeta.FindStatusCondition(cr.Status.Conditions, ConditionRotationReady) == nil {
		return StepIdle
	}

	newRotation := cr.Spec.RotationGeneration > cr.Status.ObservedRotationGeneration
	newDiscard := cr.Spec.DiscardGeneration > cr.Status.ObservedDiscardGeneration
	dualPassword := apimeta.IsStatusConditionTrue(cr.Status.Conditions, ConditionDualPassword)
	rotationReady := apimeta.IsStatusConditionTrue(cr.Status.Conditions, ConditionRotationReady)

	if newRotation {
		// Stale RotationReady=True from the previous cycle: route back
		// to Idle so handleStartRotation seeds the new cycle.
		if rotationReady {
			return StepIdle
		}
		if ConditionFalseWithReason(cr, ConditionRotationReady, ReasonRefused) {
			return StepRotationRefused
		}
		if ConditionFalseWithReason(cr, ConditionRotationReady, ReasonBlocked) {
			return StepRotationBlocked
		}
		if !dualPassword {
			// RETAIN has not yet succeeded for this cycle.
			return StepApplyingRetain
		}
		// RETAIN succeeded; pending passwords need to be distributed.
		return StepDistributingPassword
	}

	if newDiscard {
		if ConditionFalseWithReason(cr, ConditionDiscardReady, ReasonRefused) {
			return StepDiscardRefused
		}
		if ConditionFalseWithReason(cr, ConditionDiscardReady, ReasonBlocked) {
			return StepDiscardBlocked
		}
		// Stale DiscardReady=True is fine here — dualPassword still
		// being True is enough to route to ApplyingDiscard so
		// handleApplyingDiscard can flip the condition.
		if dualPassword {
			return StepApplyingDiscard
		}
		// DISCARD succeeded (DualPassword flipped back to False);
		// the Reconciler now promotes the pending passwords.
		return StepFinalizing
	}

	// Generations match. DualPassword is the authoritative physical-state
	// signal — it cannot diverge from MySQL the way the Ready conditions
	// can if a CR was persisted by an older controller version.
	//   DualPassword=False ⇒ Idle.
	//   DualPassword=True  ⇒ AwaitingRollout until the Reconciler flips
	//                        DiscardReady=True after the post-distribute
	//                        rollout settles, then AwaitingDiscard.
	// Trusting DualPassword also handles legacy CRs that carried both
	// Ready conditions as True simultaneously (the previous "generation
	// tracking" semantics), since those always also have DiscardReady=True
	// and so map cleanly to AwaitingDiscard.
	if dualPassword {
		if apimeta.IsStatusConditionTrue(cr.Status.Conditions, ConditionDiscardReady) {
			return StepAwaitingDiscard
		}
		return StepAwaitingRollout
	}
	return StepIdle
}

// IsIdle reports whether the CR is in a state that permits the operator
// to start a new rotation cycle by bumping spec.rotationGeneration. This
// is true when the previous cycle has fully completed (rotation +
// discard), or when the previous rotation request was refused without
// any mutations, or when no cycle has been reconciled yet.
func (cr *CredentialRotation) IsIdle() bool {
	switch cr.Step() {
	case StepIdle, StepRotationRefused:
		return true
	default:
		return false
	}
}

// IsAwaitingDiscard reports whether the CR is in the steady state that
// follows a completed rotation phase, where the operator may bump
// spec.discardGeneration to trigger the discard phase.
func (cr *CredentialRotation) IsAwaitingDiscard() bool {
	return cr.Step() == StepAwaitingDiscard
}

// IsDeletable reports whether the CR may be deleted without abandoning
// an actively progressing cycle. The CR is deletable when:
//   - idle (no mutations exist),
//   - the most recent rotation request was Refused without mutations,
//   - the cycle is stuck in a state that the documented recovery
//     procedure resolves by deleting the CR (Blocked / Stale, either
//     phase).
//
// AwaitingDiscard and DiscardRefused are NOT deletable: MySQL still
// holds dual passwords, and a naïve deletion would leave behind state
// that no controller can recover from. Operators must use the
// documented recovery procedure (which scales the cluster down to
// transition into Blocked first).
func (cr *CredentialRotation) IsDeletable() bool {
	switch cr.Step() {
	case StepIdle,
		StepRotationRefused,
		StepRotationBlocked,
		StepDiscardBlocked,
		StepStalePending:
		return true
	default:
		return false
	}
}

// SetRotationReady sets or updates the RotationReady condition.
func (cr *CredentialRotation) SetRotationReady(status metav1.ConditionStatus, reason, message string) {
	cr.setCondition(ConditionRotationReady, status, reason, message)
}

// SetDiscardReady sets or updates the DiscardReady condition.
func (cr *CredentialRotation) SetDiscardReady(status metav1.ConditionStatus, reason, message string) {
	cr.setCondition(ConditionDiscardReady, status, reason, message)
}

// SetDualPassword sets or updates the DualPassword condition.
func (cr *CredentialRotation) SetDualPassword(status metav1.ConditionStatus, reason, message string) {
	cr.setCondition(ConditionDualPassword, status, reason, message)
}

// StampObservedGeneration sets status.observedGeneration to the current
// metadata.generation. Call this immediately before any Status().Update()
// so that clients (kstatus, ArgoCD, Flux) see that the controller has
// caught up with the latest spec change.
func (cr *CredentialRotation) StampObservedGeneration() {
	cr.Status.ObservedGeneration = cr.Generation
}

func (cr *CredentialRotation) setCondition(condType string, status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cr.Generation,
	})
}

// ConditionFalseWithReason reports whether the named condition is present
// with Status=False and the given Reason. The Stale / Refused / Blocked
// reasons that Step() checks all live on Status=False, so this helper
// captures that pattern.
func ConditionFalseWithReason(cr *CredentialRotation, condType, reason string) bool {
	cond := apimeta.FindStatusCondition(cr.Status.Conditions, condType)
	if cond == nil {
		return false
	}
	return cond.Status == metav1.ConditionFalse && cond.Reason == reason
}
