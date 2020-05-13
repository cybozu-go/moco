Design notes
============

Motivation
----------

This Kubernetes operator automates operations for the binlog-based replication on MySQL.

InnoDB cluster is widely used for the replication purpose, but we choose not to use InnoDB cluster because it does not allow large (>2GB) transactions.

There are some existing operators which deploy a group of MySQL servers without InnoDB cluster but they does not support the point-in-time-recovery(PiTR) feature.

- [oracle/mysql-operator](https://github.com/oracle/mysql-operator) takes backups only with `mysqldump`
- [presslabs/mysql-operator](https://github.com/presslabs/mysql-operator) does not restore clusters to the state at the desired point-in-time

This operator deploys a group of MySQL servers which replicates data semi-synchronously to the slaves and takes backups with both `mysqlpump` and `mysqlbinlog`.

In this context, we call the group of MySQL servers as MySQL cluster.

Goals
-----

- Have the master replicate data semi-synchronously to the multiple slaves
- Support all the four transaction isolation levels.
- Avoid split-brain.
- Accept large transactions.
- Support multiple MySQL versions and automatic upgrading.
- Support automatic master selection and switchover.
- Support automatic failover.
- Support backups at least once in a day.
- Support a quick recovery by combining full backup and binary logs.
- Support asynchronous replication between remote data centers.
- Tenant users can specify the following parameters:
  - The version of MySQL instances.
  - The number of processor cores for each MySQL instance.
  - The amount of memory for each MySQL instance.
  - The amount of backing storage for each MySQL instance.
  - The number of replicas in the MySQL cluster.
  - Custom configuration parameters.
- Allow `CREATE / DROP TEMPORARY TABLE` during a transaction.
- Use Custom Resource Definition(CRD) to automate construction of MySQL database using replication on Kubernetes.

Non-goals
---------

- Support for InnoDB cluster.
- Zero downtime upgrade.
- Node fencing.

Components
----------

### Workloads

- Operator: Custom controller which automates MySQL cluster management with the following namespaced custom resources:
  - [`Cluster`](crd_mysql_cluster.md) represents a MySQL cluster.
  - [`BackupSchedule`](crd_mysql_backup_schedule.md) represents a full dump & binlog schedule.
    - [`Dump`](crd_mysql_dump.md) represents a full dump file information.
    - [`Binlog`](crd_mysql_binlog.md) represents a binlog file information.
  - [`RestoreJob`](crd_mysql_restore_job.md) represents a Point-in-Time Recovery (PiTR) job.
  - [`SwitchoverJob`](crd_mysql_switch_over_job.md) represents a switchover job.
- [cert-manager](https://cert-manager.io/): Provide client certifications and master-slave certifications automatically.

### External components

- Object storage: Store logical backups. It must have Amazon S3 compatible APIs (e.g. Ceph RGW).

### Tools

- `kubectl-myso`: CLI to manipulate MySQL cluster. It provides functionalities such as:
  - Change master manually.
  - Port-forward to MySQL servers.
  - Execute SQL like `mysql -u -p` without a credential file on a local environment.
  - Fetch a credential file to a local environment.

### Diagram

Overview of components. This figure is just a draft.

![overview](./images/overview.png)

Behaviors
---------

MySO is implemented as a custom controller, so it includes custom resource definitions(CRD).

### How to bootstrap multiple MySQL servers

The operator has the responsibility to create master-slave configuration of MySQL clusters.

When the `Cluster` is created, the operator starts deploying a new MySQL cluster as follows.
In this section, the name of `Cluster` is assumed to be `mysql`.

1. The operator creates `StatefulSet` which has `N`(`N`=3 or 5) `Pod`s and its headless `Service`.
1. The operator sets `mysql-0` as master and the other `Pod`s as slave.
   The index of the current master is managed under `Cluster.status.currentMasterIndex`.
1. The operator creates some k8s resources.
  - `Service` for accessing master.
  - `Service` for accessing slaves.
  - `Secrets` to store credentials.
  - `ConfigMap` to store cluster configuration.

### How to execute failover when the master fails

When the master fails, the cluster is recovered in the following process:

1. Stop the `IO_THREAD` of all slaves.
2. Select and configure new master.
3. Update `Cluster.status.currentMasterIndex` with the new master name.
4. Turn off read-only mode on the new master

In the process, the operator configures the old master as slave if the server is ready.

### How to execute failover when a slave fails

When a slave fails once and it restarts afterwards, the operator configures it to follow the master.

### How to execute switchover

Users can execute master switchover by applying `SwitchoverJob` CR which contains the master index to be switched to.

### How to make a backup

When you create `BackupSchedule` CR, it creates two `CronJob`s:
  - To get full dump backup and store it in a object storage.
  - To get binlog file and store it in a object storage.

If we want to make backups only once, set `BackupSchedule.spec.schedules` to run once.

### How to perform Point-in-Time-Recovery(PiTR)

When we create a `RestoreJob`, PiTR is performed with the following procedure.

1. The operator sets `Cluster.status.ready` as `false` and make the MySQL cluster block incoming transactions.
1. The operator makes the MySQL cluster flush binlogs. This binlog is used for recovery if the PiTR fails.
1. The operator lists `Dump` and `Binlog` candidates based on `RestoreJob.spec.sourceClusterName`.
1. The operator selects the corresponding `Dump` and `Binlog` CRs  `RestoreJob.spec.pointInTime`.
1. The operator downloads the dump file and the binlogs from the object storage.
1. The operator restores the MySQL servers to the state at `RestoreJob.spec.pointInTime`.
1. If PiTR finishes successfully, `RestoreJob.status.succeeded` and `Cluster.status.ready` are set `true`.
   Otherwise, the operator sets `RestoreJob.status.succeeded` as `false` and tries to recover the state before PiTR.
   If the recovery succeeds, the operator sets `Cluster.status.ready` as `true`.

### How to upgrade MySQL version of master and slaves

MySQL software upgrade is triggered by changing container image specified in `Cluster.spec.podTemplate`.
In this section, the name of `StatefulSet` is assumed to be `mysql`.

1. Switch master to the pod `mysql-0` if the current master is not `mysql-0`.
2. Update and apply the `StatefulSet` manifest for `mysql`, to trigger upgrading the slaves as follows:
  - Set `.spec.updateStrategy.rollingUpdate.partition` as `1`.
  - Set new image version in `.spec.template.spec.containers`.
3. Wait for all the slaves to be upgraded.
4. Switch master to `mysql-1`.
5. Update and apply the `StatefulSet` manifest to trigger upgrading `mysql-0` as follows:
  - Remove `.spec.updateStrategy.rollingUpdate.partition`.
6. Wait for `mysql-0` to be upgraded.

### TBD

- Write merge strategy of `my.cnf`.
- How to clean up zombie backup files on a object storage which does not exist in Kubernetes.

### Candidates of additional features

- Backup files verification.
- Manage MySQL users with [`User`](crd_mysql_user.md).
