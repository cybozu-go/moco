# Support for Reducing Volume Size

## Context

Support for reducing volume size.

Currently, MOCO supports [volume expansion](./support_apply_pvc_template_changes.md), but not volume reduction.
The reduction of the size of a Persistent Volume (PV) or Persistent Volume Claim (PVC) is not endorsed as a fundamental principle of Kubernetes.

However, there may be situations where users may want to reduce the volume size depending on their use cases.
In this proposal, we are considering the inclusion of a feature that allows users to reduce the volume size in MySQL clusters created by MOCO.

## Goals

* Support for reducing volume size

## Non-goals

* MOCO will not provide full automation of volume reduction

## Design

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
          image: ghcr.io/cybozu-go/moco/mysql:8.0.30
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

In the current implementation of MOCO, requests to reduce the volume size are denied by the Admission Webhook.
The restriction will be relaxed during the implementation of this proposal.

### 2. MOCO updates the `.spec.volumeClaimTemplates` of the StatefulSet. This does not propagate to existing Pods, PVCs, or PVs

The moco-controller will update the `.spec.volumeClaimTemplates` of the StatefulSet.
The actual modification of the StatefulSet's `.spec.volumeClaimTemplates` is not allowed,
so this change is achieved by recreating the StatefulSet.
During this operation, use the `--cascade=orphan` option to ensure the Pods and PVCs aren't deleted.

It is performed as follows:

```console
$ kubectl delete statefulset <statefulset-name> --cascade=orphan
```

Subsequently, the moco-controller recreates the StatefulSet with the new `.spec.volumeClaimTemplates`.

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

### Metrics

As with volume expansion, the number of times a StatefulSet is recreated and the number of errors are provided as metrics.

```text
moco_cluster_statefulset_recreate_total{name="mycluster", namespace="mynamesapce"} 3
moco_cluster_statefulset_recreate_errors_total{name="mycluster", namespace="mynamesapce"} 1
```

If a StatefulSet fails to recreate, the metrics in `moco_cluster_statefulset_recreate_errors_total` is incremented after each reconcile,
so users can notice anomalies by monitoring this metrics.
