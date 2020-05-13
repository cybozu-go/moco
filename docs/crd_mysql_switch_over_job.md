MySQLSwitchoverJob
==================

`MySQLSwitchoverJob` is a custom resource definition (CRD) that represents switchover job.

| Field        | Type                                        | Description                                                           |
| ------------ | ------------------------------------------- | --------------------------------------------------------------------- |
| `apiVersion` | string                                      | APIVersion.                                                           |
| `kind`       | string                                      | Kind.                                                                 |
| `metadata`   | [ObjectMeta]                                | Standard object's metadata with a special annotation described below. |
| `spec`       | [SwitchoverJobSpec](#SwitchoverJobSpec)     | Configuration for switchover.                                         |
| `status`     | [SwitchoverJobStatus](#SwitchoverJobStatus) | Most recently observed status of the switchover.                      |

SwitchoverJobSpec
-----------------

| Field         | Type   | Description                                                      |
| ------------- | ------ | ---------------------------------------------------------------- |
| `clusterName` | string | Target [`MySQLCluster`](crd_mysql_cluster.md) name.              |
| `masterIndex` | int    | Ordinal of the new master in `StatefulSet` after the switchover. |

SwitchoverJobStatus
-------------------

| Field            | Type    | Description                                    |
| ---------------- | ------- | ---------------------------------------------- |
| `completionTime` | [Time]  | Completion time of the switchover.             |
| `succeeded`      | boolean | `True` when the job is completed successfully. |
| `message`        | string  | Reason for the result.                         |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
