Design notes
============

## Motivation

We are creating our own Kubernetes operator for clustering MySQL instances for the following reasons:

Firstly, our application requires strict-compatibility to the traditional MySQL.  Although recent MySQL provides an advanced clustering solution called [group replication](https://dev.mysql.com/doc/refman/8.0/en/group-replication.html) that is based on [Paxos](https://en.wikipedia.org/wiki/Paxos_(computer_science)), we cannot use it because of [various limitations from group replication](https://dev.mysql.com/doc/refman/8.0/en/group-replication-limitations.html).

Secondly, we want to have a Kubernetes native and the simplest operator.  For example, we can use Kubernetes Service to load-balance read queries to multiple replicas.  Also, we do not want to support non-GTID based replications.

Lastly, none of the existing operators could satisfy our requirements.

## Goals

- Manage primary-replica cluster of MySQL instances.
    - The primary instance is the only instance that allows writes.
    - Replica instances replicate data from the primary.
- Support replication from an external MySQL instance.
- Support all the four transaction isolation levels.
- No split-brain is allowed.
- Accept large transactions.
- Upgrade this operator without restarting MySQL Pods.
- Support multiple MySQL versions and automatic upgrading.
- Support automatic primary selection and switchover.
- Support automatic failover.
- Backup and restore features.
- Support point-in-time-recovery (PiTR).
- Tenant users can specify the following parameters:
  - The version of MySQL instances.
  - The number of processor cores for each MySQL instance.
  - The amount of memory for each MySQL instance.
  - The amount of backing storage for each MySQL instance.
  - The number of replicas in the MySQL cluster.
  - Custom configuration parameters.
- Allow `CREATE / DROP TEMPORARY TABLE` during a transaction.
- Use Custom Resource Definition(CRD) to automate construction of MySQL database using replication on Kubernetes.

## Non-goals

- Node fencing.

    Fencing should be done externally.  Once Pod and PVC/PV are removed as a consequence of node fencing, the operator will restore the cluster appropriately.

## Components

### Workloads

- Operator: Custom controller which automates MySQL cluster management with the following namespaced custom resources:
    - [MySQLCluster](crd_mysql_cluster.md) represents a MySQL cluster.
    - [ObjectStorage](crd_object_storage.md) represents a connection setting to an object storage which has Amazon S3 compatible API (e.g. Ceph RGW).
- Admission Webhook: Webhook for validating custom resources (e.g. validate the object storage for backup exists).
- [cert-manager](https://cert-manager.io/): Provide client certifications and primary-replica certifications automatically.

### Tools

- `kubectl-moco`: CLI to manipulate MySQL cluster. It provides functionalities such as:
    - Execute `mysql` client for a MySQL instance running on Kubernetes.
    - Fetch a credential file to a local environment.

### Diagram

![design_architecture](https://www.plantuml.com/plantuml/png/ZPCnRzim48Lt_eg3koI3WLhUYg88ean6MhiHWg2mFL3InLPDaUo9Qci4-U-bTAf5rHRIHG3VUxplU29lAYV9rQKICdE6uB524ki4wMUHuKO_UybIKKewRa5MO2js_eaGMbLaicepz3SZjCaHllZF-qQVF1aw84tWHG1OcHta3k5krLftle0vbgWTsm3hqcHcsvgVb_5o0k--eLBcbpTVnMjGUyQPO_Br7bRSAbmnwdh8IXBE9auwVAvLWW7jMFrG-Oo1l1X7HW7oWOy-6sL6Rp2Z_sFEpvdHA7F-1dC-pkpBn8is59FH2vEUAgJUhkqM-WffePNPRNIxi7LfpqwHIoTJMI6i4sV85sV-Hc-qxpKpfPMkI1Lkz3BzZfc3BjO4VB7xOhTtjwf6U3dD93RQaL4h9JLodon0gt2t9-n4sgAvbKZ3QdoYTgYngYk7n8s52bp53zT-swhG1yxVjXD8iZtcjUgECjJEzmJ_WZS4mY3OZPj3tI8CSBUCur0W3Bay--P5mtJwwVHsuGFu3RrE8teu1E_5XD9XRmzFt0UQTtjf_vDqsPxTy-tdVZ2V2xMxmVHE6FyudJPl_O8MNT3ceYlMhkE5u0lUKBfRw2cFLXcPYzC8lTiWlBCYy_ieQ6X4OqOFcr9p3RqO_3vnWpglI_K7)

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

Read [reconcile.md](reconcile.md) on how MOCO reconciles the StatefulSet.
