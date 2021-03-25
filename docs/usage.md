# How to use MOCO

After [setting up MOCO](setup.md), you can create MySQL clusters with a custom resource called [MySQLCluster](crd_mysqlcluster.md).

- [Basics](#basics)
- [Limitations](#limitations)
  - [Errant replicas](#errant-replicas)
  - [Read-only primary](#read-only-primary)
- [Creating clusters](#creating-clusters)
  - [Creating an empty cluster](#creating-an-empty-cluster)
  - [Creating a cluster that replicates data from an external mysqld](#creating-a-cluster-that-replicates-data-from-an-external-mysqld)
  - [Configurations](#configurations)
- [Using the cluster](#using-the-cluster)
  - [`kubectl moco`](#kubectl-moco)
  - [Connecting to the primary instance](#connecting-to-the-primary-instance)
  - [Connecting to read-only replicas](#connecting-to-read-only-replicas)
- [Status, metrics, and logs](#status-metrics-and-logs)
  - [Cluster status](#cluster-status)
  - [Pod status](#pod-status)
  - [Metrics](#metrics)
  - [Logs](#logs)
- [Maintenance](#maintenance)
  - [Increasing the number of instances in the cluster](#increasing-the-number-of-instances-in-the-cluster)
  - [Switchover](#switchover)
  - [Failover](#failover)
  - [Upgrading mysql version](#upgrading-mysql-version)
  - [Re-initializing an errant replica](#re-initializing-an-errant-replica)

## Basics

MOCO creates a cluster of mysqld instances for each MySQLCluster.
A cluster can consists of 1, 3, or 5 mysqld instances.

MOCO configures [semi-synchronous][semisync] [GTID][]-based replication between mysqld instances in a cluster if the cluster size is 3 or 5.  A 3-instance cluster can tolerate up to 1 replica failure, and a 5-instance cluster can tolerate up to 2 replica failures.

In a cluster, there is only one instance called _primary_.  The primary instance is the source of truth.  It is the only writable instance in the cluster, and the source of the replication.  All other instances are called _replica_.  A replica is a read-only instance and replicates data from the primary.

## Limitations

### Errant replicas

### Read-only primary

MOCO may set the primary mysqld instance read-only for a switchover or other reasons.
Applications that use MOCO MySQL need to be aware of this.

## Creating clusters

### Creating an empty cluster

### Creating a cluster that replicates data from an external mysqld

Let's call the source mysqld instance _donor_.

We use [the clone plugin][CLONE] to copy the whole data quickly.
After the cloning, MOCO needs to create some user accounts and install plugins.
For these things, you need to create two user accounts **on the donor** as follows:

```console
mysql> CREATE USER 'clone-donor'@'%' IDENTIFIED BY 'xxxxxxxxxxx';
mysql> GRANT BACKUP_ADMIN, REPLICATION SLAVE ON *.* TO 'clone-donor'@'%';
mysql> CREATE USER 'clone-init'@'localhost' IDENTIFIED BY 'yyyyyyyyyyy';
mysql> GRANT ALL ON *.* TO 'clone-init'@'localhost' WITH GRANT OPTION;
```

You may change the user names and must change the passwords.

Then create a Secret in the same namespace as MySQLCluster:

```console
$ kubectl -n <namespace> create secret generic donor-secret \
    --from-literal=HOST=<donor-host> \
    --from-literal=PORT=<donor-port> \
    --from-literal=USER=clone-donor \
    --from-literal=PASSWORD=xxxxxxxxxxx \
    --from-literal=INIT_USER=clone-init \
    --from-literal=INIT_PASSWORD=yyyyyyyyyyy
```

You may change the secret name.

Finally, create MySQLCluster with `spec.replicationSourceSecretName: donor-secret`.

You can stop the replication from the donor by setting `spec.replicationSourceSecretName: null` afterwards.

### Configurations

If `innodb_buffer_pool_size` is not given, MOCO sets it automatically to 70% of the value of `resources.requests.memory` (or `resources.limits.memory`).

## Using the cluster

### `kubectl moco`

### Connecting to the primary instance

### Connecting to read-only replicas

## Status, metrics, and logs

### Cluster status

### Pod status

MOCO adds mysqld containers a liveness probe and a readiness probe to check the replication status in addition to the process status.

A replica Pod is _ready_ only when it is replicating data from the primary without a significant delay.

### Metrics

### Logs

## Maintenance

### Increasing the number of instances in the cluster

### Switchover

Switchver is an operation to change the live primary to one of the replicas.

MOCO automatically switch the primary when the Pod of the primary instance is to be deleted.

Users can manually trigger a switchover by annotating the Pod of the primary instance with `moco.cybozu.com/demote: true`.  You can use `kubectl` to do this:

```console
$ kubectl annotate mysqlclusters <name> moco.cybozu.com/demote=true
```

### Failover

### Upgrading mysql version

### Re-initializing an errant replica

[semisync]: https://dev.mysql.com/doc/refman/8.0/en/replication-semisync.html
[GTID]: https://dev.mysql.com/doc/refman/8.0/en/replication-gtids.html
[CLONE]: https://dev.mysql.com/doc/refman/8.0/en/clone-plugin.html
