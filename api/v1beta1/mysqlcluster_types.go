package v1beta1

import (
	"fmt"

	"github.com/cybozu-go/moco/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
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
	Replicas int32 `json:"replicas,omitempty"`

	// PodTemplate is a `Pod` template for MySQL server container.
	PodTemplate PodTemplateSpec `json:"podTemplate"`

	// VolumeClaimTemplates is a list of `PersistentVolumeClaim` templates for MySQL server container.
	// A claim named "mysql-data" must be included in the list.
	// +kubebuilder:validation:MinItems=1
	VolumeClaimTemplates []PersistentVolumeClaim `json:"volumeClaimTemplates"`

	// ServiceTemplate is a `Service` template for both primary and replicas.
	// +optional
	ServiceTemplate *ServiceTemplate `json:"serviceTemplate,omitempty"`

	// MySQLConfigMapName is a `ConfigMap` name of MySQL config.
	// +optional
	MySQLConfigMapName *string `json:"mysqlConfigMapName,omitempty"`

	// ReplicationSourceSecretName is a `Secret` name which contains replication source info.
	// If this field is given, the `MySQLCluster` works as an intermediate primary.
	// +optional
	ReplicationSourceSecretName *string `json:"replicationSourceSecretName,omitempty"`

	// ServerIDBase, if set, will become the base number of server-id of each MySQL
	// instance of this cluster.  For example, if this is 100, the server-ids will be
	// 100, 101, 102, and so on.
	// If the field is not given or zero, MOCO automatically sets a random positive integer.
	// +optional
	ServerIDBase int32 `json:"serverIDBase,omitempty"`

	// MaxDelaySeconds, if set, configures the readiness probe of mysqld container.
	// For a replica mysqld instance, if it is delayed to apply transactions over this threshold,
	// the mysqld instance will be marked as non-ready.
	// The default is 60 seconds.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxDelaySeconds int `json:"maxDelaySeconds,omitempty"`

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
	DisableErrorLogContainer bool `json:"disableErrorLogContainer,omitempty"`

	// DisableSlowQueryLogContainer controls whether to add a log agent container name of the "slow-log" to handle mysqld slow query logs.
	// If set to true, no log agent container will be added. The default is false.
	// If false and the user-defined ".spec.podTemplate.spec.containers" contained a container named "slow-log",
	// it will be merged with the default container definition using StrategicMergePatch.
	// +optional
	DisableSlowQueryLogContainer bool `json:"disableSlowQueryLogContainer,omitempty"`
}

func (s MySQLClusterSpec) validateCreate() field.ErrorList {
	var allErrs field.ErrorList
	p := field.NewPath("spec")
	pp := p.Child("volumeClaimTemplates")
	ok := false
	for _, vc := range s.VolumeClaimTemplates {
		if vc.Name == constants.MySQLDataVolumeName {
			ok = true
			break
		}
	}
	if !ok {
		allErrs = append(allErrs, field.Required(pp, fmt.Sprintf("required volume claim template %s is missing", constants.MySQLDataVolumeName)))
	}

	pp = p.Child("serverIDBase")
	if s.ServerIDBase <= 0 {
		allErrs = append(allErrs, field.Invalid(pp, s.ServerIDBase, "serverIDBase must be a positive integer"))
	}

	p = p.Child("podTemplate", "spec")

	pp = p.Child("containers")
	mysqldIndex := -1
	for i, container := range s.PodTemplate.Spec.Containers {
		if container.Name == constants.MysqldContainerName {
			mysqldIndex = i
		}
		if container.Name == constants.AgentContainerName {
			allErrs = append(allErrs, field.Invalid(pp.Index(i), container.Name, "reserved container name"))
		}
		if container.Name == constants.SlowQueryLogAgentContainerName && !s.DisableSlowQueryLogContainer {
			allErrs = append(allErrs, field.Forbidden(pp.Index(i), "reserved container name"))
		}
		if container.Name == constants.ErrorLogAgentContainerName && !s.DisableErrorLogContainer {
			allErrs = append(allErrs, field.Forbidden(pp.Index(i), "reserved container name"))
		}
	}
	if mysqldIndex == -1 {
		allErrs = append(allErrs, field.Required(pp, fmt.Sprintf("required container %s is missing", constants.MysqldContainerName)))
	} else {
		pp := p.Child("containers").Index(mysqldIndex).Child("ports")
		for i, port := range s.PodTemplate.Spec.Containers[mysqldIndex].Ports {
			switch port.ContainerPort {
			case constants.MySQLPort, constants.MySQLXPort, constants.MySQLAdminPort, constants.MySQLHealthPort:
				allErrs = append(allErrs, field.Invalid(pp.Index(i), port.ContainerPort, "reserved port"))
			}
			switch port.Name {
			case constants.MySQLPortName, constants.MySQLXPortName, constants.MySQLAdminPortName, constants.MySQLHealthPortName:
				allErrs = append(allErrs, field.Invalid(pp.Index(i), port.Name, "reserved port name"))
			}
		}
	}

	pp = p.Child("initContainers")
	for i, container := range s.PodTemplate.Spec.InitContainers {
		switch container.Name {
		case constants.InitContainerName:
			allErrs = append(allErrs, field.Invalid(pp.Index(i), container.Name, "reserved init container name"))
		}
	}

	pp = p.Child("volumes")
	for i, vol := range s.PodTemplate.Spec.Volumes {
		switch vol.Name {
		case constants.TmpVolumeName, constants.RunVolumeName, constants.VarLogVolumeName,
			constants.MySQLConfVolumeName, constants.MySQLInitConfVolumeName,
			constants.MySQLConfSecretVolumeName, constants.ErrorLogAgentConfigVolumeName,
			constants.SlowQueryLogAgentConfigVolumeName, constants.MOCOBinVolumeName:

			allErrs = append(allErrs, field.Invalid(pp.Index(i), vol.Name, "reserved volume name"))
		}
	}

	return allErrs
}

func (s MySQLClusterSpec) validateUpdate(old MySQLClusterSpec) field.ErrorList {
	var allErrs field.ErrorList
	p := field.NewPath("spec")

	if s.Replicas < old.Replicas {
		p := p.Child("replicas")
		allErrs = append(allErrs, field.Forbidden(p, "decreasing replicas is not supported yet"))
	}
	if s.ReplicationSourceSecretName != nil {
		p := p.Child("replicationSourceSecretName")
		if old.ReplicationSourceSecretName == nil {
			allErrs = append(allErrs, field.Forbidden(p, "replication can be initiated only with new clusters"))
		} else if *s.ReplicationSourceSecretName != *old.ReplicationSourceSecretName {
			allErrs = append(allErrs, field.Forbidden(p, "replication source secret name cannot be modified"))
		}
	}

	return append(allErrs, s.validateCreate()...)
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

func (p PersistentVolumeClaim) ToCoreV1() corev1.PersistentVolumeClaim {
	claim := corev1.PersistentVolumeClaim{}
	claim.Name = p.Name
	if len(p.Labels) > 0 {
		claim.Labels = make(map[string]string)
		for k, v := range p.Labels {
			claim.Labels[k] = v
		}
	}
	if len(p.Annotations) > 0 {
		claim.Annotations = make(map[string]string)
		for k, v := range p.Annotations {
			claim.Annotations[k] = v
		}
	}
	claim.Spec = *p.Spec.DeepCopy()
	if claim.Spec.VolumeMode == nil {
		modeFilesystem := corev1.PersistentVolumeFilesystem
		claim.Spec.VolumeMode = &modeFilesystem
	}
	claim.Status.Phase = corev1.ClaimPending
	return claim
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

	// CurrentPrimaryIndex is the index of the current primary Pod in StatefulSet.
	// Initially, this is zero.
	CurrentPrimaryIndex int `json:"currentPrimaryIndex"`

	// SyncedReplicas is the number of synced instances including the primary.
	// +optional
	SyncedReplicas int `json:"syncedReplicas,omitempty"`

	// ErrantReplicas is the number of instances that have errant transactions.
	// +optional
	ErrantReplicas int `json:"errantReplicas,omitempty"`

	// ErrantReplicaList is the list of indices of errant replicas.
	// +optional
	ErrantReplicaList []int `json:"errantReplicaList,omitempty"`

	// ReconcileInfo represents version information for reconciler.
	// +optional
	ReconcileInfo ReconcileInfo `json:"reconcileInfo"`
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
// +kubebuilder:validation:Enum=Initialized;Available;Healthy
type MySQLClusterConditionType string

// Valid values for MySQLClusterConditionType
const (
	ConditionInitialized MySQLClusterConditionType = "Initialized"
	ConditionAvailable   MySQLClusterConditionType = "Available"
	ConditionHealthy     MySQLClusterConditionType = "Healthy"
)

type ReconcileInfo struct {
	// Generation is the `metadata.generation` value of the last reconciliation.
	// See also https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#status-subresource
	// +optional
	Generation int64 `json:"generation,omitempty"`

	// ReconcileVersion is the version of the operator reconciler.
	// +optional
	ReconcileVersion int `json:"reconcileVersion"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Available",type="string",JSONPath=".status.conditions[?(@.type=='Available')].status"
// +kubebuilder:printcolumn:name="Healthy",type="string",JSONPath=".status.conditions[?(@.type=='Healthy')].status"
// +kubebuilder:printcolumn:name="Primary",type="integer",JSONPath=".status.currentPrimaryIndex"
// +kubebuilder:printcolumn:name="Synced replicas",type="integer",JSONPath=".status.syncedReplicas"
// +kubebuilder:printcolumn:name="Errant replicas",type="integer",JSONPath=".status.errantReplicas"

// MySQLCluster is the Schema for the mysqlclusters API
type MySQLCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MySQLClusterSpec   `json:"spec,omitempty"`
	Status MySQLClusterStatus `json:"status,omitempty"`
}

// PrefixedName returns "moco-<<metadata.name>>"
func (r *MySQLCluster) PrefixedName() string {
	return "moco-" + r.Name
}

// PodName returns PrefixedName() + "-" + index
func (r *MySQLCluster) PodName(index int) string {
	return fmt.Sprintf("%s-%d", r.PrefixedName(), index)
}

// UserSecretName returns the name of the Secret for users.
// This Secret is placed in the same namespace as r.
func (r *MySQLCluster) UserSecretName() string {
	return "moco-" + r.Name
}

// MyCnfSecretName returns the name of the Secret for users.
// The contents are formatted for mysql commands (as my.cnf).
func (r *MySQLCluster) MyCnfSecretName() string {
	return "moco-my-cnf-" + r.Name
}

// ControllerSecretName returns the name of the Secret for MOCO controller.
// This Secret is placed in the namespace of the controller.
func (r *MySQLCluster) ControllerSecretName() string {
	return fmt.Sprintf("mysql-%s.%s", r.Namespace, r.Name)
}

// HeadlessServiceName returns the name of Service for StatefulSet.
func (r *MySQLCluster) HeadlessServiceName() string {
	return r.PrefixedName()
}

// PrimaryServiceName returns the name of Service for the primary mysqld instance.
func (r *MySQLCluster) PrimaryServiceName() string {
	return r.PrefixedName() + "-primary"
}

// ReplicaServiceName returns the name of Service for replica mysqld instances.
func (r *MySQLCluster) ReplicaServiceName() string {
	return r.PrefixedName() + "-replica"
}

// PodHostname returns the hostname of a Pod with the given index.
func (r *MySQLCluster) PodHostname(index int) string {
	return fmt.Sprintf("%s.%s.%s.svc", r.PodName(index), r.PrefixedName(), r.Namespace)
}

// ErrorLogAgentConfigMapName returns the name of the error log agent config name.
func (r *MySQLCluster) ErrorLogAgentConfigMapName() string {
	return fmt.Sprintf("moco-error-log-agent-config-%s", r.Name)
}

// SlowQueryLogAgentConfigMapName returns the name of the slow query log agent config name.
func (r *MySQLCluster) SlowQueryLogAgentConfigMapName() string {
	return fmt.Sprintf("moco-slow-log-agent-config-%s", r.Name)
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
