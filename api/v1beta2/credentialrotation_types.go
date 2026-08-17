package v1beta2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CredentialRotationSpec defines the desired state of CredentialRotation.
//
// The CR is single-use: creating it starts one rotation cycle, and the
// target MySQLCluster is identified by the CR's own name and namespace
// (CredentialRotation name must equal MySQLCluster name). The spec is
// immutable except for the one-way Discard flip.
type CredentialRotationSpec struct {
	// Discard requests the discard phase of the cycle: it removes the old
	// (retained) passwords from MySQL and completes the rotation. It must
	// be false at create time and may only be set to true while the
	// verification window is open (DiscardReady=True). It can never be
	// set back to false. Discard is a bool rather than a stage enum on
	// purpose: the spec has exactly one legal transition, and a one-way
	// flag cannot express an invalid request.
	// +optional
	// +kubebuilder:default=false
	Discard bool `json:"discard,omitempty"`
}

// RotationPhase is the workflow position of a rotation cycle.
//
// The value set is open: new values may be added in future versions, and
// clients must handle unknown values gracefully.
type RotationPhase string

const (
	// PhaseApplyingRetain — pending passwords are seeded; the clustering
	// manager is applying ALTER USER ... RETAIN on every instance.
	PhaseApplyingRetain RotationPhase = "ApplyingRetain"

	// PhasePromoting — RETAIN succeeded everywhere; the reconciler
	// promotes the pending passwords to current in the controller Secret.
	PhasePromoting RotationPhase = "Promoting"

	// PhaseAwaitingRollout — waiting for the promoted passwords to be
	// distributed and for the StatefulSet rolling restart to settle.
	PhaseAwaitingRollout RotationPhase = "AwaitingRollout"

	// PhaseAwaitingDiscard — the verification window. The operator may
	// set spec.discard to true.
	PhaseAwaitingDiscard RotationPhase = "AwaitingDiscard"

	// PhaseApplyingDiscard — the clustering manager is running
	// DISCARD OLD PASSWORD and the auth plugin migration.
	PhaseApplyingDiscard RotationPhase = "ApplyingDiscard"

	// PhaseFinalizing — the reconciler removes the rotation bookkeeping
	// keys from the controller Secret.
	PhaseFinalizing RotationPhase = "Finalizing"

	// PhaseBlocked — the cluster was scaled to 0 replicas mid-cycle. The
	// cycle resumes automatically on scale-up; status.message records
	// where it stopped. Pauses caused by the stop annotations keep their
	// current phase instead of entering Blocked.
	PhaseBlocked RotationPhase = "Blocked"

	// PhaseSucceeded — the full cycle completed. The controller deletes
	// the CR after a TTL. Terminal.
	PhaseSucceeded RotationPhase = "Succeeded"

	// PhaseFailed — an unrecoverable inconsistency was detected. The CR
	// stays (and keeps the name occupied) until the operator follows the
	// recovery procedure named in status.message and deletes it. Terminal.
	PhaseFailed RotationPhase = "Failed"
)

// CredentialRotationStatus defines the observed state of CredentialRotation.
type CredentialRotationStatus struct {
	// ObservedGeneration reflects the .metadata.generation that the
	// controller has most recently reconciled.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	ObservedGeneration int64 `json:"observedGeneration"`

	// Phase is the workflow position. Empty until the first reconcile.
	// The value set is open: clients must tolerate unknown values.
	// +optional
	Phase RotationPhase `json:"phase,omitempty"`

	// Message is a human-readable detail for the current phase. On Failed
	// it explains what went wrong and names the matching recovery
	// procedure; on Blocked or a pause it names the obstacle; on
	// Succeeded it shows the scheduled TTL deletion time.
	// +optional
	Message string `json:"message,omitempty"`

	// RotationID is the UUID for this cycle. Set when the pending
	// passwords are seeded. The value, if non-empty, is a canonical
	// 36-character UUID.
	// +kubebuilder:validation:Pattern=`^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$`
	// +kubebuilder:validation:MaxLength=36
	// +optional
	RotationID string `json:"rotationID,omitempty"`

	// CompletionTime is set when the phase turns terminal (Succeeded or
	// Failed). The TTL deadline for the automatic deletion of a Succeeded
	// CR is computed from it.
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// Conditions represent the latest available observations. Three
	// conditions are exposed: DiscardReady (the verification-window
	// gate), DualPassword (MySQL's physical state), and Finished (the
	// machine-readable terminal signal). The workflow position lives in
	// Phase, not in the conditions.
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// Condition types for CredentialRotation.Status.Conditions.
const (
	// ConditionDiscardReady is True while the verification window is open
	// (phase AwaitingDiscard): the rotation phase finished, the
	// post-promotion rollout settled, MySQL holds a dual-password set,
	// and the operator may set spec.discard to true.
	ConditionDiscardReady = "DiscardReady"

	// ConditionDualPassword is True while MySQL is holding a dual-password
	// set for the MOCO system users (between successful RETAIN and
	// successful DISCARD). The condition reports an in-progress physical
	// state in MySQL — True is the affirmative observation, not a "good"
	// state, mirroring how Kubernetes uses conditions such as
	// MemoryPressure.
	ConditionDualPassword = "DualPassword"

	// ConditionFinished is True once the phase is terminal; the reason
	// tells which way (Succeeded / Failed). This is the terminal signal
	// for kubectl wait and health tooling — unlike a wait on a single
	// phase value, it terminates on both outcomes.
	ConditionFinished = "Finished"
)

// Reason constants for CredentialRotation.Status.Conditions.
//
// Each Reason has a single meaning and a fixed Status value on the
// condition that uses it (per Kubernetes API conventions).
const (
	// ReasonRolloutSettled — DiscardReady is True: the promoted passwords
	// were distributed and the rolling restart settled, so the
	// verification window is open.
	ReasonRolloutSettled = "RolloutSettled"

	// ReasonPending — DiscardReady is False and no error is recorded: the
	// window has not opened yet, or the discard is running. Also carried
	// through a rotate-side Blocked phase.
	ReasonPending = "Pending"

	// ReasonBlocked — DiscardReady is False because the discard cannot
	// progress (the cluster was scaled to 0 replicas after spec.discard
	// was set).
	ReasonBlocked = "Blocked"

	// ReasonRetained — DualPassword is True: MySQL is holding a
	// dual-password set on all system users (RETAIN has been applied).
	ReasonRetained = "Retained"

	// ReasonNotRetained — DualPassword is False: MySQL is not currently
	// holding a dual-password set (initial state for a cycle, or already
	// discarded).
	ReasonNotRetained = "NotRetained"

	// ReasonRunning — Finished is False: the cycle is in progress.
	ReasonRunning = "Running"

	// ReasonSucceeded — Finished is True and the cycle completed.
	ReasonSucceeded = "Succeeded"

	// ReasonFailed — Finished is True and the cycle failed; see
	// status.message for the diagnosis and the recovery procedure.
	ReasonFailed = "Failed"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Discard",type="boolean",JSONPath=".spec.discard"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Message",type="string",priority=1,JSONPath=".status.message"

// CredentialRotation is the Schema for the credentialrotations API
type CredentialRotation struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Spec CredentialRotationSpec `json:"spec,omitempty"`
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
