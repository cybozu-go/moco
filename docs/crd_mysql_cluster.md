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

| Field                    | Type                        | Description                                                          |
| ------------------------ | --------------------------- | -------------------------------------------------------------------- |
| `rootPasswordSecretName` | string                      | Secret name for root user.                                           |
| `preferredMasterIndexes` | []int                       | List of `StatefulSet` indexes. Former is more preferable for master. |
| `volumeClaimTemplate`    | \[\][PersistentVolumeClaim] | List of `PersistentVolumeClaim` for MySQL server pod.                |

MySQLClusterStatus
------------------

TBD

| Field                | Type    | Description                                                 |
| -------------------- | ------- | ----------------------------------------------------------- |
| `phase`              | string  | The phase in the [cluster lifecycle](cluster_lifecycle.md). |
| `ready`              | boolean | The health of the cluster.                                  |
| `currentMasterName`  | string  | Current master name.                                        |
| `availableInstances` | int     | Number of available instances.                              |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
[LabelSelector]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#labelselector-v1-meta
[PersistentVolumeClaim]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#persistentvolumeclaim-v1-core
