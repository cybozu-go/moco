FROM ghcr.io/cybozu/ubuntu-dev:24.04 AS builder

ARG MYSQL_VERSION=8.0.44

RUN apt-get update && apt-get -y install --no-install-recommends \
    cmake \
    libncurses5-dev \
    libgoogle-perftools-dev \
    libnuma-dev \
    libaio-dev \
    libtirpc-dev \
    pkg-config

RUN cd tmp/ \
    && curl -fsSL -O https://dev.mysql.com/get/Downloads/MySQL-8.0/mysql-boost-${MYSQL_VERSION}.tar.gz \
    && tar -x -z -f mysql-boost-${MYSQL_VERSION}.tar.gz \
    && cd mysql-${MYSQL_VERSION} \
    && mkdir bld \
    && cd bld \
    && cmake .. -DBUILD_CONFIG=mysql_release -DCMAKE_BUILD_TYPE=Release -DWITH_BOOST=$(ls -d ../boost/boost_*) -DWITH_NUMA=1 -DWITH_TCMALLOC=1 -DWITH_PACKAGE_FLAGS=0 \
    && make -j 20 \
    && make install

FROM ghcr.io/cybozu/ubuntu:24.04

COPY --from=builder /usr/local/mysql/LICENSE /usr/local/mysql/LICENSE
COPY --from=builder /usr/local/mysql/bin /usr/local/mysql/bin
COPY --from=builder /usr/local/mysql/lib /usr/local/mysql/lib
COPY --from=builder /usr/local/mysql/share /usr/local/mysql/share

RUN apt-get update \
  && apt-get install -y --no-install-recommends libgoogle-perftools4 libnuma1 libaio1t64 libtirpc3t64 \
  && rm -rf /var/lib/apt/lists/* \
  && mkdir -p /var/lib/mysql \
  && chown -R 10000:10000 /var/lib/mysql

ENV PATH=/usr/local/mysql/bin:"$PATH"
VOLUME /var/lib/mysql
ENTRYPOINT ["mysqld"]
EXPOSE 3306 33060 33062 8080
USER 10000:10000
