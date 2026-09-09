`moco-controller`
================

`moco-controller` controls MySQL clusters on Kubernetes.
## Environment variables

| Name            | Required | Description                                      |
| --------------- | -------- | ------------------------------------------------ |
| `POD_NAMESPACE` | Yes      | The namespace name where `moco-controller` runs. |

## Command line flags

```
Flags:
      --agent-image string                  The image of moco-agent sidecar container (default "ghcr.io/cybozu-go/moco-agent:0.15.0")
      --apiserver-qps-throttle int          The maximum QPS to the API server. (default 20)
      --backup-image string                 The image of moco-backup container (default "ghcr.io/cybozu-go/moco-backup:0.23.2")
      --cert-dir string                     webhook certificate directory
      --check-interval duration             Interval of cluster maintenance (default 1m0s)
      --disable-default-security-context    Disable injecting default runAsUser/runAsGroup on managed containers and fsGroup on managed pods. Enable this on platforms such as OpenShift that assign project-scoped UID/GID/fsGroup ranges.
      --fluent-bit-image string             The image of fluent-bit sidecar container (default "ghcr.io/cybozu-go/moco/fluent-bit:3.0.2.1")
      --grpc-cert-dir string                gRPC certificate directory (default "/grpc-cert")
      --health-probe-addr string            Listen address for health probes (default ":8081")
  -h, --help                                help for moco-controller
      --leader-election-id string           ID for leader election by controller-runtime (default "moco")
      --log_backtrace_at traceLocation      when logging hits line file:N, emit a stack trace (only for klog entries, e.g. from client-go) (default :0)
      --max-concurrent-reconciles int       The maximum number of concurrent reconciles which can be run (default 8)
      --metrics-addr string                 Listen address for metric endpoint (default ":8080")
      --mysql-configmap-history-limit int   The maximum number of MySQLConfigMap's history to be kept (default 10)
      --mysqld-exporter-image string        The image of mysqld_exporter sidecar container (default "ghcr.io/cybozu-go/moco/mysqld_exporter:0.15.1.2")
      --pprof-addr string                   Listen address for pprof endpoints. pprof is disabled by default
      --pvc-sync-annotation-keys strings    The keys of annotations from MySQLCluster's volumeClaimTemplates to be synced to the PVC
      --pvc-sync-label-keys strings         The keys of labels from MySQLCluster's volumeClaimTemplates to be synced to the PVC
      --partition-update-interval duration  The minimum update interval for partitions (e.g., 5s, 100ms) (default: 0 ms)
  -v, --v Level                             number for the log level verbosity (only for klog entries, e.g. from client-go). --zap-log-level needs to be raised as well to see them
      --version                             version for moco-controller
      --vmodule moduleSpec                  comma-separated list of pattern=N settings for file-filtered logging (only for klog entries, e.g. from client-go)
      --webhook-addr string                 Listen address for the webhook endpoint (default ":9443")
      --zap-devel                           Development Mode defaults(encoder=consoleEncoder,logLevel=Debug,stackTraceLevel=Warn). Production Mode defaults(encoder=jsonEncoder,logLevel=Info,stackTraceLevel=Error)
      --zap-encoder encoder                 Zap log encoding (one of 'json' or 'console')
      --zap-log-level level                 Zap Level to configure the verbosity of logging. Can be one of 'debug', 'info', 'error', or any integer value > 0 which corresponds to custom debug levels of increasing verbosity
      --zap-stacktrace-level level          Zap Level at and above which stacktraces are captured (one of 'info', 'error', 'panic').
      --zap-time-encoding time-encoding     Zap time encoding (one of 'epoch', 'millis', 'nano', 'iso8601', 'rfc3339' or 'rfc3339nano'). Defaults to 'epoch'.
```
