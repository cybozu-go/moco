MySQLCluster
============

`MySQLCluster` is a custom resource definition (CRD) that represents 
a MySQL cluster. In this context, MySQL cluster means a group of 
MySQL servers which replicates data semi-synchronously,
without group replication used in InnoDB cluster, to the slaves.

| Field        | Type                                      | Description                                       |
| ------------ | ----------------------------------------- | ------------------------------------------------- |
| `apiVersion` | string                                    | APIVersion.                                       |
| `kind`       | string                                    | Kind.                                             |
| `metadata`   | [ObjectMeta]                              | Standard object's metadata.                       |
| `spec`       | [MySQLClusterSpec](#MySQLClusterSpec)     | Specification of desired behavior of the cluster. |
| `status`     | [MySQLClusterStatus](#MySQLClusterStatus) | Most recently observed status of the cluster.     |

MySQLClusterSpec
----------------

| Field                         | Type                    | Description                                                                                                                                                                                                                          |
| ----------------------------- | ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `size`                        | int                     | The number of instances. Available values are 1, 3, and 5.                                                                                                                                                                           |
| `podTemplate`                 | [PodSpec]               | `Pod` template for MySQL server container.                                                                                                                                                                                           |
| `volumeClaimTemplate`         | [PersistentVolumeClaim] | `PersistentVolumeClaim` template for MySQL server container.                                                                                                                                                                         |
| `serviceTemplate`             | [ServiceSpec]           | `Service` template for endpoints of MySQL server containers.                                                                                                                                                                         |
| `mySQLConfigMapName`          | string                  | `ConfigMap` name of MySQL config.                                                                                                                                                                                                    |
| `rootPasswordSecretName`      | string                  | `Secret` name for root user.                                                                                                                                                                                                         |
| `replicationSourceSecretName` | string                  | `Secret` name which contains replication source info. Keys must appear in [options](https://dev.mysql.com/doc/refman/8.0/en/change-master-to.html).<br/> If this field is given, the `MySQLCluster` works as an intermediate master. |

MySQLClusterStatus
------------------

| Field                | Type                                      | Description                                                           |
| -------------------- | ----------------------------------------- | --------------------------------------------------------------------- |
| `conditions`         | [][`ClusterCondition`](#ClusterCondition) | Array of conditions.                                                  |
| `ready`              | boolean                                   | `True` if the cluster is ready.                                       |
| `readOnly`           | boolean                                   | `True` if the cluster is read-only (e.g. the master is intermediate). |
| `currentMasterIndex` | int                                       | Ordinal of the current master in `StatefulSet`.                       |
| `syncedReplicas`     | int                                       | Number of synced instances.                                           |

ClusterCondition
----------------

| Field                | Type   | Description                                                      |
| -------------------- | ------ | ---------------------------------------------------------------- |
| `type`               | string | Type of condition.                                               |
| `status`             | string | Status of the condition. One of `True`, `False`, `Unknown`.      |
| `reason`             | string | One-word CamelCase reason for the condition's last transition.   |
| `message`            | string | Human-readable message indicating details about last transition. |
| `lastTransitionTime` | Time   | The last time the condition transits from one status to another. |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
[LabelSelector]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#labelselector-v1-meta
[PersistentVolumeClaim]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#persistentvolumeclaim-v1-core
[PodSpec]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#podspec-v1-core
[ServiceSpec]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#servicespec-v1-core
[SecretReference]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#secretreference-v1-core
