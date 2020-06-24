`moco-controller`
================

Usage
-----

```
Usage:
  moco-controller [flags]

Flags:
      --conf-init-container-image string   The container image name of moco-conf-gen (default " quay.io/cybozu/moco-conf-gen:1.0.0")
      --constant-conf-configmap string     The ConfigMap name of the constant (== forced) MySQL configration
      --default-conf-configmap string      The ConfigMap name of the default MySQL configration
  -h, --help                               help for moco-controller
      --leader-election-id string          ID for leader election by controller-runtime (default "moco")
      --metrics-addr string                The address the metric endpoint binds to (default ":8080")
      --version                            version for moco-controller

klog flags:
      --add_dir_header                     If true, adds the file directory to the header
      --alsologtostderr                    log to standard error as well as files
      --log_backtrace_at traceLocation     when logging hits line file:N, emit a stack trace (default :0)
      --log_dir string                     If non-empty, write log files in this directory
      --log_file string                    If non-empty, use this log file
      --log_file_max_size uint             Defines the maximum size a log file can grow to. Unit is megabytes. If the value is 0, the maximum file size is unlimited. (default 1800)
      --logtostderr                        log to standard error instead of files (default true)
      --skip_headers                       If true, avoid header prefixes in the log messages
      --skip_log_headers                   If true, avoid headers when opening log files
      --stderrthreshold severity           logs at or above this threshold go to stderr (default 2)
  -v, --v Level                            number for the log level verbosity
      --vmodule moduleSpec                 comma-separated list of pattern=N settings for file-filtered logging
```
