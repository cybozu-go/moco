How to Build MySQL Container Image
==================================

The image of MySQL containers is specified via `.spec.podTemplate.spec.containers[<n>].image` in a [`MySQLCluster`](crd_mysql_cluster.md) custom resource, where `.spec.podTemplate.spec.containers[<n>].name` is `mysqld`.
You can specify your own container image for MySQL.
You can build the image from source code or modify the existing image to, for example, include plugins.

MOCO imposes the following conditions on the container images.

* The executable files of `mysqld`, `mysql`, `mysqladmin`, and `mysql_tzinfo_to_sql` must be included.
  They must be callable without directory paths.
* The default command to be executed must be specified with `ENTRYPOINT` unless `.spec.podTemplate.spec.containers[<n>].command` is specified.
  You cannot use `CMD`.
* The agent executable file must be included as `/entrypoint`.
  The agent is published as a github.com release asset with the URL of `https://github.com/cybozu-go/moco/releases/download/v<version>/agent`.
* Only the versions of 8.0.18 and 8.0.20 are tested.

Dockerfile Examples
-------------------

### Import MOCO assets to official MySQL image

This example imports `agent` and `ping.sh` from MOCO's github.com repository into the official MySQL image.
`ping.sh` is not mandatory, but it is very useful for probing.

The original image has an entrypoint script to initialize and configure MySQL.
This script should not be executed in MOCO, so the example overrides `ENTRYPONIT`.

Note that this image is configured as `secure_file_priv = /var/lib/mysql-files`.
Please prepare the directory or disable import/export operations with `secure_file_priv = NULL` in `MySQLCluster`.

```
ARG MOCO_VERSION=0.4.0
ARG MYSQL_VERSION=8.0.20

FROM alpine AS download

ARG MOCO_VERSION

RUN apk add --no-cache curl \
  && curl -fsSL -o /entrypoint https://github.com/cybozu-go/moco/releases/download/v${MOCO_VERSION}/agent \
  && chmod +x /entrypoint \
  && curl -fsSL -o /ping.sh https://github.com/cybozu-go/moco/raw/v${MOCO_VERSION}/ping.sh \
  && chmod +x /ping.sh

FROM mysql:${MYSQL_VERSION}

COPY --from=download /entrypoint /entrypoint
COPY --from=download /ping.sh /ping.sh

# The docker official MySQL image doesn't have `/var/lib/mysql-files` which is the default path of the `secure_file_priv` option.
# Please create the directory as follows or specify the `secure_file_priv` option explicitly.
# 999 is the user ID of `mysql` user in the docker official MySQL image.
RUN mkdir -p /var/lib/mysql-files \
    && chown -R 999:999 /var/lib/mysql-files

ENTRYPOINT ["mysqld"]
```

### Build MySQL from source code

This example builds MySQL from source code.

```
FROM ubuntu:20.04 AS build

ARG MOCO_VERSION=0.4.0
ARG MYSQL_VERSION=8.0.20

RUN apt-get update \
  && apt-get install -y curl \
  && curl -fsSL -o /entrypoint https://github.com/cybozu-go/moco/releases/download/v${MOCO_VERSION}/agent \
  && chmod +x /entrypoint \
  && curl -fsSL -o /ping.sh https://github.com/cybozu-go/moco/raw/v${MOCO_VERSION}/ping.sh \
  && chmod +x /ping.sh

RUN apt-get install -y cmake libncurses5-dev libjemalloc-dev libnuma-dev libreadline-dev libssl-dev pkg-config \
  && curl -fsSL -O https://dev.mysql.com/get/Downloads/MySQL-8.0/mysql-boost-${MYSQL_VERSION}.tar.gz \
  && tar -x -z -f mysql-boost-${MYSQL_VERSION}.tar.gz \
  && cd mysql-${MYSQL_VERSION} \
  && mkdir bld \
  && cd bld \
  && cmake .. -DBUILD_CONFIG=mysql_release -DCMAKE_BUILD_TYPE=Release -DWITH_BOOST=$(ls -d ../boost/boost_*) -DWITH_NUMA=1 -DWITH_JEMALLOC=1 \
  && make -j $(nproc) \
  && make install

FROM ubuntu:20.04

RUN apt-get update \
  && env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends libncurses5 libjemalloc1 libnuma1 libreadline7 libssl1.1 locales tzdata \
  && rm -rf /var/lib/apt/lists/* \
  && mkdir -p /var/lib/mysql \
  && chown -R 10000:10000 /var/lib/mysql

COPY --from=build /usr/local/mysql/LICENSE /usr/local/mysql/LICENSE
COPY --from=build /usr/local/mysql/bin /usr/local/mysql/bin
COPY --from=build /usr/local/mysql/lib /usr/local/mysql/lib
COPY --from=build /usr/local/mysql/share /usr/local/mysql/share
COPY --from=build /entrypoint /entrypoint
COPY --from=build /ping.sh /ping.sh

ENV PATH=/usr/local/mysql/bin:"$PATH"
VOLUME /var/lib/mysql
ENTRYPOINT ["mysqld"]
HEALTHCHECK CMD /ping.sh
EXPOSE 3306 33060 33062 8080
USER 10000:10000
```
