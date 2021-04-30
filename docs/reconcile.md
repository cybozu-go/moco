# How MOCO reconciles MySQLCluster

MOCO creates and updates a StatefulSet and related resources for each MySQLCluster custom resource.
This document describes how and when MOCO updates them.

## Reconciler versions

MOCO's reconciliation routine should be consistent to avoid frequent updates.

That said, we may need to modify the reconciliation process in the future.
To avoid updating the StatefulSet, MOCO has multiple versions of reconcilers.

For example, if a MySQLCluster is reconciled with version 1 of the reconciler,
MOCO will keep using the version 1 reconciler to reconcile the MySQLCluster.

If the user edits MySQLCluster's `spec` field, MOCO can reconcile the MySQLCluster
with the latest reconciler, for example version 2, because the user shall be
ready for mysqld restarts.

## StatefulSet

MOCO tries not to update the StatefulSet frequently.
It updates the StatefulSet only when the update is a must.

### The conditions for StatefulSet update

The StatefulSet will be updated when:

- Some fields under `spec` of MySQLCluster are modified.
- `my.cnf` for mysqld is updated.
- the version of the reconciler used to reconcile the StatefulSet is obsoleted.
- the image of moco-agent given to the controller is updated.

### When the StatefulSet is _not_ updated

- the image of fluent-bit given to the controller is changed.
    - because the controller does not depend on fluent-bit.

The fluent-bit sidecar container is updated only when some fields under
`spec` of MySQLCluster are modified.

## Service

MOCO creates three Services for each MySQLCluster, that is:

- A headless Service, required for every StatefulSet
- A Service for the primary mysqld instance
- A Service for replica mysql instances

The Services' labels, annotations, and `spec` fields can be customized
with MySQLCluster's `status.serviceTemplate` field.

The following fields in Service `spec` may not be customized, though.

- `clusterIP`
- `ports`
- `selector`

## PodDisruptionBudget

MOCO creates a PodDisruptionBudget for each MySQLCluster to prevent
too few semi-sync replica servers.

The `spec.maxUnavailable` value is calculated from MySQLCluster's
`spec.replicas` as follows:

    `spec.maxUnavailable` = floor(`spec.replicas` / 2)

If `spec.replicas` is 1, MOCO does not create a PDB.

## The update policy of moco-agent container

We shall try to avoid updating moco-agent as much as possible.
