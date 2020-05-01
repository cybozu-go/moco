MySQLDump
=========

`MySQLDump` is a custom resource definition (CRD) that represents
the location of a MySQL dump file.

| Field        | Type                                | Description                                                           |
| ------------ | ----------------------------------- | --------------------------------------------------------------------- |
| `apiVersion` | string                              | APIVersion.                                                           |
| `kind`       | string                              | Kind.                                                                 |
| `metadata`   | [ObjectMeta]                        | Standard object's metadata with a special annotation described below. |
| `data`       | [MySQLDumpData](#MySQLDumpData)     | Specification of desired behavior of the cluster.                     |
| `status`     | [MySQLDumpStatus](#MySQLDumpStatus) | Most recently observed status of the cluster.                         |

MySQLDumpSpec
-------------

| Field                    | Type   | Description                                                               |
| ------------------------ | ------ | ------------------------------------------------------------------------- |
| `preferredMasterIndexes` | []int  | List of `StatefulSet` indexes. Former is more preferable for master.      |
| `dumpSchedule`           | string | Schedule in Cron format, this value is passed to `CronJob.spec.schedule`. |
| `binlogSchedule`         | string | Schedule in Cron format, this value is passed to `CronJob.spec.schedule`. |

MySQLDumpStatus
---------------

TBD

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
