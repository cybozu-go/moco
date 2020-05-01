MySQLBackupSchedule
===================

`MySQLBackupSchedule` is a custom resource definition (CRD) that represents
a full dump & binlog scheduling. This CR creates two `CronJob`s.
The one creates `MySQLDump` and the other creates `MySQLBinlog`.

| Field        | Type                                                    | Description                                                           |
| ------------ | ------------------------------------------------------- | --------------------------------------------------------------------- |
| `apiVersion` | string                                                  | APIVersion.                                                           |
| `kind`       | string                                                  | Kind.                                                                 |
| `metadata`   | [ObjectMeta]                                            | Standard object's metadata with a special annotation described below. |
| `spec`       | [MySQLBackupScheduleSpec](#MySQLBackupScheduleSpec)     | Specification of scheduling.                                          |
| `status`     | [MySQLBackupScheduleStatus](#MySQLBackupScheduleStatus) | Most recently observed status of the scheduled jobs.                  |

MySQLBackupSceduleSpec
----------------------

| Field     | Type                          | Description                        |
| --------- | ----------------------------- | ---------------------------------- |
| `cluster` | string                        | Name of `MySQLCluster`             |
| `dump`    | [ScheduleSpec](#ScheduleSpec) | Schedule of invoking `mysqlpump`   |
| `binlog`  | [ScheduleSpec](#ScheduleSpec) | Schedule of invoking `mysqlbinlog` |

ScheduleSpec
------------

| Field                   | Type                                    | Description                                                               |
| ----------------------- | --------------------------------------- | ------------------------------------------------------------------------- |
| `schedule`              | string                                  | Schedule in Cron format, this value is passed to `CronJob.spec.schedule`. |
| `objectStorageEndpoint` | [ObjectStorageSpec](#ObjectStorageSpec) | Specification of S3 compatible object storage.                            |

ObjectStorageSpec
-----------------

| Field                  | Type   | Description                                                                    |
| ---------------------- | ------ | ------------------------------------------------------------------------------ |
| `endpoint`             | string | Endpoint of object storage.                                                    |
| `region`               | string | Region of object storage. Default is empty.                                    |
| `bucket`               | string | Bucket name.                                                                   |
| `credentialSecretName` | string | Secret name which is created by the controller. This contains credential info. |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
