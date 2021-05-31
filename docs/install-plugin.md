# Installing kubectl-moco

[kubectl-moco](kubectl-moco.md) is a plugin for `kubectl` to control MySQL clusters of MOCO.

Pre-built binaries are available on [GitHub releases](https://github.com/cybozu-go/moco/releases) for Windows, Linux, and MacOS.

Download one of the binaries for your OS and place it in a directory of `PATH`.

```console
$ curl -fsL -o /path/to/bin/kubectl-moco https://github.com/cybozu-go/moco/releases/latest/download/kubectl-moco-linux-amd64
$ chmod a+x /path/to/bin/kubectl-moco
```

Check the installation by running `kubectl moco -h`.

```console
$ kubectl moco -h
the utility command for MOCO.

Usage:
  kubectl-moco [command]

Available Commands:
  credential  Fetch the credential of a specified user
  help        Help about any command
  mysql       Run mysql command in a specified MySQL instance
  switchover  Switch the primary instance

...
```
