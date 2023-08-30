# Enable Pause and Resume of MySQL Cluster Clustering

## Context

There is a user need to align the `gtid_executed` of all replicas manually
by doing replication from each Pod for consistency checks between Pods in the MySQL Cluster.
In this process, it is necessary to stop the replica's sql thread and manually advance the gtid,
but there is no function to stop clustering in the current MOCO and MOCO starts replication automatically.
Therefore, we are implementing a function to temporarily stop MOCO's clustering monitoring.

## Goals

* Enable pause and resume of MySQL Cluster clustering
* Operational with the `kubectl moco` command
* Check the presence or absence of a pause with `kubectl get mysqlcluster`

## Non-goals

* Changes to any MySQLCluster field will not be processed while paused
  * Reconciliation loop will be stopped
* Recovery is not guaranteed in the event that the MySQL Cluster is damaged by user operations when resuming from a pause

## ActualDesign

Users can stop the MySQL Cluster clustering with the following command:

```console
$ kubectl moco clustering stop <CLSUTER_NAME>
```

To resume clustering, use the following command:

```console
$ kubectl moco clustering start <CLSUTER_NAME>
```

When you execute `kubectl moco clustering stop <CLSUTER_NAME>`,
the `moco.cybozu.com/clustering-stopped: "true"` is added to the `.metadata.annotation` of MySQLCluster.

```yaml
metadata:
  annotations:
    moco.cybozu.com/clustering-stopped: "true"
```

A MySQLCluster with this annotation will not undergo reconcile.
Also, the MySQL clustering is stopped because the `ClusterManager` is stopped.

```go
r.ClusterManager.Stop(req.NamespacedName)
```

The process of stopping the `ClusterManager` is carried out only at the first reconcile
after the execution of `kubectl moco clustering stop <CLSUTER_NAME>`.

The `ClusteringStopped` gets added to `.status.conditions` of MySQLCluster.
The time when the `ClusterManager` was stopped is recorded in `.lastTransitionTime`.
It also updates the `.status` of `Available` and `Healthy` to `Unknown`.

```yaml
status:
  conditions:
    - type: ClusteringStopped
      status: "True"
      lastTransitionTime: 2018-01-01T00:00:00Z
    - type: Available
      status: "Unknown"
      lastTransitionTime: 2018-01-01T00:00:00Z
    - type: Healthy
      status: "Unknown"
      lastTransitionTime: 2018-01-01T00:00:00Z
```

Also, `STOPPED` will be added to the table displayed when you do `kubectl get mysqlcluster`.

```console
$ kubectl get mysqlcluster
NAME   AVAILABLE   HEALTHY   STOPPED   PRIMARY   SYNCED REPLICAS   ERRANT REPLICAS   LAST BACKUP
test   Unknown     Unknown   True      0         2                                   <no value>
```

When you execute `kubectl moco clustering start <CLSUTER_NAME>`,
`moco.cybozu.com/clustering-stopped: "true"` is removed from `.metadata.annotation` of MySQLCluster.

The controller resumes the reconcile process for `MySQLCluster` that does not have the `moco.cybozu.com/clustering-stopped: "true"` annotation.
When the `ClusterManager` resumes operation, the `.status` of `ClusteringStopped` in `.status.conditions` is updated to `False`,
and the `.status` of other `.status.conditions` is also updated from `Unknown`.

### Metrics

The following metrics are added to the moco-controller.

```
moco_cluster_clustering_stopped{name="mycluster", namespace="mynamesapce"} 1
```

1 if the cluster is clustering stopped, 0 otherwise.
