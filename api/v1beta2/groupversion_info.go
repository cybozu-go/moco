// Package v1beta2 contains API Schema definitions for the moco.cybozu.com v1beta2 API group
// +kubebuilder:object:generate=true
// +groupName=moco.cybozu.com
package v1beta2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const MySQLClusterKind = "MySQLCluster"

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "moco.cybozu.com", Version: "v1beta2"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&MySQLCluster{}, &MySQLClusterList{},
		&BackupPolicy{}, &BackupPolicyList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
