How MOCO maintains MySQL replication clusters
===================

MOCO keeps primary-replica style MySQL clusters for each MySQLCluster with `spec.replicas` > 1.
This document describes how MOCO does the job safely.

Concepts
--------

### Constraints always to keep

In the reconcile loop, MOCO keeps the following constraints:
- An instance that can accept write requests exits alone.  In other words, there is at most one instance with the setting `read_only=off`
  - A Multi-primary cluster requires a completely different reconcile process.  MOCO only supports single-primary cluster
- Any other instance except for the primary must not have the setting `read_only=off`.  The primary instance is indicated at `.status.currentPrimaryIndex` in the `MySQLCluster` custom resource

### Primary Selection

- MOCO saves the result of primary selection in `.status.currentPrimaryIndex` and it is the single source of truth.
    - If user manipulates `.status` or executes `CHANGE MASTER TO` to a mysqld, Moco doesn't take care of it.

### When MOCO can execute failover

- Executes failover only if it can confirm all instances' statuses.
- Replica's Retrieved_Gtid_Set and Executed_Gtid_set are identical.
- Executes failover process when Moco detects empty instances if one of the following conditions is met.
    - A primary which `.status.currentPrimaryIndex` indicates is available.
    - Two replicas are available. (Three instances if a cluster is consist of Five instances) 

### Settings for initial and rebooted instance

- Initial and rebooted mysqld instance has following configurations because only MOCO determines to start replication and accept write requests.
    - `read_only=on`
    - `super_read_only=on`
    - `skip slave start=on`
    
Reconcile Flows
---------------

1. Retrieve all mysqld instances' statuses and `.status.currentPrimaryIndex`.
2. Confirm if all constraints are complied.
3. If a instance with `read_only=off` does not exist, select a primary instance.
4. Update the primary index to `.status.currentPrimaryIndex`.
5. Clone the data from the new primary to replica instances, if necessary.
6. Configure replication settings with the new primary.
7. Wait for replicas to catch up with the new primary.
8. Create Service resources to access to the primary and replicas.
9. Turn off `read_only` mode on the primary.
