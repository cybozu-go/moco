# Managing users and databases with mariadb-operator

MOCO does not create users or databases declaratively.
You can use [mariadb-operator](https://github.com/mariadb-operator/mariadb-operator) for this purpose.
It provides custom resources for databases, users, and privileges.
You can point these resources at an external MySQL server, such as a MySQLCluster managed by MOCO.

This page shows an example setup.
MOCO does not maintain or test mariadb-operator.
We tested this example with MySQL 8.4.10 and mariadb-operator 26.10.2.

## How it works

mariadb-operator connects to the primary instance of a MySQLCluster through the primary Service.
It uses the `moco-writable` user to create databases, users, and privileges.

```
mariadb-operator
  ExternalMariaDB  -->  moco-<cluster>-primary.<namespace>.svc:3306  (user: moco-writable)
  Database / User / Grant  -->  objects created in MySQL
```

The following resources are used:

| Resource | Purpose |
| -------- | ------- |
| `ExternalMariaDB` | Connection information for the MySQLCluster |
| `Database` | Creates a database |
| `User` | Creates a user |
| `Grant` | Grants privileges to a user |

## Prerequisites

- A running MySQLCluster. This page uses the cluster `test` in the namespace `foo`.
- Helm and kubectl.

## Step 1: Install mariadb-operator

Install the CRDs and the operator with Helm.
The example uses cert-manager for the webhook certificates, so install cert-manager first.

```console
$ helm repo add mariadb-operator https://helm.mariadb.com/mariadb-operator
$ helm install mariadb-operator-crds mariadb-operator/mariadb-operator-crds \
    --version 26.10.2 -n mariadb-operator --create-namespace
$ helm install mariadb-operator mariadb-operator/mariadb-operator \
    --version 26.10.2 -n mariadb-operator --wait
```

For other installation options, see the [mariadb-operator documentation](https://github.com/mariadb-operator/mariadb-operator/blob/main/docs/quickstart.md).

## Step 2: Create an ExternalMariaDB

Create an `ExternalMariaDB` resource in the same namespace as the MySQLCluster.
The password is read from the Secret that MOCO creates for the users.
The name of this Secret is `moco-<cluster name>`, and the key for `moco-writable` is `WRITABLE_PASSWORD`.

```yaml
apiVersion: k8s.mariadb.com/v1alpha1
kind: ExternalMariaDB
metadata:
  namespace: foo
  name: moco-test
spec:
  host: moco-test-primary.foo.svc
  port: 3306
  username: moco-writable
  passwordSecretKeyRef:
    name: moco-test
    key: WRITABLE_PASSWORD
```

## Step 3: Create databases, users, and grants

The example below creates the database `app`, a read-write user, and a read-only user.

Before you apply the manifest, create the Secrets for the passwords of the new users.
Do not write the passwords into the manifest.

```console
$ kubectl -n foo create secret generic app-rw-password --from-literal=password="$(openssl rand -base64 24)"
$ kubectl -n foo create secret generic app-ro-password --from-literal=password="$(openssl rand -base64 24)"
```

Apply the following manifest:

```yaml
apiVersion: k8s.mariadb.com/v1alpha1
kind: Database
metadata:
  namespace: foo
  name: app-db
spec:
  mariaDbRef:
    name: moco-test
    kind: ExternalMariaDB
  name: app
  characterSet: utf8mb4
  # Set the collation explicitly. See "Known limitations".
  collate: utf8mb4_general_ci
---
apiVersion: k8s.mariadb.com/v1alpha1
kind: User
metadata:
  namespace: foo
  name: app-rw
spec:
  mariaDbRef:
    name: moco-test
    kind: ExternalMariaDB
  name: app_rw
  host: "%"
  maxUserConnections: 20
  passwordSecretKeyRef:
    name: app-rw-password
    key: password
---
apiVersion: k8s.mariadb.com/v1alpha1
kind: Grant
metadata:
  namespace: foo
  name: app-rw
spec:
  mariaDbRef:
    name: moco-test
    kind: ExternalMariaDB
  privileges: ["SELECT", "INSERT", "UPDATE", "DELETE"]
  database: app
  table: "*"
  username: app_rw
  host: "%"
---
apiVersion: k8s.mariadb.com/v1alpha1
kind: User
metadata:
  namespace: foo
  name: app-ro
spec:
  mariaDbRef:
    name: moco-test
    kind: ExternalMariaDB
  name: app_ro
  host: "%"
  maxUserConnections: 5
  passwordSecretKeyRef:
    name: app-ro-password
    key: password
---
apiVersion: k8s.mariadb.com/v1alpha1
kind: Grant
metadata:
  namespace: foo
  name: app-ro
spec:
  mariaDbRef:
    name: moco-test
    kind: ExternalMariaDB
  privileges: ["SELECT"]
  database: app
  table: "*"
  username: app_ro
  host: "%"
```

## Step 4: Check the resources

```console
$ kubectl -n foo get externalmariadb,database,user,grant
```

Each resource should show `READY` as `True`.
You can also connect to the primary Service with the new user:

```console
$ kubectl -n foo exec -it <client-pod> -- mysql -h moco-test-primary.foo.svc -u app_rw -p app
```

## Known limitations

- **Collation.** If you do not set `collate`, the operator uses `utf8mb3_general_ci`.
  MySQL 8.4 rejects this collation with `utf8mb4`.
  The `collate` field cannot be changed after the Database is created.
  If you set it wrong, delete the Database and create it again.
- **Privileges of `moco-writable`.** This user has broad privileges on all databases and tables.
  It also has `WITH GRANT OPTION`.
  For production, consider a dedicated management user with only the privileges that mariadb-operator needs.
  We did not test this setup.
- **Deleting users.** Deleting a `User` removes the user from MySQL.
  The matching `Grant` resource stays and still shows `READY` as `True`.
  Delete the `User` and its `Grant` together.
- **Failover.** mariadb-operator connects through the primary Service.
  In our test, the resources kept working after a primary failover.
  The operator reconnects to the new primary.
- **TLS.** We did not test TLS between mariadb-operator and MOCO.
- **Scope.** mariadb-operator is designed for MariaDB.
  Our tests covered only the features on this page with MySQL 8.4.10.

MOCO does not know about the users and databases that mariadb-operator creates.
MOCO does not manage or delete them.
