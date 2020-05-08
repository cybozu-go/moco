MySQLRestoreJob
===============

`MySQLRestoreJob` is a custom resource definition (CRD) that represents a Point-in-Time Recovery (PiTR) job that targets a given [`MySQLCluster`](crd_mysql_cluster.md).

| Field        | Type                                       | Description                                                           |
| ------------ | ------------------------------------------ | --------------------------------------------------------------------- |
| `apiVersion` | string                                     | APIVersion.                                                           |
| `kind`       | string                                     | Kind.                                                                 |
| `metadata`   | [ObjectMeta]                               | Standard object's metadata with a special annotation described below. |
| `spec`       | [MySQLRestoreJobSpec](#RestoreJobSpec)     | Configuration for PiTR.                                               |
| `status`     | [MySQLRestoreJobStatus](#RestoreJobStatus) | Most recently observed status of the PiTR.                            |

RestoreJobSpec
--------------

| Field               | Type            | Description                                                  |
| ------------------- | --------------- | ------------------------------------------------------------ |
| `targetClusterName` | string          | Target [`MySQLCluster`](crd_mysql_cluster.md) name.          |
| `pointInTime`       | [Time]          | Point-in-time of the state which the cluster is restored to. |
| `dumpSelector`      | [LabelSelector] | Label selector for [`MySQLDump`](crd_mysql_dump.md).         |
| `binlogSelector`    | [LabelSelector] | Label selector for [`MySQLBinlog`](crd_mysql_binlog.md).     |

RestoreJobStatus
----------------

| Field            | Type                                                | Description                                    |
| ---------------- | --------------------------------------------------- | ---------------------------------------------- |
| `completionTime` | [Time]                                              | The completion time of restore.                |
| `succeeded`      | boolean                                             | `true` when the job is completed successfully. |
| `conditions`     | \[\][`RestoreJobConditions`](#RestoreJobConditions) | The array of conditions.                       |

RestoreJobConditions
--------------------

| Field                | Type   | Description                                                      |
| -------------------- | ------ | ---------------------------------------------------------------- |
| `type`               | string | The type of condition.                                           |
| `status`             | string | The status of the condition, one of True, False, Unknown         |
| `reason`             | string | One-word CamelCase reason for the condition's last transition.   |
| `message`            | string | Human-readable message indicating details about last transition. |
| `lastTransitionTime` | Time   | The last time the condition transit from one status to another.  |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
[LabelSelector]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#labelselector-v1-meta
