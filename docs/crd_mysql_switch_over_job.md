MySQLSwitchOverJob
==================

`MySQLSwitchOverJob` is a custom resource definition (CRD) that represents switchover job.

| Field        | Type                                        | Description                                                           |
| ------------ | ------------------------------------------- | --------------------------------------------------------------------- |
| `apiVersion` | string                                      | APIVersion.                                                           |
| `kind`       | string                                      | Kind.                                                                 |
| `metadata`   | [ObjectMeta]                                | Standard object's metadata with a special annotation described below. |
| `spec`       | [SwitchOverJobSpec](#SwitchOverJobSpec)     | Configuration for switch over.                                        |
| `status`     | [SwitchOverJobStatus](#SwitchOverJobStatus) | Most recently observed status of the switch over.                     |

SwitchOverJobSpec
-----------------

| Field         | Type   | Description                                                  |
| ------------- | ------ | ------------------------------------------------------------ |
| `clusterName` | string | Target [`MySQLCluster`](crd_mysql_cluster.md) name.          |
| `masterIndex` | int    | `StatefulSet` index of the new master after the switch over. |

SwitchOverJobStatus
-------------------

| Field            | Type    | Description                                    |
| ---------------- | ------- | ---------------------------------------------- |
| `completionTime` | [Time]  | The completion time of the switch over.        |
| `succeeded`      | boolean | `true` when the job is completed successfully. |
| `message`        | string  | The reason for the result.                     |
