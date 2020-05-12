MySQLBinlog
===========

`MySQLBinlog` is a custom resource definition (CRD) that represents
the location of a MySQL binlog file.

| Field        | Type                                    | Description                                                           |
|--------------|-----------------------------------------|-----------------------------------------------------------------------|
| `apiVersion` | string                                  | APIVersion.                                                           |
| `kind`       | string                                  | Kind.                                                                 |
| `metadata`   | [ObjectMeta]                            | Standard object's metadata with a special annotation described below. |
| `spec`       | [MySQLBinlogSpec](#MySQLBinlogSpec)     | Specification of `MySQLBinlog`.                                       |
| `status`     | [MySQLBinlogStatus](#MySQLBinlogStatus) | Most recently observed status of the `MySQLBinlog`.                   |

MySQLBinlogSpec
---------------

| Field                   | Type                                                                | Description                                       |
|-------------------------|---------------------------------------------------------------------|---------------------------------------------------|
| `clusterName`           | string                                                              | Name of `MySQLCluster`                            |
| `objectStorageEndpoint` | [ObjectStorageSpec](crd_mysql_backup_schedule.md#ObjectStorageSpec) | Specification of S3 compatible object storage.    |
| `lastTransactionTime`   | [Time]                                                              | Time for the last transaction in the binlog file  |
| `firstTransactionTime`  | [Time]                                                              | Time for the first transaction in the binlog file |

MySQLBinlogStatus
-----------------

| Field        | Type                                    | Description              |
|--------------|-----------------------------------------|--------------------------|
| `bytes`      | int                                     | Binlog file size         |
| `conditions` | [][`BinlogCondition`](#BinlogCondition) | The array of conditions. |

BinlogCondition
---------------

| Field                | Type   | Description                                                      |
|----------------------|--------|------------------------------------------------------------------|
| `type`               | string | The type of condition.                                           |
| `status`             | string | The status of the condition, one of True, False, Unknown         |
| `reason`             | string | One-word CamelCase reason for the condition's last transition.   |
| `message`            | string | Human-readable message indicating details about last transition. |
| `lastTransitionTime` | Time   | The last time the condition transit from one status to another.  |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
