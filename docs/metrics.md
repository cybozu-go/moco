Metrics
=======

- [moco-controller](#moco-controller)
  - [MySQL clusters](#mysql-clusters)
  - [Backup](#backup)
- [MySQL instance](#mysql-instance)
- [Scrape rules](#scrape-rules)

## moco-controller

[`moco-controller`](moco-controller.md) provides the following kind of metrics in Prometheus format.
Aside from [the standard Go runtime and process metrics][standard], it exposes metrics related to [controller-runtime][], MySQL clusters, and backups.

### MySQL clusters

All these metrics are prefixed with `moco_cluster_` and have `name` and `namespace` labels.

| Name                                | Description                                                            | Type    |
|-------------------------------------|------------------------------------------------------------------------|---------|
| `checks_total`                      | The number of times MOCO checked the cluster                           | Counter |
| `errors_total`                      | The number of times MOCO encountered errors when managing the cluster  | Counter |
| `available`                         | 1 if the cluster is available, 0 otherwise                             | Gauge   |
| `healthy`                           | 1 if the cluster is running without any problems, 0 otherwise          | Gauge   |
| `switchover_total`                  | The number of times MOCO changed the live primary instance             | Counter |
| `failover_total`                    | The number of times MOCO changed the failed primary instance           | Counter |
| `replicas`                          | The number of mysqld instances in the cluster                          | Gauge   |
| `ready_replicas`                    | The number of ready mysqld Pods in the cluster                         | Gauge   |
| `errant_replicas`                   | The number of mysqld instances that have [errant transactions][errant] | Gauge   |
| `volume_resized_total`              | The number of successful volume resizes                                | Counter |
| `volume_resized_errors_total`       | The number of failed volume resizes                                    | Counter |
| `statefulset_recreate_total`        | The number of successful StatefulSet recreates                         | Counter |
| `statefulset_recreate_errors_total` | The number of failed StatefulSet recreates                             | Counter |

### Backup

All these metrics are prefixed with `moco_backup_` and have `name` and `namespace` labels.

| Name                  | Description                                                                   | Type  |
| --------------------- | ----------------------------------------------------------------------------- | ----- |
| `timestamp`           | The number of seconds since January 1, 1970 UTC of the last successful backup | Gauge |
| `elapsed_seconds`     | The number of seconds taken for the last backup                               | Gauge |
| `dump_bytes`          | The size of compressed full backup data                                       | Gauge |
| `binlog_bytes`        | The size of compressed binlog files                                           | Gauge |
| `workdir_usage_bytes` | The maximum usage of the working directory                                    | Gauge |
| `warnings`            | The number of warnings in the last successful backup                          | Gauge |

## MySQL instance

For each `mysqld` instance, [moco-agent][] exposes a set of metrics.
Read [github.com/cybozu-go/moco-agent/blob/main/docs/metrics.md](https://github.com/cybozu-go/moco-agent/blob/main/docs/metrics.md) for details.

Also, if you give a set of collector flag names to `spec.collectors` of MySQLCluster, a sidecar container running [mysqld_exporter][] exposes the collected metrics for each `mysqld` instance.

## Scrape rules

This is an example `kubernetes_sd_config` for Prometheus to collect all MOCO & MySQL metrics.

```yaml
scrape_configs:
- job_name: 'moco-controller'
  kubernetes_sd_configs:
  - role: pod
  relabel_configs:
  - source_labels: [__meta_kubernetes_namespace,__meta_kubernetes_pod_label_app_kubernetes_io_component,__meta_kubernetes_pod_container_port_name]
    action: keep
    regex: moco-system;moco-controller;metrics

- job_name: 'moco-agent'
  kubernetes_sd_configs:
  - role: pod
  relabel_configs:
  - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_name,__meta_kubernetes_pod_container_port_name,__meta_kubernetes_pod_label_statefulset_kubernetes_io_pod_name]
    action: keep
    regex: mysql;agent-metrics;moco-.*
  - source_labels: [__meta_kubernetes_namespace]
    action: replace
    target_label: namespace

- job_name: 'moco-mysql'
  kubernetes_sd_configs:
  - role: pod
  relabel_configs:
  - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_name,__meta_kubernetes_pod_container_port_name,__meta_kubernetes_pod_label_statefulset_kubernetes_io_pod_name]
    action: keep
    regex: mysql;mysqld-metrics;moco-.*
  - source_labels: [__meta_kubernetes_namespace]
    action: replace
    target_label: namespace
  - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_instance]
    action: replace
    target_label: name
  - source_labels: [__meta_kubernetes_pod_label_statefulset_kubernetes_io_pod_name]
    action: replace
    target_label: index
    regex: .*-([0-9])
  - source_labels: [__meta_kubernetes_pod_label_moco_cybozu_com_role]
    action: replace
    target_label: role
```

The collected metrics should have these labels:

- `namespace`: MySQLCluster's `metadata.namespace`
- `name`: MySQLCluster's `metadata.name`
- `index`: The ordinal of MySQL instance Pod

[standard]: https://povilasv.me/prometheus-go-metrics/
[controller-runtime]: https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/internal/controller/metrics
[errant]: https://www.percona.com/blog/2014/05/19/errant-transactions-major-hurdle-for-gtid-based-failover-in-mysql-5-6/
[moco-agent]: https://github.com/cybozu-go/moco-agent/
[mysqld_exporter]: https://github.com/prometheus/mysqld_exporter/
