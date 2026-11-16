# API Reference

## Packages
- [moco.cybozu.com/v1beta2](#mococybozucomv1beta2)


## moco.cybozu.com/v1beta2

Package v1beta2 contains API Schema definitions for the moco.cybozu.com v1beta2 API group

### Resource Types
- [BackupPolicy](#backuppolicy)
- [BackupPolicyList](#backuppolicylist)
- [MySQLCluster](#mysqlcluster)
- [MySQLClusterList](#mysqlclusterlist)



#### BackupPolicy



BackupPolicy is a namespaced resource that should be referenced from MySQLCluster.



_Appears in:_
- [BackupPolicyList](#backuppolicylist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `moco.cybozu.com/v1beta2` | | |
| `kind` _string_ | `BackupPolicy` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  | Optional: \{\} <br /> |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  | Optional: \{\} <br /> |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[BackupPolicySpec](#backuppolicyspec)_ |  |  |  |


#### BackupPolicyList



BackupPolicyList contains a list of BackupPolicy





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `moco.cybozu.com/v1beta2` | | |
| `kind` _string_ | `BackupPolicyList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  | Optional: \{\} <br /> |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  | Optional: \{\} <br /> |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[BackupPolicy](#backuppolicy) array_ |  |  |  |


#### BackupPolicySpec



BackupPolicySpec defines the configuration items for MySQLCluster backup.

The following fields will be copied to CronJob.spec:

- Schedule
- TimeZone
- StartingDeadlineSeconds
- ConcurrencyPolicy
- SuccessfulJobsHistoryLimit
- FailedJobsHistoryLimit

The following fields will be copied to CronJob.spec.jobTemplate.

- ActiveDeadlineSeconds
- BackoffLimit



_Appears in:_
- [BackupPolicy](#backuppolicy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `schedule` _string_ | The schedule in Cron format for periodic backups.<br />See https://en.wikipedia.org/wiki/Cron |  |  |
| `timeZone` _string_ | The time zone name for the given schedule, see https://en.wikipedia.org/wiki/List_of_tz_database_time_zones.<br />If not specified, this defaults to the time zone of the kube-controller-manager process (native Kubernetes CronJob behavior).<br />More information can be found in https://kubernetes.io/docs/concepts/workloads/controllers/cron-jobs/#time-zones |  | Optional: \{\} <br /> |
| `jobConfig` _[JobConfig](#jobconfig)_ | Specifies parameters for backup Pod. |  |  |
| `startingDeadlineSeconds` _integer_ | Optional deadline in seconds for starting the job if it misses scheduled<br />time for any reason.  Missed jobs executions will be counted as failed ones. |  | Optional: \{\} <br /> |
| `concurrencyPolicy` _[ConcurrencyPolicy](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#concurrencypolicy-v1-batch)_ | Specifies how to treat concurrent executions of a Job.<br />Valid values are:<br />- "Allow" (default): allows CronJobs to run concurrently;<br />- "Forbid": forbids concurrent runs, skipping next run if previous run hasn't finished yet;<br />- "Replace": cancels currently running job and replaces it with a new one | Allow | Enum: [Allow Forbid Replace] <br />Optional: \{\} <br /> |
| `activeDeadlineSeconds` _integer_ | Specifies the duration in seconds relative to the startTime that the job<br />may be continuously active before the system tries to terminate it; value<br />must be positive integer. If a Job is suspended (at creation or through an<br />update), this timer will effectively be stopped and reset when the Job is<br />resumed again. |  | Optional: \{\} <br /> |
| `backoffLimit` _integer_ | Specifies the number of retries before marking this job failed.<br />Defaults to 6 |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `successfulJobsHistoryLimit` _integer_ | The number of successful finished jobs to retain.<br />This is a pointer to distinguish between explicit zero and not specified.<br />Defaults to 3. |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `failedJobsHistoryLimit` _integer_ | The number of failed finished jobs to retain.<br />This is a pointer to distinguish between explicit zero and not specified.<br />Defaults to 1. |  | Minimum: 0 <br />Optional: \{\} <br /> |


#### BackupStatus



BackupStatus represents the status of the last successful backup.



_Appears in:_
- [MySQLClusterStatus](#mysqlclusterstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `time` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#time-v1-meta)_ | The time of the backup.  This is used to generate object keys of backup files in a bucket. |  |  |
| `elapsed` _[Duration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#duration-v1-meta)_ | Elapsed is the time spent on the backup. |  |  |
| `sourceIndex` _integer_ | SourceIndex is the ordinal of the backup source instance. |  |  |
| `sourceUUID` _string_ | SourceUUID is the `server_uuid` of the backup source instance. |  |  |
| `uuidSet` _object (keys:string, values:string)_ | UUIDSet is the `server_uuid` set of all candidate instances for the backup source. |  | Optional: \{\} <br /> |
| `binlogFilename` _string_ | BinlogFilename is the binlog filename that the backup source instance was writing to<br />at the backup. |  |  |
| `gtidSet` _string_ | GTIDSet is the GTID set of the full dump of database. |  |  |
| `dumpSize` _integer_ | DumpSize is the size in bytes of a full dump of database stored in an object storage bucket. |  |  |
| `binlogSize` _integer_ | BinlogSize is the size in bytes of a tarball of binlog files stored in an object storage bucket. |  |  |
| `workDirUsage` _integer_ | WorkDirUsage is the max usage in bytes of the woking directory. |  |  |
| `warnings` _string array_ | Warnings are list of warnings from the last backup, if any. |  |  |


#### BucketConfig



BucketConfig is a set of parameter to access an object storage bucket.



_Appears in:_
- [JobConfig](#jobconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `bucketName` _string_ | The name of the bucket |  | MinLength: 1 <br /> |
| `region` _string_ | The region of the bucket.<br />This can also be set through `AWS_REGION` environment variable. |  | Optional: \{\} <br /> |
| `endpointURL` _string_ | The API endpoint URL.  Set this for non-S3 object storages. |  | Pattern: `^https?://.*` <br />Optional: \{\} <br /> |
| `usePathStyle` _boolean_ | Allows you to enable the client to use path-style addressing, i.e.,<br />https?://ENDPOINT/BUCKET/KEY. By default, a virtual-host addressing<br />is used (https?://BUCKET.ENDPOINT/KEY). |  | Optional: \{\} <br /> |
| `backendType` _string_ | BackendType is an identifier for the object storage to be used. | s3 | Enum: [s3 gcs azure] <br />Optional: \{\} <br /> |
| `caCert` _string_ | Path to SSL CA certificate file used in addition to system default. |  | Optional: \{\} <br /> |


#### JobConfig



JobConfig is a set of parameters for backup and restore job Pods.



_Appears in:_
- [BackupPolicySpec](#backuppolicyspec)
- [RestoreSpec](#restorespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `serviceAccountName` _string_ | ServiceAccountName specifies the ServiceAccount to run the Pod. |  | MinLength: 1 <br /> |
| `bucketConfig` _[BucketConfig](#bucketconfig)_ | Specifies how to access an object storage bucket. |  |  |
| `threads` _integer_ | Threads is the number of threads used for backup or restoration. | 4 | Minimum: 1 <br />Optional: \{\} <br /> |
| `cpu` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#quantity-resource-api)_ | CPU is the amount of CPU requested for the Pod. | 4 | Optional: \{\} <br /> |
| `maxCpu` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#quantity-resource-api)_ | MaxCPU is the amount of maximum CPU for the Pod. |  | Optional: \{\} <br /> |
| `memory` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#quantity-resource-api)_ | Memory is the amount of memory requested for the Pod. | 4Gi | Optional: \{\} <br /> |
| `maxMemory` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#quantity-resource-api)_ | MaxMemory is the amount of maximum memory for the Pod. |  | Optional: \{\} <br /> |


#### MySQLCluster



MySQLCluster is the Schema for the mysqlclusters API



_Appears in:_
- [MySQLClusterList](#mysqlclusterlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `moco.cybozu.com/v1beta2` | | |
| `kind` _string_ | `MySQLCluster` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  | Optional: \{\} <br /> |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  | Optional: \{\} <br /> |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[MySQLClusterSpec](#mysqlclusterspec)_ |  |  |  |
| `status` _[MySQLClusterStatus](#mysqlclusterstatus)_ |  |  |  |


#### MySQLClusterList



MySQLClusterList contains a list of MySQLCluster





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `moco.cybozu.com/v1beta2` | | |
| `kind` _string_ | `MySQLClusterList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  | Optional: \{\} <br /> |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  | Optional: \{\} <br /> |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[MySQLCluster](#mysqlcluster) array_ |  |  |  |


#### MySQLClusterSpec



MySQLClusterSpec defines the desired state of MySQLCluster



_Appears in:_
- [MySQLCluster](#mysqlcluster)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `replicas` _integer_ | Replicas is the number of instances. Available values are positive odd numbers. | 1 | Optional: \{\} <br /> |
| `podTemplate` _[PodTemplateSpec](#podtemplatespec)_ | PodTemplate is a `Pod` template for MySQL server container. |  |  |
| `volumeClaimTemplates` _[PersistentVolumeClaim](#persistentvolumeclaim) array_ | VolumeClaimTemplates is a list of `PersistentVolumeClaim` templates for MySQL server container.<br />A claim named "mysql-data" must be included in the list. |  | MinItems: 1 <br /> |
| `primaryServiceTemplate` _[ServiceTemplate](#servicetemplate)_ | PrimaryServiceTemplate is a `Service` template for primary. |  | Optional: \{\} <br /> |
| `replicaServiceTemplate` _[ServiceTemplate](#servicetemplate)_ | ReplicaServiceTemplate is a `Service` template for replica. |  | Optional: \{\} <br /> |
| `mysqlConfigMapName` _string_ | MySQLConfigMapName is a `ConfigMap` name of MySQL config. |  | Optional: \{\} <br /> |
| `replicationSourceSecretName` _string_ | ReplicationSourceSecretName is a `Secret` name which contains replication source info.<br />If this field is given, the `MySQLCluster` works as an intermediate primary. |  | Optional: \{\} <br /> |
| `collectors` _string array_ | Collectors is the list of collector flag names of mysqld_exporter.<br />If this field is not empty, MOCO adds mysqld_exporter as a sidecar to collect<br />and export mysqld metrics in Prometheus format.<br />See https://github.com/prometheus/mysqld_exporter/blob/master/README.md#collector-flags for flag names.<br />Example: ["engine_innodb_status", "info_schema.innodb_metrics"] |  | Optional: \{\} <br /> |
| `serverIDBase` _integer_ | ServerIDBase, if set, will become the base number of server-id of each MySQL<br />instance of this cluster.  For example, if this is 100, the server-ids will be<br />100, 101, 102, and so on.<br />If the field is not given or zero, MOCO automatically sets a random positive integer. |  | Optional: \{\} <br /> |
| `maxDelaySeconds` _integer_ | MaxDelaySeconds configures the readiness probe of mysqld container.<br />For a replica mysqld instance, if it is delayed to apply transactions over this threshold,<br />the mysqld instance will be marked as non-ready.<br />The default is 60 seconds.<br />Setting this field to 0 disables the delay check in the probe. | 60 | Minimum: 0 <br />Optional: \{\} <br /> |
| `maxDelaySecondsForPodDeletion` _integer_ | MaxDelaySecondsForPodDeletion configures the maximum allowed replication delay before a Pod deletion is blocked.<br />If the replication delay exceeds this threshold, deletion of the primary pod will be prevented.<br />The default is 0 seconds.<br />Setting this field to 0 disables the delay check for pod deletion. | 0 | Minimum: 0 <br />Optional: \{\} <br /> |
| `startupWaitSeconds` _integer_ | StartupWaitSeconds is the maximum duration to wait for `mysqld` container to start working.<br />The default is 3600 seconds. | 3600 | Minimum: 0 <br />Optional: \{\} <br /> |
| `logRotationSchedule` _string_ | LogRotationSchedule specifies the schedule to rotate MySQL logs.<br />If not set, the default is to rotate logs every 5 minutes.<br />See https://pkg.go.dev/github.com/robfig/cron/v3#hdr-CRON_Expression_Format for the field format. |  | Optional: \{\} <br /> |
| `logRotationSize` _integer_ | LogRotationSize specifies the size to rotate MySQL logs<br />If not set, size-based log rotation is disabled by default |  | Optional: \{\} <br /> |
| `backupPolicyName` _string_ | The name of BackupPolicy custom resource in the same namespace.<br />If this is set, MOCO creates a CronJob to take backup of this MySQL cluster periodically. |  | Optional: \{\} <br /> |
| `restore` _[RestoreSpec](#restorespec)_ | Restore is the specification to perform Point-in-Time-Recovery from existing cluster.<br />If this field is not null, MOCO restores the data as specified and create a new<br />cluster with the data.  This field is not editable. |  | Optional: \{\} <br /> |
| `disableSlowQueryLogContainer` _boolean_ | DisableSlowQueryLogContainer controls whether to add a sidecar container named "slow-log"<br />to output slow logs as the containers output.<br />If set to true, the sidecar container and configmap used by the sidecar container are not added. The default is false. |  | Optional: \{\} <br /> |
| `slowQueryLogConfigTmpl` _string_ | SlowQueryLogConfigTmpl is the template for slow query log configuration file.<br />If this field is null, MOCO uses the default slow query log configuration.<br />`\{\{ .Path \}\}` will be replaced with the path to the slow query log file. |  | Optional: \{\} <br /> |
| `agentUseLocalhost` _boolean_ | AgentUseLocalhost configures the mysqld interface to bind and be accessed over localhost instead of pod name.<br />During container init moco-agent will set mysql admin interface is bound to localhost. The moco-agent will also<br />communicate with mysqld over localhost when acting as a sidecar. |  |  |
| `initializeTimezoneData` _boolean_ | InitializeTimezoneData controls whether the init container should populate the timezone data.<br />If set to true, the init container will load timezone data into MySQL.<br />The default is false. | false | Optional: \{\} <br /> |
| `offline` _boolean_ | Offline sets the cluster offline, releasing compute resources. Data is not removed. |  | Optional: \{\} <br /> |


#### MySQLClusterStatus



MySQLClusterStatus defines the observed state of MySQLCluster



_Appears in:_
- [MySQLCluster](#mysqlcluster)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#condition-v1-meta) array_ | Conditions is an array of conditions. |  | Optional: \{\} <br /> |
| `currentPrimaryIndex` _integer_ | CurrentPrimaryIndex is the index of the current primary Pod in StatefulSet.<br />Initially, this is zero. |  |  |
| `syncedReplicas` _integer_ | SyncedReplicas is the number of synced instances including the primary. |  | Optional: \{\} <br /> |
| `errantReplicas` _integer_ | ErrantReplicas is the number of instances that have errant transactions. |  | Optional: \{\} <br /> |
| `errantReplicaList` _integer array_ | ErrantReplicaList is the list of indices of errant replicas. |  | Optional: \{\} <br /> |
| `backup` _[BackupStatus](#backupstatus)_ | Backup is the status of the last successful backup. |  | Optional: \{\} <br /> |
| `restoredTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#time-v1-meta)_ | RestoredTime is the time when the cluster data is restored. |  | Optional: \{\} <br /> |
| `cloned` _boolean_ | Cloned indicates if the initial cloning from an external source has been completed. |  | Optional: \{\} <br /> |
| `reconcileInfo` _[ReconcileInfo](#reconcileinfo)_ | ReconcileInfo represents version information for reconciler. |  | Optional: \{\} <br /> |


#### ObjectMeta



ObjectMeta is metadata of objects.
This is partially copied from metav1.ObjectMeta.



_Appears in:_
- [PersistentVolumeClaim](#persistentvolumeclaim)
- [PodTemplateSpec](#podtemplatespec)
- [ServiceTemplate](#servicetemplate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the name of the object. |  | Optional: \{\} <br /> |
| `labels` _object (keys:string, values:string)_ | Labels is a map of string keys and values. |  | Optional: \{\} <br /> |
| `annotations` _object (keys:string, values:string)_ | Annotations is a map of string keys and values. |  | Optional: \{\} <br /> |


#### OverwriteContainer



OverwriteContainer defines the container spec used for overwriting.
For more information, please read the following documentation.
https://cybozu-go.github.io/moco/customize-system-container.html



_Appears in:_
- [PodTemplateSpec](#podtemplatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _[OverwriteableContainerName](#overwriteablecontainername)_ | Name of the container to overwrite. |  | Enum: [agent copy-moco-init moco-init slow-log mysqld-exporter] <br />Required: \{\} <br /> |


#### OverwriteableContainerName

_Underlying type:_ _string_

OverwriteableContainerName is the name of the container.

_Validation:_
- Enum: [agent copy-moco-init moco-init slow-log mysqld-exporter]

_Appears in:_
- [OverwriteContainer](#overwritecontainer)



#### PersistentVolumeClaim



PersistentVolumeClaim is a user's request for and claim to a persistent volume.
This is slightly modified from corev1.PersistentVolumeClaim.



_Appears in:_
- [MySQLClusterSpec](#mysqlclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metadata` _[ObjectMeta](#objectmeta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |


#### PodTemplateSpec



PodTemplateSpec describes the data a pod should have when created from a template.
This is slightly modified from corev1.PodTemplateSpec.



_Appears in:_
- [MySQLClusterSpec](#mysqlclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metadata` _[ObjectMeta](#objectmeta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `overwriteContainers` _[OverwriteContainer](#overwritecontainer) array_ | OverwriteContainers overwrites the container definitions provided by default by the system. |  | Optional: \{\} <br /> |


#### ReconcileInfo



ReconcileInfo is the type to record the last reconciliation information.



_Appears in:_
- [MySQLClusterStatus](#mysqlclusterstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `generation` _integer_ | Generation is the `metadata.generation` value of the last reconciliation.<br />See also https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#status-subresource |  | Optional: \{\} <br /> |
| `reconcileVersion` _integer_ | ReconcileVersion is the version of the operator reconciler. |  | Optional: \{\} <br /> |


#### RestoreSpec



RestoreSpec represents a set of parameters for Point-in-Time Recovery.



_Appears in:_
- [MySQLClusterSpec](#mysqlclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `sourceName` _string_ | SourceName is the name of the source `MySQLCluster`. |  | MinLength: 1 <br /> |
| `sourceNamespace` _string_ | SourceNamespace is the namespace of the source `MySQLCluster`. |  | MinLength: 1 <br /> |
| `restorePoint` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#time-v1-meta)_ | RestorePoint is the target date and time to restore data.<br />The format is RFC3339.  e.g. "2006-01-02T15:04:05Z" |  |  |
| `jobConfig` _[JobConfig](#jobconfig)_ | Specifies parameters for restore Pod. |  |  |
| `schema` _string_ | Schema is the name of the schema to restore.<br />If empty, all schemas are restored.<br />This is used for `mysqlbinlog` option `--database`.<br />Thus, this option changes behavior depending on binlog_format.<br />For more information, please read the following documentation.<br />https://dev.mysql.com/doc/refman/8.4/en/mysqlbinlog.html#option_mysqlbinlog_database<br />NOTE: Restore will fail if any user holds privileges on tables outside the target schema. |  | Optional: \{\} <br /> |
| `users` _string_ | Users is the name of the comma separated users to restore.<br />example: "user1@%,user2@host,user3"<br />If empty, all users are restored.<br />This is used for `mysqlsh load-dump utility` option `--includeUsers`.<br />For more information, please read the following documentation.<br />https://dev.mysql.com/doc/mysql-shell/8.4/en/mysql-shell-utilities-load-dump.html#mysql-shell-utilities-load-dump-opt-filtering |  | Optional: \{\} <br /> |


#### ServiceTemplate



ServiceTemplate defines the desired spec and annotations of Service



_Appears in:_
- [MySQLClusterSpec](#mysqlclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metadata` _[ObjectMeta](#objectmeta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |




