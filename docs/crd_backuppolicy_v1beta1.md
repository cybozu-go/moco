
### Custom Resources

* [BackupPolicy](#backuppolicy)

### Sub Resources

* [BackupPolicyList](#backuppolicylist)
* [BackupPolicySpec](#backuppolicyspec)
* [BucketConfig](#bucketconfig)
* [JobConfig](#jobconfig)

#### BackupPolicy

BackupPolicy is a namespaced resource that should be referenced from MySQLCluster.

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| metadata |  | [metav1.ObjectMeta](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#ObjectMeta) | false |
| spec |  | [BackupPolicySpec](#backuppolicyspec) | true |

[Back to Custom Resources](#custom-resources)

#### BackupPolicyList

BackupPolicyList contains a list of BackupPolicy

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| metadata |  | [metav1.ListMeta](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#ListMeta) | false |
| items |  | [][BackupPolicy](#backuppolicy) | true |

[Back to Custom Resources](#custom-resources)

#### BackupPolicySpec

BackupPolicySpec defines the configuration items for MySQLCluster backup.\n\nThe following fields will be copied to CronJob.spec:\n\n- Schedule - StartingDeadlineSeconds - ConcurrencyPolicy - SuccessfulJobsHistoryLimit - FailedJobsHistoryLimit\n\nThe following fields will be copied to CronJob.spec.jobTemplate.\n\n- ActiveDeadlineSeconds - BackoffLimit

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| schedule | The schedule in Cron format for periodic backups. See https://en.wikipedia.org/wiki/Cron | string | true |
| jobConfig | Specifies parameters for backup Pod. | [JobConfig](#jobconfig) | true |
| startingDeadlineSeconds | Optional deadline in seconds for starting the job if it misses scheduled time for any reason.  Missed jobs executions will be counted as failed ones. | *int64 | false |
| concurrencyPolicy | Specifies how to treat concurrent executions of a Job. Valid values are: - \"Allow\" (default): allows CronJobs to run concurrently; - \"Forbid\": forbids concurrent runs, skipping next run if previous run hasn't finished yet; - \"Replace\": cancels currently running job and replaces it with a new one | [batchv1.ConcurrencyPolicy](https://pkg.go.dev/k8s.io/api/batch/v1#ConcurrencyPolicy) | false |
| activeDeadlineSeconds | Specifies the duration in seconds relative to the startTime that the job may be continuously active before the system tries to terminate it; value must be positive integer. If a Job is suspended (at creation or through an update), this timer will effectively be stopped and reset when the Job is resumed again. | *int64 | false |
| backoffLimit | Specifies the number of retries before marking this job failed. Defaults to 6 | *int32 | false |
| successfulJobsHistoryLimit | The number of successful finished jobs to retain. This is a pointer to distinguish between explicit zero and not specified. Defaults to 3. | *int32 | false |
| failedJobsHistoryLimit | The number of failed finished jobs to retain. This is a pointer to distinguish between explicit zero and not specified. Defaults to 1. | *int32 | false |

[Back to Custom Resources](#custom-resources)

#### BucketConfig

BucketConfig is a set of parameter to access an object storage bucket.

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| bucketName | The name of the bucket | string | true |
| region | The region of the bucket. This can also be set through `AWS_REGION` environment variable. | string | false |
| endpointURL | The API endpoint URL.  Set this for non-S3 object storages. | string | false |
| usePathStyle | Allows you to enable the client to use path-style addressing, i.e., https?://ENDPOINT/BUCKET/KEY. By default, a virtual-host addressing is used (https?://BUCKET.ENDPOINT/KEY). | bool | false |
| backendType | BackendType is an identifier for the object storage to be used. | string | false |
| caCertFilePath |  | string | false |

[Back to Custom Resources](#custom-resources)

#### JobConfig

JobConfig is a set of parameters for backup and restore job Pods.

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| serviceAccountName | ServiceAccountName specifies the ServiceAccount to run the Pod. | string | true |
| bucketConfig | Specifies how to access an object storage bucket. | [BucketConfig](#bucketconfig) | true |
| workVolume | WorkVolume is the volume source for the working directory. Since the backup or restore task can use a lot of bytes in the working directory, You should always give a volume with enough capacity.\n\nThe recommended volume source is a generic ephemeral volume. https://kubernetes.io/docs/concepts/storage/ephemeral-volumes/#generic-ephemeral-volumes | [VolumeSourceApplyConfiguration](https://pkg.go.dev/k8s.io/client-go/applyconfigurations/core/v1#VolumeSourceApplyConfiguration) | true |
| threads | Threads is the number of threads used for backup or restoration. | int | false |
| cpu | CPU is the amount of CPU requested for the Pod. | *[resource.Quantity](https://pkg.go.dev/k8s.io/apimachinery/pkg/api/resource#Quantity) | false |
| maxCpu | MaxCPU is the amount of maximum CPU for the Pod. | *[resource.Quantity](https://pkg.go.dev/k8s.io/apimachinery/pkg/api/resource#Quantity) | false |
| memory | Memory is the amount of memory requested for the Pod. | *[resource.Quantity](https://pkg.go.dev/k8s.io/apimachinery/pkg/api/resource#Quantity) | false |
| maxMemory | MaxMemory is the amount of maximum memory for the Pod. | *[resource.Quantity](https://pkg.go.dev/k8s.io/apimachinery/pkg/api/resource#Quantity) | false |
| envFrom | List of sources to populate environment variables in the container. The keys defined within a source must be a C_IDENTIFIER. All invalid keys will be reported as an event when the container is starting. When a key exists in multiple sources, the value associated with the last source will take precedence. Values defined by an Env with a duplicate key will take precedence.\n\nYou can configure S3 bucket access parameters through environment variables. See https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/config#EnvConfig | [][EnvFromSourceApplyConfiguration](https://pkg.go.dev/k8s.io/client-go/applyconfigurations/core/v1#EnvFromSourceApplyConfiguration) | false |
| env | List of environment variables to set in the container.\n\nYou can configure S3 bucket access parameters through environment variables. See https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/config#EnvConfig | [][EnvVarApplyConfiguration](https://pkg.go.dev/k8s.io/client-go/applyconfigurations/core/v1#EnvVarApplyConfiguration) | false |
| affinity | If specified, the pod's scheduling constraints. | *[AffinityApplyConfiguration](https://pkg.go.dev/k8s.io/client-go/applyconfigurations/core/v1#AffinityApplyConfiguration) | false |
| volumes | Volumes defines the list of volumes that can be mounted by containers in the Pod. | []VolumeApplyConfiguration | false |
| volumeMounts | VolumeMounts describes a list of volume mounts that are to be mounted in a container. | []VolumeMountApplyConfiguration | false |

[Back to Custom Resources](#custom-resources)
