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

## `kubectl moco credential [options] CLUSTER_NAME`

Fetch the credential information of a specified user

| Options            | Default value   | Description                                |
| ------------------ | --------------- | ------------------------------------------ |
| `-u, --mysql-user` | `moco-readonly` | Fetch the credential of the specified user |
| `--format`         | `plain`         | Output format: `plain` or `mycnf`          |

## `kubectl moco rotate-credential CLUSTER_NAME`

Rotate system user passwords for a MOCO cluster.
The command creates a single-use CredentialRotation custom resource (CR); the creation itself starts the rotation cycle.

If a CredentialRotation already occupies the name, the command refuses and explains the existing object's state:

- a cycle in flight — wait for it, or delete the CR to abandon it,
- a `Succeeded` object waiting for its automatic TTL deletion — delete it to rotate again immediately,
- a `Failed` object — follow the recovery procedure in its `status.message`, then delete it,
- a leftover from a previously deleted cluster — delete it.

As a safety measure, the command also refuses to start when the cluster cannot make progress with a rotation:

- `spec.offline` is `true`.
- The `moco.cybozu.com/reconciliation-stopped=true` or `moco.cybozu.com/clustering-stopped=true` annotation is set.
- The MySQLCluster is not `Healthy` or has 0 replicas.

Follow the rotation with `kubectl get credentialrotation CLUSTER_NAME -w` (the `PHASE` column). Then wait for the verification window — the period when both the old and the new passwords work (see [usage.md](usage.md)) — with `kubectl wait credentialrotation CLUSTER_NAME --for=condition=DiscardReady`.

## `kubectl moco discard-old-credential CLUSTER_NAME`

Discard old passwords after a successful credential rotation.
Sets `spec.discard: true` on the CredentialRotation CR.

This can only be run while the verification window is open (`DiscardReady=True`; the rolling restart of the rotate phase has settled).
After the discard completes, the CR reaches the `Succeeded` phase and is deleted automatically after a TTL (controller flag `--credential-rotation-ttl`, default 1h). Wait for completion with `kubectl wait credentialrotation CLUSTER_NAME --for=condition=Finished` and then check that `status.phase` is `Succeeded`.

The command refuses to run when the cluster is offline, when clustering is stopped, or when the cluster is not `Healthy`. Unlike `rotate-credential`, stopped reconciliation does not block it: the discard phase does not depend on reconciliation, because the new passwords were already distributed before the CR reached the verification window.

> **Note:** the CredentialRotation CR is an operation object driven by these commands. Do not manage it with GitOps tools — a sync would recreate the automatically deleted object and trigger an unrequested rotation.

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
