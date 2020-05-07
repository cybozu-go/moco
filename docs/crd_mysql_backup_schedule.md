MySQLBackupSchedule
===================

`MySQLBackupSchedule` is a custom resource definition (CRD) that represents
a full dump & binlog scheduling. This CR creates two `CronJob`s.
The one creates `MySQLDump` and the other creates `MySQLBinlog`.

| Field        | Type                                                    | Description                                                           |
|--------------|---------------------------------------------------------|-----------------------------------------------------------------------|
| `apiVersion` | string                                                  | APIVersion.                                                           |
| `kind`       | string                                                  | Kind.                                                                 |
| `metadata`   | [ObjectMeta]                                            | Standard object's metadata with a special annotation described below. |
| `spec`       | [MySQLBackupScheduleSpec](#MySQLBackupScheduleSpec)     | Specification of scheduling.                                          |
| `status`     | [MySQLBackupScheduleStatus](#MySQLBackupScheduleStatus) | Most recently observed status of the scheduled jobs.                  |

MySQLBackupScheduleSpec
-----------------------

| Field                   | Type                                    | Description                                    |
|-------------------------|-----------------------------------------|------------------------------------------------|
| `clusterName`           | string                                  | Name of `MySQLCluster`                         |
| `schedules`             | \[\][ScheduleSpec](#ScheduleSpec)       | Schedules to get full-dump or binlog.          |
| `objectStorageEndpoint` | [ObjectStorageSpec](#ObjectStorageSpec) | Specification of S3 compatible object storage. |

ScheduleSpec
------------

| Field      | Type   | Description                                                               |
|------------|--------|---------------------------------------------------------------------------|
| `name`     | string | The name of backup schedule.                                              |
| `schedule` | string | Schedule in Cron format, this value is passed to `CronJob.spec.schedule`. |
| `type`     | string | The type of backup. Allowed values are `Dump` and `Binlog`.               |

ObjectStorageSpec
-----------------

| Field                  | Type            | Description                                                           |
|------------------------|-----------------|-----------------------------------------------------------------------|
| `name`                 | string          | The name of object storage.                                           |
| `endpoint`             | [Value](#Value) | Endpoint of object storage.                                           |
| `region`               | [Value](#Value) | Region of object storage.                                             |
| `bucket`               | [Value](#Value) | Bucket name.                                                          |
| `credentialSecretName` | string          | Secret name created by the controller. This contains credential info. |

Value
-----
| Field       | Type                | Description                                                   |
|-------------|---------------------|---------------------------------------------------------------|
| `value`     | string              | The value of this field.                                      |
| `valueFrom` | [`Source`](#Source) | Source for the value. Cannot be used if `value` is not empty. |

Source
------

| Field             | Type                     | Description                   |
|-------------------|--------------------------|-------------------------------|
| `configMapKeyRef` | [`ConfigMapKeySelector`] | Selects a key of a ConfigMap. |



[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[`ConfigMapKeySelector`]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#configmapkeyselector-v1-core
