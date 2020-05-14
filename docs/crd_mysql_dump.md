# Dump

`Dump` is a custom resource definition (CRD) that represents
the location of a MySQL dump file.

| Field        | Type                      | Description                                  |
| ------------ | ------------------------- | -------------------------------------------- |
| `apiVersion` | string                    | APIVersion.                                  |
| `kind`       | string                    | Kind.                                        |
| `metadata`   | [ObjectMeta]              | Standard object's metadata.                  |
| `spec`       | [DumpSpec](#DumpSpec)     | Specification of desired state of full dump. |
| `status`     | [DumpStatus](#DumpStatus) | Most recently observed status of full dump.  |

## DumpSpec

| Field              | Type                                                         | Required | Description                               |
| ------------------ | ------------------------------------------------------------ | -------- | ----------------------------------------- |
| `clusterName`      | string                                                       | Yes      | Name of [`Cluster`](crd_mysql_cluster.md) |
| `fileName`         | string                                                       | Yes      | Name of dump file stored in bucket.       |
| `objectStorageRef` | [ObjectStorageSpec](crd_object_storage.md#ObjectStorageSpec) | Yes      | Reference of `ObjectStorage`.             |
| `dumpedTime`       | [Time]                                                       | Yes      | Timestamp when dump is executed.          |
| `bytes`            | int                                                          | Yes      | Dump file size                            |

[objectmeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
