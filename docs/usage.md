# How to use MOCO

After [setting up MOCO](setup.md), you can create MySQL clusters with a custom resource called [MySQLCluster](crd_mysqlcluster_v1beta2.md).

- [Basics](#basics)
- [Limitations](#limitations)
  - [Errant replicas](#errant-replicas)
  - [Read-only primary](#read-only-primary)
- [Creating clusters](#creating-clusters)
  - [Creating an empty cluster](#creating-an-empty-cluster)
  - [Creating a cluster that replicates data from an external mysqld](#creating-a-cluster-that-replicates-data-from-an-external-mysqld)
  - [Bring your own image](#bring-your-own-image)
- [Configurations](#configurations)
  - [InnoDB buffer pool size](#innodb-buffer-pool-size)
  - [Opaque configuration](#opaque-configuration)
- [Using the cluster](#using-the-cluster)
  - [`kubectl moco`](#kubectl-moco)
  - [MySQL users](#mysql-users)
  - [Connecting to `mysqld` over network](#connecting-to-mysqld-over-network)
- [Backup and restore](#backup-and-restore)
  - [Object storage bucket](#object-storage-bucket)
  - [BackupPolicy](#backuppolicy)
  - [Credentials to access S3 bucket](#credentials-to-access-s3-bucket)
  - [Taking an emergency backup](#taking-an-emergency-backup)
  - [Restore](#restore)
  - [Further details](#further-details)
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

The following YAML is to create a three-instance cluster.  It has an anti-affinity for Pods so that all instances will be scheduled to different Nodes.  It also sets the limits for memory and CPU to make the Pod [Guaranteed](https://kubernetes.io/docs/tasks/configure-pod-container/quality-service-pod/).

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: MySQLCluster
metadata:
  namespace: default
  name: test
spec:
  # replicas is the number of mysqld Pods.  The default is 1.
  replicas: 3
  podTemplate:
    spec:
      # Make the data directory writable. If moco-init fails with "Permission denied", uncomment the following settings.
      # securityContext:
      #   fsGroup: 10000
      #   fsGroupChangePolicy: "OnRootMismatch"  # available since k8s 1.20
      affinity:
        podAntiAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
          - labelSelector:
              matchExpressions:
              - key: app.kubernetes.io/name
                operator: In
                values:
                - mysql
              - key: app.kubernetes.io/instance
                operator: In
                values:
                - test
            topologyKey: "kubernetes.io/hostname"
      containers:
      # At least a container named "mysqld" must be defined.
      - name: mysqld
        image: ghcr.io/cybozu-go/moco/mysql:8.0.35
        # By limiting CPU and memory, Pods will have Guaranteed QoS class.
        # requests can be omitted; it will be set to the same value as limits.
        resources:
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

By default, MOCO uses `preferredDuringSchedulingIgnoredDuringExecution` to prevent Pods from being placed on the same Node.

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: moco-<MYSQLCLSTER_NAME>
  namespace: default
...
spec:
  template:
    spec:
      affinity:
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
          - podAffinityTerm:
              labelSelector:
                matchExpressions:
                - key: app.kubernetes.io/name
                  operator: In
                  values:
                  - mysql
                - key: app.kubernetes.io/created-by
                  operator: In
                  values:
                  - moco
                - key: app.kubernetes.io/instance
                  operator: In
                  values:
                  - <MYSQLCLSTER_NAME>
              topologyKey: kubernetes.io/hostname
            weight: 100
...
```

There are other example manifests in [`examples`](https://github.com/cybozu-go/moco/tree/main/examples) directory.

The complete reference of MySQLCluster is [`crd_mysqlcluster_v1beta2.md`](crd_mysqlcluster_v1beta2.md).

### Creating a cluster that replicates data from an external mysqld

Let's call the source mysqld instance _donor_.

First, make sure `partial_revokes` is enabled **on the donor**; Replicating data from the donor with `partial_revokes` disabled will [result in replication inconsistencies or errors](https://dev.mysql.com/doc/refman/8.0/en/partial-revokes.html#partial-revokes-replication) since MOCO uses `partial_revokes` functionality.

We use [the clone plugin][CLONE] to copy the whole data quickly.
After the cloning, MOCO needs to create some user accounts and install plugins.

**On the donor**, you need to install the plugin and create two user accounts as follows:

```console
mysql> INSTALL PLUGIN clone SONAME 'mysql_clone.so';
mysql> CREATE USER 'clone-donor'@'%' IDENTIFIED BY 'xxxxxxxxxxx';
mysql> GRANT BACKUP_ADMIN, REPLICATION SLAVE ON *.* TO 'clone-donor'@'%';
mysql> CREATE USER 'clone-init'@'localhost' IDENTIFIED BY 'yyyyyyyyyyy';
mysql> GRANT ALL ON *.* TO 'clone-init'@'localhost' WITH GRANT OPTION;
mysql> GRANT PROXY ON ''@'' TO 'clone-init'@'localhost' WITH GRANT OPTION;
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
apiVersion: moco.cybozu.com/v1beta2
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
        image: ghcr.io/cybozu-go/moco/mysql:8.0.35  # must be the same version as the donor
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

We provide pre-built MySQL container images at [ghcr.io/cybozu-go/moco/mysql](https://github.com/cybozu-go/moco/pkgs/container/moco%2Fmysql).
If you want to build and use your own image, read [`custom-mysqld.md`](custom-mysqld.md).

## Configurations

The default and constant configuration values for `mysqld` are available on [pkg.go.dev](https://pkg.go.dev/github.com/cybozu-go/moco/pkg/mycnf#pkg-variables).
The settings in `ConstMycnf` cannot be changed while the settings in `DefaultMycnf` can be overridden.

You can change the default values or set undefined values by creating a ConfigMap in the same namespace as MySQLCluster, and setting `spec.mysqlConfigMapName` in MySQLCluster to the name of the ConfigMap as follows:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  namespace: foo
  name: mycnf
data:
  long_query_time: "5"
  innodb_buffer_pool_size: "10G"
---
apiVersion: moco.cybozu.com/v1beta2
kind: MySQLCluster
metadata:
  namespace: foo
  name: test
spec:
  # set this to the name of ConfigMap
  mysqlConfigMapName: mycnf
  ...
```

### InnoDB buffer pool size

If `innodb_buffer_pool_size` is not specified, MOCO sets it automatically to 70% of the value of `resources.requests.memory` (or `resources.limits.memory`) for `mysqld` container.

If both `resources.request.memory` and `resources.limits.memory` are not set, `innodb_buffer_pool_size` will be set to `128M`.

### Opaque configuration

Some configuration variables cannot be fully configured with ConfigMap values.
For example, [`--performance-schema-instrument`](https://dev.mysql.com/doc/refman/8.0/en/performance-schema-startup-configuration.html) needs to be specified multiple times.

You may set them through a special config key `_include`.
The value of `_include` will be included in `my.cnf` as opaque.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  namespace: foo
  name: mycnf
data:
  _include: |
    performance-schema-instrument='memory/%=ON'
    performance-schema-instrument='wait/synch/%/innodb/%=ON'
    performance-schema-instrument='wait/lock/table/sql/handler=OFF'
    performance-schema-instrument='wait/lock/metadata/sql/mdl=OFF'
```

Care must be taken not to overwrite critical configurations such as `log_bin` since MOCO does not check the contents from `_include`.

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
apiVersion: moco.cybozu.com/v1beta2
kind: MySQLCluster
metadata:
  namespace: foo
  name: test
spec:
  primaryServiceTemplate:
    metadata:
      annotations:
        metallb.universe.tf/address-pool: production-public-ips
    spec:
      type: LoadBalancer
...
```

## Backup and restore

MOCO can take full and incremental backups regularly.
The backup data are stored in [Amazon S3][S3] compatible object storages.

You can restore data from a backup to a new MySQL cluster.

### Object storage bucket

Bucket is a management unit of objects in S3.  MOCO stores backups in a specified bucket.

MOCO does not remove backups.
To remove old backups automatically, you can set a lifecycle configuration to the bucket.

ref: [Setting lifecycle configuration on a bucket](https://docs.aws.amazon.com/AmazonS3/latest/userguide/how-to-set-lifecycle-configuration-intro.html)

A bucket can be shared safely across multiple MySQLClusters.
Object keys are prefixed with `moco/`.

### BackupPolicy

[BackupPolicy](crd_backuppolicy_v1beta2.md) is a custom resource to define a policy for taking backups.

The following is an example BackupPolicy to take a backup every day and store data in [MinIO][]:

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: BackupPolicy
metadata:
  namespace: backup
  name: daily
spec:
  # Backup schedule.  Any CRON format is allowed.
  schedule: "@daily"

  jobConfig:
    # An existing ServiceAccount name is required.
    serviceAccountName: backup-owner
    env:
    - name: AWS_ACCESS_KEY_ID
      value: minioadmin
    - name: AWS_SECRET_ACCESS_KEY
      value: minioadmin

    # bucketName is required.  Other fields are optional.
    bucketConfig:
      bucketName: moco
      endpointURL: http://minio.default.svc:9000
      usePathStyle: true

    # MOCO uses a filesystem volume to store data temporarily.
    workVolume:
      # Using emptyDir as a working directory is NOT recommended.
      # The recommended way is to use generic ephemeral volume with a provisioner
      # that can provide enough capacity.
      # https://kubernetes.io/docs/concepts/storage/ephemeral-volumes/#generic-ephemeral-volumes
      emptyDir: {}
```

To enable backup for a MySQLCluster, reference the BackupPolicy name like this:

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: MySQLCluster
metadata:
  namespace: default
  name: foo
spec:
  backupPolicyName: daily  # The policy name
...
```

MOCO creates a [CronJob][] for each MySQLCluster that has `spec.backupPolicyName`.

The CronJob's name is `moco-backup-` + the name of MySQLCluster.
For the above example, a CronJob named `moco-backup-foo` is created in `default` namespace.

The following podAntiAffinity is set by default for CronJob.
If you want to override it, set `BackupPolicy.spec.jobConfig.affinity`.

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: moco-backup-foo
spec:
...
  jobTemplate:
    spec:
      template:
        spec:
          affinity:
            podAntiAffinity:
              preferredDuringSchedulingIgnoredDuringExecution:
                - podAffinityTerm:
                    labelSelector:
                      matchExpressions:
                        - key: app.kubernetes.io/name
                          operator: In
                          values:
                            - mysql-backup
                        - key: app.kubernetes.io/created-by
                          operator: In
                          values:
                            - moco
                    topologyKey: kubernetes.io/hostname
                  weight: 100
...
```

### Credentials to access S3 bucket

Depending on your Kubernetes service provider and object storage, there are various ways to give credentials to access the object storage bucket.

For Amazon's [Elastic Kubernetes Service (EKS)][EKS] and S3 users, the easiest way is probably to use [IAM Roles for Service Accounts (IRSA)](https://aws.github.io/aws-eks-best-practices/security/docs/iam/#iam-roles-for-service-accounts-irsa).

ref: [IAM ROLES FOR SERVICE ACCOUNTS](https://www.eksworkshop.com/beginner/110_irsa/)

Another popular way is to set `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` environment variables as shown in the above example.

### Taking an emergency backup

You can take an emergency backup by creating a Job from the CronJob for backup.

```console
$ kubectl create job --from=cronjob/moco-backup-foo emergency-backup
```

### Restore

To restore data from a backup, create a new MyQLCluster with `spec.restore` field as follows:

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: MySQLCluster
metadata:
  namespace: backup
  name: target
spec:
  # restore field is not editable.
  # to modify parameters, delete and re-create MySQLCluster.
  restore:
    # The source MySQLCluster's name and namespace
    sourceName: source
    sourceNamespace: backup

    # The restore point-in-time in RFC3339 format.
    restorePoint: "2021-05-26T12:34:56Z"

    # jobConfig is the same in BackupPolicy
    jobConfig:
      serviceAccountName: backup-owner
      env:
      - name: AWS_ACCESS_KEY_ID
        value: minioadmin
      - name: AWS_SECRET_ACCESS_KEY
        value: minioadmin
      bucketConfig:
        bucketName: moco
        endpointURL: http://minio.default.svc:9000
        usePathStyle: true
      workVolume:
        emptyDir: {}
...
```

### Further details

Read [backup.md](backup.md) for further details.

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
apiVersion: moco.cybozu.com/v1beta2
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

MOCO provides a built-in support to collect and expose `mysqld` metrics using [mysqld_exporter][].

This is an example YAML to enable `mysqld_exporter`.
`spec.collectors` is a list of `mysqld_exporter` flag names without `collect.` prefix.

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: MySQLCluster
metadata:
  namespace: foo
  name: test
spec:
  collectors:
  - engine_innodb_status
  - info_schema.innodb_metrics
  podTemplate:
    ...
```

See [`metrics.md`](metrics.md) for all available metrics and how to collect them using Prometheus.

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
apiVersion: moco.cybozu.com/v1beta2
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

Switchover is an operation to change the live primary to one of the replicas.

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
apiVersion: moco.cybozu.com/v1beta2
kind: MySQLCluster
metadata:
  namespace: default
  name: test
spec:
      containers:
      - name: mysqld
        # Edit the next line
        image: ghcr.io/cybozu-go/moco/mysql:8.0.35
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
[mysqld_exporter]: https://github.com/prometheus/mysqld_exporter/
[S3]: https://aws.amazon.com/s3/
[MinIO]: https://min.io/
[EKS]: https://aws.amazon.com/eks/
[CronJob]: https://kubernetes.io/docs/concepts/workloads/controllers/cron-jobs/

### Stop Clustering and Reconciliation

In MOCO, you can optionally stop the clustering and reconciliation of a MySQLCluster.

To stop clustering and reconciliation, use the following commands.

```console
$ kubectl moco stop clustering <CLSUTER_NAME>
$ kubectl moco stop reconciliation <CLSUTER_NAME>
```

To resume the stopped clustering and reconciliation, use the following commands.

```console
$ kubectl moco start clustering <CLSUTER_NAME>
$ kubectl moco start reconciliation <CLSUTER_NAME>
```

You could use this feature in the following cases:

1. To stop the replication of a MySQLCluster and perform a manual operation to align the GTID
    * Run the `kubectl moco stop clustering` command on the MySQLCluster where you want to stop the replication
2. To suppress the full update of MySQLCluster that occurs during the upgrade of MOCO
    * Run the `kubectl moco stop reconciliation` command on the MySQLCluster on which you want to suppress the update

To check whether clustering and reconciliation are stopped, use `kubectl get mysqlcluster`.
Moreover, while clustering is stopped, `AVAILABLE` and `HEALTHY` values will be `Unknown`.

```console
$ kubectl get mysqlcluster
NAME   AVAILABLE   HEALTHY   PRIMARY   SYNCED REPLICAS   ERRANT REPLICAS   CLUSTERING ACTIVE   RECONCILE ACTIVE   LAST BACKUP
test   Unknown     Unknown   0         3                                   False               False              <no value>
```

The MOCO controller outputs the following metrics to indicate that clustering has been stopped.
1 if the cluster is clustering or reconciliation stopped, 0 otherwise.

```text
moco_cluster_clustering_stopped{name="mycluster", namespace="mynamesapce"} 1
moco_cluster_reconciliation_stopped{name="mycluster", namespace="mynamesapce"} 1
```

During the stop of clustering, monitoring of the cluster from MOCO will be halted, and the value of the following metrics will become NaN.

```text
moco_cluster_available{name="test",namespace="default"} NaN
moco_cluster_healthy{name="test",namespace="default"} NaN
moco_cluster_ready_replicas{name="test",namespace="default"} NaN
moco_cluster_errant_replicas{name="test",namespace="default"} NaN
```
