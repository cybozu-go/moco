Metrics
=======

MOCO exposes the following metrics with the Prometheus format.  All these metrics are prefixed with `moco_`

| Name            | Description                                   | Type    | Labels              |
| --------------- | --------------------------------------------- | ------- | ------------------- |
| operation_phase | The operation is in the labeled phase or not. | Gauge   | cluster_name, phase |
| failover_count  | The failover count.                           | Counter | cluster_name        |

Note that MOCO also exposes the metrics provided by the Prometheus client library which located under `go` and `process` namespaces.
