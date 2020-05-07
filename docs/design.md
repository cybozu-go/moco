Design notes
============

Motivation
----------

Automate operations of binlog-based replication on MySQL.

Reason for why we choose semi-sync replication and don't use InnoDB cluster is InnoDB cluster does not allow large (>2GB) transactions.

These softwares provide operator functionality over MySQL, but they are designed on different motivation.
- https://github.com/oracle/mysql-operator
- https://github.com/presslabs/mysql-operator

Goals
-----

- Use Custom Resource Definition to automate construction of MySQL database using replication.
- Provide functionality for stabilizing distributed systems that MySQL replication does not provide.
- While keeping consistency, automating configuration of cluster as much as possible including fail-over situation.

Components
----------

### Workloads

- Operator: Custom controller which automates MySQL management using MySQL CR and User CR.
- Backup job: CronJob which uploads logical full-backup and binary logs onto object storage.
- MySQL servers: StatefulSet which works as master/slave.
- [cert-manager](https://cert-manager.io/): Automate to provide client certification and master-slave certification.

### Client

- `mysoctl`: Utility tool to manipulate MySQL from external location (e.g. change master manually).

### Custom Resource Definitions

- [`MySQLCluster`](crd_mysql_cluster.md) defines a MySQL cluster.
  In this context, MySQL cluster means a cluster of MySQL servers which are replicated by `mysqlbinlog` and `mysqlpump` without group replication such as InnoDB cluster.
- [`MySQLUser`](crd_mysql_user.md) defines a login user in MySQL server.
- [`MySQLBackupSchedule`](crd_mysql_backup_schedule.md) represents a full dump & binary schedule.
- [`MySQLDump`](crd_mysql_dump.md) represents a full dump job and contains the file path.
- [`MySQLBinlog`](crd_mysql_binlog.md) represents a binlog writing job and contains the file path.

### External components

- Object storage: Store logical backups and binary logs. It must have Amazon S3 compatible APIs (e.g. Ceph RGW).

### Diagram

TBD

Behaviors
---------

MySO is one of the custom controllers, so it includes custom resource definitions(CRD).

### How to bootstrap MySQL servers

The operator has the responsibility to create master-slave configuration of MySQL clusters.

When the `MySQLCluster` is created, the operator starts deploying a new MySQL cluster as follows.

1. The operator creates StatefulSet which has 3 replicas and its head-less Service.
1. The operator provisions the replicas as 3-node master/slave MySQL servers.
1. The operator creates some k8s resources.
    - Two Services. The first one is for accessing master. The later one is for accessing slaves.
    - Secrets to store credentials.
    - ...

### How to control master-slave configuration

The operator has the responsibility to manage master-slave configuration of MySQL clusters.

The operator stores clusters' states on `MySQLCluster`.
It represents who is the master.

Based on the CR, The operator controls the MySQL servers.

If there is a difference between the desired master defined in `MySQLCluster.spec` and actual master defined in `MySQLCluster.status`,
operator changes the master.
Therefore users can change the master through the CR spec.

### How to failover when a single MySQL instance failure

When the master fails, the cluster is recovered in the following process:
1. Stop the `IO_THREAD` of all slaves
2. Execute master election
3. Configure new master
4. Configure other instances as slaves of the new master
5. Write the new master name to `MySQLCluster.status`
6. Turn off read-only mode on the new master

### How to make a backup

Users can declare the settings of full dump backup with `mysqldump` and binary log backup with `mysqlbinlog` via `MySQLBackupSchedule` CRs. 

- If you create `MySQLBackupSchedule` CR, it creates `CronJob` to make full dump backup and store it in a S3-compatible object storage.
- If you create `MySQLBackupSchedule` CR, it creates `CronJob` to make binlog file to the object storage.

If you want to make backups at once, set `MySQLBackupSchedule.spec.schedules` as run at once.

### How to execute master switchover

Users can execute master switchover via `MySQLCluster` CR using the following field:
- `preferredMasterIndexes`: The array of indexes which instance is preferred as master

### How to perform Point-in-Time-Recovery(PiTR)

If you want to perform PiTR, you need to download a full dump backup file and subsequent binlog files.

When the `MySQLCluster` with `spec.restore` is created, the operator starts deploying a MySQL cluster as follows.

1. The operator bootstraps the MySQL servers as same as [this procedure](#How-to-deploy-MySQL-servers).
2. The operator creates backup job if `MySQLCluster.spec.restore.pointInTime` is within today.
3. The operator lists `MySQLDump` and `MySQLBinlog` based on `MySQLCluster.spec.restore.fromSelector`.
4. The operator fetches dump files and binary logs from object storage.
5. The operator restores the MySQL servers at `MySQLCluster.spec.restore.pointInTime`.
