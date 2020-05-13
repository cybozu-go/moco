MySQLSwitchoverJob
==================

`MySQLSwitchoverJob` is a custom resource definition (CRD) that represents switchover job.

| Field        | Type                                        | Description                                      |
| ------------ | ------------------------------------------- | ------------------------------------------------ |
| `apiVersion` | string                                      | APIVersion.                                      |
| `kind`       | string                                      | Kind.                                            |
| `metadata`   | [ObjectMeta]                                | Standard object's metadata.                      |
| `spec`       | [SwitchoverJobSpec](#SwitchoverJobSpec)     | Configuration for switchover.                    |
| `status`     | [SwitchoverJobStatus](#SwitchoverJobStatus) | Most recently observed status of the switchover. |

SwitchoverJobSpec
-----------------

| Field         | Type   | Description                                                      |
| ------------- | ------ | ---------------------------------------------------------------- |
| `clusterName` | string | Target [`MySQLCluster`](crd_mysql_cluster.md) name.              |
| `masterIndex` | int    | Ordinal of the new master in `StatefulSet` after the switchover. |

SwitchoverJobStatus
-------------------

| Field            | Type                                                  | Description                                    |
| ---------------- | ----------------------------------------------------- | ---------------------------------------------- |
| `completionTime` | [Time]                                                | Completion time of the switchover.             |
| `succeeded`      | boolean                                               | `True` when the job is completed successfully. |
| `conditions`     | [][`SwitchoverJobCondition`](#SwitchoverJobCondition) | Array of conditions.                           |

SwitchoverJobCondition
----------------------

| Field                | Type   | Description                                                      |
| -------------------- | ------ | ---------------------------------------------------------------- |
| `type`               | string | Type of condition.                                               |
| `status`             | string | Status of the condition, one of `True`, `False`, `Unknown`       |
| `reason`             | string | One-word CamelCase reason for the condition's last transition.   |
| `message`            | string | Human-readable message indicating details about last transition. |
| `lastTransitionTime` | Time   | The last time the condition transit from one status to another.  |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
