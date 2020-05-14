# Binlog

`Binlog` is a custom resource definition (CRD) that represents
the location of a MySQL binlog file.

| Field        | Type                          | Description                               |
| ------------ | ----------------------------- | ----------------------------------------- |
| `apiVersion` | string                        | APIVersion.                               |
| `kind`       | string                        | Kind.                                     |
| `metadata`   | [ObjectMeta]                  | Standard object's metadata.               |
| `spec`       | [BinlogSpec](#BinlogSpec)     | Specification of desired state of binlog. |
| `status`     | [BinlogStatus](#BinlogStatus) | Most recently observed status of binlog.  |

## BinlogSpec

| Field                  | Type                                                         | Required | Description                                       |
| ---------------------- | ------------------------------------------------------------ | -------- | ------------------------------------------------- |
| `clusterName`          | string                                                       | Yes      | Name of [`Cluster`](crd_mysql_cluster.md).        |
| `fileName`             | string                                                       | Yes      | Name of binlog file stored in bucket.             |
| `objectStorageRef`     | [ObjectStorageSpec](crd_object_storage.md#ObjectStorageSpec) | Yes      | Reference of `ObjectStorage`.                     |
| `lastTransactionTime`  | [Time]                                                       | Yes      | Time for the last transaction in the binlog file  |
| `firstTransactionTime` | [Time]                                                       | Yes      | Time for the first transaction in the binlog file |
| `bytes`                | int                                                          | Yes      | Binlog file size                                  |

[objectmeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
