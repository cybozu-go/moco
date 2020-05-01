MySQLBinlog
===========

`MySQLBinlog` is a custom resource definition (CRD) that represents
the location of a MySQL binlog file.

| Field        | Type                                    | Description                                                           |
| ------------ | --------------------------------------- | --------------------------------------------------------------------- |
| `apiVersion` | string                                  | APIVersion.                                                           |
| `kind`       | string                                  | Kind.                                                                 |
| `metadata`   | [ObjectMeta]                            | Standard object's metadata with a special annotation described below. |
| `spec`       | [MySQLBinlogSpec](#MySQLBinlogSpec)     | Specification of `MySQLBinlog`.                                       |
| `status`     | [MySQLBinlogStatus](#MySQLBinlogStatus) | Most recently observed status of the `MySQLBinlog`.                   |

MySQLBinlogSpec
---------------

| Field                   | Type                                                                | Description                                    |
| ----------------------- | ------------------------------------------------------------------- | ---------------------------------------------- |
| `cluster`               | string                                                              | Name of `MySQLCluster`                         |
| `objectStorageEndpoint` | [ObjectStorageSpec](crd_mysql_backup_schedule.md#ObjectStorageSpec) | Specification of S3 compatible object storage. |

MySQLBinlogStatus
-----------------

| Field         | Type   | Description                                         |
| ------------- | ------ | --------------------------------------------------- |
| `phase`       | string | pending, running, succeeded, failed                 |
| `phaseReason` | string | Reason for entering current phase                   |
| `gtidFrom`    | string | Initial GTID in the binlog file                     |
| `gtidTo`      | string | Last GTID in the binlog file                        |
| `timeFrom`    | [Time] | Time for the initial transaction in the binlog file |
| `timeTo`      | [Time] | Time for the last transaction in the binlog file    |
| `bytes`       | int    | Binlog file size                                    |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
