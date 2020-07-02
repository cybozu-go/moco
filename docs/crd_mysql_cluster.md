MySQLCluster
===========

`MySQLCluster` is a custom resource definition (CRD) that represents a cluster of MySQL servers synchronizing data using binlog-based replication.
The cluster does not mean [NDB cluster](https://dev.mysql.com/doc/refman/8.0/en/mysql-cluster.html) nor [InnoDB cluster](https://dev.mysql.com/doc/refman/8.0/en/mysql-innodb-cluster-userguide.html).

| Field        | Type                                      | Description                                       |
| ------------ | ----------------------------------------- | ------------------------------------------------- |
| `apiVersion` | string                                    | APIVersion.                                       |
| `kind`       | string                                    | Kind.                                             |
| `metadata`   | [ObjectMeta]                              | Standard object's metadata.                       |
| `spec`       | [MySQLClusterSpec](#MySQLClusterSpec)     | Specification of desired behavior of the cluster. |
| `status`     | [MySQLClusterStatus](#MySQLClusterStatus) | Most recently observed status of the cluster.     |

MySQLClusterSpec
----------------

| Field                         | Type                        | Required | Description                                                                                                                                                                       |
| ----------------------------- | --------------------------- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `replicas`                    | int                         | No       | The number of instances. Available values are 1, 3, and 5. Default value is 1.                                                                                                    |
| `podTemplate`                 | [PodTemplateSpec]           | Yes      | `Pod` template for MySQL server container.<br /> Strictly, the metadata for this template is a subset of [ObjectMeta].                                                            |
| `dataVolumeClaimTemplateSpec` | [PersistentVolumeClaimSpec] | Yes      | `PersistentVolumeClaimSpec` template for MySQL data volume.                                                                                                                       |
| `volumeClaimTemplates`        | \[\][PersistentVolumeClaim] | No       | `PersistentVolumeClaim` templates for volumes used by MySQL server container, except for data volume.<br /> Strictly, the metadata for each template is a subset of [ObjectMeta]. |
| `serviceTemplate`             | [ServiceSpec]               | No       | `Service` template for both primary and replicas.                                                                                                                                 |
| `mysqlConfigMapName`          | string                      | No       | `ConfigMap` name of MySQL config.                                                                                                                                                 |
| `rootPasswordSecretName`      | string                      | No       | `Secret` name for root user config.                                                                                                                                               |
| `replicationSourceSecretName` | string                      | No       | `Secret` name which contains replication source info. Keys must appear in [Options].<br/> If this field is given, the `MySQLCluster` works as an intermediate primary.            |
| `restore`                     | [RestoreSpec](#RestoreSpec) | No       | Specification to perform Point-in-Time-Recovery from existing cluster.<br/> If this field is filled, start restoring. This field is unable to be updated.                         |

The configMap specified with `mysqlConfigMapName` contains MySQL options of mysqld section as key-value pairs.

RestoreSpec
-----------

| Field               | Type   | Required | Description                                                  |
| ------------------- | ------ | -------- | ------------------------------------------------------------ |
| `sourceClusterName` | string | Yes      | Source `MySQLCluster` name.                                  |
| `pointInTime`       | [Time] | Yes      | Point-in-time of the state which the cluster is restored to. |

MySQLClusterStatus
------------------

| Field                 | Type                                                | Required | Description                                             |
| --------------------- | --------------------------------------------------- | -------- | ------------------------------------------------------- |
| `conditions`          | \[\][MySQLClusterCondition](#MySQLClusterCondition) | No       | Array of conditions.                                    |
| `ready`               | Enum                                                | Yes      | Status of readiness. One of `True`, `False`, `Unknown`. |
| `currentPrimaryIndex` | int                                                 | No       | Ordinal of the current primary in `StatefulSet`.        |
| `syncedReplicas`      | int                                                 | Yes      | Number of synced instances including the primary.       |

MySQLClusterCondition
---------------------

| Field                | Type   | Required | Description                                                      |
| -------------------- | ------ | -------- | ---------------------------------------------------------------- |
| `type`               | Enum   | Yes      | The type of condition. Possible values are (TBD).                |
| `status`             | Enum   | Yes      | Status of the condition. One of `True`, `False`, `Unknown`.      |
| `reason`             | string | No       | One-word CamelCase reason for the condition's last transition.   |
| `message`            | string | No       | Human-readable message indicating details about last transition. |
| `lastTransitionTime` | [Time] | Yes      | The last time the condition transits from one status to another. |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.18/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.18/#time-v1-meta
[PersistentVolumeClaim]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.18/#persistentvolumeclaim-v1-core
[PersistentVolumeClaimSpec]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.18/#persistentvolumeclaimspec-v1-core
[PodTemplateSpec]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.18/#podtemplatespec-v1-core
[ServiceSpec]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.18/#servicespec-v1-core
[Options]: https://dev.mysql.com/doc/refman/8.0/en/change-master-to.html
