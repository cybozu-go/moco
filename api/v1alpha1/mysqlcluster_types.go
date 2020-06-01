/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

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

	// Replicas is a number of instances. Available values are 1, 3, and 5.
	// +kubebuilder:validation:Enum=1;3;5
	// +kubebuilder:default=1
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// PodTemplate is a `Pod` template for MySQL server container.
	// +kubebuilder:validation:Required
	PodTemplate PodTemplateSpec `json:"podTemplate"`

	// VolumeClaimTemplates is a list of `PersistentVolumeClaim` templates for MySQL server container.
	// +optional
	VolumeClaimTemplates []PersistentVolumeClaim `json:"volumeClaimTemplates"`

	// ServiceTemplate is a `Service` template for both master and slaves.
	// +optional
	ServiceTemplate *corev1.ServiceSpec `json:"serviceTemplate,omitempty"`

	// MySQLConfigMapName is a `ConfigMap` name of MySQL config.
	// +optional
	MySQLConfigMapName *string `json:"mySQLConfigMapName,omitempty"`

	// RootPasswordSecretName is a `Secret` name for root user config.
	// +kubebuilder:validation:Required
	RootPasswordSecretName string `json:"rootPasswordSecretName"`

	// ReplicationSourceSecretName is a `Secret` name which contains replication source info.
	// Keys must appear in https://dev.mysql.com/doc/refman/8.0/en/change-master-to.html.
	// If this field is given, the `MySQLCluster` works as an intermediate master.
	// +optional
	ReplicationSourceSecretName *string `json:"replicationSourceSecretName,omitempty"`

	// Restore is a Specification to perform Point-in-Time-Recovery from existing cluster.
	// If this field is filled, start restoring. This field is unable to be updated.
	// +optional
	Restore *RestoreSpec `json:"restore,omitempty"`
}

// ObjectMeta is metadata that all persisted resources must have, which includes all objects
// users must create.
// This is partially copied from metav1.ObjectMeta to cope with the following issue.
// https://github.com/kubernetes-sigs/controller-tools/issues/385
type ObjectMeta struct {
	// Name must be unique within a namespace. Is required when creating resources, although
	// some resources may allow a client to request the generation of an appropriate name
	// automatically. Name is primarily intended for creation idempotence and configuration
	// definition.
	// Cannot be updated.
	// More info: http://kubernetes.io/docs/user-guide/identifiers#names
	// +optional
	Name string `json:"name,omitempty" protobuf:"bytes,1,opt,name=name"`

	// Namespace defines the space within each name must be unique. An empty namespace is
	// equivalent to the "default" namespace, but "default" is the canonical representation.
	// Not all objects are required to be scoped to a namespace - the value of this field for
	// those objects will be empty.
	//
	// Must be a DNS_LABEL.
	// Cannot be updated.
	// More info: http://kubernetes.io/docs/user-guide/namespaces
	// +optional
	// Excluded to avoid users to create resources, e.g. StatefulSet, in an arbitrary namespace.
	// Namespace string `json:"namespace,omitempty" protobuf:"bytes,3,opt,name=namespace"`

	// Map of string keys and values that can be used to organize and categorize
	// (scope and select) objects. May match selectors of replication controllers
	// and services.
	// More info: http://kubernetes.io/docs/user-guide/labels
	// +optional
	Labels map[string]string `json:"labels,omitempty" protobuf:"bytes,4,rep,name=labels"`

	// Annotations is an unstructured key value map stored with a resource that may be
	// set by external tools to store and retrieve arbitrary metadata. They are not
	// queryable and should be preserved when modifying objects.
	// More info: http://kubernetes.io/docs/user-guide/annotations
	// +optional
	Annotations map[string]string `json:"annotations,omitempty" protobuf:"bytes,5,rep,name=annotations"`
}

// PodTemplateSpec describes the data a pod should have when created from a template
// This is slightly modified from corev1.PodTemplateSpec to cope with the following issue.
// https://github.com/kubernetes-sigs/controller-tools/issues/385
type PodTemplateSpec struct {
	// Standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	// +optional
	ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	// Specification of the desired behavior of the pod.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#spec-and-status
	// +optional
	Spec corev1.PodSpec `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
}

// PersistentVolumeClaim is a user's request for and claim to a persistent volume
// This is slightly modified from corev1.PersistentVolumeClaim to cope with the following issue.
// https://github.com/kubernetes-sigs/controller-tools/issues/385
type PersistentVolumeClaim struct {
	metav1.TypeMeta `json:",inline"`
	// Standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	// +optional
	ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	// Spec defines the desired characteristics of a volume requested by a pod author.
	// More info: https://kubernetes.io/docs/concepts/storage/persistent-volumes#persistentvolumeclaims
	// +optional
	Spec corev1.PersistentVolumeClaimSpec `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`

	// Status represents the current information/status of a persistent volume claim.
	// Read-only.
	// More info: https://kubernetes.io/docs/concepts/storage/persistent-volumes#persistentvolumeclaims
	// +optional
	Status corev1.PersistentVolumeClaimStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// RestoreSpec defines the desired spec of Point-in-Time-Recovery
type RestoreSpec struct {
	// SourceClusterName is a source `MySQLCluster` name.
	// +kubebuilder:validation:Required
	SourceClusterName string `json:"restore"`

	// PointInTime is a point-in-time of the state which the cluster is restored to.
	// +kubebuilder:validation:Required
	PointInTime metav1.Time `json:"pointInTime"`

	// ObjectStorageName is a name of `ObjectStorage`.
	// +kubebuilder:validation:Required
	ObjectStorageName string `json:"objectStorageName"`
}

// MySQLClusterStatus defines the observed state of MySQLCluster
type MySQLClusterStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// MySQLCluster is the Schema for the mysqlclusters API
type MySQLCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MySQLClusterSpec   `json:"spec,omitempty"`
	Status MySQLClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MySQLClusterList contains a list of MySQLCluster
type MySQLClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MySQLCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MySQLCluster{}, &MySQLClusterList{})
}
