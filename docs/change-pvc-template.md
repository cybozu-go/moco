# Change the volumeClaimTemplates

MOCO supports MySQLCluster `.spec.volumeClaimTemplates` changes.

When `.spec.volumeClaimTemplates` is changed, moco-controller will try to recreate the StatefulSet.
This is because modification of `volumeClaimTemplates` in StatefulSet is currently not allowed.

Re-creation StatefulSet is done with the same behavior as `kubectl delete sts moco-xxx --cascade=orphan`, without removing the working Pod.

> NOTE: It may be possible to edit the StatefulSet directly in the future.
>
> ref: https://github.com/kubernetes/enhancements/issues/661

When re-creating a StatefulSet, moco-controller does not support any operation other than volume expansion as described below.
It only re-creates the StatefulSet.
Therefore, changes to the label and annotation assigned to the PVC must be done by the user.
This is because there is a concern that if someone other than the moco-controller is editing the PVC's metadata, there may be side effects.

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

## References

* [Design document](./designdoc/support_apply_pvc_template_changes.md)
