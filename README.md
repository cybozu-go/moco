[![GitHub release](https://img.shields.io/github/release/cybozu-go/moco.svg?maxAge=60)][releases]
[![CI](https://github.com/cybozu-go/moco/actions/workflows/ci.yaml/badge.svg)](https://github.com/cybozu-go/moco/actions/workflows/ci.yaml)
[![PkgGoDev](https://pkg.go.dev/badge/github.com/cybozu-go/moco)](https://pkg.go.dev/github.com/cybozu-go/moco)
[![Go Report Card](https://goreportcard.com/badge/github.com/cybozu-go/moco)](https://goreportcard.com/report/github.com/cybozu-go/moco)

# MOCO

MOCO is a Kubernetes operator for [MySQL][].
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

## Supported software

- MySQL: 8.0.18 and 8.0.25
- Kubernetes: 1.19 and 1.20

Other versions may work, though not tested.

## Features

- Cluster of 1, 3, or 5 MySQL instances
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
2. Checkout MOCO and go to `e2e` directory.
3. Run `make start`

You can then create a three-instance MySQL cluster as follows:

```console
$ cat > mycluster.yaml <<'EOF'
apiVersion: moco.cybozu.com/v1beta1
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
        image: quay.io/cybozu/moco-mysql:8.0.25
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

- [`docs/setup.md`](docs/setup.md) for installing MOCO.
- [`docs/usage.md`](docs/usage.md) is the user manual of MOCO.
- [`examples`](examples/) directory contains example MySQLCluster manifests.

[`docs`](docs/) directory also contains other design or specification documents.

## Docker images

Docker images are available on [ghcr.io/cybozu-go/moco](https://github.com/orgs/cybozu-go/packages/container/package/moco).

[releases]: https://github.com/cybozu-go/moco/releases
[MySQL]: https://www.mysql.com/
[mysqld_exporter]: https://github.com/prometheus/mysqld_exporter
