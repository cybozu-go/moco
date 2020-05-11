MySQLCluster
============

`MySQLCluster` is a custom resource definition (CRD) that represents
a MySQL cluster. In this context, MySQL cluster means a cluster of MySQL servers
which are replicated by `mysqlbinlog` and `mysqlpump` without group replication
such as InnoDB cluster.

| Field        | Type                                      | Description                                                           |
| ------------ | ----------------------------------------- | --------------------------------------------------------------------- |
| `apiVersion` | string                                    | APIVersion.                                                           |
| `kind`       | string                                    | Kind.                                                                 |
| `metadata`   | [ObjectMeta]                              | Standard object's metadata with a special annotation described below. |
| `spec`       | [MySQLClusterSpec](#MySQLClusterSpec)     | Specification of desired behavior of the cluster.                     |
| `status`     | [MySQLClusterStatus](#MySQLClusterStatus) | Most recently observed status of the cluster.                         |

MySQLClusterSpec
----------------

| Field                    | Type                    | Description                                                               |
| ------------------------ | ----------------------- | ------------------------------------------------------------------------- |
| `rootPasswordSecretName` | string                  | Secret name for root user.                                                |
| `volumeClaimTemplate`    | [PersistentVolumeClaim] | `PersistentVolumeClaim` for MySQL server container.                       |
| `size`                   | int                     | The number of instances. Available values are 1, 3, and 5.                |
| `podTemplate`            | [PodSpec]               | `Pod` template for MySQL server container.                                |
| `serviceTemplate`        | [ServiceSpec]           | `Service` template for endpoints of MySQL server containers.              |
| `mySQLConfigName`        | string                  | `ConfigMap` name of MySQL config. ToDO:  write merge strategy of `my.cnf` |

MySQLClusterStatus
------------------

| Field               | Type                                                                | Description                 |
| ------------------- | ------------------------------------------------------------------- | --------------------------- |
| `conditions`        | \[\][`MySQLClusterStatusConditions`](#MySQLClusterStatusConditions) | The array of conditions.    |
| `ready`             | boolean                                                             | The health of the cluster.  |
| `currentMasterName` | string                                                              | Current master name.        |
| `syncedReplicas`    | int                                                                 | Number of synced instances. |

MySQLClusterStatusConditions
----------------------------

| Field                | Type   | Description                                                      |
| -------------------- | ------ | ---------------------------------------------------------------- |
| `type`               | string | The type of condition.                                           |
| `status`             | string | The status of the condition, one of True, False, Unknown         |
| `reason`             | string | One-word CamelCase reason for the condition's last transition.   |
| `message`            | string | Human-readable message indicating details about last transition. |
| `lastHeartbeatTime`  | Time   | The last time we got an update on a given condition.             |
| `lastTransitionTime` | Time   | The last time the condition transit from one status to another.  |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
[LabelSelector]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#labelselector-v1-meta
[PersistentVolumeClaim]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#persistentvolumeclaim-v1-core
[PodSpec]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#podspec-v1-core
