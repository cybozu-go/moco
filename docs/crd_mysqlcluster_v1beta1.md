
### Custom Resources

* [MySQLCluster](#mysqlcluster)

### Sub Resources

* [BackupStatus](#backupstatus)
* [MySQLClusterList](#mysqlclusterlist)
* [MySQLClusterSpec](#mysqlclusterspec)
* [MySQLClusterStatus](#mysqlclusterstatus)
* [ObjectMeta](#objectmeta)
* [PersistentVolumeClaim](#persistentvolumeclaim)
* [PodTemplateSpec](#podtemplatespec)
* [ReconcileInfo](#reconcileinfo)
* [RestoreSpec](#restorespec)
* [ServiceTemplate](#servicetemplate)
* [BucketConfig](#bucketconfig)
* [JobConfig](#jobconfig)

#### BackupStatus

BackupStatus represents the status of the last successful backup.

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| time | The time of the backup.  This is used to generate object keys of backup files in a bucket. | [metav1.Time](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#Time) | true |
| elapsed | Elapsed is the time spent on the backup. | [metav1.Duration](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#Duration) | true |
| sourceIndex | SourceIndex is the ordinal of the backup source instance. | int | true |
| sourceUUID | SourceUUID is the `server_uuid` of the backup source instance. | string | true |
| uuidSet | UUIDSet is the `server_uuid` set of all candidate instances for the backup source. | map[string]string | true |
| binlogFilename | BinlogFilename is the binlog filename that the backup source instance was writing to at the backup. | string | true |
| gtidSet | GTIDSet is the GTID set of the full dump of database. | string | true |
| dumpSize | DumpSize is the size in bytes of a full dump of database stored in an object storage bucket. | int64 | true |
| binlogSize | BinlogSize is the size in bytes of a tarball of binlog files stored in an object storage bucket. | int64 | true |
| workDirUsage | WorkDirUsage is the max usage in bytes of the woking directory. | int64 | true |
| warnings | Warnings are list of warnings from the last backup, if any. | []string | true |

[Back to Custom Resources](#custom-resources)

#### MySQLCluster

MySQLCluster is the Schema for the mysqlclusters API

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| metadata |  | [metav1.ObjectMeta](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#ObjectMeta) | false |
| spec |  | [MySQLClusterSpec](#mysqlclusterspec) | false |
| status |  | [MySQLClusterStatus](#mysqlclusterstatus) | false |

[Back to Custom Resources](#custom-resources)

#### MySQLClusterList

MySQLClusterList contains a list of MySQLCluster

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| metadata |  | [metav1.ListMeta](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#ListMeta) | false |
| items |  | [][MySQLCluster](#mysqlcluster) | true |

[Back to Custom Resources](#custom-resources)

#### MySQLClusterSpec

MySQLClusterSpec defines the desired state of MySQLCluster

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| replicas | Replicas is the number of instances. Available values are positive odd numbers. | int32 | false |
| podTemplate | PodTemplate is a `Pod` template for MySQL server container. | [PodTemplateSpec](#podtemplatespec) | true |
| volumeClaimTemplates | VolumeClaimTemplates is a list of `PersistentVolumeClaim` templates for MySQL server container. A claim named \"mysql-data\" must be included in the list. | [][PersistentVolumeClaim](#persistentvolumeclaim) | true |
| serviceTemplate | ServiceTemplate is a `Service` template for both primary and replicas. | *[ServiceTemplate](#servicetemplate) | false |
| mysqlConfigMapName | MySQLConfigMapName is a `ConfigMap` name of MySQL config. | *string | false |
| replicationSourceSecretName | ReplicationSourceSecretName is a `Secret` name which contains replication source info. If this field is given, the `MySQLCluster` works as an intermediate primary. | *string | false |
| collectors | Collectors is the list of collector flag names of mysqld_exporter. If this field is not empty, MOCO adds mysqld_exporter as a sidecar to collect and export mysqld metrics in Prometheus format.\n\nSee https://github.com/prometheus/mysqld_exporter/blob/master/README.md#collector-flags for flag names.\n\nExample: [\"engine_innodb_status\", \"info_schema.innodb_metrics\"] | []string | false |
| serverIDBase | ServerIDBase, if set, will become the base number of server-id of each MySQL instance of this cluster.  For example, if this is 100, the server-ids will be 100, 101, 102, and so on. If the field is not given or zero, MOCO automatically sets a random positive integer. | int32 | false |
| maxDelaySeconds | MaxDelaySeconds configures the readiness probe of mysqld container. For a replica mysqld instance, if it is delayed to apply transactions over this threshold, the mysqld instance will be marked as non-ready. The default is 60 seconds. Setting this field to 0 disables the delay check in the probe. | *int | false |
| startupDelaySeconds | StartupWaitSeconds is the maximum duration to wait for `mysqld` container to start working. The default is 3600 seconds. | int32 | false |
| logRotationSchedule | LogRotationSchedule specifies the schedule to rotate MySQL logs. If not set, the default is to rotate logs every 5 minutes. See https://pkg.go.dev/github.com/robfig/cron/v3#hdr-CRON_Expression_Format for the field format. | string | false |
| backupPolicyName | The name of BackupPolicy custom resource in the same namespace. If this is set, MOCO creates a CronJob to take backup of this MySQL cluster periodically. | *string | false |
| restore | Restore is the specification to perform Point-in-Time-Recovery from existing cluster. If this field is not null, MOCO restores the data as specified and create a new cluster with the data.  This field is not editable. | *[RestoreSpec](#restorespec) | false |
| disableSlowQueryLogContainer | DisableSlowQueryLogContainer controls whether to add a sidecar container named \"slow-log\" to output slow logs as the containers output. If set to true, the sidecar container is not added. The default is false. | bool | false |

[Back to Custom Resources](#custom-resources)

#### MySQLClusterStatus

MySQLClusterStatus defines the observed state of MySQLCluster

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| conditions | Conditions is an array of conditions. | [][metav1.Condition](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#Condition) | false |
| currentPrimaryIndex | CurrentPrimaryIndex is the index of the current primary Pod in StatefulSet. Initially, this is zero. | int | true |
| syncedReplicas | SyncedReplicas is the number of synced instances including the primary. | int | false |
| errantReplicas | ErrantReplicas is the number of instances that have errant transactions. | int | false |
| errantReplicaList | ErrantReplicaList is the list of indices of errant replicas. | []int | false |
| backup | Backup is the status of the last successful backup. | [BackupStatus](#backupstatus) | true |
| restoredTime | RestoredTime is the time when the cluster data is restored. | *[metav1.Time](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#Time) | false |
| cloned | Cloned indicates if the initial cloning from an external source has been completed. | bool | false |
| reconcileInfo | ReconcileInfo represents version information for reconciler. | [ReconcileInfo](#reconcileinfo) | true |

[Back to Custom Resources](#custom-resources)

#### ObjectMeta

ObjectMeta is metadata of objects. This is partially copied from metav1.ObjectMeta.

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| name | Name is the name of the object. | string | false |
| labels | Labels is a map of string keys and values. | map[string]string | false |
| annotations | Annotations is a map of string keys and values. | map[string]string | false |

[Back to Custom Resources](#custom-resources)

#### PersistentVolumeClaim

PersistentVolumeClaim is a user's request for and claim to a persistent volume. This is slightly modified from corev1.PersistentVolumeClaim.

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| metadata | Standard object's metadata. | [ObjectMeta](#objectmeta) | true |
| spec | Spec defines the desired characteristics of a volume requested by a pod author. | [PersistentVolumeClaimSpecApplyConfiguration](https://pkg.go.dev/k8s.io/client-go/applyconfigurations/core/v1#PersistentVolumeClaimSpecApplyConfiguration) | true |

[Back to Custom Resources](#custom-resources)

#### PodTemplateSpec

PodTemplateSpec describes the data a pod should have when created from a template. This is slightly modified from corev1.PodTemplateSpec.

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| metadata | Standard object's metadata.  The name in this metadata is ignored. | [ObjectMeta](#objectmeta) | false |
| spec | Specification of the desired behavior of the pod. The name of the MySQL server container in this spec must be `mysqld`. | [PodSpecApplyConfiguration](https://pkg.go.dev/k8s.io/client-go/applyconfigurations/core/v1#PodSpecApplyConfiguration) | true |

[Back to Custom Resources](#custom-resources)

#### ReconcileInfo

ReconcileInfo is the type to record the last reconciliation information.

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| generation | Generation is the `metadata.generation` value of the last reconciliation. See also https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#status-subresource | int64 | false |
| reconcileVersion | ReconcileVersion is the version of the operator reconciler. | int | true |

[Back to Custom Resources](#custom-resources)

#### RestoreSpec

RestoreSpec represents a set of parameters for Point-in-Time Recovery.

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| sourceName | SourceName is the name of the source `MySQLCluster`. | string | true |
| sourceNamespace | SourceNamespace is the namespace of the source `MySQLCluster`. | string | true |
| restorePoint | RestorePoint is the target date and time to restore data. The format is RFC3339.  e.g. \"2006-01-02T15:04:05Z\" | [metav1.Time](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#Time) | true |
| jobConfig | Specifies parameters for restore Pod. | [JobConfig](#jobconfig) | true |

[Back to Custom Resources](#custom-resources)

#### ServiceTemplate

ServiceTemplate defines the desired spec and annotations of Service

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| metadata | Standard object's metadata.  Only `annotations` and `labels` are valid. | [ObjectMeta](#objectmeta) | false |
| spec | Spec is the ServiceSpec | *[ServiceSpecApplyConfiguration](https://pkg.go.dev/k8s.io/client-go/applyconfigurations/core/v1#ServiceSpecApplyConfiguration) | false |

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
| caCert | Path to SSL CA certificate file used in addition to system default. | string | false |

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
