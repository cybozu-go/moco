package v1beta2

import (
	"encoding/json"
	"errors"
	"strings"

	"filippo.io/age"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation/field"
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
}

// VolumeSourceApplyConfiguration is the type defined to implement the DeepCopy method.
type VolumeSourceApplyConfiguration corev1ac.VolumeSourceApplyConfiguration

// DeepCopy is copying the receiver, creating a new VolumeSourceApplyConfiguration.
func (in *VolumeSourceApplyConfiguration) DeepCopy() *VolumeSourceApplyConfiguration {
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

// DeepCopy is copying the receiver, creating a new VolumeSourceApplyConfiguration.
func (in *VolumeApplyConfiguration) DeepCopy() *VolumeApplyConfiguration {
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

// DeepCopy is copying the receiver, creating a new VolumeSourceApplyConfiguration.
func (in *VolumeMountApplyConfiguration) DeepCopy() *VolumeMountApplyConfiguration {
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

// DeepCopy is copying the receiver, creating a new EnvFromSourceApplyConfiguration.
func (in *EnvFromSourceApplyConfiguration) DeepCopy() *EnvFromSourceApplyConfiguration {
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

// DeepCopy is copying the receiver, creating a new EnvVarApplyConfiguration.
func (in *EnvVarApplyConfiguration) DeepCopy() *EnvVarApplyConfiguration {
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

	// CustomerKeySecret references a key in a Secret that holds a base64-encoded
	// 256-bit key for server-side encryption with a customer-provided key:
	// SSE-C for `s3`, customer-supplied encryption keys (CSEK) for `gcs`, and
	// customer-provided keys (CPK) for `azure`.
	// When set, backup files are uploaded with this key, and the same key is used
	// to download them for restoration.
	// The Secret must exist in the namespace of the backup or restore Job.
	// +optional
	CustomerKeySecret *SecretKeyRef `json:"customerKeySecret,omitempty"`

	// Age configures client-side encryption of backup files with age (https://age-encryption.org).
	// moco-backup encrypts the files before uploading them, so this works with every backend type.
	// Encrypted files get the suffix `.age`.
	// In a BackupPolicy, `recipients` is required and cannot be combined with `customerKeySecret`.
	// In a MySQLCluster restore spec, `identitySecret` is required.
	// +optional
	Age *AgeConfig `json:"age,omitempty"`
}

// AgeConfig is a set of parameters for encrypting backup files with age.
type AgeConfig struct {
	// Recipients are the age public keys (`age1...`) to encrypt backup files to.
	// Any of the matching private keys can decrypt the files.
	// This is used by backup Jobs only.
	// +optional
	Recipients []string `json:"recipients,omitempty"`

	// IdentitySecret references a key in a Secret that holds age identities
	// (private keys, `AGE-SECRET-KEY-1...`), one per line, to decrypt backup files.
	// This is used by restore Jobs only.
	// The Secret must exist in the namespace of the restore Job.
	// +optional
	IdentitySecret *SecretKeyRef `json:"identitySecret,omitempty"`
}

// validateForBackup validates BucketConfig of a BackupPolicy. The path p points to the BucketConfig.
func (bc BucketConfig) validateForBackup(p *field.Path) field.ErrorList {
	if bc.Age == nil {
		return nil
	}

	var allErrs field.ErrorList
	pp := p.Child("age", "recipients")
	if len(bc.Age.Recipients) == 0 {
		allErrs = append(allErrs, field.Required(pp, "at least one recipient is required to encrypt backups with age"))
	}
	for i, r := range bc.Age.Recipients {
		if err := validateAgeRecipient(r); err != nil {
			// Do not show the value: it might be a private key put here by mistake.
			allErrs = append(allErrs, field.Invalid(pp.Index(i), field.OmitValueType{}, err.Error()))
		}
	}
	if bc.CustomerKeySecret != nil {
		allErrs = append(allErrs, field.Forbidden(p.Child("age"), "age cannot be combined with customerKeySecret"))
	}
	return allErrs
}

// validateForRestore validates BucketConfig of a restore spec. The path p points to the BucketConfig.
func (bc BucketConfig) validateForRestore(p *field.Path) field.ErrorList {
	if bc.Age == nil {
		return nil
	}
	if bc.Age.IdentitySecret == nil {
		return field.ErrorList{field.Required(p.Child("age", "identitySecret"), "an identity is required to restore backups encrypted with age")}
	}
	return nil
}

func validateAgeRecipient(r string) error {
	if strings.HasPrefix(strings.ToUpper(r), "AGE-SECRET-KEY-") {
		return errors.New("must be a public key (age1...), not a private key")
	}
	if strings.ContainsAny(r, "\r\n") {
		return errors.New("must be a single recipient")
	}
	recipients, err := age.ParseRecipients(strings.NewReader(r))
	if err != nil {
		return err
	}
	if len(recipients) != 1 {
		return errors.New("must be a single recipient")
	}
	return nil
}

// SecretKeyRef references a key in a Secret in the same namespace.
type SecretKeyRef struct {
	// Name is the name of the Secret.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Key is the key in the Secret.
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// AffinityApplyConfiguration is the type defined to implement the DeepCopy method.
type AffinityApplyConfiguration corev1ac.AffinityApplyConfiguration

// DeepCopy is copying the receiver, creating a new AffinityApplyConfiguration.
func (in *AffinityApplyConfiguration) DeepCopy() *AffinityApplyConfiguration {
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

// LocalObjectReferenceApplyConfiguration is the type defined to implement the DeepCopy method.
type LocalObjectReferenceApplyConfiguration corev1ac.LocalObjectReferenceApplyConfiguration

// DeepCopy is copying the receiver, creating a new LocalObjectReferenceApplyConfiguration.
func (in *LocalObjectReferenceApplyConfiguration) DeepCopy() *LocalObjectReferenceApplyConfiguration {
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
