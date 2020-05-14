# BackupSchedule

`BackupSchedule` is a custom resource definition (CRD) that represents
a full dump & binlog scheduling.

`BackupSchedule` creates `CronJob` which collects binlog and then rotate dump.

| Field        | Type                                          | Description                                          |
| ------------ | --------------------------------------------- | ---------------------------------------------------- |
| `apiVersion` | string                                        | APIVersion.                                          |
| `kind`       | string                                        | Kind.                                                |
| `metadata`   | [ObjectMeta]                                  | Standard object's metadata.                          |
| `spec`       | [BackupScheduleSpec](#BackupScheduleSpec)     | Specification of scheduling.                         |
| `status`     | [BackupScheduleStatus](#BackupScheduleStatus) | Most recently observed status of the scheduled jobs. |

## BackupScheduleSpec

| Field                    | Type                                    | Required | Description                                                               |
| ------------------------ | --------------------------------------- | -------- | ------------------------------------------------------------------------- |
| `clusterName`            | string                                  | Yes      | Name of [`Cluster`](crd_mysql_cluster.md)                                 |
| `schedule`               | string                                  | Yes      | Schedule in Cron format, this value is passed to `CronJob.spec.schedule`. |
| `objectStorageEndpoint`  | [ObjectStorageSpec](#ObjectStorageSpec) | Yes      | Specification of S3 compatible object storage.                            |
| `retentionPeriodSeconds` | int                                     | Yes      | Retention period of each backup file.                                     |

## BackupScheduleStatus

| Field                | Type                                                    | Description                                    |
| -------------------- | ------------------------------------------------------- | ---------------------------------------------- |
| `lastCompletionTime` | [Time]                                                  | Completion time of the last backup.            |
| `succeeded`          | boolean                                                 | `True` when the job is completed successfully. |
| `conditions`         | [][`BackupScheduleCondition`](#BackupScheduleCondition) | The array of conditions.                       |

## BackupScheduleCondition

| Field                | Type   | Required | Description                                                      |
| -------------------- | ------ | -------- | ---------------------------------------------------------------- |
| `type`               | string | Yes      | The type of condition.                                           |
| `status`             | Enum   | Yes      | Status of the condition. One of `True`, `False`, `Unknown`.      |
| `reason`             | string | No       | One-word CamelCase reason for the condition's last transition.   |
| `message`            | string | No       | Human-readable message indicating details about last transition. |
| `lastTransitionTime` | [Time] | Yes      | The last time the condition transit from one status to another.  |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
