# Building custom image of `mysqld`

There are pre-built `mysqld` container images for MOCO on [`ghcr.io/cybozu-go/moco/mysql`](https://github.com/cybozu-go/moco/pkgs/container/moco%2Fmysql).
Users can use one of these images to supply `mysqld` container in [MySQLCluster](crd_mysqlcluster.md) like:

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: MySQLCluster
spec:
  podTemplate:
    spec:
      containers:
      - name: mysqld
        image: ghcr.io/cybozu-go/moco/mysql:8.4.7
```

If you want to build and use your own `mysqld`, read the rest of this document.

## Dockerfile

The easiest way to build a custom `mysqld` for MOCO is to copy and edit our Dockerfile.
You can find it under [`containers/mysql` directory in `github.com/cybozu-go/moco`](https://github.com/cybozu-go/moco/tree/main/containers/mysql).

You should keep the following points:

- `ENTRYPOINT` should be `["mysqld"]`
- `USER` should be `10000:10000`
- `sleep` command must exist in one of the `PATH` directories.

## How to build `mysqld`

On Ubuntu 24.04, you can build the source code as follows:

```console
$ sudo apt-get update
$ sudo apt-get -y --no-install-recommends install build-essential libssl-dev \
    cmake libncurses5-dev libgoogle-perftools-dev libnuma-dev libaio-dev pkg-config libtirpc-dev
$ curl -fsSL -O https://dev.mysql.com/get/Downloads/MySQL-8.4/mysql-8.4.7.tar.gz
$ tar -x -z -f mysql-8.4.7.tar.gz
$ cd mysql-8.4.7
$ mkdir bld
$ cd bld
$ cmake .. -DBUILD_CONFIG=mysql_release -DCMAKE_BUILD_TYPE=Release \
    -DWITH_NUMA=1 -DWITH_TCMALLOC=1
$ make -j $(nproc)
$ make install
```
