package v1beta2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CredentialRotationSpec defines the desired state of CredentialRotation.
// The target MySQLCluster is identified by the CR's own name and namespace
// (CredentialRotation name must equal MySQLCluster name).
type CredentialRotationSpec struct {
	// RotationGeneration is a monotonically increasing counter.
	// Incrementing this value triggers a new rotation cycle.
	// +optional
	RotationGeneration int64 `json:"rotationGeneration,omitempty"`

	// DiscardOldPassword triggers the discard phase.
	// Can only be set to true when Phase is Rotated.
	// Must be reset to false when incrementing rotationGeneration.
	// +optional
	DiscardOldPassword bool `json:"discardOldPassword,omitempty"`
}

// CredentialRotationStatus defines the observed state of CredentialRotation.
type CredentialRotationStatus struct {
	// Phase is the current rotation phase.
	// +optional
	Phase RotationPhase `json:"phase,omitempty"`

	// RotationID is the UUID for this rotation cycle.
	// +optional
	RotationID string `json:"rotationID,omitempty"`

	// ObservedRotationGeneration is the last rotationGeneration
	// that completed successfully.
	// +optional
	ObservedRotationGeneration int64 `json:"observedRotationGeneration,omitempty"`
}

// RotationPhase represents the phase of a credential rotation.
type RotationPhase string

const (
	RotationPhaseRotating   RotationPhase = "Rotating"
	RotationPhaseRetained   RotationPhase = "Retained"
	RotationPhaseRotated    RotationPhase = "Rotated"
	RotationPhaseDiscarding RotationPhase = "Discarding"
	RotationPhaseDiscarded  RotationPhase = "Discarded"
	RotationPhaseCompleted  RotationPhase = "Completed"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Generation",type="integer",JSONPath=".spec.rotationGeneration"
// +kubebuilder:printcolumn:name="Observed",type="integer",JSONPath=".status.observedRotationGeneration"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// CredentialRotation is the Schema for the credentialrotations API
type CredentialRotation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CredentialRotationSpec   `json:"spec,omitempty"`
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
