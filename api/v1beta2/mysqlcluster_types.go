package v1beta2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MySQLClusterSpec defines the desired state of MySQLCluster
type MySQLClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Replicas is the number of instances. Available values are positive odd numbers.
	// +kubebuilder:default=1
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// PodTemplate is a `Pod` template for MySQL server container.
	PodTemplate PodTemplateSpec `json:"podTemplate"`

	// VolumeClaimTemplates is a list of `PersistentVolumeClaim` templates for MySQL server container.
	// A claim named "mysql-data" must be included in the list.
	// +kubebuilder:validation:MinItems=1
	VolumeClaimTemplates []PersistentVolumeClaim `json:"volumeClaimTemplates"`

	// PrimaryServiceTemplate is a `Service` template for primary.
	// +optional
	PrimaryServiceTemplate *ServiceTemplate `json:"primaryServiceTemplate,omitempty"`

	// ReplicaServiceTemplate is a `Service` template for replica.
	// +optional
	ReplicaServiceTemplate *ServiceTemplate `json:"replicaServiceTemplate,omitempty"`

	// MySQLConfigMapName is a `ConfigMap` name of MySQL config.
	// +nullable
	// +optional
	MySQLConfigMapName *string `json:"mysqlConfigMapName,omitempty"`

	// ReplicationSourceSecretName is a `Secret` name which contains replication source info.
	// If this field is given, the `MySQLCluster` works as an intermediate primary.
	// +nullable
	// +optional
	ReplicationSourceSecretName *string `json:"replicationSourceSecretName,omitempty"`

	// Collectors is the list of collector flag names of mysqld_exporter.
	// If this field is not empty, MOCO adds mysqld_exporter as a sidecar to collect
	// and export mysqld metrics in Prometheus format.
	//
	// See https://github.com/prometheus/mysqld_exporter/blob/master/README.md#collector-flags for flag names.
	//
	// Example: ["engine_innodb_status", "info_schema.innodb_metrics"]
	// +optional
	Collectors []string `json:"collectors,omitempty"`

	// ServerIDBase, if set, will become the base number of server-id of each MySQL
	// instance of this cluster.  For example, if this is 100, the server-ids will be
	// 100, 101, 102, and so on.
	// If the field is not given or zero, MOCO automatically sets a random positive integer.
	// +optional
	ServerIDBase int32 `json:"serverIDBase,omitempty"`

	// MaxDelaySeconds configures the readiness probe of mysqld container.
	// For a replica mysqld instance, if it is delayed to apply transactions over this threshold,
	// the mysqld instance will be marked as non-ready.
	// The default is 60 seconds.
	// Setting this field to 0 disables the delay check in the probe.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=60
	// +optional
	MaxDelaySeconds *int `json:"maxDelaySeconds,omitempty"`

	// MaxDelaySecondsForPodDeletion configures the maximum allowed replication delay before a Pod deletion is blocked.
	// If the replication delay exceeds this threshold, deletion of the primary pod will be prevented.
	// The default is 0 seconds.
	// Setting this field to 0 disables the delay check for pod deletion.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	// +optional
	MaxDelaySecondsForPodDeletion int64 `json:"maxDelaySecondsForPodDeletion,omitempty"`

	// StartupWaitSeconds is the maximum duration to wait for `mysqld` container to start working.
	// The default is 3600 seconds.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=3600
	// +optional
	StartupWaitSeconds int32 `json:"startupWaitSeconds,omitempty"`

	// LogRotationSchedule specifies the schedule to rotate MySQL logs.
	// If not set, the default is to rotate logs every 5 minutes.
	// See https://pkg.go.dev/github.com/robfig/cron/v3#hdr-CRON_Expression_Format for the field format.
	// +optional
	LogRotationSchedule string `json:"logRotationSchedule,omitempty"`

	// The name of BackupPolicy custom resource in the same namespace.
	// If this is set, MOCO creates a CronJob to take backup of this MySQL cluster periodically.
	// +nullable
	// +optional
	BackupPolicyName *string `json:"backupPolicyName,omitempty"`

	// Restore is the specification to perform Point-in-Time-Recovery from existing cluster.
	// If this field is not null, MOCO restores the data as specified and create a new
	// cluster with the data.  This field is not editable.
	// +optional
	Restore *RestoreSpec `json:"restore,omitempty"`

	// DisableSlowQueryLogContainer controls whether to add a sidecar container named "slow-log"
	// to output slow logs as the containers output.
	// If set to true, the sidecar container is not added. The default is false.
	// +optional
	DisableSlowQueryLogContainer bool `json:"disableSlowQueryLogContainer,omitempty"`

	// AgentUseLocalhost configures the mysqld interface to bind and be accessed over localhost instead of pod name.
	// During container init moco-agent will set mysql admin interface is bound to localhost. The moco-agent will also
	// communicate with mysqld over localhost when acting as a sidecar.
	AgentUseLocalhost bool `json:"agentUseLocalhost,omitempty"`

	// Offline sets the cluster offline, releasing compute resources. Data is not removed.
	// +optional
	Offline bool `json:"offline,omitempty"`
}

func (s MySQLClusterSpec) validateCreate() (admission.Warnings, field.ErrorList) {
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

	for _, vc := range s.VolumeClaimTemplates {
		if vc.Spec.Resources == nil || vc.Spec.Resources.Requests == nil || vc.Spec.Resources.Requests.Storage() == nil {
			allErrs = append(allErrs, field.Required(pp, fmt.Sprintf("required volume claim template %s storage requests is missing", vc.Name)))
		}
	}

	pp = p.Child("serverIDBase")
	if s.ServerIDBase <= 0 {
		allErrs = append(allErrs, field.Invalid(pp, s.ServerIDBase, "serverIDBase must be a positive integer"))
	}

	pp = p.Child("logRotationSchedule")
	if s.LogRotationSchedule != "" {
		_, err := cron.ParseStandard(s.LogRotationSchedule)
		if err != nil {
			allErrs = append(allErrs, field.Invalid(pp, s.LogRotationSchedule, err.Error()))
		}
	}

	pp = p.Child("replicas")
	if s.Replicas%2 == 0 {
		allErrs = append(allErrs, field.Invalid(pp, s.Replicas, "replicas must be a positive odd number"))
	}
	if s.Replicas <= 0 {
		allErrs = append(allErrs, field.Invalid(pp, s.Replicas, "replicas must be a positive integer"))
	}

	p = p.Child("podTemplate", "spec")

	pp = p.Child("containers")
	mysqldIndex := -1
	for i, container := range s.PodTemplate.Spec.Containers {
		if container.Name == nil {
			allErrs = append(allErrs, field.Forbidden(pp.Index(i), "container name is required"))
			continue
		}

		if *container.Name == constants.MysqldContainerName {
			mysqldIndex = i
		}
		if *container.Name == constants.AgentContainerName {
			allErrs = append(allErrs, field.Forbidden(pp.Index(i), "reserved container name"))
		}
		if *container.Name == constants.SlowQueryLogAgentContainerName && !s.DisableSlowQueryLogContainer {
			allErrs = append(allErrs, field.Forbidden(pp.Index(i), "reserved container name"))
		}
		if *container.Name == constants.ExporterContainerName && len(s.Collectors) > 0 {
			allErrs = append(allErrs, field.Forbidden(pp.Index(i), "reserved container name"))
		}
	}
	if mysqldIndex == -1 {
		allErrs = append(allErrs, field.Required(pp, fmt.Sprintf("required container %s is missing", constants.MysqldContainerName)))
	} else {
		pp := p.Child("containers").Index(mysqldIndex).Child("ports")
		for i, port := range s.PodTemplate.Spec.Containers[mysqldIndex].Ports {
			if port.ContainerPort != nil {
				switch *port.ContainerPort {
				case constants.MySQLPort, constants.MySQLXPort, constants.MySQLAdminPort, constants.MySQLHealthPort:
					allErrs = append(allErrs, field.Invalid(pp.Index(i), port.ContainerPort, "reserved port"))
				}
			}

			if port.Name != nil {
				switch *port.Name {
				case constants.MySQLPortName, constants.MySQLXPortName, constants.MySQLAdminPortName, constants.MySQLHealthPortName:
					allErrs = append(allErrs, field.Invalid(pp.Index(i), port.Name, "reserved port name"))
				}
			}
		}
	}

	pp = p.Child("initContainers")
	for i, container := range s.PodTemplate.Spec.InitContainers {
		if container.Name == nil {
			allErrs = append(allErrs, field.Forbidden(pp.Index(i), "init container name is required"))
			continue
		}

		switch *container.Name {
		case constants.InitContainerName:
			allErrs = append(allErrs, field.Invalid(pp.Index(i), container.Name, "reserved init container name"))
		}
	}

	pp = p.Child("volumes")
	for i, vol := range s.PodTemplate.Spec.Volumes {
		if vol.Name == nil {
			continue
		}

		switch *vol.Name {
		case constants.TmpVolumeName, constants.RunVolumeName, constants.VarLogVolumeName,
			constants.MySQLConfVolumeName, constants.MySQLInitConfVolumeName,
			constants.MySQLConfSecretVolumeName, constants.SlowQueryLogAgentConfigVolumeName:

			allErrs = append(allErrs, field.Invalid(pp.Index(i), vol.Name, "reserved volume name"))
		}
	}

	return nil, allErrs
}

func (s MySQLClusterSpec) validateUpdate(ctx context.Context, apiReader client.Reader, old MySQLClusterSpec) (admission.Warnings, field.ErrorList) {
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
	if !equality.Semantic.DeepEqual(s.Restore, old.Restore) {
		p := p.Child("restore")
		allErrs = append(allErrs, field.Forbidden(p, "not editable"))
	}

	oldPVCSet := make(map[string]PersistentVolumeClaim)
	for _, oldPVC := range old.VolumeClaimTemplates {
		oldPVCSet[oldPVC.Name] = oldPVC
	}

	volumeExpansionTargetIndices := make([]int, 0)

	for i, pvc := range s.VolumeClaimTemplates {
		if old, ok := oldPVCSet[pvc.Name]; ok {
			newSize := pvc.StorageSize()
			oldSize := old.StorageSize()

			switch cmp := newSize.Cmp(oldSize); {
			case cmp == -1:
				// noop
				// Allow users to reduce the volume size by operating.
				// ref: docs/designdoc/support_reduce_volume_size.md
			case cmp == 1:
				volumeExpansionTargetIndices = append(volumeExpansionTargetIndices, i)
			case cmp == 0:
				// noop
			}
		}
	}

	if len(volumeExpansionTargetIndices) > 0 {
		allErrs = append(allErrs, s.validateVolumeExpansionSupported(ctx, apiReader, volumeExpansionTargetIndices)...)
	}

	warns, errs := s.validateCreate()
	return warns, append(allErrs, errs...)
}

func (s MySQLClusterSpec) validateVolumeExpansionSupported(ctx context.Context, apiReader client.Reader, targetIndices []int) field.ErrorList {
	var allErrs field.ErrorList
	p := field.NewPath("spec").Child("volumeClaimTemplates")

	for _, idx := range targetIndices {
		pvc := s.VolumeClaimTemplates[idx]
		if pvc.Spec.StorageClassName != nil {
			var sc storagev1.StorageClass
			if err := apiReader.Get(ctx, types.NamespacedName{Name: *pvc.Spec.StorageClassName}, &sc); err != nil {
				p := p.Index(idx).Child("spec").Child("storageClassName")
				allErrs = append(allErrs, field.InternalError(p, fmt.Errorf("failed to get storage class %s: %w", *pvc.Spec.StorageClassName, err)))
			} else {
				if !isVolumeExpansionSupported(&sc) {
					p := p.Index(idx).Child("spec").Child("storageClassName")
					allErrs = append(allErrs, field.Forbidden(p, fmt.Sprintf("storage class %q is not allowed to expand volume", *pvc.Spec.StorageClassName)))
				}
			}
		} else {
			sc, err := getDefaultStorageClass(ctx, apiReader)
			if err != nil {
				p := p.Index(idx)
				allErrs = append(allErrs, field.InternalError(p, fmt.Errorf("failed to get storage class: %w", err)))
			} else {
				if !isVolumeExpansionSupported(sc) {
					p := p.Index(idx)
					allErrs = append(allErrs, field.Forbidden(p, fmt.Sprintf("default storage class %q is not allowed to expand volume", sc.Name)))
				}
			}
		}
	}

	return allErrs
}

func isVolumeExpansionSupported(sc *storagev1.StorageClass) bool {
	if sc.AllowVolumeExpansion == nil {
		return false
	}

	return *sc.AllowVolumeExpansion
}

func getDefaultStorageClass(ctx context.Context, client client.Reader) (*storagev1.StorageClass, error) {
	var scs storagev1.StorageClassList
	if err := client.List(ctx, &scs); err != nil {
		return nil, err
	}

	for _, sc := range scs.Items {
		if isDefaultStorageClass(&sc) {
			return &sc, nil
		}
	}

	return nil, errors.New("not found default storage class")
}

func isDefaultStorageClass(sc *storagev1.StorageClass) bool {
	if len(sc.Annotations) == 0 {
		return false
	}

	// refs: https://github.com/kubernetes/kubernetes/blob/v1.24.2/pkg/apis/storage/v1beta1/util/helpers.go
	if sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" ||
		sc.Annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true" {
		return true
	}

	return false
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

// PodSpecApplyConfiguration is the type defined to implement the DeepCopy method.
type PodSpecApplyConfiguration corev1ac.PodSpecApplyConfiguration

// DeepCopy is copying the receiver, creating a new PodSpecApplyConfiguration.
func (in *PodSpecApplyConfiguration) DeepCopy() *PodSpecApplyConfiguration {
	out := new(PodSpecApplyConfiguration)
	bytes, err := json.Marshal(in)
	if err != nil {
		panic("Failed to marshal")
	}
	err = json.Unmarshal(bytes, out)
	if err != nil {
		panic("Failed to unmarshal")
	}
	return out
}

// PodTemplateSpec describes the data a pod should have when created from a template.
// This is slightly modified from corev1.PodTemplateSpec.
type PodTemplateSpec struct {
	// Standard object's metadata.  The name in this metadata is ignored.
	// +optional
	ObjectMeta `json:"metadata,omitempty"`

	// Specification of the desired behavior of the pod.
	// The name of the MySQL server container in this spec must be `mysqld`.
	Spec PodSpecApplyConfiguration `json:"spec"`

	// OverwriteContainers overwrites the container definitions provided by default by the system.
	// +optional
	OverwriteContainers []OverwriteContainer `json:"overwriteContainers,omitempty"`
}

// OverwriteableContainerName is the name of the container.
// +kubebuilder:validation:Enum=agent;moco-init;slow-log;mysqld-exporter
type OverwriteableContainerName string

// String implements the fmt.Stringer interface.
func (c OverwriteableContainerName) String() string {
	return string(c)
}

const (
	AgentContainerName             OverwriteableContainerName = constants.AgentContainerName
	InitContainerName              OverwriteableContainerName = constants.InitContainerName
	SlowQueryLogAgentContainerName OverwriteableContainerName = constants.SlowQueryLogAgentContainerName
	ExporterContainerName          OverwriteableContainerName = constants.ExporterContainerName
)

// OverwriteContainer defines the container spec used for overwriting.
type OverwriteContainer struct {
	// Name of the container to overwrite.
	// +kubebuilder:validation:Required
	Name OverwriteableContainerName `json:"name"`

	// Resources is the container resource to be overwritten.
	// +optional
	Resources *ResourceRequirementsApplyConfiguration `json:"resources,omitempty"`
}

// ResourceRequirementsApplyConfiguration is the type defined to implement the DeepCopy method.
type ResourceRequirementsApplyConfiguration corev1ac.ResourceRequirementsApplyConfiguration

// DeepCopy is copying the receiver, creating a new OverwriteContainer.
func (in *ResourceRequirementsApplyConfiguration) DeepCopy() *ResourceRequirementsApplyConfiguration {
	out := new(ResourceRequirementsApplyConfiguration)
	bytes, err := json.Marshal(in)
	if err != nil {
		panic("Failed to marshal")
	}
	err = json.Unmarshal(bytes, out)
	if err != nil {
		panic("Failed to unmarshal")
	}
	return out
}

// PersistentVolumeClaimSpecApplyConfiguration is the type defined to implement the DeepCopy method.
type PersistentVolumeClaimSpecApplyConfiguration corev1ac.PersistentVolumeClaimSpecApplyConfiguration

// DeepCopy is copying the receiver, creating a new PersistentVolumeClaimSpecApplyConfiguration.
func (in *PersistentVolumeClaimSpecApplyConfiguration) DeepCopy() *PersistentVolumeClaimSpecApplyConfiguration {
	out := new(PersistentVolumeClaimSpecApplyConfiguration)
	bytes, err := json.Marshal(in)
	if err != nil {
		panic("Failed to marshal")
	}
	err = json.Unmarshal(bytes, out)
	if err != nil {
		panic("Failed to unmarshal")
	}
	return out
}

// PersistentVolumeClaim is a user's request for and claim to a persistent volume.
// This is slightly modified from corev1.PersistentVolumeClaim.
type PersistentVolumeClaim struct {
	// Standard object's metadata.
	ObjectMeta `json:"metadata"`

	// Spec defines the desired characteristics of a volume requested by a pod author.
	Spec PersistentVolumeClaimSpecApplyConfiguration `json:"spec"`
}

func (in PersistentVolumeClaim) StorageSize() resource.Quantity {
	if in.Spec.Resources != nil && in.Spec.Resources.Requests != nil {
		requests := *in.Spec.Resources.Requests
		return requests[corev1.ResourceStorage]
	}

	return resource.Quantity{}
}

// ToCoreV1 converts the PersistentVolumeClaim to a PersistentVolumeClaimApplyConfiguration.
func (in PersistentVolumeClaim) ToCoreV1() *corev1ac.PersistentVolumeClaimApplyConfiguration {
	// If you use this, the namespace will not be nil and will not match for "equality.Semantic.DeepEqual".
	// claim := corev1ac.PersistentVolumeClaim(in.Name, "").
	claim := &corev1ac.PersistentVolumeClaimApplyConfiguration{}

	claim.WithName(in.Name).
		WithLabels(in.Labels).
		WithAnnotations(in.Annotations).
		WithStatus(corev1ac.PersistentVolumeClaimStatus())

	spec := corev1ac.PersistentVolumeClaimSpecApplyConfiguration(*in.Spec.DeepCopy())
	claim.WithSpec(&spec)

	if claim.Spec.VolumeMode == nil {
		claim.Spec.WithVolumeMode(corev1.PersistentVolumeFilesystem)
	}

	claim.Status.WithPhase(corev1.ClaimPending)

	return claim
}

// ServiceSpecApplyConfiguration is the type defined to implement the DeepCopy method.
type ServiceSpecApplyConfiguration corev1ac.ServiceSpecApplyConfiguration

// DeepCopy is copying the receiver, creating a new ServiceSpecApplyConfiguration.
func (in *ServiceSpecApplyConfiguration) DeepCopy() *ServiceSpecApplyConfiguration {
	out := new(ServiceSpecApplyConfiguration)
	bytes, err := json.Marshal(in)
	if err != nil {
		panic("Failed to marshal")
	}
	err = json.Unmarshal(bytes, out)
	if err != nil {
		panic("Failed to unmarshal")
	}
	return out
}

// ServiceTemplate defines the desired spec and annotations of Service
type ServiceTemplate struct {
	// Standard object's metadata.  Only `annotations` and `labels` are valid.
	// +optional
	ObjectMeta `json:"metadata,omitempty"`

	// Spec is the ServiceSpec
	// +optional
	Spec *ServiceSpecApplyConfiguration `json:"spec,omitempty"`
}

// RestoreSpec represents a set of parameters for Point-in-Time Recovery.
type RestoreSpec struct {
	// SourceName is the name of the source `MySQLCluster`.
	// +kubebuilder:validation:MinLength=1
	SourceName string `json:"sourceName"`

	// SourceNamespace is the namespace of the source `MySQLCluster`.
	// +kubebuilder:validation:MinLength=1
	SourceNamespace string `json:"sourceNamespace"`

	// RestorePoint is the target date and time to restore data.
	// The format is RFC3339.  e.g. "2006-01-02T15:04:05Z"
	RestorePoint metav1.Time `json:"restorePoint"`

	// Specifies parameters for restore Pod.
	JobConfig `json:"jobConfig"`

	// Schema is the name of the schema to restore.
	// If empty, all schemas are restored.
	// This is used for `mysqlbinlog` option `--database`.
	// Thus, this option changes behavior depending on binlog_format.
	// For more information, please read the following documentation.
	// https://dev.mysql.com/doc/refman/8.0/en/mysqlbinlog.html#option_mysqlbinlog_database
	// +optional
	Schema string `json:"schema,omitempty"`
}

// MySQLClusterStatus defines the observed state of MySQLCluster
type MySQLClusterStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Conditions is an array of conditions.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

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

	// Backup is the status of the last successful backup.
	// +optional
	Backup BackupStatus `json:"backup"`

	// RestoredTime is the time when the cluster data is restored.
	// +optional
	RestoredTime *metav1.Time `json:"restoredTime,omitempty"`

	// Cloned indicates if the initial cloning from an external source has been completed.
	// +optional
	Cloned bool `json:"cloned,omitempty"`

	// ReconcileInfo represents version information for reconciler.
	// +optional
	ReconcileInfo ReconcileInfo `json:"reconcileInfo"`
}

const (
	ConditionInitialized          string = "Initialized"
	ConditionAvailable            string = "Available"
	ConditionHealthy              string = "Healthy"
	ConditionStatefulSetReady     string = "StatefulSetReady"
	ConditionReconcileSuccess     string = "ReconcileSuccess"
	ConditionReconciliationActive string = "ReconciliationActive"
	ConditionClusteringActive     string = "ClusteringActive"
)

// BackupStatus represents the status of the last successful backup.
type BackupStatus struct {
	// The time of the backup.  This is used to generate object keys of backup files in a bucket.
	// +nullable
	Time metav1.Time `json:"time"`

	// Elapsed is the time spent on the backup.
	Elapsed metav1.Duration `json:"elapsed"`

	// SourceIndex is the ordinal of the backup source instance.
	SourceIndex int `json:"sourceIndex"`

	// SourceUUID is the `server_uuid` of the backup source instance.
	SourceUUID string `json:"sourceUUID"`

	// UUIDSet is the `server_uuid` set of all candidate instances for the backup source.
	// +optional
	UUIDSet map[string]string `json:"uuidSet"`

	// BinlogFilename is the binlog filename that the backup source instance was writing to
	// at the backup.
	BinlogFilename string `json:"binlogFilename"`

	// GTIDSet is the GTID set of the full dump of database.
	GTIDSet string `json:"gtidSet"`

	// DumpSize is the size in bytes of a full dump of database stored in an object storage bucket.
	DumpSize int64 `json:"dumpSize"`

	// BinlogSize is the size in bytes of a tarball of binlog files stored in an object storage bucket.
	BinlogSize int64 `json:"binlogSize"`

	// WorkDirUsage is the max usage in bytes of the woking directory.
	WorkDirUsage int64 `json:"workDirUsage"`

	// Warnings are list of warnings from the last backup, if any.
	// +nullable
	Warnings []string `json:"warnings"`
}

// ReconcileInfo is the type to record the last reconciliation information.
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
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Available",type="string",JSONPath=".status.conditions[?(@.type=='Available')].status"
// +kubebuilder:printcolumn:name="Healthy",type="string",JSONPath=".status.conditions[?(@.type=='Healthy')].status"
// +kubebuilder:printcolumn:name="Primary",type="integer",JSONPath=".status.currentPrimaryIndex"
// +kubebuilder:printcolumn:name="Synced replicas",type="integer",JSONPath=".status.syncedReplicas"
// +kubebuilder:printcolumn:name="Errant replicas",type="integer",JSONPath=".status.errantReplicas"
// +kubebuilder:printcolumn:name="Clustering Active",type="string",JSONPath=".status.conditions[?(@.type=='ClusteringActive')].status"
// +kubebuilder:printcolumn:name="Reconcile Active",type="string",JSONPath=".status.conditions[?(@.type=='ReconciliationActive')].status"
// +kubebuilder:printcolumn:name="Last backup",type="string",JSONPath=".status.backup.time"

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
	return fmt.Sprintf("%s.%s.%s.svc", r.PodName(index), r.HeadlessServiceName(), r.Namespace)
}

// SlowQueryLogAgentConfigMapName returns the name of the slow query log agent config name.
func (r *MySQLCluster) SlowQueryLogAgentConfigMapName() string {
	return fmt.Sprintf("moco-slow-log-agent-config-%s", r.Name)
}

// CertificateName returns the name of Certificate issued for moco-agent gRPC server.
// The Certificate will be created in the namespace of the controller.
//
// This is also the Secret name created from the Certificate.
func (r *MySQLCluster) CertificateName() string {
	return fmt.Sprintf("moco-agent-%s.%s", r.Namespace, r.Name)
}

// GRPCSecretName returns the name of Secret of TLS server certificate for moco-agent.
// The Secret will be created in the MySQLCluster namespace.
func (r *MySQLCluster) GRPCSecretName() string {
	return fmt.Sprintf("%s-grpc", r.PrefixedName())
}

// BackupCronJobName returns the name of CronJob for backup.
func (r *MySQLCluster) BackupCronJobName() string {
	return fmt.Sprintf("moco-backup-%s", r.Name)
}

// BackupRoleName returns the name of Role/RoleBinding for backup.
func (r *MySQLCluster) BackupRoleName() string {
	return fmt.Sprintf("moco-backup-%s", r.Name)
}

// RestoreJobName returns the name of Job for restoration.
func (r *MySQLCluster) RestoreJobName() string {
	return fmt.Sprintf("moco-restore-%s", r.Name)
}

// RestoreRoleName returns the name of Role/RoleBinding for restoration.
func (r *MySQLCluster) RestoreRoleName() string {
	return fmt.Sprintf("moco-restore-%s", r.Name)
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
