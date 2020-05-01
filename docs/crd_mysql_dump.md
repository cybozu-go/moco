MySQLDump
=========

`MySQLDump` is a custom resource definition (CRD) that represents
the location of a MySQL dump file.

| Field        | Type                                | Description                                                           |
| ------------ | ----------------------------------- | --------------------------------------------------------------------- |
| `apiVersion` | string                              | APIVersion.                                                           |
| `kind`       | string                              | Kind.                                                                 |
| `metadata`   | [ObjectMeta]                        | Standard object's metadata with a special annotation described below. |
| `spec`       | [MySQLDumpSpec](#MySQLDumpSpec)     | Specification of `MySQLDump`.                                         |
| `status`     | [MySQLDumpStatus](#MySQLDumpStatus) | Most recently observed status of the `MySQLDump`.                     |

MySQLDumpSpec
-------------

| Field                   | Type                                                                | Description                                    |
| ----------------------- | ------------------------------------------------------------------- | ---------------------------------------------- |
| `cluster`               | string                                                              | Name of `MySQLCluster`                         |
| `objectStorageEndpoint` | [ObjectStorageSpec](crd_mysql_backup_schedule.md#ObjectStorageSpec) | Specification of S3 compatible object storage. |

MySQLDumpStatus
---------------

| Field         | Type   | Description                         |
| ------------- | ------ | ----------------------------------- |
| `phase`       | string | pending, running, succeeded, failed |
| `phaseReason` | string | Reason for entering current phase   |
| `snappedAt`   | [Time] | Start time for dump process         |
| `bytes`       | int    | Dump file size                      |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
