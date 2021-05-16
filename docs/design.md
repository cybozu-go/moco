Design notes
============

The purpose of this document is to describe the backgrounds and the goals of MOCO.
Implementation details are described in other documents.

## Motivation

We are creating our own Kubernetes operator for clustering MySQL instances for the following reasons:

Firstly, our application requires strict-compatibility to the traditional MySQL.  Although recent MySQL provides an advanced clustering solution called [group replication](https://dev.mysql.com/doc/refman/8.0/en/group-replication.html) that is based on [Paxos](https://en.wikipedia.org/wiki/Paxos_(computer_science)), we cannot use it because of [various limitations from group replication](https://dev.mysql.com/doc/refman/8.0/en/group-replication-limitations.html).

Secondly, we want to have a Kubernetes native and the simplest operator.  For example, we can use Kubernetes Service to load-balance read queries to multiple replicas.  Also, we do not want to support non-GTID based replications.

Lastly, none of the existing operators could satisfy our requirements.

## Goals

- Manage primary-replica style clustering of MySQL instances.
    - The primary instance is the only instance that allows writes.
    - Replica instances replicate data from the primary and are read-only.
- Support replication from an external MySQL instance.
- Support all the four transaction isolation levels.
- No split-brain.
- Allow large transactions.
- Upgrade the operator without restarting MySQL Pods.
- Safe and automatic upgrading of MySQL version.
- Support automatic primary selection and switchover.
- Support automatic failover.
- Backup and restore features.
    - Support point-in-time recovery (PiTR).
- Tenant users can specify the following parameters:
    - The version of MySQL instances.
    - The number of processor cores for each MySQL instance.
    - The amount of memory for each MySQL instance.
    - The amount of backing storage for each MySQL instance.
    - The number of replicas in the MySQL cluster.
    - Custom configuration parameters.
- Allow `CREATE / DROP TEMPORARY TABLE` during a transaction.

## Non-goals

- Support for older MySQL versions (5.6, 5.7)

    As a late comer, we focus our development effort on the latest MySQL.
    This simplifies things and allows us to use advanced mechanisms such as `CLONE INSTANCE`.

- Node fencing

    Fencing is a technique to safely isolated a failed Node.
    MOCO does not rely on Node fencing as it should be done externally.

    We can still implement failover in a safe way by configuring semi-sync parameters appropriately.
