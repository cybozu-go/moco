package v1beta2

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CurrentStep returns the Reason of the Rotating condition if it is True,
// otherwise the empty string. The empty string indicates the CR is not in
// an in-flight sub-step (idle, completed, refused, or condition absent).
func (cr *CredentialRotation) CurrentStep() string {
	cond := apimeta.FindStatusCondition(cr.Status.Conditions, ConditionRotating)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		return ""
	}
	return cond.Reason
}

// RotatingReason returns the current Reason of the Rotating condition
// regardless of its Status (or the empty string if the condition is absent).
// Use this when you need to distinguish terminal Status=False reasons such
// as Completed or RotationRefused from the absence of the condition; for
// "is the CR in an active sub-step?" prefer CurrentStep.
func (cr *CredentialRotation) RotatingReason() string {
	cond := apimeta.FindStatusCondition(cr.Status.Conditions, ConditionRotating)
	if cond == nil {
		return ""
	}
	return cond.Reason
}

// IsIdle reports whether the CR is in a state that permits starting a new
// rotation cycle. Idle means the Rotating condition is absent, or has
// Status=False with one of the terminal idle reasons.
func (cr *CredentialRotation) IsIdle() bool {
	cond := apimeta.FindStatusCondition(cr.Status.Conditions, ConditionRotating)
	if cond == nil {
		return true
	}
	if cond.Status != metav1.ConditionFalse {
		return false
	}
	switch cond.Reason {
	case ReasonNotStarted, ReasonCompleted, ReasonRotationRefused:
		return true
	default:
		return false
	}
}

// IsDeletable reports whether the CR may be deleted without abandoning an
// actively progressing rotation. The CR is deletable when idle, or when
// stuck in a state that the documented recovery procedure resolves by
// deleting the CR (RotationBlocked or StalePending).
func (cr *CredentialRotation) IsDeletable() bool {
	if cr.IsIdle() {
		return true
	}
	cond := apimeta.FindStatusCondition(cr.Status.Conditions, ConditionRotating)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		return false
	}
	switch cond.Reason {
	case ReasonRotationBlocked, ReasonStalePending:
		return true
	default:
		return false
	}
}

// SetRotating sets or updates the Rotating condition.
func (cr *CredentialRotation) SetRotating(status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:               ConditionRotating,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cr.Generation,
	})
}

// SetOldPasswordRetained sets or updates the OldPasswordRetained condition.
func (cr *CredentialRotation) SetOldPasswordRetained(status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:               ConditionOldPasswordRetained,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cr.Generation,
	})
}

// SetReady sets or updates the Ready condition.
func (cr *CredentialRotation) SetReady(status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:               ConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cr.Generation,
	})
}
