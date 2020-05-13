Dump
====

`Dump` is a custom resource definition (CRD) that represents
the location of a MySQL dump file.

| Field        | Type                      | Description                                  |
| ------------ | ------------------------- | -------------------------------------------- |
| `apiVersion` | string                    | APIVersion.                                  |
| `kind`       | string                    | Kind.                                        |
| `metadata`   | [ObjectMeta]              | Standard object's metadata.                  |
| `spec`       | [DumpSpec](#DumpSpec)     | Specification of desired state of full dump. |
| `status`     | [DumpStatus](#DumpStatus) | Most recently observed status of full dump.  |

DumpSpec
--------

| Field                   | Type                                                                | Description                                    |
| ----------------------- | ------------------------------------------------------------------- | ---------------------------------------------- |
| `clusterName`           | string                                                              | Name of [`Cluster`](crd_mysql_cluster.md)      |
| `fileName`              | string                                                              | Name of dump file stored in bucket.            |
| `objectStorageEndpoint` | [ObjectStorageSpec](crd_mysql_backup_schedule.md#ObjectStorageSpec) | Specification of S3 compatible object storage. |
| `dumpedTime`            | [Time]                                                              | Timestamp when dump is executed.               |

DumpStatus
----------

| Field   | Type | Description    |
| ------- | ---- | -------------- |
| `bytes` | int  | Dump file size |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
