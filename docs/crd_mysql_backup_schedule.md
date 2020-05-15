MySQLBackupSchedule
===================

`MySQLBackupSchedule` is a custom resource definition (CRD) that represents
a full dump & binlog scheduling.

`MySQLBackupSchedule` creates `CronJob` which collects binlog and then rotate dump.

| Field        | Type                                                    | Description                                          |
| ------------ | ------------------------------------------------------- | ---------------------------------------------------- |
| `apiVersion` | string                                                  | APIVersion.                                          |
| `kind`       | string                                                  | Kind.                                                |
| `metadata`   | [ObjectMeta]                                            | Standard object's metadata.                          |
| `spec`       | [MySQLBackupScheduleSpec](#MySQLBackupScheduleSpec)     | Specification of scheduling.                         |
| `status`     | [MySQLBackupScheduleStatus](#MySQLBackupScheduleStatus) | Most recently observed status of the scheduled jobs. |

MySQLBackupScheduleSpec
-----------------------

| Field               | Type       | Required | Description                                                               |
| ------------------- | ---------- | -------- | ------------------------------------------------------------------------- |
| `clusterName`       | string     | Yes      | Name of [MySQLCluster](crd_mysql_cluster.md).                             |
| `schedule`          | string     | Yes      | Schedule in Cron format, this value is passed to `CronJob.spec.schedule`. |
| `objectStorageName` | string     | Yes      | Name of [ObjectStorage](crd_object_storage.md).                           |
| `retentionPeriod`   | [Duration] | Yes      | Retention period of each backup file.                                     |

MySQLBackupScheduleStatus
-------------------------

| Field                | Type                                                              | Description                                    |
| -------------------- | ----------------------------------------------------------------- | ---------------------------------------------- |
| `lastCompletionTime` | [Time]                                                            | Completion time of the last backup.            |
| `succeeded`          | boolean                                                           | `true` when the job is completed successfully. |
| `conditions`         | \[\][MySQLBackupScheduleCondition](#MySQLBackupScheduleCondition) | The array of conditions.                       |

MySQLBackupScheduleCondition
----------------------------

| Field                | Type   | Required | Description                                                      |
| -------------------- | ------ | -------- | ---------------------------------------------------------------- |
| `type`               | Enum   | Yes      | The type of condition. Possible values are (TBD).                |
| `status`             | Enum   | Yes      | Status of the condition. One of `True`, `False`, `Unknown`.      |
| `reason`             | string | No       | One-word CamelCase reason for the condition's last transition.   |
| `message`            | string | No       | Human-readable message indicating details about last transition. |
| `lastTransitionTime` | [Time] | Yes      | The last time the condition transit from one status to another.  |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Duration]: https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1?tab=doc#Duration
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
