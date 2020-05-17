SwitchoverJob
=============

`SwitchoverJob` is a custom resource definition (CRD) that represents a switchover job.

| Field        | Type                                        | Description                                      |
| ------------ | ------------------------------------------- | ------------------------------------------------ |
| `apiVersion` | string                                      | APIVersion.                                      |
| `kind`       | string                                      | Kind.                                            |
| `metadata`   | [ObjectMeta]                                | Standard object's metadata.                      |
| `spec`       | [SwitchoverJobSpec](#SwitchoverJobSpec)     | Configuration for switchover.                    |
| `status`     | [SwitchoverJobStatus](#SwitchoverJobStatus) | Most recently observed status of the switchover. |

SwitchoverJobSpec
-----------------

| Field                     | Type   | Required | Description                                                                                                                                                                                                  |
| ------------------------- | ------ | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `clusterName`             | string | Yes      | Target [MySQLCluster](crd_mysql_cluster.md) name.                                                                                                                                                            |
| `masterIndex`             | int    | Yes      | Ordinal of the new master in `StatefulSet` after the switchover.                                                                                                                                             |
| `ttlSecondsAfterFinished` | int    | No       | Limits the lifetime of a Job that has finished execution (either Complete or Failed).<br />If this field is set, ttlSecondsAfterFinished after the SwitchoverJob finishes, it is eligible to be automatically deleted.|

SwitchoverJobStatus
-------------------

| Field            | Type                                                  | Description                                    |
| ---------------- | ----------------------------------------------------- | ---------------------------------------------- |
| `completionTime` | [Time]                                                | Completion time of the switchover.             |
| `succeeded`      | boolean                                               | `true` when the job is completed successfully. |
| `conditions`     | \[\][SwitchoverJobCondition](#SwitchoverJobCondition) | Array of conditions.                           |

SwitchoverJobCondition
----------------------

| Field                | Type   | Required | Description                                                      |
| -------------------- | ------ | -------- | ---------------------------------------------------------------- |
| `type`               | Enum   | Yes      | The type of condition. Possible values are (TBD).                |
| `status`             | Enum   | Yes      | Status of the condition. One of `True`, `False`, `Unknown`.      |
| `reason`             | string | No       | One-word CamelCase reason for the condition's last transition.   |
| `message`            | string | No       | Human-readable message indicating details about last transition. |
| `lastTransitionTime` | [Time] | Yes      | The last time the condition transit from one status to another.  |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
