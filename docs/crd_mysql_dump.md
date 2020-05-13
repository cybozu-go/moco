MySQLDump
=========

`MySQLDump` is a custom resource definition (CRD) that represents
the location of a MySQL dump file.

| Field        | Type                                | Description                                  |
| ------------ | ----------------------------------- | -------------------------------------------- |
| `apiVersion` | string                              | APIVersion.                                  |
| `kind`       | string                              | Kind.                                        |
| `metadata`   | [ObjectMeta]                        | Standard object's metadata.                  |
| `spec`       | [MySQLDumpSpec](#MySQLDumpSpec)     | Specification of desired state of full dump. |
| `status`     | [MySQLDumpStatus](#MySQLDumpStatus) | Most recently observed status of full dump.  |

MySQLDumpSpec
-------------

| Field                   | Type                                                                | Description                                    |
| ----------------------- | ------------------------------------------------------------------- | ---------------------------------------------- |
| `clusterName`           | string                                                              | Name of [`MySQLCluster`](crd_mysql_cluster.md) |
| `fileName`              | string                                                              | Name of dump file stored in bucket.            |
| `objectStorageEndpoint` | [ObjectStorageSpec](crd_mysql_backup_schedule.md#ObjectStorageSpec) | Specification of S3 compatible object storage. |
| `dumpedTime`            | [Time]                                                              | Timestamp when dump is executed.               |

MySQLDumpStatus
---------------

| Field   | Type | Description    |
| ------- | ---- | -------------- |
| `bytes` | int  | Dump file size |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
