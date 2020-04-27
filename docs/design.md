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

- [`MySQLCluster`](mysql_cluster.md) defines a MySQL cluster.
  In this context, MySQL cluster means a cluster of MySQL servers which are replicated by `mysqlbinlog` and `mysqlpump` without group replication such as InnoDB cluster.
- [`MySQLUser`](mysql_user.md) defines a login user in MySQL server.
- [`MySQLDump`](mysql_dump.md) represents a dump file.
- [`MySQLBinlog`](mysql_binlog.md) represents a binary log file.

### External components

- Object storage: Store logical backups and binary logs. e.g. Ceph RGW, S3

### Diagram

TBD

Architecture
------------

MySO is one of the custom controllers, so it includes custom resource definitions(CRD).

### How to bootstrap MySQL servers

Operator has the responsibility to create master-slave configuration of MySQL clusters.

When the `MySQLCluster` is created, operator starts deploying a new MySQL cluster as follows.

1. Operator creates StatefulSet which has 3 replicas and its head-less Service.
1. Operator provisions the replicas as 3-node master/slave MySQL servers.
1. Operator creates some k8s resources.
    - Two Services. The first one is for accessing master. The later one is for accessing slaves.
    - Secrets to store credentials.
    - ...

### How to control master-slave configuration

Operator has the responsibility to manage master-slave configuration of MySQL clusters.

Operator stores clusters' states on `MySQLCluster`.
It represents that who is the master.

Based on the CR, Operator control the MySQL servers.

If there is a difference between the desired master defined in `MySQLCluster.spec` and actual master defined in `MySQLCluster.status`,
operator changes the master.
Therefore clients can change the master through the CR spec.

### How to perform Point-in-Time-Recovery(PiTR)

Backup job has the responsibility as follows:

- Upload full-dump files to object storage and create `MySQLDump` resources.
- Upload binary logs to object storage and create `MySQLBinlog` resources.

Operator executes the Job periodically by creating CronJob for each `MySQLCluster`.

When the `MySQLCluster` with `spec.restore` is created, operator starts deploying a MySQL cluster as follows.

1. Operator bootstrap the MySQL servers as same as [this procedure](#How-to-deploy-MySQL-servers).
1. Operator lists `MySQLDump` and `MySQLBinlog` based on `MySQLCluster.spec.restore.fromSelector`.
1. Operator create backup job if `MySQLCluster.spec.restore.pointInTime` is within today.
1. Operator fetches dump files and binary logs from object storage.
1. Operator restores the MySQL servers at `MySQLCluster.spec.restore.pointInTime`.

Packaging and deployment
------------------------
