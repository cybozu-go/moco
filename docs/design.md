Design notes
============

Motivation
----------

This Kubernetes operator automates operations for the binlog-based replication on MySQL.

InnoDB cluster can be used for the replication purpose, but we choose not to use InnoDB cluster because it does not allow large (>2GB) transactions.

There are some existing operators which deploy a group of MySQL servers without InnoDB cluster but they does not support the Point-in-Time-Recovery(PiTR) feature.

- [oracle/mysql-operator](https://github.com/oracle/mysql-operator) takes backups only with `mysqldump`
- [presslabs/mysql-operator](https://github.com/presslabs/mysql-operator) does not restore clusters to the state at the desired Point-in-Time

This operator deploys a group of MySQL servers which replicates data semi-synchronously to the replicas and takes backups with both `mysqlpump` and `mysqlbinlog`.

In this context, we call the group of MySQL servers as MySQL cluster.

Goals
-----

- Have the primary replicate data semi-synchronously to the multiple replicas
- Support all the four transaction isolation levels.
- Avoid split-brain.
- Accept large transactions.
- Upgrade this operator without restarting MySQL `Pod`s.
- Support multiple MySQL versions and automatic upgrading.
- Support automatic primary selection and switchover.
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
  - Fencing should be done externally.  Once Pod and PVC/PV are removed as a consequence of node fencing, MySO will restore the cluster appropriately.

Components
----------

### Workloads

- Operator: Custom controller which automates MySQL cluster management with the following namespaced custom resources:
  - [MySQLCluster](crd_mysql_cluster.md) represents a MySQL cluster.
  - [ObjectStorage](crd_object_storage.md) represents a connection setting to an object storage which has Amazon S3 compatible API (e.g. Ceph RGW).
  - [MySQLBackupSchedule](crd_mysql_backup_schedule.md) represents a full dump & binlog schedule.
  - [SwitchoverJob](crd_mysql_switch_over_job.md) represents a switchover job.
- Admission Webhook: Webhook for validating custom resources (e.g. validate the object storage for backup exists).
- [cert-manager](https://cert-manager.io/): Provide client certifications and primary-replica certifications automatically.

### Tools

- `kubectl-myso`: CLI to manipulate MySQL cluster. It provides functionalities such as:
  - Change primary manually.
  - Port-forward to MySQL servers.
  - Execute SQL like `mysql -u -p` without a credential file on a local environment.
  - Fetch a credential file to a local environment.

### Diagram

![design_architecture](http://www.plantuml.com/plantuml/png/ZLEnRjim4Dtv5GTdIGS3jRmLHH54cOoaSKi4GM5xeAIBhPeYLP9AQmJvzrAaB96nXMGHmFSUxzqz7Q-qOSeq5ISiDrB1WqP5LXJLSvdZMZnPg6BQeDe0qr1fJxnHQCwUKJk5FYg8a0N2T_lvtEAJHwdsg2RmGW1gbk_P5k5cwQWRhBayL2YCfm5MrK7BZIFl3lH-0XU_a7FvrvAlv3MeFJjgVjp2dUlpqXjzYfqezKfgcC6dXbEClFxmOkRj_67SC0aCQJlsRCmmUSSm-PZX_ArXCcOuVyTmFcuub4aNJfGgAHOowojabcxg3GmLib9mkLqBTwYRgZuPeM26PKmZhAYHoXRt_ckn5hRNN5OrCsN6SItkiz-O6-XahS0MkostEtFD6uINFf1K2gCaNt8cqFVf0N28xxQtdR2wRBSHLLpDJ-GIJNdJx_OaH2xJdpRVYsKfzbv-xGAW-GwBXdUJnDZ9bU1FU7q0HC8kx4sS_23mlNetBmg0oDO7txvXc4w_zSyLle3L3xWUmSC4B9SgP0O7Efvt4BIFzTpzHsRotS36rq_v89upjRjS1YQKVvrEsoT-1alEO7FI5NFdT47yTbkNumfQbuUQIrQGFaB7qfbbbah-En0T4yaOCPXDLRk35Wp-7hb2KJGL_my0)


The operator has the responsibility to create primary-replica configuration of MySQL clusters.

When the `MySQLCluster` is created, the operator starts deploying a new MySQL cluster as follows.
In this section, the name of `MySQLCluster` is assumed to be `mysql`.

1. The operator creates `StatefulSet` which has `N`(`N`=3 or 5) `Pod`s and its headless `Service`.
2. The operator sets `mysql-0` as primary and the other `Pod`s as replica.
   The index of the current primary is managed under `MySQLCluster.status.currentPrimaryIndex`.
3. The operator creates some k8s resources.
  - `Service` for accessing primary, both for MySQL protocol and X protocol.
  - `Service` for accessing replicas, both for MySQL protocol and X protocol.
  - `Secrets` to store credentials.
  - `ConfigMap` to store cluster configuration.
  - `ServiceAccount`, `Role` and `RoleBinding` to allow Pods to access resources.

#### How to implement the initialization of MySQL pods with avoiding unnecessary restart at the operator update

When initializing MySQL pods, the following procedures should be executed in their init containers.

- Initialize data-dir.
- Create mysql users.
- Create `my.cnf`.
- etc.

When upgrading the operator, we want to avoid unnecessary restarts of MySQL pods.
Creating mysql users and initializing data-dir are done in an init container of which image is the same with
the `mysqld` container, so the MySQL pods are not restarted at the upgrade.

On the other hand, `my.cnf` is created by merging the following three configurations.

- Default: This is created by the operator and contains default value of `my.cnf`.
- User: This is created by users and contains user-defined values of `my.cnf`. This overwrites Default values.
- Constant: This is created by the operator. This overwrites User values.

If the default and constant configurations are implemented in an init container, the container might be updated
every time the version of the operator changes and the operator restarts the existing MySQL clusters frequently.

To avoid these unnecessary restarts of the clusters, we prepare another container image that is responsible only for the merging logic.
This container image is independent of MOCO image.
The container takes the Default, User and Constant configurations as `ConfigMap`, and then it merges the three `ConfigMap`s to create `my.cnf`.

So, in short we prepare the following two init containers.
1. A container to initialize the MySQL cluster, for example, creating necessary users.
  The entrypoint binary is provided by this repository.
  If users want to use their custom image, they have to include the binary in the image.
2. A container to create `my.cnf` from `ConfigMap`s created by the operator and the user.
  This container is injected by the operator.

If users want to change their MySQL cluster configuration, users basically should execute mysql commands like `SET GLOBAL ...`.
In case that we cannot avoid restarting MySQL cluster to apply changes in the user-defined `ConfigMap`,
we should execute a command like `kubectl moco graceful-restart CLUSTER`.

Behaviors
---------

### How to execute failover when the primary fails

When the primary fails, the cluster is recovered in the following process:

1. Stop the `IO_THREAD` of all replicas.
2. Select and configure new primary.
3. Update `MySQLCluster.status.currentPrimaryIndex` with the new primary name.
4. Turn off read-only mode on the new primary

In the process, the operator configures the old primary as replica if the server is ready.

### How to execute failover when a replica fails

When a replica fails once and it restarts afterwards, the operator basically configures it to follow the primary.

#### How to handle the case a replica continues to fail and restart

If one of the replicas fails again after restarting, the `StatefulSet` controller restarts the replica again.
This means that the replica may continue to fail and restart again and again.
This loop can occur, for example, in the case that the data in the replica is corrupted.
Users must handle this failure manually by deleting the `Pod` or/and `PersistentVolumeClaim`.
Then, the replica `Pod` is scheduled again onto a different node and the operator configures it to follow the primary automatically.

Users can keep the data with a [`VolumeSnapshot`](https://kubernetes.io/docs/concepts/storage/volume-snapshots/) and
create `PersistentVolumeClaim` from the snapshot even after this failure happens.
This feature is available only if the underlying `StorageClass` supports it.

### How to execute switchover

Users can execute primary switchover by applying `SwitchoverJob` CR which contains the primary index to be switched to.

Note that while any `SwitchoverJob` is running, another `SwitchoverJob` can be created but the operator waits for the completion of running jobs.

### How to make a backup

When you create `MySQLBackupSchedule` CR, it creates `CronJob` which stores dump and binlog to an object storage:

If we want to make backups only once, set `MySQLBackupSchedule.spec.schedules` to run once.

### How to perform PiTR

When we create a `MySQLCluster` with `.spec.restore` specified, the operator performs PiTR with the following procedure.

`.spec.restore` is unable to be updated, so PiTR can be executed only when the cluster is being creating.

1. The operator sets the source cluster's `.status.ready` as `False` and make the MySQL cluster block incoming transactions.
2. The operator makes the MySQL cluster flush binlogs from the source `MySQLCluster`. This binlog is used for recovery if the PiTR fails.
3. The operator lists `MySQLBackup` candidates based on `MySQLCluster.spec.restore.sourceClusterName`.
4. The operator selects the corresponding `MySQLBackup` CRs according to `MySQLCluster.spec.restore.pointInTime`.
5. The operator downloads the dump file and the binlogs for `MySQLCluster.spec.restore.pointInTime` from the object storage.
6. The operator restores the MySQL servers to the state at `MySQLCluster.spec.restore.pointInTime`.
7. If the recovery succeeds, the operator sets the source cluster's `.status.ready` as `True`.

### How to upgrade MySQL version of primary and replicas

MySQL software upgrade is triggered by changing container image specified in `MySQLCluster.spec.podTemplate`.
In this section, the name of `StatefulSet` is assumed to be `mysql`.

1. Switch primary to the pod `mysql-0` if the current primary is not `mysql-0`.
2. Update and apply the `StatefulSet` manifest for `mysql`, to trigger upgrading the replicas as follows:
  - Set `.spec.updateStrategy.rollingUpdate.partition` as `1`.
  - Set new image version in `.spec.template.spec.containers`.
3. Wait for all the replicas to be upgraded.
4. Switch primary to `mysql-1`.
5. Update and apply the `StatefulSet` manifest to trigger upgrading `mysql-0` as follows:
  - Remove `.spec.updateStrategy.rollingUpdate.partition`.
6. Wait for `mysql-0` to be upgraded.

### How to manage recovery from blackouts

In the scenario of the recovery from data center blackouts, all members of the MySQL cluster perform cold boots.

The operator waits for all members to boot up again.
It automatically recovers the cluster only after all members come back, not just after the quorum come back.
This is to prevent the data loss even in corner cases.

If a part of the cluster never finishes boot-up, users must intervene the recovery process.
The process is as follows.
1. Users delete the problematic Pods and/or PVCs.  (or they might have been deleted already)
   - Users need to delete PVCs before Pods if they want to delete both due to the [Storage Object in Use Protection](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#storage-object-in-use-protection).
   - [The StatefulSet controller may recreate Pods improperly](https://github.com/kubernetes/kubernetes/issues/74374).
     Users may need to delete the Nodes in this case.
2. The StatefulSet controller creates new blank Pods and/or PVCs.
3. The operator tries to select the primary of the cluster only after all members are up and running, and the quorum of the cluster has data.
   - If the quorum of the cluster does not have data, users need to recover the cluster from a backup.
4. The operator initializes the new blank Pods as new replicas.

### How to collect metrics for Prometheus

To avoid unnecessary restart when the operator is upgraded, we prepare external Pods which collect mysqld metrics over the network,
and export them as Prometheus metrics.

The detail is TBD.

### TBD

- Write merge strategy of `my.cnf`.
- How to clean up zombie backup files on a object storage which does not exist in Kubernetes.

### Candidates of additional features

- Backup files verification.
- Manage MySQL users with [MySQLUser](crd_mysql_user.md).
