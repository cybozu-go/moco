`moco-controller`
================

Usage
-----

```
Usage:
  moco-controller [flags]

Flags:
      --add_dir_header                       If true, adds the file directory to the header
      --alsologtostderr                      log to standard error as well as files
      --binary-copy-container-image string   The container image name that includes moco's binaries (default "ghcr.io/cybozu-go/moco-agent:0.2.0")
      --conn-max-lifetime duration           The maximum amount of time a connection may be reused (default 30m0s)
      --connection-timeout duration          Dial timeout (default 3s)
      --curl-container-image string          The container image name of curl (default "quay.io/cybozu/ubuntu:20.04")
  -h, --help                                 help for moco-controller
      --leader-election-id string            ID for leader election by controller-runtime (default "moco")
      --log_backtrace_at traceLocation       when logging hits line file:N, emit a stack trace (default :0)
      --log_dir string                       If non-empty, write log files in this directory
      --log_file string                      If non-empty, use this log file
      --log_file_max_size uint               Defines the maximum size a log file can grow to. Unit is megabytes. If the value is 0, the maximum file size is unlimited. (default 1800)
      --logfile string                       Log filename
      --logformat string                     Log format [plain,logfmt,json]
      --loglevel string                      Log level [critical,error,warning,info,debug]
      --logtostderr                          log to standard error instead of files (default true)
      --metrics-addr string                  The address the metric endpoint binds to (default ":8080")
      --read-timeout duration                I/O read timeout (default 30s)
      --skip_headers                         If true, avoid header prefixes in the log messages
      --skip_log_headers                     If true, avoid headers when opening log files
      --stderrthreshold severity             logs at or above this threshold go to stderr (default 2)
  -v, --v Level                              number for the log level verbosity
      --version                              version for moco-controller
      --vmodule moduleSpec                   comma-separated list of pattern=N settings for file-filtered logging
      --wait-time duration                   The waiting time which some tasks are under processing (default 10s)
```
