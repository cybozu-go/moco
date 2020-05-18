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
	// +kubebuilder:default=1
	// +kubebuilder:validation:Enum=1;3;5
	Replicas int `json:"replicas"`

	// PodTemplate is a `Pod` template for MySQL serer container.
	// +kubebuilder:validation:Required
	PodTemplate corev1.PodTemplateSpec `json:"podTemplate"`

	// VolumeClaimTemplate is a `PersistentVolumeClaim` template for MySQL serer container.
	// +kubebuilder:validation:Required
	VolumeClaimTemplate corev1.PersistentVolumeClaim `json:"volumeClaimTemplate"`

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
