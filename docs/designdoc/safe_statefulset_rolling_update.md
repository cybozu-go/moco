# Safe Rolling Update of StatefulSet

## Context

In the current specification of MOCO, the following problems have occurred in the past.

* Due to a node failure, one MySQL Pod stopped working, and the MySQLCluster became Unhealthy.
* In this state, the MySQLCluster was updated, and the StatefulSet's rolling update was executed.
* Due to the failure and update, two MySQL Pods went down at the same time, and the MySQLCluster became Unavailable.
* At this time, PDB was set. However, because the StatefulSet update does not consider the PDB or the state of the existing Pods, the MySQL Pod was updated even though the cluster was Unhealthy.

To prevent such problems, MOCO will implement a function to safely update the StatefulSet.

## Goals

* Update Pods considering the state of MySQLCluster when updating StatefulSet
* Do not involve destructive changes

## ActualDesign

When updating the StatefulSet of MySQLCluster, set `.spec.updateStrategy.rollingUpdate.partition` of StatefulSet dynamically and update Pods considering the state of MySQLCluster.

Partition is a parameter to divide the update of StatefulSet's Pods.
If you specify `.partition`, updates will be made only for StatefulSet Pods with a number equal to or greater than the specified number.
If `.partition` is larger than `.spec.replicas`, updates to `.spec.template` will not propagate to that Pod.
By using this function, you can roll out the update of StatefulSet's Pods in stages.

```yaml
updateStrategy:
  type: RollingUpdate
  rollingUpdate:
    partition: 3
```

In MOCO, `updateStrategy` of the StatefulSet of the created MySQLCluster is fixed to `type: RollingUpdate`.
This document proposes to add a reconciler that dynamically sets `.partition` to realize safe updating of StatefulSet.

### When creating MySQLCluster

Until MySQLCluster successfully starts for the first time, applies StatefulSet without setting `.partition`.
This is done to prevent a situation where a user-corrected StatefulSet, due to misconfigurations like image issues, does not roll out due to partition.

### When updating MySQLCluster

When MySQLCluster is updated, set the same value as `.spec.replicas` to `.partition` of StatefulSet in MutatingWebhook.
At this time, the following conditions must be met:

1. `Initialized` in `.status.conditions` of MySQLCluster is `True`
2. If the update difference of StatefulSet is only `.partition`, do not update `.partition`

#### `Initialized` in `.status.conditions` of MySQLCluster is `True`

This check is done to confirm whether the Pod of MySQLCluster is running normally.

```yaml
status:
  conditions:
  - lastTransitionTime: "2024-02-07T23:59:52Z"
    message: the current state is Healthy
    reason: Healthy
    status: "True"
    type: Initialized
```

`Initialized` indicates that the cluster state of MySQLCluster is not Cloning.
If `Initialized` is `True`, it means Pods under StatefulSet have started successfully.
If the Pod spec is incorrect, `Initialized` will not become `True`, and `.partition` will not be set in StatefulSet.
When applying StatefulSet, the MutatingWebhook checks if `Initialized` is `True` and sets `.partition` only in that case.

#### If the update difference of StatefulSet is only `.partition`, do not update `.partition`

The update of `.partition` of StatefulSet will be performed by the reconciler described later.
At this time, do not overwrite `.partition` in MutatingWebhook.

### Checking the state of StatefulSet when performing rolling updates

The new reconciler will subtract `.partition` of StatefulSet.
This reconciler only updates `.partition` for StatefulSet.
The reconciler watches the state of StatefulSet and MySQLCluster and determines the timing of subtracting `.partition`.

If the following conditions are met, the reconciler will subtract one from `.partition`.

1. `Healthy` in `.status.conditions` of MySQLCluster is `True`
2. `.status.currentRevision` and `.status.updateRevision` of StatefulSet are different

Until `.partition` becomes 0, the reconciler will continue to subtract from `.partition`.
When the subtraction is successful, the reconciler outputs it as an Event.

After confirming that the MySQLCluster is `Healthy`, you can safely perform a rolling update by subtracting the `.partition`.

### Forcing rolling updates

By adding the annotation `moco.cybozu.com/force-rolling-update: "true"` to MySQLCluster,
you can disable the staging of rolling updates using `.partition` of StatefulSet and force it to execute.

### Checking the state of rolling updates

Users can execute the following command to check the state of the rolling update.

```console
$ kubectl rollout status statefulset moco-test
partitioned roll out complete: 1 new pods have been updated...
```

When it is completed, the display will be as follows.

```console
$ kubectl rollout status statefulset moco-test
partitioned roll out complete: 4 new pods have been updated...
```

## Case-Specific Behavior

### When updating Pod template and replicas simultaneously

1. Pods scale out using the old template.
    * At this time, MySQLCluster's `Healthy` becomes `False`.

```console
$ kubectl get pod
NAME              READY   STATUS     RESTARTS   AGE
moco-test-0       3/3     Running    0          3m42s
moco-test-1       3/3     Running    0          3m42s
moco-test-2       3/3     Running    0          3m42s
moco-test-3       0/3     Init:1/2   0          6s
moco-test-4       0/3     Init:1/2   0          6s

$ kubectl get mysqlcluster
NAME   AVAILABLE   HEALTHY   PRIMARY   SYNCED REPLICAS   ERRANT REPLICAS   CLUSTERING ACTIVE   RECONCILE ACTIVE   LAST BACKUP
test   True        False     0         3                                   True                True               <no value>
```

2. Once MySQLCluster becomes `Healthy`, one Pod is updated at a time.

```console
$ kubectl rollout status statefulset moco-test
partitioned roll out complete: 1 new pods have been updated...
```

### Recovery method when rolling update is manually stopped due to the updated Pod failing to start

1. The updated Pod fails to start.
    * At this point, the moco-controller stops the update without reducing the `.partition` of StatefulSet.
    * Automatic recovery is not performed.

```console
$ kubectl get pod
NAME              READY   STATUS              RESTARTS   AGE
moco-test-0       3/3     Running             0          5m16s
moco-test-1       3/3     Running             0          6m59s
moco-test-2       3/3     Running             0          8m32s
moco-test-3       3/3     Running             0          10m
moco-test-4       0/3     Init:ErrImagePull   0          7s
```

2. The user corrects MySQLCluster's manifests and reapplies them.
    * At this point, the moco-controller sets `.partition` back to the same value as replicas when updating StatefulSet.

```console
$ kubectl edit mysqlcluster test
mysqlcluster.moco.cybozu.com/test edited
```

3. The user manually resumes the rollout.

```console
$ kubectl patch statefulset moco-test -p '{"spec":{"updateStrategy":{"type":"RollingUpdate","rollingUpdate":{"partition":4}}}}'

$ kubectl rollout status statefulset moco-test
Waiting for 1 pods to be ready...
```
