Binlog
======

`Binlog` is a custom resource definition (CRD) that represents
the location of a MySQL binlog file.

| Field        | Type                          | Description                               |
| ------------ | ----------------------------- | ----------------------------------------- |
| `apiVersion` | string                        | APIVersion.                               |
| `kind`       | string                        | Kind.                                     |
| `metadata`   | [ObjectMeta]                  | Standard object's metadata.               |
| `spec`       | [BinlogSpec](#BinlogSpec)     | Specification of desired state of binlog. |
| `status`     | [BinlogStatus](#BinlogStatus) | Most recently observed status of binlog.  |

BinlogSpec
----------

| Field                   | Type                                                                | Description                                       |
| ----------------------- | ------------------------------------------------------------------- | ------------------------------------------------- |
| `clusterName`           | string                                                              | Name of [`Cluster`](crd_mysql_cluster.md).        |
| `fileName`              | string                                                              | Name of binlog file stored in bucket.             |
| `objectStorageEndpoint` | [ObjectStorageSpec](crd_mysql_backup_schedule.md#ObjectStorageSpec) | Specification of S3 compatible object storage.    |
| `lastTransactionTime`   | [Time]                                                              | Time for the last transaction in the binlog file  |
| `firstTransactionTime`  | [Time]                                                              | Time for the first transaction in the binlog file |

BinlogStatus
------------

| Field   | Type | Description      |
| ------- | ---- | ---------------- |
| `bytes` | int  | Binlog file size |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
