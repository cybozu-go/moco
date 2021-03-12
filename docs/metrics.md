Metrics
=======

## Controller

MOCO controller exposes the following metrics with the Prometheus format.  All these metrics are prefixed with `moco_controller_`

| Name                     | Description                                        | Type    | Labels              |
| ------------------------ | -------------------------------------------------- | ------- | ------------------- |
| operation_phase          | The operation is in the labeled phase or not       | Gauge   | cluster_name, phase |
| failover_count_total     | The failover count                                 | Counter | cluster_name        |
| total_replicas           | The number of replicas                             | Gauge   | cluster_name        |
| synced_replicas          | The number of replicas which are in "synced" state | Gauge   | cluster_name        |
| cluster_violation_status | The cluster status about violation condition       | Gauge   | cluster_name        |
| cluster_failure_status   | The cluster status about failure condition         | Gauge   | cluster_name        |
| cluster_healthy_status   | The cluster status about healthy condition         | Gauge   | cluster_name        |
| cluster_available_status | The cluster status about available condition       | Gauge   | cluster_name        |

Note that MOCO controller also exposes the metrics provided by the Prometheus client library which located under `go` and `process` namespaces. [The metrics](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/internal/controller/metrics) exposed by `controller-runtime` is also available.
