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
| `clusterName`           | string                                                              | Name of `MySQLCluster`                         |
| `fileName`              | string                                                              | Name of backup file stored in bucket.          |
| `objectStorageEndpoint` | [ObjectStorageSpec](crd_mysql_backup_schedule.md#ObjectStorageSpec) | Specification of S3 compatible object storage. |

### TBD

- timestamp when dumped
- how to cleanup zombie files

MySQLDumpStatus
---------------

| Field        | Type                                | Description              |
| ------------ | ----------------------------------- | ------------------------ |
| `bytes`      | int                                 | Dump file size           |
| `conditions` | [][`DumpCondition`](#DumpCondition) | The array of conditions. |

DumpCondition
-------------

| Field                | Type   | Description                                                      |
| -------------------- | ------ | ---------------------------------------------------------------- |
| `type`               | string | The type of condition.                                           |
| `status`             | string | The status of the condition, one of True, False, Unknown         |
| `reason`             | string | One-word CamelCase reason for the condition's last transition.   |
| `message`            | string | Human-readable message indicating details about last transition. |
| `lastTransitionTime` | Time   | The last time the condition transit from one status to another.  |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[Time]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#time-v1-meta
