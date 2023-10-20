package v1beta1

import (
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// BackupPolicySpec defines the configuration items for MySQLCluster backup.
//
// The following fields will be copied to CronJob.spec:
//
// - Schedule
// - StartingDeadlineSeconds
// - ConcurrencyPolicy
// - SuccessfulJobsHistoryLimit
// - FailedJobsHistoryLimit
//
// The following fields will be copied to CronJob.spec.jobTemplate.
//
// - ActiveDeadlineSeconds
// - BackoffLimit
type BackupPolicySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// The schedule in Cron format for periodic backups.
	// See https://en.wikipedia.org/wiki/Cron
	Schedule string `json:"schedule"`

	// Specifies parameters for backup Pod.
	JobConfig JobConfig `json:"jobConfig"`

	// Optional deadline in seconds for starting the job if it misses scheduled
	// time for any reason.  Missed jobs executions will be counted as failed ones.
	// +nullable
	// +optional
	StartingDeadlineSeconds *int64 `json:"startingDeadlineSeconds,omitempty"`

	// Specifies how to treat concurrent executions of a Job.
	// Valid values are:
	// - "Allow" (default): allows CronJobs to run concurrently;
	// - "Forbid": forbids concurrent runs, skipping next run if previous run hasn't finished yet;
	// - "Replace": cancels currently running job and replaces it with a new one
	// +kubebuilder:validation:Enum=Allow;Forbid;Replace
	// +kubebuilder:default=Allow
	// +optional
	ConcurrencyPolicy batchv1.ConcurrencyPolicy `json:"concurrencyPolicy,omitempty"`

	// Specifies the duration in seconds relative to the startTime that the job
	// may be continuously active before the system tries to terminate it; value
	// must be positive integer. If a Job is suspended (at creation or through an
	// update), this timer will effectively be stopped and reset when the Job is
	// resumed again.
	// +nullable
	// +optional
	ActiveDeadlineSeconds *int64 `json:"activeDeadlineSeconds,omitempty"`

	// Specifies the number of retries before marking this job failed.
	// Defaults to 6
	// +kubebuilder:validation:Minimum=0
	// +nullable
	// +optional
	BackoffLimit *int32 `json:"backoffLimit,omitempty"`

	// The number of successful finished jobs to retain.
	// This is a pointer to distinguish between explicit zero and not specified.
	// Defaults to 3.
	// +kubebuilder:validation:Minimum=0
	// +nullable
	// +optional
	SuccessfulJobsHistoryLimit *int32 `json:"successfulJobsHistoryLimit,omitempty"`

	// The number of failed finished jobs to retain.
	// This is a pointer to distinguish between explicit zero and not specified.
	// Defaults to 1.
	// +kubebuilder:validation:Minimum=0
	// +nullable
	// +optional
	FailedJobsHistoryLimit *int32 `json:"failedJobsHistoryLimit,omitempty"`
}

//+kubebuilder:object:root=true

// BackupPolicy is a namespaced resource that should be referenced from MySQLCluster.
type BackupPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec BackupPolicySpec `json:"spec"`
}

//+kubebuilder:object:root=true

// BackupPolicyList contains a list of BackupPolicy
type BackupPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BackupPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BackupPolicy{}, &BackupPolicyList{})
}
