# Change the volumeClaimTemplates

MOCO supports MySQLCluster `.spec.volumeClaimTemplates` changes.

When `.spec.volumeClaimTemplates` is changed, moco-controller will try to recreate the StatefulSet.
This is because modification of `volumeClaimTemplates` in StatefulSet is currently not allowed.

Re-creation StatefulSet is done with the same behavior as `kubectl delete sts moco-xxx --cascade=orphan`, without removing the working Pod.

> NOTE: It may be possible to edit the StatefulSet directly in the future.
>
> ref: https://github.com/kubernetes/enhancements/issues/661

When re-creating a StatefulSet, moco-controller supports no operation except for volume expansion as described below.
It simply re-creates the StatefulSet.
However, by specifying the `--pvc-sync-annotation-keys` and `--pvc-sync-label-keys` flags in the controller, you can designate the annotations and labels to be synchronized from `.spec.volumeClaimTemplates` to PVC during the recreation of the StatefulSet.

For all other labels and annotations, given the potential side effects, such updates must be performed by the user themselves.
This guideline is essential to prevent potential side-effects if entities other than the moco-controller are manipulating the PVC's metadata.

### Metrics

The success or failure of the re-creating a StatefulSet is notified to the user in the following metrics:

```text
moco_cluster_statefulset_recreate_total{name="mycluster", namespace="mynamesapce"} 3
moco_cluster_statefulset_recreate_errors_total{name="mycluster", namespace="mynamesapce"} 1
```

If a StatefulSet fails to recreate, the metrics in `moco_cluster_statefulset_recreate_errors_total` is incremented after each reconcile,
so users can notice anomalies by monitoring this metrics.

See the [metrics documentation](./metrics.md) for more details.

## Volume expansion

moco-controller automatically resizes the PVC when the size of the MySQLCluster volume claim is extended.
If the volume plugin supports [online file system expansion](https://kubernetes.io/blog/2018/07/12/resizing-persistent-volumes-using-kubernetes/#online-file-system-expansion),
the PVs used by the Pod will be expanded online.

If volume is to be expanded, `.allowVolumeExpansion` of the StorageClass must be `true`.
moco-controller will validate with the admission webhook and reject the request if volume expansion is not allowed.

If the volume plugin does not support online file system expansion,
the Pod must be restarted for the volume expansion to reflect.
This must be done manually by the user.

When moco-controller resizes a PVC, there may be a discrepancy between the PVC defined in the MySQLCluster and the actual PVC size.
For example, if you are using [github.com/topolvm/pvc-autoresizer](https://github.com/topolvm/pvc-autoresizer).
In this case, moco-controller will only update if the actual PVC size is smaller than the PVC size after the change.

### Metrics

The success or failure of the PVC resizing is notified to the user in the following metrics:

```text
moco_cluster_volume_resized_total{name="mycluster", namespace="mynamesapce"} 4
moco_cluster_volume_resized_errors_total{name="mycluster", namespace="mynamesapce"} 1
```

This metrics is incremented if the volume size change succeeds or fails.
If fails to volume size changed, the metrics in `moco_cluster_volume_resized_errors_total` is incremented after each reconcile,
so users can notice anomalies by monitoring this metrics.

See the [metrics documentation](./metrics.md) for more details.

## Volume reduction

MOCO supports PVC reduction, but unlike PVC expansion, the user must perform the operation manually.

The steps are as follows:

1. The user modifies the `.spec.volumeClaimTemplates` of the MySQLCluster and sets a smaller volume size.
2. MOCO updates the `.spec.volumeClaimTemplates` of the StatefulSet. This does not propagate to existing Pods, PVCs, or PVs.
3. The user manually deletes the MySQL Pod & PVC.
4. Wait for the Pod & PVC to be recreated by the statefulset-controller, and for MOCO to clone the data.
5. Once the cluster becomes Healthy, the user deletes the next Pod and PVC.
6. It is completed when all Pods and PVCs are recreated.

### 1. The user modifies the `.spec.volumeClaimTemplates` of the MySQLCluster and sets a smaller volume size

For example, the user modifies the `.spec.volumeClaimTemplates` of the MySQLCluster as follows:

```diff
  apiVersion: moco.cybozu.com/v1beta2
  kind: MySQLCluster
  metadata:
    namespace: default
    name: test
  spec:
    replicas: 3
    podTemplate:
      spec:
        containers:
        - name: mysqld
          image: ghcr.io/cybozu-go/moco/mysql:8.4.4
    volumeClaimTemplates:
    - metadata:
        name: mysql-data
      spec:
        accessModes: [ "ReadWriteOnce" ]
        resources:
          requests:
-           storage: 1Gi
+           storage: 500Mi
```

### 2. MOCO updates the `.spec.volumeClaimTemplates` of the StatefulSet. This does not propagate to existing Pods, PVCs, or PVs

The moco-controller will update the `.spec.volumeClaimTemplates` of the StatefulSet.
The actual modification of the StatefulSet's `.spec.volumeClaimTemplates` is not allowed,
so this change is achieved by recreating the StatefulSet.
At this time, only the recreation of StatefulSet is performed, without deleting the Pods and PVCs.

### 3. The user manually deletes the MySQL Pod & PVC

The user manually deletes the PVC and Pod.
Use the following command to delete them:

```console
$ kubectl delete --wait=false pvc <pvc-name>
$ kubectl delete --grace-period=1 <pod-name>
```

### 4. Wait for the Pod & PVC to be recreated by the statefulset-controller, and for MOCO to clone the data

The statefulset-controller recreates Pods and PVCs, creating a new PVC with a reduced size.
Once the MOCO successfully starts a Pod, it begins cloning the data.

```console
$ kubectl get mysqlcluster,po,pvc
NAME                                AVAILABLE   HEALTHY   PRIMARY   SYNCED REPLICAS   ERRANT REPLICAS   LAST BACKUP
mysqlcluster.moco.cybozu.com/test   True        False     0         2                                   <no value>

NAME              READY   STATUS     RESTARTS   AGE
pod/moco-test-0   3/3     Running    0          2m14s
pod/moco-test-1   3/3     Running    0          114s
pod/moco-test-2   0/3     Init:1/2   0          7s

NAME                                           STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS   AGE
persistentvolumeclaim/mysql-data-moco-test-0   Bound    pvc-03c73525-0d6d-49de-b68a-f8af4c4c7faa   1Gi        RWO            standard       2m14s
persistentvolumeclaim/mysql-data-moco-test-1   Bound    pvc-73c26baa-3432-4c85-b5b6-875ffd2456d9   1Gi        RWO            standard       114s
persistentvolumeclaim/mysql-data-moco-test-2   Bound    pvc-779b5b3c-3efc-4048-a549-a4bd2d74ed4e   500Mi      RWO            standard       7s
```

### 5. Once the cluster becomes Healthy, the user deletes the next Pod and PVC

The user waits until the MySQLCluster state becomes Healthy, and then deletes the next Pod and PVC.

```console
$ kubectl get mysqlcluster
NAME                                AVAILABLE   HEALTHY   PRIMARY   SYNCED REPLICAS   ERRANT REPLICAS   LAST BACKUP
mysqlcluster.moco.cybozu.com/test   True        True      1         3                                   <no value>
```

### 6. It is completed when all Pods and PVCs are recreated

Repeat steps 3 to 5 until all Pods and PVCs are recreated.

## References

* [Design document](./designdoc/support_apply_pvc_template_changes.md)
