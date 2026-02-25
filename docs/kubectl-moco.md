# kubectl moco plugin

`kubectl-moco` is a kubectl plugin for MOCO.

```
kubectl moco [global options] <subcommand> [sub options] args...
```

## Global options

Global options are compatible with kubectl.
For example, the following options are available.

| Global options    | Default value        | Description                                           |
| ----------------- | -------------------- | ----------------------------------------------------- |
| `--kubeconfig`    | `$HOME/.kube/config` | Path to the kubeconfig file to use for CLI requests.  |
| `-n, --namespace` | `default`            | If present, the namespace scope for this CLI request. |

## MySQL users

You can choose one of the following user for `--mysql-user` option value.

| Name            | Description                                        |
| --------------- | -------------------------------------------------- |
| `moco-readonly` | A read-only user.                                  |
| `moco-writable` | A user that can edit users, databases, and tables. |
| `moco-admin`    | The super-user.                                    |

## `kubectl moco mysql [options] CLUSTER_NAME [-- mysql args...]`

Run `mysql` command in a specified MySQL instance.

| Options            | Default value        | Description                        |
| ------------------ | -------------------- | ---------------------------------- |
| `-u, --mysql-user` | `moco-readonly`      | Login as the specified user        |
| `--index`          | index of the primary | Index of the target mysql instance |
| `-i, --stdin`      | `false`              | Pass stdin to the mysql container  |
| `-t, --tty`        | `false`              | Stdin is a TTY                     |

### Examples

This executes `SELECT VERSION()` on the primary instance in `mycluster` in `foo` namespace:

```console
$ kubectl moco -n foo mysql mycluster -- -N -e 'SELECT VERSION()'
```

To execute SQL from a file:

```console
$ cat sample.sql | kubectl moco -n foo mysql -u moco-writable -i mycluster
```

To run `mysql` interactively for the instance 2 in `mycluster` in the default namespace:

```console
$ kubectl moco mysql --index 2 -it mycluster
```

## `kubectl moco credential <subcommand>`

Manage credentials for MySQLCluster.

> **Note:** `kubectl moco credential CLUSTER_NAME` (without the `show` subcommand) still works for backward compatibility but is deprecated. Use `kubectl moco credential show CLUSTER_NAME` instead.

### `kubectl moco credential show [options] CLUSTER_NAME`

Fetch the credential information of a specified user.

| Options            | Default value   | Description                                |
| ------------------ | --------------- | ------------------------------------------ |
| `-u, --mysql-user` | `moco-readonly` | Fetch the credential of the specified user |
| `--format`         | `plain`         | Output format: `plain` or `mycnf`          |

### `kubectl moco credential rotate CLUSTER_NAME`

Trigger Phase 1 of system user password rotation.
This generates new passwords, executes `ALTER USER ... IDENTIFIED BY ... RETAIN CURRENT PASSWORD` on all instances (with `sql_log_bin=0`), and distributes the new passwords to per-namespace Secrets.
After this command, both old and new passwords are valid (MySQL dual password).

### `kubectl moco credential discard CLUSTER_NAME`

Trigger Phase 2 of system user password rotation.
This executes `ALTER USER ... DISCARD OLD PASSWORD` on all instances (with `sql_log_bin=0`), then migrates the authentication plugin with `ALTER USER ... IDENTIFIED WITH <plugin> BY ...` (determined from `@@global.authentication_policy`, enabling transparent migration from legacy plugins like `mysql_native_password`).
Finally, it confirms the new passwords in the source Secret and resets the rotation status to Idle.
This command requires that `kubectl moco credential rotate` has been run first and completed successfully.

## `kubectl moco switchover CLUSTER_NAME`

Switch the primary instance to one of the replicas.


## Stop or start clustering and reconciliation

Read [Stop Clustering and Reconciliation](./usage.md#Stop-Clustering-and-Reconciliation).

### `kubectl moco stop clustering CLUSTER_NAME`
Stop the clustering of the specified MySQLCluster.

### `kubectl moco start clustering CLUSTER_NAME`
Start the clustering of the specified MySQLCluster.

### `kubectl moco stop reconciliation CLUSTER_NAME`
Stop the reconciliation of the specified MySQLCluster.

### `kubectl moco start reconciliation CLUSTER_NAME`
Start the reconciliation of the specified MySQLCluster.
