# Building custom image of `mysqld`

There are pre-built `mysqld` container images for MOCO on [`quay.io/cybozu/mysql`](https://quay.io/repository/cybozu/mysql?tag=latest&tab=tags).
Users can use one of these images to supply `mysqld` container in [MySQLCluster](crd_mysqlcluster.md) like:

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: MySQLCluster
spec:
  podTemplate:
    spec:
      containers:
      - name: mysqld
        image: quay.io/cybozu/mysql:8.0.30
```

If you want to build and use your own `mysqld`, read the rest of this document.

## Dockerfile

The easiest way to build a custom `mysqld` for MOCO is to copy and edit our Dockerfile.
You can find it under [`mysql` directory in `github.com/cybozu/neco-containers`](https://github.com/cybozu/neco-containers/tree/main/mysql).

You should keep the following points:

- `ENTRYPOINT` should be `["mysqld"]`
- `USER` should be `10000:10000`
- `sleep` command must exist in one of the `PATH` directories.

## How to build `mysqld`

On Ubuntu 20.04, you can build the source code as follows:

```console
$ sudo apt-get update
$ sudo apt-get -y --no-install-recommends install build-essential libssl-dev \
    cmake libncurses5-dev libjemalloc-dev libnuma-dev libaio-dev pkg-config
$ curl -fsSL -O https://dev.mysql.com/get/Downloads/MySQL-8.0/mysql-boost-8.0.20.tar.gz
$ tar -x -z -f mysql-boost-8.0.20.tar.gz
$ cd mysql-8.0.20
$ mkdir bld
$ cd bld
$ cmake .. -DBUILD_CONFIG=mysql_release -DCMAKE_BUILD_TYPE=Release \
    -DWITH_BOOST=$(ls -d ../boost/boost_*) -DWITH_NUMA=1 -DWITH_JEMALLOC=1
$ make -j $(nproc)
$ make install
```
