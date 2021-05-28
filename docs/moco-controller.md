`moco-controller`
================

## Environment variables

| Name            | Required | Description                                      |
| --------------- | -------- | ------------------------------------------------ |
| `POD_NAMESPACE` | Yes      | The namespace name where `moco-controller` runs. |

## Command line flags

```
Flags:
      --add_dir_header                   If true, adds the file directory to the header
      --agent-image string               The image of moco-agent sidecar container
      --alsologtostderr                  log to standard error as well as files
      --backup-image string              The image of moco-backup container
      --cert-dir string                  webhook certificate directory
      --check-interval duration          Interval of cluster maintenance (default 1m0s)
      --fluent-bit-image string          The image of fluent-bit sidecar container
      --grpc-cert-dir string             gRPC certificate directory (default "/grpc-cert")
      --health-probe-addr string         Listen address for health probes (default ":8081")
  -h, --help                             help for moco-controller
      --leader-election-id string        ID for leader election by controller-runtime (default "moco")
      --log_backtrace_at traceLocation   when logging hits line file:N, emit a stack trace (default :0)
      --log_dir string                   If non-empty, write log files in this directory
      --log_file string                  If non-empty, use this log file
      --log_file_max_size uint           Defines the maximum size a log file can grow to. Unit is megabytes. If the value is 0, the maximum file size is unlimited. (default 1800)
      --logtostderr                      log to standard error instead of files (default true)
      --metrics-addr string              The address the metric endpoint binds to (default ":8080")
      --mysqld-exporter-image string     The image of mysqld_exporter sidecar container
      --skip_headers                     If true, avoid header prefixes in the log messages
      --skip_log_headers                 If true, avoid headers when opening log files
      --stderrthreshold severity         logs at or above this threshold go to stderr (default 2)
  -v, --v Level                          number for the log level verbosity
      --version                          version for moco-controller
      --vmodule moduleSpec               comma-separated list of pattern=N settings for file-filtered logging
      --webhook-addr string              Listen address for the webhook endpoint (default ":9443")
      --zap-devel                        Development Mode defaults(encoder=consoleEncoder,logLevel=Debug,stackTraceLevel=Warn). Production Mode defaults(encoder=jsonEncoder,logLevel=Info,stackTraceLevel=Error)
      --zap-encoder encoder              Zap log encoding (one of 'json' or 'console')
      --zap-log-level level              Zap Level to configure the verbosity of logging. Can be one of 'debug', 'info', 'error', or any integer value > 0 which corresponds to custom debug levels of increasing verbosity
      --zap-stacktrace-level level       Zap Level at and above which stacktraces are captured (one of 'info', 'error', 'panic').
```
