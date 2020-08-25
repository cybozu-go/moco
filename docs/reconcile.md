Reconcile procedure
===================

MOCO constructs the MySQL Cluster by its reconcile loop.  Note that the reconcile loop is called periodically even if there is no CRUD of Kubernetes API resources.

Concepts
--------

### Constraints always to keep

In the reconcile loop, MOCO keeps the following constraints:
- An instance that can accept write requests exits alone.  In other words, there is at most one instance with the setting `read_only=off`
  - A Multi-primary cluster requires a completely different reconcile process.  MOCO only supports single-primary cluster
- Any other instance except for the primary must not have the setting `read_only=off`.  The primary instance is indicated at `.status.currentPrimaryIndex` in the `MySQLCluster` custom resource
- Once `.status.currentPrimaryIndex` is set, the value can be changed but cannot be empty
  - TODO: Is it a correct constraint?

### When MOCO can execute failover




### Settings for initial and rebooted instance
