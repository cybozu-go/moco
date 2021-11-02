# Installing kubectl-moco

[kubectl-moco](kubectl-moco.md) is a plugin for `kubectl` to control MySQL clusters of MOCO.

Pre-built binaries are available on [GitHub releases](https://github.com/cybozu-go/moco/releases) for Windows, Linux, and MacOS.

## Installing using Krew

[Krew](https://krew.sigs.k8s.io/) is the plugin manager for kubectl command-line tool.

See the [documentation](https://krew.sigs.k8s.io/docs/user-guide/setup/install/) for how to install Krew.

```console
$ kubectl krew update
$ kubectl krew install moco
```

## Installing manually

1. Set `OS` to the operating system name

   OS is one of `linux`, `windows`, or `darwin` (MacOS).

   If Go is available, `OS` can be set automatically as follows:

    ```console
    $ OS=$(go env GOOS)
    ```

2. Set `ARCH` to the architecture name

   ARCH is one of `amd64` or `arm64`.

   If Go is available, `ARCH` can be set automatically as follows:

    ```console
    $ ARCH=$(go env GOARCH)
    ```

3. Set `VERSION` to the MOCO version

   See the MOCO release page: https://github.com/cybozu-go/moco/releases

   ```console
   $ VERSION=< The version you want to install >
   ```

4. Download the binary and put it in a directory of your `PATH`.

   The following is an example to install the plugin in `/usr/local/bin`.

    ```console
    $ curl -L -sS https://github.com/cybozu-go/moco/releases/download/$(VERSION)/kubectl-moco_$(VERSION)_$(OS)_$(ARCH).tar.gz \
      | tar xz -C /usr/local/bin kubectl-moco
    ```

5. Check the installation by running `kubectl moco -h`.

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
