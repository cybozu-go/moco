# kubectl moco plugin

`kubectl-moco` is a tool to help maintain the MySQL cluster build by MOCO.

```
kubectl moco [global options] <subcommand> [sub options] args...
```

Global options are compatible with kubectl.
For example, the following options are available.

| Global options    | Default value    | Description                                           |
| ----------------- | ---------------- | ----------------------------------------------------- |
| `--kubeconfig`    | `~/.kube/config` | Path to the kubeconfig file to use for CLI requests.  |
| `-n, --namespace` |                  | If present, the namespace scope for this CLI request. |

## `kubectl moco [global options] mysql [options] CLUSTER_NAME [-- args...]`

Run `mysql` command in a specified MySQL instance.

| Options            | Default value        | Description                                                       |
| ------------------ | -------------------- | ----------------------------------------------------------------- |
| `-u, --mysql-user` | `moco-readonly`      | Run mysql as a specified user: `moco-writable` or `moco-readonly` |
| `--index`          | index of the primary | Index of a target mysql instance                                  |
| `-i, --stdin`      | `false`              | Pass stdin to the mysql container                                 |
| `-t, --tty`        | `false`              | Stdin is a TTY                                                    |

The normal `mysql` command can be run as follows:

```
kubectl moco mysql mycluster -- --version
```

You can redirect SQL file as follows:

```
cat sample.sql | kubectl moco mysql -i mycluster
```

You can run `mysql` interactively as follows:

```
kubectl moco mysql -i -t mycluster
```

## `kubectl moco [global options] credential [options] CLUSTER_NAME`

Fetch the credential information of a specified user

| Options            | Default value   | Description                                                                  |
| ------------------ | --------------- | ---------------------------------------------------------------------------- |
| `-u, --mysql-user` | `moco-readonly` | Fetch the credential of a specified user: `moco-writable` or `moco-readonly` |
| `--format`         | `plain`         | Output format: `plain` or `myconf`                                           |
