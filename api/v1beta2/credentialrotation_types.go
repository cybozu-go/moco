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
	// rotation. The bump is only honored while the CR is in the
	// awaiting-discard steady state (DiscardReady=True, DualPassword=True).
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	DiscardGeneration int64 `json:"discardGeneration"`
}

// CredentialRotationStatus defines the observed state of CredentialRotation.
type CredentialRotationStatus struct {
	// ObservedGeneration reflects the .metadata.generation that the
	// controller has most recently reconciled. Clients (kstatus, ArgoCD,
	// Flux) use this together with the RotationReady/DiscardReady
	// conditions to determine whether the controller has caught up with
	// the latest spec change.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	ObservedGeneration int64 `json:"observedGeneration"`

	// Conditions represent the latest available observations of the
	// rotation state. Three orthogonal observations are exposed:
	// RotationReady, DiscardReady, and DualPassword. The internal
	// workflow step is derived from their combination together with the
	// spec/observed generation comparison; it is not stored on the CR.
	// See docs/designdoc/credential_rotation_crd.md for the canonical
	// Type/Reason definitions and the step matrix.
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// RotationID is the UUID for the in-flight rotation cycle.
	// Empty when no cycle is active. The value, if non-empty, is a
	// canonical 36-character UUID.
	// +kubebuilder:validation:Pattern=`^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$`
	// +kubebuilder:validation:MaxLength=36
	// +optional
	RotationID string `json:"rotationID,omitempty"`

	// ObservedRotationGeneration is the last rotationGeneration whose
	// rotation phase (RETAIN + pending-password distribution) completed
	// successfully. Equality with spec.rotationGeneration is a necessary
	// condition for the cycle to leave the rotation phase, but not
	// sufficient on its own: RotationReady=True is only set at the very
	// end of the full cycle (after the discard phase finalises).
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	ObservedRotationGeneration int64 `json:"observedRotationGeneration"`

	// ObservedDiscardGeneration is the last discardGeneration that
	// completed successfully. Equality with spec.discardGeneration is a
	// necessary condition for the cycle to leave the discard phase, but
	// not sufficient on its own: DiscardReady=True is only set in the
	// awaiting-discard steady state, which additionally requires
	// DualPassword=True and the post-distribute rollout to have settled.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	ObservedDiscardGeneration int64 `json:"observedDiscardGeneration"`
}

// Condition types for CredentialRotation.Status.Conditions.
//
// Each condition is an action-availability guard: True signals that the
// operator may now perform the corresponding action. RotationReady and
// DiscardReady are mutually exclusive (both cannot be True at the same
// time) because the cycle state itself is mutually exclusive: a CR is
// either idle (rotate allowed, no dual password held) or awaiting-discard
// (discard allowed, dual password held). DualPassword reports MySQL's
// physical state independently of action availability.
//
// The internal workflow step is derived from the combination of these
// conditions plus spec/status generation comparisons; it is not stored
// as a separate Phase field.
const (
	// ConditionRotationReady is True iff the CR is in the idle steady
	// state — no rotation or discard is in flight, no dual password is
	// held, and the operator may bump spec.rotationGeneration to start
	// a new cycle. Aligned with IsIdle().
	ConditionRotationReady = "RotationReady"

	// ConditionDiscardReady is True iff the CR is in the awaiting-discard
	// steady state — the rotation phase has finished, MySQL is holding a
	// dual-password set, and the operator may bump spec.discardGeneration
	// to start the discard phase. Aligned with IsAwaitingDiscard().
	ConditionDiscardReady = "DiscardReady"

	// ConditionDualPassword is True while MySQL is holding a dual-password
	// set for the MOCO system users (between successful RETAIN and
	// successful DISCARD). The condition reports an in-progress physical
	// state in MySQL — True is the affirmative observation, not a "good"
	// state. The cycle's terminal state has DualPassword=False, mirroring
	// how Kubernetes uses conditions such as MemoryPressure where True
	// describes the situation, not health.
	ConditionDualPassword = "DualPassword"
)

// Reason constants for CredentialRotation.Status.Conditions.
//
// Each Reason has a single meaning across every condition that uses it
// (per Kubernetes API conventions). The same identifier never means
// different things in different conditions.
const (
	// ReasonReconciled — the condition's Status is True via normal
	// reconciliation: RotationReady=True means idle, DiscardReady=True
	// means awaiting-discard. Used by RotationReady and DiscardReady.
	ReasonReconciled = "Reconciled"

	// ReasonPending — the condition's Status is False because the cycle
	// is not in the state that would make it True. For RotationReady
	// this covers everything that is not idle (an in-flight cycle, or
	// the awaiting-discard steady state where DiscardReady is the True
	// condition instead). For DiscardReady this covers everything that
	// is not the awaiting-discard window (idle, in-flight rotation,
	// in-flight discard). Used by RotationReady and DiscardReady.
	ReasonPending = "Pending"

	// ReasonRefused — the requested operation could not start (e.g.
	// MySQLCluster has 0 replicas). Nothing has been mutated in MySQL
	// or the source Secret. Used by RotationReady and DiscardReady.
	ReasonRefused = "Refused"

	// ReasonBlocked — a cycle that previously started cannot progress
	// (e.g. MySQLCluster scaled to 0 after pending passwords were
	// written). Manual scale-up or the documented recovery procedure is
	// required. Used by RotationReady and DiscardReady.
	ReasonBlocked = "Blocked"

	// ReasonStale — persisted state (typically the source Secret) is
	// inconsistent (rotation ID mismatch, partial pending keys, etc.).
	// Manual recovery is required. Used by RotationReady and
	// DiscardReady.
	ReasonStale = "Stale"

	// ReasonRetained — DualPassword is True because MySQL is holding a
	// dual-password set on all system users (RETAIN has been applied).
	ReasonRetained = "Retained"

	// ReasonNotRetained — DualPassword is False; MySQL is not currently
	// holding a dual-password set (initial state for a cycle, or already
	// discarded).
	ReasonNotRetained = "NotRetained"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="RotReady",type="string",JSONPath=`.status.conditions[?(@.type=="RotationReady")].status`
// +kubebuilder:printcolumn:name="DiscReady",type="string",JSONPath=`.status.conditions[?(@.type=="DiscardReady")].status`
// +kubebuilder:printcolumn:name="DualPassword",type="string",JSONPath=`.status.conditions[?(@.type=="DualPassword")].status`
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
