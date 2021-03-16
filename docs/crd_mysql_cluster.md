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

| Field                         | Type                                | Required | Description                                                                                                                                                                                                |
| ----------------------------- | ----------------------------------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `replicas`                    | int                                 | No       | The number of instances. Available values are 1, 3, and 5. Default value is 1.                                                                                                                             |
| `podTemplate`                 | [PodTemplateSpec]                   | Yes      | `Pod` template for MySQL server container.<br /> Strictly, the metadata for this template is a subset of [ObjectMeta].                                                                                     |
| `dataVolumeClaimTemplateSpec` | [PersistentVolumeClaimSpec]         | Yes      | `PersistentVolumeClaimSpec` template for MySQL data volume.                                                                                                                                                |
| `volumeClaimTemplates`        | \[\][PersistentVolumeClaim]         | No       | `PersistentVolumeClaim` templates for volumes used by MySQL server container, except for data volume.<br /> Strictly, the metadata for each template is a subset of [ObjectMeta].                          |
| `serviceTemplate`             | [ServiceTemplate](#ServiceTemplate) | No       | `Service` template for both of primary and replicas.                                                                                                                                                       |
| `mysqlConfigMapName`          | string                              | No       | `ConfigMap` name of MySQL config.                                                                                                                                                                          |
| `replicationSourceSecretName` | string                              | No       | `Secret` name which contains replication source info. Keys must appear in [Options].<br/> If this field is given, the `MySQLCluster` works as an intermediate primary (i.e., works as read-only replicas). |
| `logRotationSchedule`         | string                              | No       | Schedule in Cron format for MySQL log rotation.                                                                                                                                                            |
| `restore`                     | [RestoreSpec](#RestoreSpec)         | No       | Specification to perform Point-in-Time-Recovery from existing cluster.<br/> If this field is filled, start restoring. This field is unable to be updated.                                                  |
| `disableErrorLogContainer`    | boolean                             | No       | DisableErrorLogContainer controls whether to add a log agent container name of the "err-log" to handle mysqld error logs. If set to true, no log agent container will be added. The default is false. If false and the user-defined ".spec.podTemplate.spec.containers" contained a container named "err-log", it will be merged with the default container definition using StrategicMergePatch. |
| `disableSlowQueryLogContainer`| boolean                             | No       | DisableSlowQueryLogContainer controls whether to add a log agent container name of the "slow-log" to handle mysqld slow query logs. If set to true, no log agent container will be added. The default is false. If false and the user-defined ".spec.podTemplate.spec.containers" contained a container named "slow-log", it will be merged with the default container definition using StrategicMergePatch. |

The configMap specified with `mysqlConfigMapName` contains MySQL options of `mysqld` section as key-value pairs.

Note that `podTemplate` must include `name: myslqd` container. This container must specify a container image that runs `mysqld`. Besides, the container `name: agent` cannot be included in `podTemplate` because it is reserved by MOCO controller.

The secret given to `replicationSourceSecretName` have the following keys:

| Key                                                  | Required | Description                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| ---------------------------------------------------- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `PRIMARY_HOST`, `PRIMARY_PORT`                       | Yes      | The host name and port number of donor for intermediate primary.                                                                                                                                                                                                                                                                                                                                                                             |
| `PRIMARY_USER`, `PRIMARY_PASSWORD`                   | Yes      | The user name and its password of donor user in donor host. This user must have `BACKUP_ADMIN` privilege.                                                                                                                                                                                                                                                                                                                                    |
| `INIT_AFTER_CLONE_USER`, `INIT_AFTER_CLONE_PASSWORD` | Yes      | The user name and its password used to recover administrative users used by moco after cloning. This user must be created in the donor in advance. This user must have `SUPER` privilege (same privilege as root) and may be root. Because moco connects to `mysqld` via UNIX domain socket during recovery, this user does not have to be able to connect via network and it is recommended to create the user as `'somebody'@'localhost'`. |

ServiceTemplate
---------------

| Field      | Type          | Required | Description                                                                                                               |
| ---------- | ------------- | -------- | ------------------------------------------------------------------------------------------------------------------------- |
| `metadata` | [ObjectMeta]  | No       | Metadata attached to `Service` of primary and replicas. Only `annotations` and `labels` are valid.                        |
| `spec`     | [ServiceSpec] | No       | `Service` template spec for primary and replicas.<br/> Note that MOCO will overwrites only `Ports` and `Selector` fields. |

RestoreSpec
-----------

| Field               | Type   | Required | Description                                                  |
| ------------------- | ------ | -------- | ------------------------------------------------------------ |
| `sourceClusterName` | string | Yes      | Source `MySQLCluster` name.                                  |
| `pointInTime`       | [Time] | Yes      | Point-in-time of the state which the cluster is restored to. |

MySQLClusterStatus
------------------

| Field                 | Type                                                | Required | Description                                                                                                             |
| --------------------- | --------------------------------------------------- | -------- | ----------------------------------------------------------------------------------------------------------------------- |
| `conditions`          | \[\][MySQLClusterCondition](#MySQLClusterCondition) | No       | Array of conditions.                                                                                                    |
| `ready`               | Enum                                                | Yes      | Status of readiness. One of `True`, `False`, `Unknown`.                                                                 |
| `currentPrimaryIndex` | int                                                 | No       | Ordinal of the current primary in `StatefulSet`.                                                                        |
| `syncedReplicas`      | int                                                 | Yes      | Number of synced instances including the primary.                                                                       |
| `serverIDBase`        | uint32                                              | No       | This value plus the Pod index number is used as the server-id for each Pod. This value is automatically filled by MOCO. |

MySQLClusterCondition
---------------------

| Field                | Type   | Required | Description                                                                |
| -------------------- | ------ | -------- | -------------------------------------------------------------------------- |
| `type`               | Enum   | Yes      | The type of condition. Please see `MySQLClusterConditionType` for details. |
| `status`             | Enum   | Yes      | Status of the condition. One of `True`, `False`, `Unknown`.                |
| `reason`             | string | No       | One-word CamelCase reason for the condition's last transition.             |
| `message`            | string | No       | Human-readable message indicating details about last transition.           |
| `lastTransitionTime` | [Time] | Yes      | The last time the condition transits from one status to another.           |

MySQLClusterConditionType
------------------------

| Value       | Description                                                                                                                          |
| ----------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| Initialized | All the objects needed to make up the cluster have been created successfully.                                                        |
| Healthy     | As for all replicas, semi-sync replication is working without `OutOfSync`, and the primary is writable.                              |
| Available   | In a minimum number of replicas (1 of 3, 2 of 5), semi-sync replication is working without `OutOfSync`, and the primary is writable. |
| OutOfSync   | There are replicas whose data is out of sync (i.e., `Last_IO_Errno` not equal zero).                                                 |
| Failure     | Any errors were detected. The primary is not writable.                                                                               |
| Violation   | The constraints violation was detected. Once detected, it will not be changed.                                                       |



[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.18/#objectmeta-v1-meta
[Options]: https://dev.mysql.com/doc/refman/8.0/en/change-master-to.html
[PersistentVolumeClaim]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.18/#persistentvolumeclaim-v1-core
[PersistentVolumeClaimSpec]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.18/#persistentvolumeclaimspec-v1-core
[PodSecurityContext]: https://v1-18.docs.kubernetes.io/docs/reference/generated/kubernetes-api/v1.18/#podsecuritycontext-v1-core
[PodTemplateSpec]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.18/#podtemplatespec-v1-core
[ServiceSpec]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.18/#servicespec-v1-core
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.18/#time-v1-meta
