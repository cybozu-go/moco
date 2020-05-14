# MySQLCluster

`MySQLCluster` is a custom resource definition (CRD) that represents a cluster of MySQL servers synchronizing data using binlog-based replication.  The cluster does not mean [NDB cluster](https://dev.mysql.com/doc/refman/8.0/en/mysql-cluster.html) nor [InnoDB cluster](https://dev.mysql.com/doc/refman/8.0/en/mysql-innodb-cluster-userguide.html)`.

| Field        | Type                                      | Description                                       |
| ------------ | ----------------------------------------- | ------------------------------------------------- |
| `apiVersion` | string                                    | APIVersion.                                       |
| `kind`       | string                                    | Kind.                                             |
| `metadata`   | [ObjectMeta]                              | Standard object's metadata.                       |
| `spec`       | [MySQLClusterSpec](#MySQLClusterSpec)     | Specification of desired behavior of the cluster. |
| `status`     | [MySQLClusterStatus](#MySQLClusterStatus) | Most recently observed status of the cluster.     |

MySQLClusterSpec
----------------

| Field                         | Type                    | Required | Description                                                                                                                                                           |
| ----------------------------- | ----------------------- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `replicas`                    | int                     | No       | The number of instances. Available values are 1, 3, and 5. Default value is 1.                                                                                        |
| `podTemplate`                 | [PodTemplateSpec]       | No       | `Pod` template for MySQL server container.                                                                                                                            |
| `volumeClaimTemplate`         | [PersistentVolumeClaim] | No       | `PersistentVolumeClaim` template for MySQL server container.                                                                                                          |
| `serviceTemplate`             | [ServiceSpec]           | No       | `Service` template for endpoints of MySQL server containers.                                                                                                          |
| `mySQLConfigMapName`          | string                  | No       | `ConfigMap` name of MySQL config.                                                                                                                                     |
| `rootPasswordSecretName`      | string                  | Yes      | `Secret` name for root user.                                                                                                                                          |
| `replicationSourceSecretName` | string                  | Yes      | `Secret` name which contains replication source info. Keys must appear in [options].<br/> If this field is given, the `MySQLCluster` works as an intermediate master. |
| `restore`                     | RestoreSpec             | No       | Spec to perform PiTR from existing cluster. If this field is filled, start restoring. This field is unable to be updated.                                             |

RestoreSpec
-----------

| Field               | Type   | Required | Description                                                  |
| ------------------- | ------ | -------- | ------------------------------------------------------------ |
| `sourceClusterName` | string | Yes      | Source `MySQLCluster` name.                                  |
| `pointInTime`       | [Time] | Yes      | Point-in-time of the state which the cluster is restored to. |

MySQLClusterStatus
-------------

| Field                | Type                                                | Description                                                       |
| -------------------- | --------------------------------------------------- | ----------------------------------------------------------------- |
| `conditions`         | [][`MySQLClusterCondition`](#MySQLClusterCondition) | Array of conditions.                                              |
| `ready`              | Enum                                                | Status of readiness. One of `True`, `False`, `Unknown`.           |
| `readOnly`           | boolean                                             | The cluster is read-only or not(e.g. the master is intermediate). |
| `currentMasterIndex` | int                                                 | Ordinal of the current master in `StatefulSet`.                   |
| `syncedReplicas`     | int                                                 | Number of synced instances.                                       |

MySQLClusterCondition
----------------

| Field                | Type   | Required | Description                                                      |
| -------------------- | ------ | -------- | ---------------------------------------------------------------- |
| `type`               | string | Yes      | Type of condition.                                               |
| `status`             | Enum   | Yes      | Status of the condition. One of `True`, `False`, `Unknown`.      |
| `reason`             | string | No       | One-word CamelCase reason for the condition's last transition.   |
| `message`            | string | No       | Human-readable message indicating details about last transition. |
| `lastTransitionTime` | [Time] | Yes      | The last time the condition transits from one status to another. |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
[PersistentVolumeClaim]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#persistentvolumeclaim-v1-core
[PodTemplateSpec]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#podtemplatespec-v1-core
[ServiceSpec]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#servicespec-v1-core
