package v1beta2

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/api/resource"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
)

// JobConfig is a set of parameters for backup and restore job Pods.
type JobConfig struct {
	// ServiceAccountName specifies the ServiceAccount to run the Pod.
	// +kubebuilder:validation:MinLength=1
	ServiceAccountName string `json:"serviceAccountName"`

	// Specifies how to access an object storage bucket.
	BucketConfig BucketConfig `json:"bucketConfig"`

	// WorkVolume is the volume source for the working directory.
	// Since the backup or restore task can use a lot of bytes in the working directory,
	// You should always give a volume with enough capacity.
	//
	// The recommended volume source is a generic ephemeral volume.
	// https://kubernetes.io/docs/concepts/storage/ephemeral-volumes/#generic-ephemeral-volumes
	WorkVolume VolumeSourceApplyConfiguration `json:"workVolume"`

	// Threads is the number of threads used for backup or restoration.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=4
	// +optional
	Threads int `json:"threads,omitempty"`

	// CPU is the amount of CPU requested for the Pod.
	// +kubebuilder:default=4
	// +nullable
	// +optional
	CPU *resource.Quantity `json:"cpu,omitempty"`

	// MaxCPU is the amount of maximum CPU for the Pod.
	// +nullable
	// +optional
	MaxCPU *resource.Quantity `json:"maxCpu,omitempty"`

	// Memory is the amount of memory requested for the Pod.
	// +kubebuilder:default="4Gi"
	// +nullable
	// +optional
	Memory *resource.Quantity `json:"memory,omitempty"`

	// MaxMemory is the amount of maximum memory for the Pod.
	// +nullable
	// +optional
	MaxMemory *resource.Quantity `json:"maxMemory,omitempty"`

	// List of sources to populate environment variables in the container.
	// The keys defined within a source must be a C_IDENTIFIER. All invalid keys
	// will be reported as an event when the container is starting. When a key exists in multiple
	// sources, the value associated with the last source will take precedence.
	// Values defined by an Env with a duplicate key will take precedence.
	//
	// You can configure S3 bucket access parameters through environment variables.
	// See https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/config#EnvConfig
	//
	// +optional
	EnvFrom []EnvFromSourceApplyConfiguration `json:"envFrom,omitempty"`

	// List of environment variables to set in the container.
	//
	// You can configure S3 bucket access parameters through environment variables.
	// See https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/config#EnvConfig
	//
	// +optional
	Env []EnvVarApplyConfiguration `json:"env,omitempty"`

	// If specified, the pod's scheduling constraints.
	//
	// +optional
	Affinity *AffinityApplyConfiguration `json:"affinity,omitempty"`

	// If specified, the pod's tolerations.
	//
	// +optional
	Tolerations []TolerationApplyConfiguration `json:"tolerations,omitempty"`

	// NodeSelector is a selector which must be true for the pod to fit on a node.
	//
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Volumes defines the list of volumes that can be mounted by containers in the Pod.
	//
	// +optional
	Volumes []VolumeApplyConfiguration `json:"volumes,omitempty"`

	// VolumeMounts describes a list of volume mounts that are to be mounted in a container.
	//
	// +optional
	VolumeMounts []VolumeMountApplyConfiguration `json:"volumeMounts,omitempty"`

	// ImagePullSecrets is a list of references to secrets in the same namespace to use for pulling
	// backup and restore job images. This applies specifically to the backup CronJob and restore Job Pods.
	// To configure imagePullSecrets for the MySQLCluster's StatefulSet Pods, use `podTemplate.spec.imagePullSecrets` instead.
	//
	// +optional
	ImagePullSecrets []LocalObjectReferenceApplyConfiguration `json:"imagePullSecrets,omitempty"`

	// PodSecurityContext holds pod-level security attributes for the backup and restore job Pods.
	// Fields that are not set are filled with MOCO's defaults (`fsGroup` and `fsGroupChangePolicy`).
	//
	// +optional
	PodSecurityContext *PodSecurityContextApplyConfiguration `json:"podSecurityContext,omitempty"`

	// ContainerSecurityContext holds the security configuration for the backup and restore containers.
	// Fields that are not set are filled with MOCO's defaults (`readOnlyRootFilesystem`,
	// `runAsUser`, and `runAsGroup`).
	//
	// +optional
	ContainerSecurityContext *SecurityContextApplyConfiguration `json:"containerSecurityContext,omitempty"`
}

// PodSecurityContextApplyConfiguration is the type defined to implement the DeepCopy method.
type PodSecurityContextApplyConfiguration corev1ac.PodSecurityContextApplyConfiguration

// DeepCopy returns a deep copy of in, or nil if in is nil.
func (in *PodSecurityContextApplyConfiguration) DeepCopy() *PodSecurityContextApplyConfiguration {
	if in == nil {
		return nil
	}
	out := new(PodSecurityContextApplyConfiguration)
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

// VolumeSourceApplyConfiguration is the type defined to implement the DeepCopy method.
type VolumeSourceApplyConfiguration corev1ac.VolumeSourceApplyConfiguration

// DeepCopy returns a deep copy of in, or nil if in is nil.
func (in *VolumeSourceApplyConfiguration) DeepCopy() *VolumeSourceApplyConfiguration {
	if in == nil {
		return nil
	}
	out := new(VolumeSourceApplyConfiguration)
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

// VolumeApplyConfiguration is the type defined to implement the DeepCopy method.
type VolumeApplyConfiguration corev1ac.VolumeApplyConfiguration

// DeepCopy returns a deep copy of in, or nil if in is nil.
func (in *VolumeApplyConfiguration) DeepCopy() *VolumeApplyConfiguration {
	if in == nil {
		return nil
	}
	out := new(VolumeApplyConfiguration)
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

// VolumeMountApplyConfiguration is the type defined to implement the DeepCopy method.
type VolumeMountApplyConfiguration corev1ac.VolumeMountApplyConfiguration

// DeepCopy returns a deep copy of in, or nil if in is nil.
func (in *VolumeMountApplyConfiguration) DeepCopy() *VolumeMountApplyConfiguration {
	if in == nil {
		return nil
	}
	out := new(VolumeMountApplyConfiguration)
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

// EnvFromSourceApplyConfiguration is the type defined to implement the DeepCopy method.
type EnvFromSourceApplyConfiguration corev1ac.EnvFromSourceApplyConfiguration

// DeepCopy returns a deep copy of in, or nil if in is nil.
func (in *EnvFromSourceApplyConfiguration) DeepCopy() *EnvFromSourceApplyConfiguration {
	if in == nil {
		return nil
	}
	out := new(EnvFromSourceApplyConfiguration)
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

// EnvVarApplyConfiguration is the type defined to implement the DeepCopy method.
type EnvVarApplyConfiguration corev1ac.EnvVarApplyConfiguration

// DeepCopy returns a deep copy of in, or nil if in is nil.
func (in *EnvVarApplyConfiguration) DeepCopy() *EnvVarApplyConfiguration {
	if in == nil {
		return nil
	}
	out := new(EnvVarApplyConfiguration)
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

// BucketConfig is a set of parameter to access an object storage bucket.
type BucketConfig struct {
	// The name of the bucket
	// +kubebuilder:validation:MinLength=1
	BucketName string `json:"bucketName"`

	// The region of the bucket.
	// This can also be set through `AWS_REGION` environment variable.
	// +optional
	Region string `json:"region,omitempty"`

	// The API endpoint URL.  Set this for non-S3 object storages.
	// +kubebuilder:validation:Pattern="^https?://.*"
	// +optional
	EndpointURL string `json:"endpointURL,omitempty"`

	// Allows you to enable the client to use path-style addressing, i.e.,
	// https?://ENDPOINT/BUCKET/KEY. By default, a virtual-host addressing
	// is used (https?://BUCKET.ENDPOINT/KEY).
	// +optional
	UsePathStyle bool `json:"usePathStyle,omitempty"`

	// BackendType is an identifier for the object storage to be used.
	//
	// +kubebuilder:validation:Enum=s3;gcs;azure
	// +kubebuilder:default=s3
	// +optional
	BackendType string `json:"backendType,omitempty"`

	// Path to SSL CA certificate file used in addition to system default.
	// +optional
	CaCert string `json:"caCert,omitempty"`
}

// AffinityApplyConfiguration is the type defined to implement the DeepCopy method.
type AffinityApplyConfiguration corev1ac.AffinityApplyConfiguration

// DeepCopy returns a deep copy of in, or nil if in is nil.
func (in *AffinityApplyConfiguration) DeepCopy() *AffinityApplyConfiguration {
	if in == nil {
		return nil
	}
	out := new(AffinityApplyConfiguration)
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

// TolerationApplyConfiguration is the type defined to implement the DeepCopy method.
type TolerationApplyConfiguration corev1ac.TolerationApplyConfiguration

// DeepCopy returns a deep copy of in, or nil if in is nil.
func (in *TolerationApplyConfiguration) DeepCopy() *TolerationApplyConfiguration {
	if in == nil {
		return nil
	}
	out := new(TolerationApplyConfiguration)
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

// LocalObjectReferenceApplyConfiguration is the type defined to implement the DeepCopy method.
type LocalObjectReferenceApplyConfiguration corev1ac.LocalObjectReferenceApplyConfiguration

// DeepCopy returns a deep copy of in, or nil if in is nil.
func (in *LocalObjectReferenceApplyConfiguration) DeepCopy() *LocalObjectReferenceApplyConfiguration {
	if in == nil {
		return nil
	}
	out := new(LocalObjectReferenceApplyConfiguration)
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
