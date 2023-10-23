# Backup and restore <!-- omit from toc -->

This document describes how MOCO takes a backup of MySQLCluster data and restores a cluster from a backup.

- [Overview](#overview)
- [Design goals](#design-goals)
- [Implementation](#implementation)
  - [Backup file keys](#backup-file-keys)
  - [Timestamps](#timestamps)
  - [Backup](#backup)
  - [Restore](#restore)
  - [Caveats](#caveats)
- [Considered options](#considered-options)
  - [Why do we use S3-compatible object storage to store backups?](#why-do-we-use-s3-compatible-object-storage-to-store-backups)
  - [What object storage is supported?](#what-object-storage-is-supported)
  - [Why do we use Jobs for backup and restoration?](#why-do-we-use-jobs-for-backup-and-restoration)
  - [Why do we prefer `mysqlsh` to `mysqldump`?](#why-do-we-prefer-mysqlsh-to-mysqldump)
  - [Why don't we do continuous backup?](#why-dont-we-do-continuous-backup)

## Overview

A MySQLCluster can be configured to take backups regularly by referencing a BackupPolicy in `spec.backupPolicyName`.  For each MySQLCluster associated with a BackupPolicy, `moco-controller` creates a CronJob.
The CronJob creates a Job to take a full backup periodically.
The Job also takes a backup of binary logs for [Point-in-Time Recovery (PiTR)][PiTR].
The backups are stored in a S3-compatible object storage bucket.

This figure illustrates how MOCO takes a backup of a MySQLCluster.

![Backup](https://www.plantuml.com/plantuml/svg/VP9DRzim38Rl-XM4xpOIfzkXXw1hRiE-Sonwa6N6LIUBdZu614FwsoTHTkjjM4yct-SbOPAwyK6w44SZ5DdWo40rag9wpWow2gI7h0bn8jEZW-gJ7D5FKY6SY9YdB_mI001t7y_7hnyE9lg0xfvhp_w7AUnMgkzn-a96gpEpYKE6R8DwFsjm3GvFwD0gBCK7H_OzTLodKbpKHNcaZeLCe6dsMKWzsYOfACFSuuZkfrRuJYcADd2XbuoolSv9CNuZWunT2bwaMsrxROTdqfMS3QiyZS7fUeX_FSdavH-MYn3AKEoXEkxWGECaW-uCmkVk4LM0Oo0d1wpcLI_dA5k5TjDkwqrRRwxu91shxUnblpO8LH_7YGqvQ1bUNcstMxNRljvk-nTCeneQ69VmnS1sgEyUTD-ZlNTwU0ZrVkswv7NaXyVdvBjUmtQvvpFXdVwNBDjUy_BIdfwMDx9h-6z4obZbnIJzge4fXaLU9aZWpGhiWTibzMq3SUfbGF11XkZ53ThKolm6)

1. `moco-controller` creates a CronJob and Role/RoleBinding to allow access to MySQLCluster for the Job Pod.
2. At each configured interval, CronJob creates a Job.
3. The Job dumps all data from a `mysqld` using [MySQL shell's dump instance utility][dump].
4. The Job creates a tarball of the dumped data and put it in a bucket of S3 compatible object storage.
5. The Job also dumps binlogs since the last backup and put it in the same bucket (with a different name, of course).
6. The Job finally updates MySQLCluster status to record the last successful backup.

To restore from a backup, users need to create a new MySQLCluster with `spec.restore` filled with necessary information such as the bucket name of the object storage, the object key, and so on.

The next figure illustrates how MOCO restores MySQL cluster from a backup.

![Restore](https://www.plantuml.com/plantuml/svg/TPA_xzCm4CLt_nMdx8xwJM3f49LsG_me3LlLmR6lgOlZYnm7X53xjuFj6WAeItBtFiydvrqsanVEpuDXPp8A7HGVn83JA2H29rm1OWfl-C4008xllxUVtktCF7bqfy1agXFTauhYI2e86K9PDa63DKY7mcDolwKkkg_K9U501hVQActx2Dollalz7yDlagGMtLSjyOsKD5iVuIG22cE16pnxdfN5FE2yYJsimU4P7Lg9_viQcCHVZXjZqj2ie6XhsD4m2gyxW_2nkwBqq7foeiUOsbG_Gil1ReNyCezGhQeNlghugaxX9ZLPerqRX4BDmnXvAFvXdRQ6-aXJcNaq0pzUj59eJqxt5y-RXUSMFu2iTsIW64WIVtG6qww3nbXuXgt54DVmKkR5PH1ZUavoW4j3lDlzdcTM9IZwPWq9nO8IIFf7wXAYckwzEFBgwP7N9UprvmFRe5NpO7u2)

1. `moco-controller` creates a Job and Role/RoleBinding for restoration.
2. The Job downloads a tarball of dumped files of the specified backup.
3. The Job loads data into an empty `mysqld` using [MySQL shell's dump loading utility][load].
4. If the user wanted to restore data at a point-in-time, the Job downloads saved binlogs.
5. The Job applies binlogs up to the specified point-in-time using [`mysqlbinlog`][mysqlbinlog].
6. The Job finally updates MySQLCluster status to record the restoration time.

## Design goals

Must:

- Users must be able to configure different backup policies for each MySQLCluster.
- Users must be able to restore MySQL data at a point-in-time from backups.
- Users must be able to restore MySQL data without the original MySQLCluster resource.
- `moco-controller` must export metrics about backups.

Should:

- Backup data should be compressed to save the storage space.
- Backup data should be stored in an object storage.
- Backups should be taken from a replica instance as much as possible.

These "should's" are mostly in terms of money or performance.

## Implementation

### Backup file keys

Backup files are stored in an object storage bucket with the following keys.

- Key for a tarball of a fully dumped MySQL: `moco/<namespace>/<name>/YYYYMMDD-hhmmss/dump.tar`
- Key for a compressed tarball of binlog files: `moco/<namespace>/<name>/YYYYMMDD-hhmmss/binlog.tar.zst`

`<namespace>` is the namespace of MySQLCluster, and `<name>` is the name of MySQLCluster.
`YYYYMMDD-hhmmss` is the date and time of the backup where `YYYY` is the year, `MM` is two-digit month, `DD` is two-digit day, `hh` is two-digit hour in 24-hour format, `mm` is two-digit minute, and `ss` is two-digit second.

Example: `moco/foo/bar/20210515-230003/dump.tar`

This allows multiple MySQLClusters to share the same bucket.

### Timestamps

Internally, the time for PiTR is formatted in UTC timezone.

The restore Job runs `mysqlbinlog` with `TZ=Etc/UTC` timezone.

### Backup

As described in Overview, the backup process is implemented with CronJob and Job.
In addition, users need to provide a ServiceAccount for the Job.

The ServiceAccount is often used to grant access to the object storage bucket where the backup files will be stored.
For instance, Amazon Elastic Kubernetes Service (EKS) has [a feature to create such a ServiceAccount][iamrole].
Kubernetes itself is also developing such an enhancement called [Container Object Storage Interface (COSI)][COSI].

To allow the backup Job to update MySQLCluster status, MOCO creates Role and RoleBinding.
The RoleBinding grants the access to the given ServiceAccount.

By default, MOCO uses the Amazon S3 API, the most popular object storage API.
Therefore, it also works with object storage that has an S3-compatible API, such as [MinIO][] and [Ceph][].
Object storage that uses non-S3 compatible APIs is only partially supported.

Currently supported object storage includes:

* Amazon S3-compatible API
* Google Cloud Storage API

For the first time, the backup Job chooses a replica instance as the backup source if available.
For the second and subsequent backups, the Job will choose the last chosen instance as long as it is still a replica and available.

The backups are divided into two: a full dump and binlogs.
A full dump is a snapshot of the entire MySQL database.
Binlogs are records of transactions.
With `mysqlbinlog`, binlogs can be used to apply transactions to a database restored from a full dump for PiTR.

For the first time, MOCO only takes a full dump of a MySQL instance, and records the GTID at the backup.
For the second and subsequent backups, MOCO will retrieve binlogs since the GTID of the last backup until now.

To take a full dump, MOCO uses [MySQL shell's dump instance utility][dump].
It performs [significantly faster][faster] than `mysqldump` or `mysqlpump`.
The dump is compressed with [zstd compression algorithm][zstd].

MOCO then creates a tarball of the dump and puts it to an object storage bucket.

To retrieve transactions since the last backup until now, `mysqlbinlog` is used with these flags:

- [`--read-from-remote-master=BINLOG-DUMP-GTIDS`](https://dev.mysql.com/doc/refman/8.0/en/mysqlbinlog.html#option_mysqlbinlog_read-from-remote-master)
- [`--exclude-gtids=<the GTID of the last backup>`](https://dev.mysql.com/doc/refman/8.0/en/mysqlbinlog.html#option_mysqlbinlog_exclude-gtids)
- [`--to-last-log`](https://dev.mysql.com/doc/refman/8.0/en/mysqlbinlog.html#option_mysqlbinlog_to-last-log)

The retrieved binlog files are packed into a tarball and compressed with zstd, then put to an object storage bucket.

Finally, the Job updates MySQLCluster status field with the following information:

- The time of backup
- The time spent on the backup
- The ordinal of the backup source instance
- `server_uuid` of the instance (to check whether the instance was re-initialized or not)
- The binlog filename in `SHOW MASTER STATUS` output.
- The size of the tarball of the dumped files
- The size of the tarball of the binlog files
- The maximum usage of the working directory
- Warnings, if any

### Restore

To restore MySQL data from a backup, users need to create a new MySQLCluster with appropriate `spec.restore` field.
`spec.restore` needs to provide at least the following information:

- The bucket name
- Namespace and name of the original MySQLCluster
- A point-in-time in RFC3339 format

After `moco-controller` identifies `mysqld` is running, it creates a Job to retrieve backup files and load them into `mysqld`.

The Job looks for the most recent tarball of the dumped files that is older than the specified point-in-time in the bucket, and retrieves it.
The dumped files are then loaded to `mysqld` using [MySQL shell's load dump utility][load].

If the point-in-time is different from the time of the dump file, and if there is a compressed tarball of binlog files, then the Job retrieves binlog files and applies transactions up to the point-in-time.

After restoration process finishes, the Job updates MySQLCluster status to record the restoration time.
`moco-controller` then configures the clustering as usual.

If the Job fails, `moco-controller` leaves the Job as is.
The restored MySQL cluster will also be left read-only.
If some of the data have been restored, they can be read from the cluster.

If a failed Job is deleted, `moco-controller` will create a new Job to give it another chance.
Users can safely delete a successful Job.

### Caveats

- No automatic deletion of backup files

    MOCO does not delete old backup files from object storage.
    Users should configure [a bucket lifecycle policy][lifecycle] to delete old backups automatically.

- Duplicated backup Jobs

    CronJob may create two or more Jobs at a time.
    If this happens, only one Job can update MySQLCluster status.

- Lost binlog files

    If [`binlog_expire_logs_seconds`](https://dev.mysql.com/doc/refman/8.0/en/replication-options-binary-log.html#sysvar_binlog_expire_logs_seconds) or [`expire_logs_days`](https://dev.mysql.com/doc/refman/8.0/en/replication-options-binary-log.html#sysvar_expire_logs_days) is set to a shorter value than the interval of backups, MOCO cannot save binlogs correctly.
    Users are responsible to configure `binlog_expire_logs_seconds` appropriately.

## Considered options

There were many design choices and alternative methods to implement backup/restore feature for MySQL.
Here are descriptions of why we determined the current design.

### Why do we use S3-compatible object storage to store backups?

Compared to file systems, object storage is generally more cost-effective.
It also has many useful features such as [object lifecycle management][lifecycle].

AWS S3 API is the most prevailing API for object storages.

### What object storage is supported?

MOCO currently supports the following object storage APIs:

* Amazon S3
* Google Cloud Storage

MOCO uses the Amazon S3 API by default.
You can specify `BackupPolicy.spec.jobConfig.bucketConfig.backendType` to specify the object storage API to use.
Currently, two identifiers can be specified, `backendType` for `s3` or `gcs`.
If not specified, it will be defaults to `s3`.

The following is an example of a backup setup using Google Cloud Storage:

```yaml
apiVersion: moco.cybozu.com/v1beta1
kind: BackupPolicy
...
spec:
  schedule: "@daily"
  jobConfig:
    serviceAccountName: backup-owner
    env:
    - name: GOOGLE_APPLICATION_CREDENTIALS
      value: <dummy>
    bucketConfig:
      bucketName: moco
      endpointURL: https://storage.googleapis.com
      backendType: gcs
    workVolume:
      emptyDir: {}
```

### Why do we use Jobs for backup and restoration?

Backup and restoration can be a CPU- and memory-consuming task.
Running such a task in `moco-controller` is dangerous because `moco-controller` manages a lot of MySQLCluster.

`moco-agent` is also not a safe place to run backup job because it is a sidecar of `mysqld` Pod.
If backup is run in `mysqld` Pod, it would interfere with the `mysqld` process.

### Why do we prefer `mysqlsh` to `mysqldump`?

The biggest reason is the difference in how these tools lock the instance.

`mysqlsh` uses [`LOCK INSTANCE FOR BACKUP`](https://dev.mysql.com/doc/refman/8.0/en/lock-instance-for-backup.html) which blocks DDL until the lock is released.  `mysqldump`, on the other hand, allows DDL to be executed.  Once DDL is executed _and_ acquire meta data lock, which means that **any DML for the table modified by DDL will be blocked**.

Blocking DML during backup is not desirable, especially when the only available backup source is the primary instance.

Another reason is that `mysqhsl` is [much faster][faster] than `mysqldump` / `mysqlpump`.

### Why don't we do continuous backup?

Continuous backup is a technique to save executed transactions in real time.
For MySQL, this can be done with `mysqlbinlog --stop-never`.  This command continuously retrieves transactions from binary logs and outputs them to stdout.

MOCO does not adopt this technique for the following reasons:

- We assume MOCO clusters have replica instances in most cases.

    When the data of the primary instance is lost, one of replicas can be promoted as a new primary.

- It is troublesome to control the continuous backup process on Kubernetes.

    The process needs to be kept running between full backups.
    If we do so, the entire backup process should be a persistent workload, not a (Cron)Job.

[PiTR]: https://dev.mysql.com/doc/refman/8.0/en/point-in-time-recovery.html
[dump]: https://dev.mysql.com/doc/mysql-shell/8.0/en/mysql-shell-utilities-dump-instance-schema.html
[load]: https://dev.mysql.com/doc/mysql-shell/8.0/en/mysql-shell-utilities-load-dump.html
[mysqlbinlog]: https://dev.mysql.com/doc/refman/8.0/en/mysqlbinlog.html
[iamrole]: https://aws.amazon.com/blogs/opensource/introducing-fine-grained-iam-roles-service-accounts/
[COSI]: https://github.com/kubernetes-sigs/container-object-storage-interface-api
[MinIO]: https://min.io/
[Ceph]: https://ceph.io/
[faster]: https://mysqlserverteam.com/mysql-shell-dump-load-part-2-benchmarks/
[zstd]: https://facebook.github.io/zstd/
[lifecycle]: https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-lifecycle-mgmt.html
