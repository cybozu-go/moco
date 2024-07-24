# How MOCO reconciles MySQLCluster

MOCO creates and updates a StatefulSet and related resources for each MySQLCluster custom resource.
This document describes how and when MOCO updates them.

- [Reconciler versions](#reconciler-versions)
- [The update policy of moco-agent container](#the-update-policy-of-moco-agent-container)
- [Clustering related resources](#clustering-related-resources)
  - [StatefulSet](#statefulset)
  - [When the StatefulSet is _not_ updated](#when-the-statefulset-is-not-updated)
  - [Status about StatefulSet](#status-about-statefulset)
  - [Secrets](#secrets)
  - [Certificate](#certificate)
  - [Service](#service)
  - [ConfigMap](#configmap)
  - [PodDisruptionBudget](#poddisruptionbudget)
  - [ServiceAccount](#serviceaccount)
- [Backup and restore related resources](#backup-and-restore-related-resources)
  - [CronJob](#cronjob)
  - [Job](#job)
- [Status of Reconcliation](#status-of-reconcliation)

## Reconciler versions

MOCO's reconciliation routine should be consistent to avoid frequent updates.

That said, we may need to modify the reconciliation process in the future.
To avoid updating the StatefulSet, MOCO has multiple versions of reconcilers.

For example, if a MySQLCluster is reconciled with version 1 of the reconciler,
MOCO will keep using the version 1 reconciler to reconcile the MySQLCluster.

If the user edits MySQLCluster's `spec` field, MOCO can reconcile the MySQLCluster with the latest reconciler, for example version 2, because the user shall be ready for mysqld restarts.

## The update policy of moco-agent container

We shall try to avoid updating moco-agent as much as possible.

## Clustering related resources

The figure below illustrates the overview of resources related to clustering MySQL instances.

![Overview of clustering related resources](https://www.plantuml.com/plantuml/svg/XLF1QkCm4BtxAqHwoHvis1u33TtDNXRQqiLx2pceqZWHo9RGZ5hCqdxxnYDLgKj8VJ3pvZq_ZvwaMoGPAFQswfpL4CJYGVQ0NYeGlLEknX49-eMGA5Bvq8f_bJW-oWqKd1KBrcLa8R3s15cxRK458B5yb8WlBcZyjfjaFiDYcBuHJRCkd5W95K0ILCdgDsA4m9yRBbDx0u5CPzHHo9mwuZEst2-My-7-thLfBBB836lhTS7fVwBXZbWbN7sGRWz6QnXsmUmFLC-MLy19fTtBKBFvQtKc_yuvpZ8YX9Bwzdvi_znjR4JA8QXKvwMG9EYYRO6OHBoOlt9-dEOILzOCq6X71FfyAAmbUqxwwBHO-c1w6SQapU037S1ResIYC_Z-1N_zFabuDWe-_GAVbP_nnQFmAHDoUw03XFS0DsxepFvUUh7inqODNTBtux6S6VvGoiKXvoZBhByCXkZ9kE5dr7j8lDTFB7ZbDx_olapd9-y2egXvpzSbe7bx7ioyNmBXbOkRxTMJZR2B_cRcnkkUiyNq8dqz7S9oHj_MfxEnuhAQzlvjcYTpP0lrwOeXdYxOBGmwzlO_)

### StatefulSet

MOCO tries not to update the StatefulSet frequently.
It updates the StatefulSet only when the update is a must.

#### The conditions for StatefulSet update

The StatefulSet will be updated when:

- Some fields under `spec` of MySQLCluster are modified.
- `my.cnf` for mysqld is updated.
- the version of the reconciler used to reconcile the StatefulSet is obsoleted.
- the image of moco-agent given to the controller is updated.
- the image of mysqld_exporter given to the controller is updated.

### When the StatefulSet is _not_ updated

- the image of fluent-bit given to the controller is changed.
    - because the controller does not depend on fluent-bit.

The fluent-bit sidecar container is updated only when some fields under `spec` of MySQLCluster are modified.


### Status about StatefulSet

- In `MySQLCluster.Status.Condition`, there is a condition named `StatefulSetReady`.
- This indicates the readieness of StatefulSet.
- The condition will be `True` when the rolling update of StatefulSet completely finishes.

### Secrets

MOCO generates random passwords for users that MOCO uses to access MySQL.

The generated passwords are stored in two Secrets.
One is in the same namespace as `moco-controller`, and the other is in the namespace of MySQLCluster.

### Certificate

MOCO creates a Certificate in the same namespace as `moco-controller` to issue a TLS certificate for `moco-agent`.

After cert-manager issues a TLS certificate and creates a Secret for it, MOCO copies the Secret to the namespace of MySQLCluster.  For details, read [security.md](security.md).

### Service

MOCO creates three Services for each MySQLCluster, that is:

- A headless Service, required for every StatefulSet
- A Service for the primary mysqld instance
- A Service for replica mysql instances

The Services' labels, annotations, and `spec` fields can be customized with MySQLCluster's `spec.primaryServiceTemplate` and `spec.replicaServiceTemplate` field.
The `spec.primaryServiceTemplate` configures the Service for the primary mysqld instance
and the `spec.replicaServiceTemplate` configures the Service for the replica mysqld instances.

The following fields in Service `spec` may not be customized, though.

- `clusterIP`
- `selector`

The `ports` field in the Service `spec` is also customizable.
However, for the `mysql` and `mysqlx` ports, MOCO overwrites the fixed value to the `port`, `protocol` and `targetPort` fields.

### ConfigMap

MOCO creates and updates a ConfigMap for `my.cnf`.
The name of this ConfigMap is calculated from the contents of `my.cnf` that may be changed by users.

MOCO deletes old ConfigMaps of `my.cnf` after a new ConfigMap for `my.cnf` is created.

If the cluster does not disable a sidecar container for slow query logs, MOCO creates a ConfigMap for the sidecar.

### PodDisruptionBudget

MOCO creates a PodDisruptionBudget for each MySQLCluster to prevent
too few semi-sync replica servers.

The `spec.maxUnavailable` value is calculated from MySQLCluster's
`spec.replicas` as follows:

    `spec.maxUnavailable` = floor(`spec.replicas` / 2)

If `spec.replicas` is 1, MOCO does not create a PDB.

### ServiceAccount

MOCO creates a ServiceAccount for Pods of the StatefulSet.
The ServiceAccount is not bound to any Roles/ClusterRoles.

## Backup and restore related resources

See [backup.md](backup.md) for the overview of the backup and restoration mechanism.

### CronJob

This is the only resource created when backup is enabled for MySQLCluster.

If the backup is disabled, the CronJob is deleted.

### Job

To restore data from a backup, MOCO creates a Job.
MOCO deletes the Job after the Job finishes successfully.

If the Job fails, MOCO leaves the Job.

## Status of Reconcliation

- In `MySQLCluster.Status.Condition`, there is a condition named `ReconcileSuccess`.
- This indicates the status of reconcilation.
- The condition will be `True` when the reconcile function successfully finishes.
