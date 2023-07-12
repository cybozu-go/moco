# Support apply PVC template changes to StatefulSet

## Context

Support apply PVC changes.

MOCO is using StatefulSet to build a MySQL cluster.
Currently, the StatefulSet PVC template is not editable.

Users can apply PVCs changes by performing manual operations:

> 1. Edit all PVCs manually
> 2. Edit MySQLCluster and change the PVC template.
> 3. Delete StatefulSet w/o deleting Pods
>     - `kubectl delete sts moco-xxx --cascade=orphan`
>
> ref: https://github.com/cybozu-go/moco/issues/265

The moco-controller supports this operation so that the user does not have to do it.

> :note: It may be possible to edit the StatefulSet directly in the future.
> 
> ref: https://github.com/kubernetes/enhancements/issues/661

## Goals

* Support apply PVC template changes to StatefulSet
* Automatically extend the volume of PVCs used by MySQLCluster
* No breaking changes

## Non-goals

* No enhancement will be added to the `moco.cybozu.com/v1beta1` API
* moco-controller does not automatically restart StatefulSet

## ActualDesign

moco-controller re-creates StatefulSet when `.spec.volumeClaimTemplates` in MySQLCluster is changed.
This is done with the same behavior as `kubectl delete sts moco-xxx --cascade=orphan`, without removing the working Pod.

When re-creating a StatefulSet, moco-controller does not support any operation other than volume expansion as described below.
It only re-creates the StatefulSet.
Therefore, changes to the label and annotation assigned to the PVC must be done by the user.
This is because there is a concern that if someone other than the moco-controller is editing the PVC's metadata, there may be side effects.

### Volume expansion

moco-controller automatically resizes the PVC when the size of the MySQLCluster volume claim is extended.

The steps are as follows:

1. Change the size of the MySQLCluster volume claim
2. Resize all PVCs
3. Delete only the StatefulSet without deleting the Pods
4. StatefulSet will be recreated with the new PVC size

If the volume plugin supports [online file system expansion](https://kubernetes.io/blog/2018/07/12/resizing-persistent-volumes-using-kubernetes/#online-file-system-expansion),
the PVs used by the Pod will be expanded online.

If the volume plugin does not support online file system expansion,
the Pod must be restarted for the volume expansion to reflect.
This must be done manually by the user.

Online file system expansion is implemented in all major volume plugins. (Also supported by [TopoLVM](https://blog.kintone.io/entry/topolvm-release-0.4#Volume-expansion))
Also, the `ExpandInUsePersistentVolumes` feature gate is enabled by default starting with Kubernetes v1.15.

When moco-controller resizes a PVC, there may be a discrepancy between the PVC defined in the MySQLCluster and the actual PVC size.
For example, if you are using [github.com/topolvm/pvc-autoresizer](https://github.com/topolvm/pvc-autoresizer).
In this case, moco-controller will only update if the actual PVC size is smaller than the PVC size after the change.

If the PVC update fails due to the PVC size decreasing,
the moco-controller will keep trying to update the PVC every time reconcile is executed and will keep outputting a failure log.
Since this condition will not resolve itself naturally, we will add metrics to notify the user.

Export the metrics:

```text
moco_cluster_volume_resized_total{name="mycluster", namespace="mynamesapce"} 4
moco_cluster_volume_resized_errors_total{name="mycluster", namespace="mynamesapce"} 1
```

This metrics is incremented if the volume size change succeeds or fails.
If fails to volume size changed, the metrics in `moco_cluster_volume_resized_errors_total` is incremented after each reconcile,
so users can notice anomalies by monitoring this metrics.

### Validation

Currently, MOCO does not validate against `.spec.volumeClaimTemplates`.
This time add the following validate:

* Allow only incremental changes in storage size

> :note: This will become unnecessary due to the support of volume size reduction in the following proposal.
>
> [Support for Reducing Volume Size](./support_reduce_volume_size.md)

### Metrics

When recreating a StatefulSet, there may be cases where recreating the StatefulSet fails with a validation error.
In this case, the Pod continues to run but there is no StatefulSet,
so there is a risk of unreliability if the Pod is terminated for some reason.

The following metrics are exported to notify users of this status:

```text
moco_cluster_statefulset_recreate_total{name="mycluster", namespace="mynamesapce"} 3
moco_cluster_statefulset_recreate_errors_total{name="mycluster", namespace="mynamesapce"} 1
```

If a StatefulSet fails to recreate, the metrics in `moco_cluster_statefulset_recreate_errors_total` is incremented after each reconcile,
so users can notice anomalies by monitoring this metrics.
