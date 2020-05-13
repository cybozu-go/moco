MySQLBackupSchedule
===================

`MySQLBackupSchedule` is a custom resource definition (CRD) that represents
a full dump & binlog scheduling. This CR creates two `CronJob`s.
The one is for `MySQLDump` and the other is for `MySQLBinlog`.

| Field        | Type                                                    | Description                                          |
| ------------ | ------------------------------------------------------- | ---------------------------------------------------- |
| `apiVersion` | string                                                  | APIVersion.                                          |
| `kind`       | string                                                  | Kind.                                                |
| `metadata`   | [ObjectMeta]                                            | Standard object's metadata.                          |
| `spec`       | [MySQLBackupScheduleSpec](#MySQLBackupScheduleSpec)     | Specification of scheduling.                         |
| `status`     | [MySQLBackupScheduleStatus](#MySQLBackupScheduleStatus) | Most recently observed status of the scheduled jobs. |

MySQLBackupScheduleSpec
-----------------------

| Field                    | Type                                    | Description                                                               |
| ------------------------ | --------------------------------------- | ------------------------------------------------------------------------- |
| `clusterName`            | string                                  | Name of [`MySQLCluster`](crd_mysql_cluster.md)                            |
| `schedule`               | string                                  | Schedule in Cron format, this value is passed to `CronJob.spec.schedule`. |
| `objectStorageEndpoint`  | [ObjectStorageSpec](#ObjectStorageSpec) | Specification of S3 compatible object storage.                            |
| `retentionPeriodSeconds` | int                                     | Retention period of each backup file.                                     |

MySQLBackupScheduleStatus
-------------------------

| Field                | Type                                                      | Description                                    |
| -------------------- | --------------------------------------------------------- | ---------------------------------------------- |
| `lastCompletionTime` | [Time]                                                    | Completion time of the last backup.            |
| `succeeded`          | boolean                                                   | `True` when the job is completed successfully. |
| `conditions`         | [][`BackupScheduleCondition`](#BackupScheduleCondition) | The array of conditions.                       |

ObjectStorageSpec
-----------------

| Field                  | Type            | Description                                                           |
| ---------------------- | --------------- | --------------------------------------------------------------------- |
| `endpoint`             | [Value](#Value) | Endpoint of object storage.                                           |
| `region`               | [Value](#Value) | Region of object storage.                                             |
| `bucket`               | [Value](#Value) | Bucket name.                                                          |
| `prefix`               | string          | File name prefix.                                                     |
| `credentialSecretName` | string          | Secret name created by the controller. This contains credential info. |

Value
-----

| Field       | Type                | Description                                                   |
| ----------- | ------------------- | ------------------------------------------------------------- |
| `value`     | string              | Value of this field.                                          |
| `valueFrom` | [`Source`](#Source) | Source for the value. Cannot be used if `value` is not empty. |

Source
------

| Field             | Type                     | Description                   |
| ----------------- | ------------------------ | ----------------------------- |
| `configMapKeyRef` | [`ConfigMapKeySelector`] | Selects a key of a ConfigMap. |

BackupScheduleCondition
------------------------

| Field                | Type   | Description                                                      |
| -------------------- | ------ | ---------------------------------------------------------------- |
| `type`               | string | The type of condition.                                           |
| `status`             | string | The status of the condition, one of True, False, Unknown         |
| `reason`             | string | One-word CamelCase reason for the condition's last transition.   |
| `message`            | string | Human-readable message indicating details about last transition. |
| `lastTransitionTime` | Time   | The last time the condition transit from one status to another.  |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
[`ConfigMapKeySelector`]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#configmapkeyselector-v1-core
