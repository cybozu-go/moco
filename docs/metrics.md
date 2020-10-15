Metrics
=======

## Controller

MOCO controller exposes the following metrics with the Prometheus format.  All these metrics are prefixed with `moco_controller_`

| Name                     | Description                                        | Type    | Labels               |
| ------------------------ | -------------------------------------------------- | ------- | -------------------- |
| operation_phase          | The operation is in the labeled phase or not       | Gauge   | cluster_name, phase  |
| failover_count_total     | The failover count                                 | Counter | cluster_name         |
| total_replicas           | The number of replicas                             | Gauge   | cluster_name         |
| synced_replicas          | The number of replicas which are in "synced" state | Gauge   | cluster_name         |
| cluster_violation_status | The cluster status about violation condition       | Gauge   | cluster_name, status |
| cluster_failure_status   | The cluster status about failure condition         | Gauge   | cluster_name, status |
| cluster_healthy_status   | The cluster status about healthy condition         | Gauge   | cluster_name, status |
| cluster_available_status | The cluster status about available condition       | Gauge   | cluster_name, status |

Note that MOCO controller also exposes the metrics provided by the Prometheus client library which located under `go` and `process` namespaces.

## Agents

MOCO agents expose the following metrics with the Prometheus format.  All these metrics are prefixed with `moco_agent_`

| Name                          | Description                      | Type      | Labels       |
| ----------------------------- | -------------------------------- | --------- | ------------ |
| clone_duration_seconds        | The time took to clone operation | Histogram | cluster_name |
| clone_count                   | The clone operation count        | Counter   | cluster_name |
| clone_failure_count           | The failed clone operation count | Counter   | cluster_name |
| log_rotation_duration_seconds | The time took to log rotation    | Histogram | cluster_name |
| log_rotation_count            | The log rotation count           | Counter   | cluster_name |
| log_rotation_failure_count    | The failed log rotation count    | Counter   | cluster_name |
