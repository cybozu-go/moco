# Enable Pause and Resume of MySQL Cluster Clustering

## Context

There is a user need to align the `gtid_executed` of all replicas manually
by doing replication from each Pod for consistency checks between Pods in the MySQL Cluster.
In this process, it is necessary to stop the replica's sql thread and manually advance the gtid,
but there is no function to stop clustering in the current MOCO and MOCO starts replication automatically.
Therefore, we are implementing a function to temporarily stop MOCO's clustering monitoring.

## Goals

* Enable pause and resume of MySQL Cluster clustering
* Enable pausing and resuming the reconciliation loop for the MySQLCluster resource
* Operational with the `kubectl moco` command
* Check the presence or absence of a pause with `kubectl get mysqlcluster`

## Non-goals

* Recovery is not guaranteed in the event that the MySQL Cluster is damaged by user operations when resuming from a pause

## ActualDesign

Users can stop the MySQL Cluster clustering or reconciliation with the following command:

```console
$ kubectl moco stop clustering <CLSUTER_NAME>
$ kubectl moco stop reconciliation <CLSUTER_NAME>
```

To resume clustering or reconciliation, use the following command:

```console
$ kubectl moco start clustering <CLSUTER_NAME>
$ kubectl moco start reconciliation <CLSUTER_NAME>
```

### Clustering

When you execute `kubectl moco stop clustering <CLSUTER_NAME>`,
the `moco.cybozu.com/clustering-stopped: "true"` is added to the `.metadata.annotation` of MySQLCluster.

```yaml
metadata:
  annotations:
    moco.cybozu.com/clustering-stopped: "true"
```

This annotation will stop the `ClusterManager` of the annotated MySQLCluster.
Reconciliation processes due to changes in other MySQLCluster fields will continue to be performed.

```go
r.ClusterManager.Stop(req.NamespacedName)
```

The process of stopping the `ClusterManager` is carried out only at the first reconcile
after the execution of `kubectl moco stop clustering <CLSUTER_NAME>`.

The `ClusteringActive` is added to `.status.conditions` of MySQLCluster.
Normally `True` is recorded, but `False` is recorded when the clustering is stopped.
Also, when the clustering is stopped, the `Available` and `Healthy` statuses are also updated to `Unknown`.

```yaml
status:
  conditions:
    - type: ClusteringActive
      status: "False"
      lastTransitionTime: 2018-01-01T00:00:00Z
    - type: Available
      status: "Unknown"
      lastTransitionTime: 2018-01-01T00:00:00Z
    - type: Healthy
      status: "Unknown"
      lastTransitionTime: 2018-01-01T00:00:00Z
```

Also, `CLUSTERING STOPPED` will be added to the table displayed when you do `kubectl get mysqlcluster`.

```console
$ kubectl get mysqlcluster
NAME   AVAILABLE   HEALTHY   PRIMARY   SYNCED REPLICAS   ERRANT REPLICAS   CLUSTERING STOPPED   RECONCILE STOPPED   LAST BACKUP
test   Unknown     Unknown   0         2                                   False                True                <no value>
```

When you execute `kubectl moco start clustering <CLSUTER_NAME>`,
`moco.cybozu.com/clustering-stopped: "true"` is removed from `.metadata.annotation` of MySQLCluster.

The controller resumes the `ClusterManager` for `MySQLCluster` that does not have the `moco.cybozu.com/clustering-stopped: "true"` annotation.
When the `ClusterManager` resumes operation, the `.status` of `ClusteringStopped` in `.status.conditions` is updated to `False`,
and the `.status` of other `.status.conditions` is also updated from `Unknown`.

### Reconciliation

When you execute `kubectl moco stop reconciliation <CLSUTER_NAME>`,
the `moco.cybozu.com/reconciliation-stopped: "true"` is added to the `.metadata.annotation` of MySQLCluster.

```yaml
metadata:
  annotations:
    moco.cybozu.com/reconciliation-stopped: "true"
```

The reconcile process will not be executed for MySQLCluster resources annotated with this annotation.
Therefore, even if changes are made to the fields of the MySQLCluster resource, changes will not be propagated to other resources.

The only exception is the stopping and restarting of clustering by the `moco.cybozu.com/clustering-stopped` annotation.
Also, the `ClusterManager` does not stop even if the reconcile process is stopped.

The `ReconciliationActive` gets added to `.status.conditions` of MySQLCluster.
Normally `True` is recorded, but `False` is recorded when the reconciliation is stopped.

```yaml
status:
  conditions:
    - type: ReconciliationActive
      status: "False"
      lastTransitionTime: 2018-01-01T00:00:00Z
```

Also, `RECONCILE ACTIVE` will be added to the table displayed when you do `kubectl get mysqlcluster`.

```console
$ kubectl get mysqlcluster
NAME   AVAILABLE   HEALTHY   PRIMARY   SYNCED REPLICAS   ERRANT REPLICAS   CLUSTERING ACTIVE   RECONCILE ACTIVE   LAST BACKUP
test   Unknown     Unknown   0         2                                   True                False              <no value>
```

When you execute `kubectl moco start reconciliation <CLSUTER_NAME>`,
`moco.cybozu.com/reconciliation-stopped: "true"` is removed from `.metadata.annotation` of MySQLCluster.

The controller will resume the reconcile of MySQLCluster resources that do not have `moco.cybozu.com/clustering-stopped: "true"`.
If `ReconciliationStopped` in `.status.conditions` is `True` during this time, the controller will update `ReconciliationStopped` to `False`.

### Metrics

The following metrics are added to the moco-controller.

```
moco_cluster_clustering_stopped{name="mycluster", namespace="mynamesapce"} 1
moco_cluster_reconciliation_stopped{name="mycluster", namespace="mynamesapce"} 1
```

1 if the cluster is clustering or reconciliation stopped, 0 otherwise.
