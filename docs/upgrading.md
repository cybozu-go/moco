# Upgrading mysqld

This document describes how mysqld upgrades its data and what MOCO has to do about it.

- [Preconditions](#preconditions)
  - [MySQL data](#mysql-data)
  - [Downgrading](#downgrading)
  - [Upgrading a replication setup](#upgrading-a-replication-setup)
  - [StatefulSet behavior](#statefulset-behavior)
  - [Automatic switchover](#automatic-switchover)
- [MOCO implementation](#moco-implementation)
  - [Example](#example)
  - [Limitations](#limitations)
- [User's responsibility](#users-responsibility)

## Preconditions

### MySQL data

Beginning with 8.0.16, `mysqld` can update all data that need to be updated when it starts running.
This means that MOCO needs nothing to do with MySQL data.

One thing that we should care about is that the update process may take a long time.
The startup probe of `mysqld` container should be configured to wait for `mysqld` to
complete updating data.

ref: https://dev.mysql.com/doc/refman/8.0/en/upgrading-what-is-upgraded.html

### Downgrading

MySQL 8.0 does not support any kind of downgrading.

ref: https://dev.mysql.com/doc/refman/8.0/en/downgrading.html

Internally, MySQL has a version called "data dictionary (DD) version".
If two MySQL versions have the same DD version, they are considered to have data compatibility.

ref: https://github.com/mysql/mysql-server/blob/mysql-8.0.24/sql/dd/dd_version.h#L209

Nevertheless, DD versions do change from time to time between revisions of MySQL 8.0.
Therefore, the simplest way to avoid DD version mismatch is to not downgrade MySQL.

### Upgrading a replication setup

In a nutshell, replica MySQL instances should be the same or newer than the source MySQL instance.

refs:

- https://dev.mysql.com/doc/refman/8.0/en/replication-compatibility.html
- https://dev.mysql.com/doc/refman/8.0/en/replication-upgrade.html

### StatefulSet behavior

When the Pod template of a StatefulSet is updated, Kubernetes updates the Pods.
With the default update strategy `RollingUpdate`, the Pods are updated one by one
from the largest ordinal to the smallest.

StatefulSet controller keeps the old Pod template until it completes the rolling update.
If a Pod that is not being updated are deleted, StatefulSet controller restores the Pod
from the old template.

This means that, if the cluster is Healthy, MySQL is assured to be updated one by one
from the instance of the largest ordinal to the smallest.

refs: 
- https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/#rolling-updates
- https://kubernetes.io/docs/tutorials/stateful-application/basic-stateful-set/#rolling-update

### Automatic switchover

MOCO switches the primary instance when the Pod of the instance is being deleted.
Read [`clustering.md`](clustering.md) for details.

## MOCO implementation

With the preconditions listed above, MOCO can upgrade `mysqld` in MySQLCluster safely
as follows.

1. Set `.spec.updateStrategy` field in StatefulSet to `RollingUpdate`.
2. Choose the lowest ordinal Pod as the next primary upon a switchover.
3. Configure the startup probe of `mysqld` container to wait long enough.
    - By default, MOCO configures the probe to wait up to one hour.
    - Users can adjust the duration for each MySQLCluster.

### Example

Suppose that we are updating a three-instance cluster.
The `mysqld` instances in the cluster have ordinals 0, 1, and 2, and the
current primary instance is instance 1.

After MOCO updates the Pod template of the StatefulSet created for the cluster,
Kubernetes start re-creating Pods starting from instance 2.

Instance 2 is a replica and therefore is safe for an update.

Next to instance 2, the instance 1 Pod is deleted.  The deletion triggers
an automatic switchover so that MOCO changes the primary to the instance 0
because it has the lowest ordinal.  Because instance 0 is running an old
`mysqld`, the preconditions are kept.

Finally, instance 0 is re-created in the same way.  This time, MOCO switches
the primary to instance 1.  Since both instance 1 and 2 has been updated and
instance 0 is being deleted, the preconditions are kept.

### Limitations

If an instance is down during an upgrade, MOCO may choose an already updated
instance as the new primary even though some instances are still running an
old version.

If this happens, users may need to manually delete the old replica data and
re-initialize the replica to restore the cluster health.

## User's responsibility

- Make sure that the cluster is healthy before upgrading
- Check and [prepare your installation for upgrade](https://dev.mysql.com/doc/refman/8.0/en/upgrade-prerequisites.html)
- Do not attempt to downgrade MySQL
