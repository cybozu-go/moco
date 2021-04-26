Metrics
=======

[`moco-controller`](moco-controller.md) provides the following kind of metrics in Prometheus format.
Aside from [the standard Go runtime and process metrics][standard], it exposes metrics related to [controller-runtime][], MySQL clusters, and backups.

## MySQL clusters

All these metrics are prefixed with `moco_cluster_` and have `name` and `namespace` labels.

| Name             | Description                                                            | Type    |
| ---------------- | ---------------------------------------------------------------------- | ------- |
| checks_total     | The number of times MOCO checked the cluster                           | Counter |
| errors_total     | The number of times MOCO encountered errors when managing the cluster  | Counter |
| available        | 1 if the cluster is available, 0 otherwise                             | Gauge   |
| healthy          | 1 if the cluster is running without any problems, 0 otherwise          | Gauge   |
| switchover_total | The number of times MOCO changed the live primary instance             | Counter |
| failover_total   | The number of times MOCO changed the failed primary instance           | Counter |
| replicas         | The number of mysqld instances in the cluster                          | Gauge   |
| ready_replicas   | The number of ready mysqld Pods in the cluster                         | Gauge   |
| errant_replicas  | The number of mysqld instances that have [errant transactions][errant] | Gauge   |

## Backup

TBD

## MySQL instance

For each `mysqld` instance, [moco-agent][] exposes a set of metrics.
Read [github.com/cybozu-go/moco-agent/blob/main/docs/metrics.md](https://github.com/cybozu-go/moco-agent/blob/main/docs/metrics.md) for details.

[standard]: https://povilasv.me/prometheus-go-metrics/
[controller-runtime]: https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/internal/controller/metrics
[errant]: https://www.percona.com/blog/2014/05/19/errant-transactions-major-hurdle-for-gtid-based-failover-in-mysql-5-6/
[moco-agent]: https://github.com/cybozu-go/moco-agent/
