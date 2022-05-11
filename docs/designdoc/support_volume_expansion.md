# Support Volume Expansion Through MySQLCluster

## Context

Support PVC expansion in MOCO.

MOCO is using StatefulSet to build a MySQL cluster.
Currently, the StatefulSet PVC template is not editable.

Users can expand PVCs by performing manual operations:

> 1. Resize all PVCs manually
> 2. Edit MySQLCluster and change the requested volume size
> 3. Delete StatefulSet w/o deleting Pods
>     - `kubectl delete sts moco-xxx --cascade=false`
>
> ref: https://github.com/cybozu-go/moco/issues/265

The moco-controller supports this operation so that the user does not have to do it.

> :note: It may be possible to edit the StatefulSet directly in the future.
> 
> ref: https://github.com/kubernetes/enhancements/issues/661

## Goals

* Automatically extend the volume of PVCs used by MySQLCluster
* No breaking changes

## Non-goals

* No enhancement will be added to the `moco.cybozu.com/v1beta1` API.

## ActualDesign

moco-controller automatically resizes the PVC when the size of the MySQLCluster volume claim is extended.

The steps are as follows:

1. Checks if `allowVolumeExpansion` is `true` for the StorageClass specified in the PVC
2. Resize all PVCs
3. Delete only the StatefulSet without deleting the Pods
4. StatefulSet will be recreated with the new PVC size
5. (Optional) Restart the StatefulSet

The functions that must be performed to achieve this functionality are steps 1 through 4.
Step 5 is not necessary if the volume plugin supports [online file system expansion](https://kubernetes.io/blog/2018/07/12/resizing-persistent-volumes-using-kubernetes/#online-file-system-expansion).

### Validation

Currently, MOCO does not validate against `.spec.volumeClaimTemplates`.
This time add the following validate:

* Make volumeClaimTemplates invariant except for storage size
* Allow only incremental changes in storage size
  * If storage expansion is involved, the storage class `allowVolumeExpansion` is set to `true`.
  * Refer to the storage size in the StatefulSet to compare with the actual size.

### Resize PVCs

When moco-controller resizes a PVC, there may be a discrepancy between the PVC defined in the MySQLCluster and the actual PVC size.
For example, if you are using [github.com/topolvm/pvc-autoresizer](https://github.com/topolvm/pvc-autoresizer).
In this case, moco-controller will only update if the actual PVC size is smaller than the PVC size after the change.

Failure to update will result in a discrepancy between the PVC defined in the MySQLCluster and the actual PVC.
Add the condition `VolumeResized` to the MySQLCluster condition to signal this condition.

```yaml
kind: MySQLCluster
status:
  conditions:
    - lastTransitionTime: "2022-05-01T16:29:24Z"
      status: "False"
      type: VolumeResized
      message: "Validation failed: ..."
```

Set the `VolumeResized` condition's status to `True` if the PVC expansion was successful.

### Restart the StatefulSet

If the volume plugin you are using does not support [online file system expansion](https://kubernetes.io/blog/2018/07/12/resizing-persistent-volumes-using-kubernetes/#online-file-system-expansion),
you will need to restart the Pod to reflect the PVC expansion.

Online file system expansion is implemented in all major volume plugins. (Also supported by [TopoLVM](https://blog.kintone.io/entry/topolvm-release-0.4#Volume-expansion))
Also, the `ExpandInUsePersistentVolumes` feature gate is enabled by default starting with Kubernetes v1.15.

It would be possible for MOCO to support this feature, but since many plugins support online file system expansion, we do not consider it a high priority.
We will consider implementation when requested by users.
