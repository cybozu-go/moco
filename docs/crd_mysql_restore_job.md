MySQLRestoreJob
===============

`MySQLRestoreJob` is a custom resource definition (CRD) that representsa Point-in-Time Recovery (PiTR)
job that targets a given [`MySQLCluster`](crd_mysql_cluster.md).

Restoration fails if the appropriate dump or binlog file is lost, and the operator does nothing.

| Field        | Type                                  | Description                                |
| ------------ | ------------------------------------- | ------------------------------------------ |
| `apiVersion` | string                                | APIVersion.                                |
| `kind`       | string                                | Kind.                                      |
| `metadata`   | [ObjectMeta]                          | Standard object's metadata.                |
| `spec`       | [RestoreJobSpec](#RestoreJobSpec)     | Configuration for PiTR.                    |
| `status`     | [RestoreJobStatus](#RestoreJobStatus) | Most recently observed status of the PiTR. |

RestoreJobSpec
--------------

| Field               | Type   | Description                                                  |
| ------------------- | ------ | ------------------------------------------------------------ |
| `targetClusterName` | string | Target [`MySQLCluster`](crd_mysql_cluster.md) name.          |
| `sourceClusterName` | string | Source [`MySQLCluster`](crd_mysql_cluster.md) name.          |
| `pointInTime`       | [Time] | Point-in-time of the state which the cluster is restored to. |

RestoreJobStatus
----------------

| Field            | Type                                            | Description                                    |
| ---------------- | ----------------------------------------------- | ---------------------------------------------- |
| `completionTime` | [Time]                                          | Completion time of restoration.                |
| `succeeded`      | boolean                                         | `True` when the job is completed successfully. |
| `conditions`     | [][`RestoreJobCondition`](#RestoreJobCondition) | Array of conditions.                           |

RestoreJobCondition
--------------------

| Field                | Type   | Description                                                      |
| -------------------- | ------ | ---------------------------------------------------------------- |
| `type`               | string | Type of condition.                                               |
| `status`             | string | Status of the condition, one of `True`, `False`, `Unknown`       |
| `reason`             | string | One-word CamelCase reason for the condition's last transition.   |
| `message`            | string | Human-readable message indicating details about last transition. |
| `lastTransitionTime` | Time   | The last time the condition transit from one status to another.  |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
