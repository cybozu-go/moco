MySQLBackup
==========

`MySQLBackup` is a custom resource definition (CRD) that represents
the backup of MySQL data.
This includes information about the binlog file and the dump file.

| Field        | Type                                | Description                               |
| ------------ | ----------------------------------- | ----------------------------------------- |
| `apiVersion` | string                              | APIVersion.                               |
| `kind`       | string                              | Kind.                                     |
| `metadata`   | [ObjectMeta]                        | Standard object's metadata.               |
| `spec`       | [MySQLBackupSpec](#MySQLBackupSpec) | Specification of desired state of backup. |

MySQLBackupSpec
---------------

| Field               | Type                      | Required | Description                                       |
| ------------------- | ------------------------- | -------- | ------------------------------------------------- |
| `clusterName`       | string                    | Yes      | Name of [`MySQLCluster`](crd_mysql_cluster.md).   |
| `objectStorageName` | string                    | Yes      | Name of [`ObjectStorage`](crd_object_storage.md). |
| `binlog`            | [BinlogSpec](#BinlogSpec) | Yes      | Specification of the binlog file.                 |
| `dump`              | [DumpSpec](#DumpSpec)     | Yes      | Specification of the dump file.                   |

BinlogSpec
----------

| Field                  | Type   | Required | Description                                        |
| ---------------------- | ------ | -------- | -------------------------------------------------- |
| `fileName`             | string | Yes      | Name of binlog file stored in bucket.              |
| `lastTransactionTime`  | [Time] | Yes      | Time for the last transaction in the binlog file.  |
| `firstTransactionTime` | [Time] | Yes      | Time for the first transaction in the binlog file. |
| `bytes`                | int    | Yes      | Binlog file size.                                  |

DumpSpec
--------

| Field        | Type   | Required | Description                         |
| ------------ | ------ | -------- | ----------------------------------- |
| `fileName`   | string | Yes      | Name of dump file stored in bucket. |
| `dumpedTime` | [Time] | Yes      | Timestamp when dump is executed.    |
| `bytes`      | int    | Yes      | Dump file size                      |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
