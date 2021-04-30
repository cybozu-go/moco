# How to use MOCO

After [setting up MOCO](setup.md), you can create MySQL clusters with a custom resource called [MySQLCluster](crd_mysqlcluster.md).

- [Basics](#basics)
- [Limitations](#limitations)
  - [Errant replicas](#errant-replicas)
  - [Read-only primary](#read-only-primary)
- [Creating clusters](#creating-clusters)
  - [Creating an empty cluster](#creating-an-empty-cluster)
  - [Creating a cluster that replicates data from an external mysqld](#creating-a-cluster-that-replicates-data-from-an-external-mysqld)
  - [Bring your own image](#bring-your-own-image)
  - [Configurations](#configurations)
- [Using the cluster](#using-the-cluster)
  - [`kubectl moco`](#kubectl-moco)
  - [MySQL users](#mysql-users)
  - [Connecting to `mysqld` over network](#connecting-to-mysqld-over-network)
- [Deleting the cluster](#deleting-the-cluster)
- [Status, metrics, and logs](#status-metrics-and-logs)
  - [Cluster status](#cluster-status)
  - [Pod status](#pod-status)
  - [Metrics](#metrics)
  - [Logs](#logs)
- [Maintenance](#maintenance)
  - [Increasing the number of instances in the cluster](#increasing-the-number-of-instances-in-the-cluster)
  - [Switchover](#switchover)
  - [Failover](#failover)
  - [Upgrading mysql version](#upgrading-mysql-version)
  - [Re-initializing an errant replica](#re-initializing-an-errant-replica)

## Basics

MOCO creates a cluster of mysqld instances for each MySQLCluster.
A cluster can consists of 1, 3, or 5 mysqld instances.

MOCO configures [semi-synchronous][semisync] [GTID][]-based replication between mysqld instances in a cluster if the cluster size is 3 or 5.  A 3-instance cluster can tolerate up to 1 replica failure, and a 5-instance cluster can tolerate up to 2 replica failures.

In a cluster, there is only one instance called _primary_.  The primary instance is the source of truth.  It is the only writable instance in the cluster, and the source of the replication.  All other instances are called _replica_.  A replica is a read-only instance and replicates data from the primary.

## Limitations

### Errant replicas

An inherent limitation of GTID-based semi-synchronous replication is that a failed instance would have [errant transactions](https://www.percona.com/blog/2014/05/19/errant-transactions-major-hurdle-for-gtid-based-failover-in-mysql-5-6/).  If this happens, the instance needs to be re-created by removing all data.

MOCO does not re-create such an instance.  It only detects instances having errant transactions and excludes them from the cluster.  Users need to monitor them and re-create the instances.

### Read-only primary

MOCO from time to time sets the primary mysqld instance read-only for a switchover or other reasons.
Applications that use MOCO MySQL need to be aware of this.

## Creating clusters

### Creating an empty cluster

An empty cluster always has a writable instance called _the primary_.  All other instances are called _replicas_.  Replicas are read-only and replicate data from the primary.

The following YAML is to create a three-instance cluster.  It has an anti-affinity for Pods so that all instances will be scheduled to different Nodes.  It also has the same values for memory and CPU requests and limits making the Pod to have [Guaranteed](https://kubernetes.io/docs/tasks/configure-pod-container/quality-service-pod/) QoS.

```yaml
apiVersion: moco.cybozu.com/v1beta1
kind: MySQLCluster
metadata:
  namespace: default
  name: test
spec:
  # replicas is the number of mysqld Pods.  The default is 1.
  replicas: 3
  podTemplate:
    spec:
      affinity:
        podAntiAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
          - labelSelector:
              matchExpressions:
              - key: app.kubernetes.io/name
                operator: In
                values:
                - moco
              - key: app.kubernetes.io/instance
                operator: In
                values:
                - test
            topologyKey: "kubernetes.io/hostname"
      containers:
      # At least a container named "mysqld" must be defined.
      - name: mysqld
        image: quay.io/cybozu/moco-mysql:8.0.24
        resources:
          requests:
            cpu: "10"
            memory: "10Gi"
          limits:
            cpu: "10"
            memory: "10Gi"
  volumeClaimTemplates:
  # At least a PVC named "mysql-data" must be defined.
  - metadata:
      name: mysql-data
    spec:
      accessModes: [ "ReadWriteOnce" ]
      resources:
        requests:
          storage: 1Gi
```

There are other example manifests in [`examples`](../examples/) directory.

The complete reference of MySQLCluster is [`crd_mysqlcluster.md`](crd_mysqlcluster.md).

### Creating a cluster that replicates data from an external mysqld

Let's call the source mysqld instance _donor_.

We use [the clone plugin][CLONE] to copy the whole data quickly.
After the cloning, MOCO needs to create some user accounts and install plugins.

**On the donor**, you need to install the plugin and create two user accounts as follows:

```console
mysql> INSTALL PLUGIN clone SONAME mysql_clone.so;
mysql> CREATE USER 'clone-donor'@'%' IDENTIFIED BY 'xxxxxxxxxxx';
mysql> GRANT BACKUP_ADMIN, REPLICATION SLAVE ON *.* TO 'clone-donor'@'%';
mysql> CREATE USER 'clone-init'@'localhost' IDENTIFIED BY 'yyyyyyyyyyy';
mysql> GRANT ALL ON *.* TO 'clone-init'@'localhost' WITH GRANT OPTION;
```

You may change the user names and should change their passwords.

Then create a Secret in the same namespace as MySQLCluster:

```console
$ kubectl -n <namespace> create secret generic donor-secret \
    --from-literal=HOST=<donor-host> \
    --from-literal=PORT=<donor-port> \
    --from-literal=USER=clone-donor \
    --from-literal=PASSWORD=xxxxxxxxxxx \
    --from-literal=INIT_USER=clone-init \
    --from-literal=INIT_PASSWORD=yyyyyyyyyyy
```

You may change the secret name.

Finally, create MySQLCluster with `spec.replicationSourceSecretName` set to the Secret name as follows.
The mysql image must be the same version as the donor's.

```yaml
apiVersion: moco.cybozu.com/v1beta1
kind: MySQLCluster
metadata:
  namespace: foo
  name: test
spec:
  replicationSourceSecretName: donor-secret
  podTemplate:
    spec:
      containers:
      - name: mysqld
        image: quay.io/cybozu/moco-mysql:8.0.24  # must be the same version as the donor
  volumeClaimTemplates:
  - metadata:
      name: mysql-data
    spec:
      accessModes: [ "ReadWriteOnce" ]
      resources:
        requests:
          storage: 1Gi
```

To stop the replication from the donor, update MySQLCluster with `spec.replicationSourceSecretName: null`.

### Bring your own image

We provide a pre-built MySQL container image at [quay.io/cybozu/moco-mysql](http://quay.io/cybozu/moco-mysql).
If you want to build and use your own image, read [`custom-mysqld.md`](custom-mysqld.md).

### Configurations

The configuration values for `mysqld` is available on [pkg.go.dev](https://pkg.go.dev/github.com/cybozu-go/moco/pkg/mycnf#pkg-constants).  The settings in `ConstMycnf` cannot be changed while the settings in `DefaultMycnf` can be overridden.

To change some of the default values or to set a new option value, create a ConfigMap in the same namespace as MySQLCluster like this.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  namespace: foo
  name: mycnf
data:
  long_query_time: "5"
  innodb_buffer_pool_size: "10G"
```

and set the name of the ConfigMap in MySQLCluster as follows:

```yaml
apiVersion: moco.cybozu.com/v1beta1
kind: MySQLCluster
metadata:
  namespace: foo
  name: test
spec:
  mysqlConfigMapName: mycnf
  ...
```

If `innodb_buffer_pool_size` is not given, MOCO sets it automatically to 70% of the value of `resources.requests.memory` (or `resources.limits.memory`) for `mysqld` container.

## Using the cluster

### `kubectl moco`

From outside of your Kubernetes cluster, you can access MOCO MySQL instances using `kubectl-moco`.
`kubectl-moco` is [a plugin for `kubectl`](https://kubernetes.io/docs/tasks/extend-kubectl/kubectl-plugins/).
Pre-built binaries are available on [GitHub releases](https://github.com/cybozu-go/moco/releases/latest).

The following is an example to run `mysql` command interactively to access the primary instance of `test` MySQLCluster in `foo` namespace.

```console
$ kubectl moco -n foo mysql -it test
```

Read [the reference manual of `kubectl-moco`](kubectl-moco.md) for further details and examples.

### MySQL users

MOCO prepares a set of users.

- `moco-readonly` can read all tables of all databases.
- `moco-writable` can create users, databases, or tables.
- `moco-admin` is the super user.

The exact privileges that `moco-readonly` has are:

- PROCESS
- REPLICATION CLIENT
- REPLICATION SLAVE
- SELECT
- SHOW DATABASES
- SHOW VIEW

The exact privileges that `moco-writable` has are:

- ALTER
- ALTER ROUTINE
- CREATE
- CREATE ROLE
- CREATE ROUTINE
- CREATE TEMPORARY TABLES
- CREATE USER
- CREATE VIEW
- DELETE
- DROP
- DROP ROLE
- EVENT
- EXECUTE
- INDEX
- INSERT
- LOCK TABLES
- PROCESS
- REFERENCES
- REPLICATION CLIENT
- REPLICATION SLAVE
- SELECT
- SHOW DATABASES
- SHOW VIEW
- TRIGGER
- UPDATE

`moco-writable` cannot edit tables in `mysql` database, though.

You can create other users and grant them certain privileges as either `moco-writable` or `moco-admin`.

```console
$ kubectl moco mysql -u moco-writable test -- -e "CREATE USER 'foo'@'%' IDENTIFIED BY 'bar'"
$ kubectl moco mysql -u moco-writable test -- -e "CREATE DATABASE db1"
$ kubectl moco mysql -u moco-writable test -- -e "GRANT ALL ON db1.* TO 'foo'@'%'"
```

### Connecting to `mysqld` over network

MOCO prepares two Services for each MySQLCluster.
For example, a MySQLCluster named `test` in `foo` Namespace has the following Services.

| Service Name        | DNS Name                    | Description                      |
| ------------------- | --------------------------- | -------------------------------- |
| `moco-test-primary` | `moco-test-primary.foo.svc` | Connect to the primary instance. |
| `moco-test-replica` | `moco-test-replica.foo.svc` | Connect to replica instances.    |

`moco-test-replica` can be used only for read access.

The type of these Services is usually ClusterIP.
The following is an example to change Service type to LoadBalancer and add an annotation for [MetalLB][].

```yaml
apiVersion: moco.cybozu.com/v1beta1
kind: MySQLCluster
metadata:
  namespace: foo
  name: test
spec:
  serviceTemplate:
    metadata:
      annotations:
        metallb.universe.tf/address-pool: production-public-ips
    spec:
      type: LoadBalancer
...
```

## Deleting the cluster

By deleting MySQLCluster, all resources **including PersistentVolumeClaims** generated from the templates are automatically removed.

If you want to keep the PersistentVolumeClaims, remove `metadata.ownerReferences` from them before you delete a MySQLCluster.

## Status, metrics, and logs

### Cluster status

You can see the health and availability status of MySQLCluster as follows:

```console
$ kubectl get mysqlcluster
NAME   AVAILABLE   HEALTHY   PRIMARY   SYNCED REPLICAS   ERRANT REPLICAS
test   True        True      0         3
```

- The cluster is available when the primary Pod is running and ready.
- The cluster is healthy when there is no problems.
- `PRIMARY` is the index of the current primary instance Pod.
- `SYNCED REPLICAS` is the number of ready Pods.
- `ERRANT REPLICAS` is the number of instances having errant transactions.

You can also use `kubectl describe mysqlcluster` to see the recent events on the cluster.

### Pod status

MOCO adds mysqld containers a liveness probe and a readiness probe to check the replication status in addition to the process status.

A replica Pod is _ready_ only when it is replicating data from the primary without a significant delay.
The default threshold of the delay is 60 seconds.  The threshold can be configured as follows.

```yaml
apiVersion: moco.cybozu.com/v1beta1
kind: MySQLCluster
metadata:
  namespace: foo
  name: test
spec:
  maxDelaySeconds: 180
  ...
```

Unready replica Pods are automatically excluded from the load-balancing targets so that users will not see too old  data.

### Metrics

See [`metrics.md`](metrics.md) for available metrics in Prometheus format.

### Logs

Error logs from `mysqld` can be viewed as follows:

```console
$ kubectl logs moco-test-0 mysqld
```

Slow logs from `mysqld` can be viewed as follows:

```console
$ kubectl logs moco-test-0 slow-log
```

## Maintenance

### Increasing the number of instances in the cluster

Edit `spec.replicas` field of MySQLCluster:

```yaml
apiVersion: moco.cybozu.com/v1beta1
kind: MySQLCluster
metadata:
  namespace: foo
  name: test
spec:
  replicas: 5
  ...
```

You can only increase the number of instances in a MySQLCluster from 1 to 3 or 5, or from 3 to 5.
Decreasing the number of instances is not allowed.

### Switchover

Switchver is an operation to change the live primary to one of the replicas.

MOCO automatically switch the primary when the Pod of the primary instance is to be deleted.

Users can manually trigger a switchover with `kubectl moco switchover CLUSTER_NAME`.
Read [`kubectl-moco.md`](kubectl-moco.md) for details.

### Failover

Failover is an operation to replace the dead primary with the most advanced replica.
MOCO automatically does this as soon as it detects that the primary is down.

The most advanced replica is a replica who has retrieved the most up-to-date transaction from the dead primary.
Since MOCO configures loss-less semi-synchronous replication, the failover is guaranteed not to lose any user data.

After a failover, the old primary may become an errant replica [as described](#errant-replicas).

### Upgrading mysql version

You can upgrade the MySQL version of a MySQL cluster as follows:

1. Check that the cluster is healthy.
2. Check release notes of MySQL for any incompatibilities between the current and the new versions.
3. Edit the Pod template of the MySQLCluster and update `mysqld` container image:

```yaml
apiVersion: moco.cybozu.com/v1beta1
kind: MySQLCluster
metadata:
  namespace: default
  name: test
spec:
      containers:
      - name: mysqld
        # Edit the next line
        image: quay.io/cybozu/moco-mysql:8.0.24
```

You are advised to make backups and/or create a replica cluster before starting the upgrade process.
Read [`upgrading.md`](upgrading.md) for further details.

### Re-initializing an errant replica

Delete the PVC and Pod of the errant replica, like this:

```console
$ kubectl delete --wait=false pvc mysql-data-moco-test-0
$ kubectl delete --grace-period=1 pods moco-test-0
```

Depending on your Kubernetes version, StatefulSet controller may create a pending Pod before PVC gets deleted.
Delete such pending Pods until PVC is actually removed.

[semisync]: https://dev.mysql.com/doc/refman/8.0/en/replication-semisync.html
[GTID]: https://dev.mysql.com/doc/refman/8.0/en/replication-gtids.html
[CLONE]: https://dev.mysql.com/doc/refman/8.0/en/clone-plugin.html
[MetalLB]: https://metallb.universe.tf/
