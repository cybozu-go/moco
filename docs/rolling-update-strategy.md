# RollingUpdate strategy

MOCO manages MySQLCluster pods using StatefulSets.

```text
MySQLCluster/test
└─StatefulSet/moco-test
  ├─ControllerRevision/moco-test-554c56f456
  ├─ControllerRevision/moco-test-5794c57c7c
  ├─Pod/moco-test-0
  ├─Pod/moco-test-1
  └─Pod/moco-test-2
```

By default, StatefulSet's standard rolling update does not consider whether MySQLCluster is Healthy during pod updates.
This can sometimes cause problems, as a rolling update may proceed even if MySQLCluster becomes UnHealthy during the process.

To address this issue, MOCO controls StatefulSet partitions to perform rolling updates. This behavior is enabled by default.

## Partitions

By setting a number in `.spec.updateStrategy.rollingUpdate.partition` of a StatefulSet, you can divide the rolling update into partitions.
When a partition is specified, pods with a pod number equal to or greater than the partition value are updated.
Pods with a pod number smaller than the partition value are not updated, and even if those pods are deleted, they will be recreated with the previous version.

## Architecture

### When Creating a StatefulSet

When creating a StatefulSet, MOCO updates the partition of the StatefulSet to the same value as the replica using MutatingAdmissionWebhook.

### When Updating a StatefulSet

When a StatefulSet is updated, MOCO determines the contents of the StatefulSet update and controls partitions using AdmissionWebhook.

1. If the StatefulSet update is only the partition number
    * The MutatingAdmissionWebhook does nothing.
2. If fields other than the partition of the StatefulSet are updated
    * The MutatingAdmissionWebhook updates the partition of the StatefulSet to the same value as the replica using MutatingAdmissionWebhook.

    ```yaml
    replicas: 3
    ...
    updateStrategy:
      type: RollingUpdate
      rollingUpdate:
        partition: 3
    ...
    ```

### Updating Partitions

MOCO monitors the rollout status of the StatefulSet and the status of MySQLCluster.
If the update of pods based on the current partition value is completed successfully and the containers are Running, and the status of MySQLCluster is Healthy, MOCO decrements the partition of the StatefulSet by 1.
This operation is repeated until the partition value reaches 0.

### Forcefully Rolling Out

By setting the annotation `moco.cybozu.com/force-rolling-update` to `true`, you can update the StatefulSet without partition control.

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: MySQLCluster
metadata:
  namespace: default
  name: test
  annotations:
    moco.cybozu.com/force-rolling-update: "true"
...
```

When creating or updating a StatefulSet with the annotation `moco.cybozu.com/force-rolling-update` set, MOCO deletes the partition setting using MutatingAdmissionWebhook.

### Metrics

MOCO outputs the following metrics related to rolling updates:

* `moco_cluster_current_replicas`
  * The same as `.status.currentReplicas` of the StatefulSet.
* `moco_cluster_updated_replicas`
  * The same as `.status.updatedReplicas` of the StatefulSet.
* `moco_cluster_last_partition_updated`
  * The time the partition was last updated.

By setting an alert with the condition that `moco_cluster_updated_replicas` is not equal to `moco_cluster_replicas` and a certain amount of time has passed since `moco_cluster_last_partition_updated`, you can detect MySQLClusters where the rolling update is stopped.
