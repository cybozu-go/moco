# How MOCO maintains MySQL clusters

For each [MySQLCluster](crd_mysqlcluster.md), MOCO creates and maintains a set of `mysqld` instances.
The set contains one _primary_ instance and may contain _multiple_ replica instances depending on the `spec.replicas` value of MySQLCluster.

This document describes how MOCO does this job safely.

- [Terminology](#terminology)
- [Prerequisites](#prerequisites)
- [Limitations](#limitations)
- [Possible states](#possible-states)
  - [MySQLCluster](#mysqlcluster)
  - [Pod](#pod)
  - [MySQL data](#mysql-data)
- [Invariants](#invariants)
- [The maintenance flow](#the-maintenance-flow)
  - [Gather the current status](#gather-the-current-status)
  - [Update `status` of MySQLCluster](#update-status-of-mysqlcluster)
  - [Determine what MOCO should do for the cluster](#determine-what-moco-should-do-for-the-cluster)

## Terminology

- Replication: [GTID-based replication](https://dev.mysql.com/doc/refman/8.0/en/replication-gtids-concepts.html) between `mysqld` instances.
- Cluster: a group of `mysqld` instances that replicate data between them.
- Primary (instance): a single source instance of `mysqld` in a cluster.
- Replica (instance): a read-only instance of `mysqld` that synchronizes data with the primary instance.
- Intermediate primary: a special primary instance that replicates data from an external `mysqld`.
- [Errant transaction][errant]: a transaction that exists only on a replica instance.
- Errant replica: a replica instance that has errant transactions.
- Switchover: operation to change a live primary to a replica and promote a replica to the new primary.
- Failover: operation to replace a dead primary with a replica.

## Prerequisites

MySQLCluster allows positive odd numbers for `spec.replicas` value.  If 1, MOCO runs a single `mysqld` instance without configuring replication.  If 3 or greater, MOCO chooses a `mysqld` instance as a primary, writable instance and configures all other instances as replicas of the primary instance.

`status.currentPrimaryIndex` in MySQLCluster is used to record the current chosen primary instance.
Initially, `status.currentPrimaryIndex` is zero and therefore the index of the primary instance is zero.

As a special case, if `spec.replicationSourceSecretName` is set for MySQLCluster, the primary instance is configured as a replica of an external MySQL server.  In this case, the primary instance will not be writable.  We call this type of primary instance _intermediate primary_.

If `spec.replicationSourceSecretName` is _not_ set, MOCO configures [semisynchronous replication](https://dev.mysql.com/doc/refman/8.0/en/replication-semisync.html) between the primary and replicas.  Otherwise, the replication is asynchronous.

For semi-synchronous replication, MOCO configures [`rpl_semi_sync_master_timeout`](https://dev.mysql.com/doc/refman/8.0/en/replication-options-source.html#sysvar_rpl_semi_sync_master_timeout) long enough so that it never degrades to asynchronous replication.

Likewise, MOCO configures [`rpl_semi_sync_master_wait_for_slave_count`](https://dev.mysql.com/doc/refman/8.0/en/replication-options-source.html#sysvar_rpl_semi_sync_master_wait_for_slave_count) to (`spec.replicas` - 1 / 2) to make sure that at least half of replica instances have the same commit as the primary.  e.g., If `spec.replicas` is 5, `rpl_semi_sync_master_wait_for_slave_count` will be set to 2.

MOCO also disables [`relay_log_recovery`](https://dev.mysql.com/doc/refman/8.0/en/replication-options-replica.html#sysvar_relay_log_recovery) because enabling it would drop the relay logs on replicas.

`mysqld` always starts with `super_read_only=1` to prevent erroneous writes, and with `skip_replica_start` to prevent misconfigured replication.

[`moco-agent`][agent], a sidecar container for MOCO, initializes MySQL users and plugins.  At the end of the initialization, it issues `RESET MASTER | RESET BINARY LOGS AND GTIDS` to clear [executed GTID set](https://dev.mysql.com/doc/refman/8.0/en/replication-options-gtids.html#sysvar_gtid_executed).

`moco-agent` also provides a readiness probe for `mysqld` container.  If a replica instance does not start replication threads or is too delayed to execute transactions, the container and the Pod will be determined as unready.

## Limitations

Currently, MOCO does not re-initialize data after the primary instance fails.

After failover to a replica instance, the old primary may have [errant transactions][errant] because it may recover unacknowledged transactions in its binary log.  This is an inevitable limitation in MySQL semi-synchronous replication.

If this happens, MOCO detects the errant transaction and will not allow the old primary to rejoin the cluster as a replica.

Users need to delete the volume data (PersistentVolumeClaim) and the pod of the old primary to re-initialize it.

## Possible states

### MySQLCluster

MySQLCluster can be one of the following states.

The initial state is _Cloning_ if `spec.replicationSourceSecretName` is set, or _Restoring_ if `spec.restore` is set.
Otherwise, the initial state is _Incomplete_.

Note that, if the primary Pod is **ready**, the `mysqld` is assured writable.
Likewise, if a replica Pod is ready, the `mysqld` is assured read-only and running replication threads w/o too much delay.

1. Healthy
    - All Pods are ready.
    - All replicas have no errant transactions.
    - All replicas are read-only and connected to the primary.
    - For intermediate primary instance, the primary works as a replica for an external `mysqld` and is read-only.
2. Cloning
    - `spec.replicationSourceSecretName` is set.
    - `status.cloned` is false.
    - The cloning result exists and is not "Completed" _or_ there is no cloning result and the instance has no data.
    - (note: if the primary has some data and has no cloning result, the instance was used to be a replica and then promoted to the primary.)
3. Restoring
    - `spec.restore` is set.
    - `status.restoredTime` is not set.
4. Degraded
    - The primary Pod is ready and does not lose data.
    - For intermediate primary instance, the primary works as a replica for an external `mysqld` and is read-only.
    - Half or more replicas are ready, read-only, connected to the primary, and have no errant transactions.  For example, if `spec.replicas` is 5, two or more such replicas are needed.
    - At least one replica has some problems.
      - This also includes cases where a replica's `rpl_semi_sync_master_wait_sessions` is greater than 0. See related issues. [#813](https://github.com/cybozu-go/moco/issues/813)
5. Failed
    - The primary instance is not running or lost data.
    - More than half of replicas are running and have data without errant transactions.  For example, if `spec.replicas` is 5, three or more such replicas are needed.
6. Lost
    - The primary instance is not running or lost data.
    - Half or more replicas are not running or lost data or have errant transactions.
7. Incomplete
    - None of the above states applies.

MOCO can recover the cluster to Healthy from **Degraded**, **Failed**, or **Incomplete** if all Pods are running and there are no [errant transactions][errant].  

MOCO can recover the cluster to Degraded from **Failed** when not all Pods are running.  Recovering from Failed is called _failover_.

MOCO cannot recover the cluster from **Lost**.  Users need to restore data from backups.

### Pod

`mysqld` is run as a container in a Pod.
Therefore, MOCO needs to be aware of the following conditions.

1. Missing: the Pod does not exist.
2. Exist: the Pod exists and not _Terminating_ or _Demoting_.
3. Terminating: The Pod exists and `metadata.deletionTimestamp` is _not_ null.
4. Demoting: The Pod exists and has `moco.cybozu.com/demote: true` annotation.

If there are missing Pods, MOCO does nothing for the MySQLCluster.

If a primary instance Pod is _Terminating_ or _Demoting_, MOCO controller changes the primary to one of the replica instances.  This operation is called _switchover_.

### MySQL data

MOCO checks replica instances whether they have errant transactions compared to the primary instance.
If it detects such an instance, MOCO records the instance with MySQLCluster and excludes it from the cluster.

The user needs to delete the Pod and the volume manually and let the StatefulSet controller to re-create them.
After a newly initialized instance gets created, MOCO will allow it to rejoin the cluster.

## Invariants

- By definition, the primary instance recorded in MySQLCluster has no errant transactions.  It is always the single source of truth.
- Errant replicas are not treated as ready even if their Pod status is ready.

## The maintenance flow

MOCO runs the following infinite loop for each MySQLCluster.
It stops when MySQLCluster resource is deleted.

1. Gather the current status
2. Update `status` of MySQLCluster
3. Determine what MOCO should do for the cluster
4. If there is nothing to do, wait a while and go to 1
5. Do the determined operation then go to 1

Read the following sub-sections about 1 to 3.

### Gather the current status

MOCO gathers the information from `kube-apiserver` and `mysqld` as follows:

- MySQLCluster resource
- Pod resources
    - If some of the Pods are missing, MOCO does nothing.
- `mysqld`
    - `SHOW REPLICAS` (on the primary)
    - `SHOW REPLICA STATUS` (on the replicas)
    - Global variables such as `gtid_executed` or `super_read_only`
    - Result of CLONE from `performance_schema.clone_status` table

If MOCO cannot connect to an instance for a certain period, that instance is determined as failed.

### Update `status` of MySQLCluster

In this phase, MOCO updates `status` field of MySQLCluster as follows:

1. Determine the current MySQLCluster state.
2. Add or update type=`Initialized` condition to `status.conditions` as
    - `True` if the cluster state is not Cloning.
    - otherwise, `False`.
3. Add or update type=`Available` condition to `status.conditions` as
    - `True` if the cluster state is Healthy or Degraded.
    - otherwise, `False`.
3. Add or update type=`Healthy` condition to `status.conditions` as
    - `True` if the cluster state is Healthy.
    - otherwise, `False`.
    - The `Reason` field is set to the cluster state such as "Failed" or "Incomplete".
4. Set the number of ready replica Pods to `status.syncedReplicas`.
5. Add newly found errant replicas to `status.errantReplicaList`.
6. Remove re-initialized and/or no-longer errant replicas from `status.errantReplicaList`
7. Set `status.errantReplicas` to the length of `status.errantReplicaList`.
8. Set `status.cloned` to true if `spec.replicationSourceSecret` is not nil and the state is not Cloning.

### Determine what MOCO should do for the cluster

The operation depends on the current cluster state.

The operation and its result are recorded as Events of MySQLCluster resource.

cf. [Application Introspection and Debugging][Event]

#### Healthy

If the primary instance Pod is Terminating or Demoting, switch the primary instance to another replica.
Otherwise, just wait a while.

The switchover is done as follows.
It takes at least several seconds for a new primary to become writable.

1. Make the primary instance `super_read_only=1`.
2. Kill all existing connections except ones from `localhost` and ones for MOCO.
3. Wait for a replica to catch up the executed GTID set of the primary instance.
4. Set `status.currentPrimaryIndex` to the replica's index.
5. If the old primary is Demoting, remove `moco.cybozu.com/demote` annotation from the Pod.

#### Cloning

Execute [`CLONE INSTANCE`](https://dev.mysql.com/doc/refman/8.0/en/clone-plugin-remote.html) on the intermediate primary instance to clone data from an external MySQL instance.

If the cloning goes successful, do the same as Intermediate case.

#### Restoring

Do nothing.

#### Degraded

First, check if the primary instance Pod is Terminating or Demoting, and if it is, do the switchover just like Healthy case.

Then, do the same as Intermediate case to try to fix the problems.
It is not possible to recover the cluster to Healthy if there are errant or stopped replicas, though.

#### Failed

MOCO chooses the most advanced instance as the new primary instance.
The most advanced means that its retrieved GTID set is the superset of all other replicas except for those have errant transactions.

To prevent accidental writes to the old primary instance (so-called split-brain), MOCO stops replication IO_THREAD for all replicas.  This way, the old primary cannot get necessary acks from replicas to write further transactions.

The failover is done as follows:

1. Stop IO_THREAD on all replicas.
2. Choose the most advanced replica as the new primary.  Errant replicas recorded in MySQLCluster are excluded from the candidates.
3. Wait for the replica to execute all retrieved GTID set.
4. Update `status.currentPrimaryIndex` to the new primary's index.

#### Lost

There is nothing can be done.

#### Intermediate

- On the primary that was an intermediate primary, wait for all the retrieved GTID set to be executed.
- Start replication between the primary and non-errant replicas.
    - If a replication has no data, MOCO clones the primary data to the replica first.
- Stop replication of errant replicas.
- Set `super_read_only=1` for replica instances that are writable.
- Adjust `moco.cybozu.com/role` label to Pods according to their roles.
    - For errant replicas, the label is removed to prevent users from reading inconsistent data.
- Finally, make the primary `mysqld` writable if the primary is not an intermediate primary.

[agent]: https://github.com/cybozu-go/moco-agent
[errant]: https://www.percona.com/blog/2014/05/19/errant-transactions-major-hurdle-for-gtid-based-failover-in-mysql-5-6/
[Event]: https://kubernetes.io/docs/tasks/debug-application-cluster/debug-application-introspection/
