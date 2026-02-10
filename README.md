[![GitHub release](https://img.shields.io/github/release/cybozu-go/moco.svg?maxAge=60)][releases]
[![CI](https://github.com/cybozu-go/moco/actions/workflows/ci.yaml/badge.svg)](https://github.com/cybozu-go/moco/actions/workflows/ci.yaml)
[![PkgGoDev](https://pkg.go.dev/badge/github.com/cybozu-go/moco)](https://pkg.go.dev/github.com/cybozu-go/moco)
[![Go Report Card](https://goreportcard.com/badge/github.com/cybozu-go/moco)](https://goreportcard.com/report/github.com/cybozu-go/moco)

# MOCO

<img src="./docs/logo.svg" width="160" alt="moco logo" />

MOCO is a [MySQL][] operator on Kubernetes.
Its primary function is to manage MySQL clusters using [GTID-based](https://dev.mysql.com/doc/refman/8.0/en/replication-gtids.html) [semi-synchronous](https://dev.mysql.com/doc/refman/8.0/en/replication-semisync.html) replication.  It does _not_ manage [group replication](https://dev.mysql.com/doc/refman/8.0/en/group-replication.html) clusters.

MOCO is designed to have the following properties.

- Compatibility with the standard MySQL
    - This is the reason that MOCO does not adopt group replication that has [a number of limitations](https://dev.mysql.com/doc/refman/8.0/en/group-replication-limitations.html).
- Safety
    - MOCO only allows writes to a single instance called the primary at a time.
    - MOCO configures loss-less semi-synchronous replication with sufficient replicas.
    - MOCO detects and excludes instances having [errant transactions](https://www.percona.com/blog/2014/05/19/errant-transactions-major-hurdle-for-gtid-based-failover-in-mysql-5-6/).
- Availability
    - MOCO can quickly switch the primary in case of the primary failure or restart.
    - MOCO allows up to 5 instances in a cluster.

Blog article: [Introducing MOCO, a modern MySQL operator on Kubernetes](https://blog.kintone.io/entry/moco)

## Supported software

- MySQL: 8.0.28, 8.0.43, 8.0.44, 8.0.45, 8.4.4, 8.4.8
- Kubernetes: 1.32, 1.33, 1.34

MOCO supports (tests) the LTS releases of MySQL 8.
Innovation releases would probably work. But they are not tested in our CI.

## Features

- Cluster with odd number of MySQL instances
- [`kubectl` plugin](docs/kubectl-moco.md)
- Replication from an external MySQL instance
- Manual and automatic switchover of the primary instance
- Automatic failover of the primary instance
- Backup and [Point-in-Time Recovery](https://dev.mysql.com/doc/refman/8.0/en/point-in-time-recovery-positions.html)
- Errant transaction detection
- Different MySQL versions for each cluster
- Upgrading MySQL version of a cluster
- Monitor for replication delays
- Built-in [mysqld_exporter][] for `mysqld` metrics
- Services for the primary and replicas, respectively
- Custom `my.cnf` configurations
- Custom Pod, Service, and PersistentVolumeClaim templates
- Redirect slow query logs to a sidecar container
- Auto-generate [PodDisruptionBudget](https://kubernetes.io/docs/concepts/workloads/pods/disruptions/#pod-disruption-budgets)

## Quick start

You can quickly run MOCO using [kind](https://kind.sigs.k8s.io/).

1. Prepare a Linux machine and install Docker.
2. Install aqua by following the instructions at https://aquaproj.github.io/docs/install/.
3. Checkout MOCO and go to `e2e` directory.
4. Run `make start`

You can then create a three-instance MySQL cluster as follows:

```console
$ cat > mycluster.yaml <<'EOF'
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
        image: ghcr.io/cybozu-go/moco/mysql:8.4.8
  volumeClaimTemplates:
  - metadata:
      name: mysql-data
    spec:
      accessModes: [ "ReadWriteOnce" ]
      resources:
        requests:
          storage: 1Gi
EOF

$ export KUBECONFIG=$(pwd)/.kubeconfig
$ ../bin/kubectl apply -f mycluster.yaml
```

Check the status of MySQLCluster until it becomes healthy as follows:

```console
$ ../bin/kubectl get mysqlcluster test
NAME   AVAILABLE   HEALTHY   PRIMARY   SYNCED REPLICAS   ERRANT REPLICAS
test   True        True      0         3
```

Once it becomes healthy, you can use `kubectl-moco` to play with `mysql` client.

```console
$ ../bin/kubectl moco mysql -it test
```

To destroy the Kubernetes cluster, run:

```console
$ make stop
```

## Documentation

See https://cybozu-go.github.io/moco/

[`examples`](examples/) directory contains example MySQLCluster manifests.

## Contributing

We require all commits to comply with the [Developer Certificate of Origin](https://developercertificate.org/). Use `git commit -s` (or configure your Git client to add a `Signed-off-by` trailer) to sign off every commit before opening a pull request. Pull requests without the trailer will not pass our checks.

## Docker images

Docker images are available on [ghcr.io/cybozu-go/moco](https://github.com/orgs/cybozu-go/packages/container/package/moco).

[releases]: https://github.com/cybozu-go/moco/releases
[MySQL]: https://www.mysql.com/
[mysqld_exporter]: https://github.com/prometheus/mysqld_exporter
