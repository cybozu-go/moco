package v1beta1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MySQLClusterSpec defines the desired state of MySQLCluster
type MySQLClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Replicas is the number of instances. Available values are 1, 3, and 5.
	// +kubebuilder:validation:Enum=1;3;5
	// +kubebuilder:default=1
	// +optional
	Replicas int32 `json:"replicas"`

	// PodTemplate is a `Pod` template for MySQL server container.
	PodTemplate PodTemplateSpec `json:"podTemplate"`

	// DataVolumeClaimTemplateSpec is a `PersistentVolumeClaimSpec` template for the MySQL data volume.
	DataVolumeClaimTemplateSpec corev1.PersistentVolumeClaimSpec `json:"dataVolumeClaimTemplateSpec"`

	// VolumeClaimTemplates is a list of `PersistentVolumeClaim` templates for MySQL server container, except for the MySQL data volume.
	// +optional
	VolumeClaimTemplates []PersistentVolumeClaim `json:"volumeClaimTemplates,omitempty"`

	// ServiceTemplate is a `Service` template for both primary and replicas.
	// +optional
	ServiceTemplate *ServiceTemplate `json:"serviceTemplate,omitempty"`

	// MySQLConfigMapName is a `ConfigMap` name of MySQL config.
	// +optional
	MySQLConfigMapName *string `json:"mysqlConfigMapName,omitempty"`

	// ReplicationSourceSecretName is a `Secret` name which contains replication source info.
	// Keys must appear in https://dev.mysql.com/doc/refman/8.0/en/change-master-to.html.
	// If this field is given, the `MySQLCluster` works as an intermediate primary.
	// +optional
	ReplicationSourceSecretName *string `json:"replicationSourceSecretName,omitempty"`

	// ServerIDBase, if set, will become the base number of server-id of each MySQL
	// instance of this cluster.  For example, if this is 100, the server-ids will be
	// 100, 101, 102, and so on.
	// If the field is not given, MOCO automatically sets a random number.
	// +optional
	ServerIDBase *uint32 `json:"serverIDBase,omitempty"`

	// LogRotationSchedule is a schedule in Cron format for MySQL log rotation.
	// If not set, the default is to rotate logs every 5 minutes.
	// +kubebuilder:validation:Pattern=`^(@(annually|yearly|monthly|weekly|daily|hourly|reboot))|(@every (\d+(ns|us|µs|ms|s|m|h))+)|((((\d+,)+\d+|(\d+(\/|-)\d+)|\d+|\*) ?){5,7})$`
	// +optional
	LogRotationSchedule string `json:"logRotationSchedule,omitempty"`

	// Restore is the specification to perform Point-in-Time-Recovery from existing cluster.
	// If this field is filled, start restoring. This field is unable to be updated.
	// +optional
	Restore *RestoreSpec `json:"restore,omitempty"`

	// DisableErrorLogContainer controls whether to add a log agent container name of the "err-log" to handle mysqld error logs.
	// If set to true, no log agent container will be added. The default is false.
	// If false and the user-defined ".spec.podTemplate.spec.containers" contained a container named "err-log",
	// it will be merged with the default container definition using StrategicMergePatch.
	// +optional
	DisableErrorLogContainer bool `json:"disableErrorLogContainer"`

	// DisableSlowQueryLogContainer controls whether to add a log agent container name of the "slow-log" to handle mysqld slow query logs.
	// If set to true, no log agent container will be added. The default is false.
	// If false and the user-defined ".spec.podTemplate.spec.containers" contained a container named "slow-log",
	// it will be merged with the default container definition using StrategicMergePatch.
	// +optional
	DisableSlowQueryLogContainer bool `json:"disableSlowQueryLogContainer"`
}

// ObjectMeta is metadata of objects.
// This is partially copied from metav1.ObjectMeta.
type ObjectMeta struct {
	// Name is the name of the object.
	// +optional
	Name string `json:"name,omitempty"`

	// Labels is a map of string keys and values.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations is a map of string keys and values.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// PodTemplateSpec describes the data a pod should have when created from a template.
// This is slightly modified from corev1.PodTemplateSpec.
type PodTemplateSpec struct {
	// Standard object's metadata.  The name in this metadata is ignored.
	// +optional
	ObjectMeta `json:"metadata,omitempty"`

	// Specification of the desired behavior of the pod.
	// The name of the MySQL server container in this spec must be `mysqld`.
	Spec corev1.PodSpec `json:"spec"`
}

// PersistentVolumeClaim is a user's request for and claim to a persistent volume.
// This is slightly modified from corev1.PersistentVolumeClaim.
type PersistentVolumeClaim struct {
	// Standard object's metadata.
	ObjectMeta `json:"metadata"`

	// Spec defines the desired characteristics of a volume requested by a pod author.
	Spec corev1.PersistentVolumeClaimSpec `json:"spec"`
}

// ServiceTemplate defines the desired spec and annotations of Service
type ServiceTemplate struct {
	// Standard object's metadata.  Only `annotations` and `labels` are valid.
	// +optional
	ObjectMeta `json:"metadata,omitempty"`

	// Spec is the ServiceSpec
	// +optional
	Spec *corev1.ServiceSpec `json:"spec,omitempty"`
}

// RestoreSpec defines the desired spec of Point-in-Time-Recovery
// TBD
type RestoreSpec struct {
	// // SourceClusterName is the name of the source `MySQLCluster`.
	// SourceClusterName string `json:"restore"`

	// // PointInTime is the point-in-time of the state which the cluster is restored to.
	// PointInTime metav1.Time `json:"pointInTime"`
}

// MySQLClusterStatus defines the observed state of MySQLCluster
type MySQLClusterStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Conditions is an array of conditions.
	// +optional
	Conditions []MySQLClusterCondition `json:"conditions,omitempty"`

	// Ready represents the status of readiness.
	Ready corev1.ConditionStatus `json:"ready"`

	// CurrentPrimaryIndex is the ordinal of the current primary in StatefulSet.
	// +optional
	CurrentPrimaryIndex *int `json:"currentPrimaryIndex,omitempty"`

	// SyncedReplicas is the number of synced instances including the primary.
	SyncedReplicas int `json:"syncedReplicas"`
}

// MySQLClusterCondition defines the condition of MySQLCluster.
type MySQLClusterCondition struct {
	// Type is the type of the condition.
	Type MySQLClusterConditionType `json:"type"`

	// Status is the status of the condition.
	Status corev1.ConditionStatus `json:"status"`

	// Reason is a one-word CamelCase reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty"`

	// Message is a human-readable message indicating details about last transition.
	// +optional
	Message string `json:"message,omitempty"`

	// LastTransitionTime is the last time the condition transits from one status to another.
	LastTransitionTime metav1.Time `json:"lastTransitionTime"`
}

// MySQLClusterConditionType is the type of MySQLCluster condition.
// +kubebuilder:validation:Enum=Initialized;Healthy;Available;OutOfSync;Failure;Violation
type MySQLClusterConditionType string

// Valid values for MySQLClusterConditionType
const (
	ConditionInitialized MySQLClusterConditionType = "Initialized"
	ConditionHealthy     MySQLClusterConditionType = "Healthy"
	ConditionAvailable   MySQLClusterConditionType = "Available"
	ConditionOutOfSync   MySQLClusterConditionType = "OutOfSync"
	ConditionFailure     MySQLClusterConditionType = "Failure"
	ConditionViolation   MySQLClusterConditionType = "Violation"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="PRIMARY",type="integer",JSONPath=".status.currentPrimaryIndex"
// +kubebuilder:printcolumn:name="SYNCED",type="integer",JSONPath=".status.syncedReplicas"

// MySQLCluster is the Schema for the mysqlclusters API
type MySQLCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MySQLClusterSpec   `json:"spec,omitempty"`
	Status MySQLClusterStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// MySQLClusterList contains a list of MySQLCluster
type MySQLClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MySQLCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MySQLCluster{}, &MySQLClusterList{})
}
