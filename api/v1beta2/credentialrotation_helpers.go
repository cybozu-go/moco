package v1beta2

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IsTerminal reports whether the cycle has ended (Succeeded or Failed).
func (cr *CredentialRotation) IsTerminal() bool {
	return cr.Status.Phase == PhaseSucceeded || cr.Status.Phase == PhaseFailed
}

// IsDiscardReady reports whether the verification window is open, i.e.
// the operator may set spec.discard to true. The validating webhook uses
// this to gate the discard flip.
func (cr *CredentialRotation) IsDiscardReady() bool {
	return apimeta.IsStatusConditionTrue(cr.Status.Conditions, ConditionDiscardReady)
}

// SetPhase sets the phase and the human-readable message together. It
// must be persisted in the same Status().Update() as any condition
// changes that belong to the transition, so a single read never sees the
// phase and the conditions disagree.
func (cr *CredentialRotation) SetPhase(phase RotationPhase, message string) {
	cr.Status.Phase = phase
	cr.Status.Message = message
}

// SetDiscardReady sets or updates the DiscardReady condition.
func (cr *CredentialRotation) SetDiscardReady(status metav1.ConditionStatus, reason, message string) {
	cr.setCondition(ConditionDiscardReady, status, reason, message)
}

// SetDualPassword sets or updates the DualPassword condition.
func (cr *CredentialRotation) SetDualPassword(status metav1.ConditionStatus, reason, message string) {
	cr.setCondition(ConditionDualPassword, status, reason, message)
}

// SetFinished sets or updates the Finished condition.
func (cr *CredentialRotation) SetFinished(status metav1.ConditionStatus, reason, message string) {
	cr.setCondition(ConditionFinished, status, reason, message)
}

// InitConditions seeds the three conditions with their initial in-flight
// values. Called on the first status write of a cycle (including a
// Blocked start), so every phase always carries a complete condition set.
func (cr *CredentialRotation) InitConditions() {
	if apimeta.FindStatusCondition(cr.Status.Conditions, ConditionDiscardReady) == nil {
		cr.SetDiscardReady(metav1.ConditionFalse, ReasonPending,
			"The verification window has not opened yet.")
	}
	if apimeta.FindStatusCondition(cr.Status.Conditions, ConditionDualPassword) == nil {
		cr.SetDualPassword(metav1.ConditionFalse, ReasonNotRetained,
			"No RETAIN has been issued in this cycle yet.")
	}
	if apimeta.FindStatusCondition(cr.Status.Conditions, ConditionFinished) == nil {
		cr.SetFinished(metav1.ConditionFalse, ReasonRunning,
			"The rotation cycle is in progress.")
	}
}

// Fail moves the CR to the terminal Failed phase: it sets the phase, the
// diagnosis message, CompletionTime, and Finished=True (reason=Failed) in
// one in-memory mutation. The caller persists it with a single status
// update and emits the matching Warning Event.
func (cr *CredentialRotation) Fail(message string, now metav1.Time) {
	cr.InitConditions()
	cr.SetPhase(PhaseFailed, message)
	cr.Status.CompletionTime = &now
	cr.SetFinished(metav1.ConditionTrue, ReasonFailed, message)
}

// Succeed moves the CR to the terminal Succeeded phase.
func (cr *CredentialRotation) Succeed(message string, now metav1.Time) {
	cr.SetPhase(PhaseSucceeded, message)
	cr.Status.CompletionTime = &now
	cr.SetFinished(metav1.ConditionTrue, ReasonSucceeded, "The rotation cycle completed.")
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

// HasStatus reports whether the controller has ever written the status.
// Together with the ownerReference this defines staleness: status is only
// ever written after adoption (the ownerReference is added first), so a
// CR with a status but no MySQLCluster ownerReference can only be an
// orphan left over from a deleted cluster.
func (cr *CredentialRotation) HasStatus() bool {
	return cr.Status.Phase != "" || len(cr.Status.Conditions) > 0 || cr.Status.RotationID != ""
}

// IsConditionFalseWithReason reports whether the named condition is present
// with Status=False and the given Reason. Named to match
// apimeta.IsStatusConditionTrue / IsStatusConditionFalse /
// IsStatusConditionPresentAndEqual.
func IsConditionFalseWithReason(cr *CredentialRotation, condType, reason string) bool {
	cond := apimeta.FindStatusCondition(cr.Status.Conditions, condType)
	if cond == nil {
		return false
	}
	return cond.Status == metav1.ConditionFalse && cond.Reason == reason
}

// IsStaleFor reports whether the CR is a leftover from a different
// MySQLCluster incarnation and must not act on (or be acted on for) the
// given live cluster. A CR is stale when its MySQLCluster ownerReference
// carries a UID different from the live cluster's, or — without a
// MySQLCluster ownerReference — when it has a non-empty status (an orphan;
// see HasStatus) or is older than the live cluster. The age rule covers a
// CR that was never reconciled before its original cluster was deleted and
// recreated under the same name (controller downtime or backlog): a
// genuinely fresh CR is always younger than its cluster, because the
// create webhook requires the cluster to exist.
func (cr *CredentialRotation) IsStaleFor(cluster *MySQLCluster) bool {
	for _, ref := range cr.OwnerReferences {
		if ref.Kind == "MySQLCluster" && ref.APIVersion == GroupVersion.String() {
			return ref.UID != cluster.UID
		}
	}
	return cr.HasStatus() || cr.CreationTimestamp.Before(&cluster.CreationTimestamp)
}
