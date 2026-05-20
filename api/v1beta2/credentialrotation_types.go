package v1beta2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CredentialRotationSpec defines the desired state of CredentialRotation.
// The target MySQLCluster is identified by the CR's own name and namespace
// (CredentialRotation name must equal MySQLCluster name).
// +kubebuilder:validation:XValidation:rule="self.discardGeneration <= self.rotationGeneration",message="discardGeneration must be <= rotationGeneration"
type CredentialRotationSpec struct {
	// RotationGeneration is a monotonically increasing counter.
	// Incrementing this value triggers a new rotation cycle.
	// +kubebuilder:validation:Minimum=1
	// +required
	RotationGeneration int64 `json:"rotationGeneration"`

	// DiscardGeneration is a monotonically increasing counter that triggers
	// the discard step. Must satisfy 0 <= discardGeneration <= rotationGeneration.
	// Bumping this value (typically to match rotationGeneration) signals the
	// controller to discard the retained old password from the previous
	// rotation. The bump is only honored when the Rotating condition's
	// Reason is AwaitingDiscard.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	DiscardGeneration int64 `json:"discardGeneration"`
}

// CredentialRotationStatus defines the observed state of CredentialRotation.
type CredentialRotationStatus struct {
	// ObservedGeneration reflects the .metadata.generation that the
	// controller has most recently reconciled. Clients (kstatus, ArgoCD,
	// Flux) use this together with the Ready condition to determine
	// whether the controller has caught up with the latest spec change.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	ObservedGeneration int64 `json:"observedGeneration"`

	// Conditions represent the latest available observations of the
	// rotation state. See docs/designdoc/credential_rotation_crd.md
	// for canonical Type/Reason definitions.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// RotationID is the UUID for the in-flight rotation cycle.
	// Empty when no cycle is active.
	// +kubebuilder:validation:Pattern=`^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$`
	// +optional
	RotationID string `json:"rotationID,omitempty"`

	// ObservedRotationGeneration is the last rotationGeneration
	// that completed successfully.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	ObservedRotationGeneration int64 `json:"observedRotationGeneration"`

	// ObservedDiscardGeneration is the last discardGeneration
	// that completed successfully.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	ObservedDiscardGeneration int64 `json:"observedDiscardGeneration"`
}

// Condition type constants for CredentialRotation.Status.Conditions.
//
// The three condition types are orthogonal:
//   - Rotating describes whether a workflow is in flight and which sub-step
//     it has reached (via Reason).
//   - OldPasswordRetained describes MySQL's dual-password state.
//   - Ready describes whether spec.*Generation has been fully reconciled.
//
// See docs/designdoc/credential_rotation_crd.md for the full mapping
// between sub-steps and condition values, including the step matrix.
const (
	// ConditionRotating is True while a rotation/discard cycle is in
	// flight (including stuck states). The current sub-step is exposed
	// as the Reason field.
	//
	// In-flight Reason values (Status=True) form the workflow sequence:
	//   ApplyingRetain → DistributingPassword → AwaitingDiscard →
	//   WaitingForRollout → ApplyingDiscard → Finalizing.
	// Stuck-in-flight Reasons (Status=True) require manual recovery:
	//   RotationBlocked (cluster scaled to 0 mid-cycle, pending state
	//   already written) and StalePending (source Secret inconsistent).
	// Terminal Reasons (Status=False) mean no cycle is in flight:
	//   NotStarted (initial), Completed (last cycle finished),
	//   RotationRefused (rotation could not start; nothing mutated).
	ConditionRotating = "Rotating"

	// ConditionOldPasswordRetained is True while MySQL holds a
	// dual-password set for the MOCO system users (between successful
	// RETAIN and successful DISCARD on all instances).
	//
	// Reasons: NotRetained (initial), Retained (RETAIN succeeded),
	// Discarded (DISCARD succeeded).
	ConditionOldPasswordRetained = "OldPasswordRetained"

	// ConditionReady is True when the latest user request has been
	// fully reconciled into MySQL and Secrets:
	// observedRotationGeneration == spec.rotationGeneration AND
	// observedDiscardGeneration == spec.discardGeneration.
	//
	// Reasons: NotStarted (no cycle yet), InProgress (a cycle is in
	// flight, blocked, or refused), Completed (latest cycle finished).
	//
	// Use `kubectl wait --for=condition=Ready` to block on a full
	// rotate-and-discard cycle.
	ConditionReady = "Ready"
)

// Reason constants for the Rotating condition.

// In-flight sub-step Reasons (used with Status=True, workflow progressing):
const (
	// ReasonApplyingRetain — ClusterManager is executing
	// ALTER USER ... RETAIN CURRENT PASSWORD on all instances.
	ReasonApplyingRetain = "ApplyingRetain"
	// ReasonDistributingPassword — RETAIN finished; Reconciler is
	// applying pending passwords to per-namespace Secrets and triggering
	// a rolling restart.
	ReasonDistributingPassword = "DistributingPassword"
	// ReasonAwaitingDiscard — pending passwords distributed and the
	// rolling restart is complete; waiting for the operator to bump
	// spec.discardGeneration.
	ReasonAwaitingDiscard = "AwaitingDiscard"
	// ReasonWaitingForRollout — discardGeneration has been bumped;
	// Reconciler is waiting for any in-flight StatefulSet rollout to
	// finish before issuing DISCARD.
	ReasonWaitingForRollout = "WaitingForRollout"
	// ReasonApplyingDiscard — ClusterManager is executing
	// DISCARD OLD PASSWORD and auth plugin migration on all instances.
	ReasonApplyingDiscard = "ApplyingDiscard"
	// ReasonFinalizing — DISCARD finished; Reconciler is promoting the
	// pending passwords to current in the source Secret.
	ReasonFinalizing = "Finalizing"
)

// Stuck-in-flight Reasons (used with Status=True; manual recovery required):
const (
	// ReasonRotationBlocked — the cycle started (pending passwords are
	// already in the source Secret, possibly RETAIN was partially
	// applied) but cannot progress, typically because the MySQLCluster
	// has been scaled to 0 replicas mid-cycle.
	ReasonRotationBlocked = "RotationBlocked"
	// ReasonStalePending — the source Secret contains inconsistent
	// pending state (rotation ID mismatch, partial *_PENDING keys).
	// Recovery: delete this CR, clean the source Secret, restart Pods,
	// and recreate the CR. See the design doc's Recovery Procedures.
	ReasonStalePending = "StalePending"
)

// Idle Reasons for the Rotating condition (used with Status=False):
const (
	// ReasonNotStarted — the CR has not yet started any cycle
	// (initial state).
	ReasonNotStarted = "NotStarted"
	// ReasonCompleted — the latest cycle finished successfully.
	// rotationGeneration and discardGeneration have both been observed.
	ReasonCompleted = "Completed"
	// ReasonRotationRefused — a rotation request could not start
	// (e.g. cluster has 0 replicas at start). Nothing has been mutated
	// in MySQL or the source Secret.
	ReasonRotationRefused = "RotationRefused"
)

// Reason constants for the OldPasswordRetained condition.
const (
	// ReasonNotRetained — RETAIN has not been issued in the current
	// cycle (initial state for a new cycle).
	ReasonNotRetained = "NotRetained"
	// ReasonRetained — RETAIN succeeded on all instances.
	// MySQL is holding a dual-password set.
	ReasonRetained = "Retained"
	// ReasonDiscarded — DISCARD OLD PASSWORD succeeded on all
	// instances. MySQL has cleared the secondary password.
	ReasonDiscarded = "Discarded"
)

// Reason constants for the Ready condition.
// The True case reuses ReasonCompleted; the False cases reuse
// ReasonNotStarted (no cycle yet) or use ReasonInProgress (any cycle in
// flight, blocked, or refused).
const (
	// ReasonInProgress — a rotation/discard request is in flight from
	// the user's perspective (observed*Generation < spec.*Generation).
	ReasonInProgress = "InProgress"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Step",type="string",JSONPath=`.status.conditions[?(@.type=="Rotating")].reason`
// +kubebuilder:printcolumn:name="Retained",type="string",JSONPath=`.status.conditions[?(@.type=="OldPasswordRetained")].status`
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="RotationGen",type="integer",JSONPath=".spec.rotationGeneration"
// +kubebuilder:printcolumn:name="ObservedRotation",type="integer",JSONPath=".status.observedRotationGeneration"
// +kubebuilder:printcolumn:name="DiscardGen",type="integer",JSONPath=".spec.discardGeneration"
// +kubebuilder:printcolumn:name="ObservedDiscard",type="integer",JSONPath=".status.observedDiscardGeneration"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// CredentialRotation is the Schema for the credentialrotations API
type CredentialRotation struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec CredentialRotationSpec `json:"spec"`
	// +optional
	Status CredentialRotationStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// CredentialRotationList contains a list of CredentialRotation
type CredentialRotationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CredentialRotation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CredentialRotation{}, &CredentialRotationList{})
}
