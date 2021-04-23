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
| `moco-readable` | A read-only user.                                  |
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

## `kubectl moco credential [options] CLUSTER_NAME`

Fetch the credential information of a specified user

| Options            | Default value   | Description                                |
| ------------------ | --------------- | ------------------------------------------ |
| `-u, --mysql-user` | `moco-readonly` | Fetch the credential of the specified user |
| `--format`         | `plain`         | Output format: `plain` or `mycnf`          |
