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
	// +kubebuilder:validation:Minimum=1
	// +required
	RotationGeneration int64 `json:"rotationGeneration"`

	// DiscardGeneration is a monotonically increasing counter that triggers
	// the discard phase. Must satisfy 0 <= discardGeneration <= rotationGeneration.
	// Bumping this value (typically to match rotationGeneration) signals the
	// controller to discard the retained old password from the previous
	// rotation. The bump is only honored when Phase is Rotated.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	DiscardGeneration int64 `json:"discardGeneration"`
}

// CredentialRotationStatus defines the observed state of CredentialRotation.
type CredentialRotationStatus struct {
	// Phase is the current rotation phase.
	// +optional
	Phase RotationPhase `json:"phase,omitempty"`

	// RotationID is the UUID for this rotation cycle.
	// An empty value means no rotation has been started yet.
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

// RotationPhase represents the phase of a credential rotation.
// +kubebuilder:validation:Enum=Rotating;Retained;Rotated;Discarding;Discarded;Completed
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
