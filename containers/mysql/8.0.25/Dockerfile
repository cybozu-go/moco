FROM quay.io/cybozu/ubuntu-dev:20.04 AS builder

ARG MYSQL_VERSION=8.0.25

RUN apt-get update && apt-get -y install --no-install-recommends \
    cmake \
    libncurses5-dev \
    libjemalloc-dev \
    libnuma-dev \
    libaio-dev \
    pkg-config

RUN cd tmp/ \
    && curl -fsSL -O https://dev.mysql.com/get/Downloads/MySQL-8.0/mysql-boost-${MYSQL_VERSION}.tar.gz \
    && tar -x -z -f mysql-boost-${MYSQL_VERSION}.tar.gz \
    && cd mysql-${MYSQL_VERSION} \
    && mkdir bld \
    && cd bld \
    && cmake .. -DBUILD_CONFIG=mysql_release -DCMAKE_BUILD_TYPE=Release -DWITH_BOOST=$(ls -d ../boost/boost_*) -DWITH_NUMA=1 -DWITH_JEMALLOC=1 -DWITH_PACKAGE_FLAGS=0 \
    && make -j 20 \
    && make install

FROM quay.io/cybozu/ubuntu:20.04

COPY --from=builder /usr/local/mysql/LICENSE /usr/local/mysql/LICENSE
COPY --from=builder /usr/local/mysql/bin /usr/local/mysql/bin
COPY --from=builder /usr/local/mysql/lib /usr/local/mysql/lib
COPY --from=builder /usr/local/mysql/share /usr/local/mysql/share

RUN apt-get update \
  && apt-get install -y --no-install-recommends libjemalloc2 libnuma1 libaio1 \
  && rm -rf /var/lib/apt/lists/* \
  && mkdir -p /var/lib/mysql \
  && chown -R 10000:10000 /var/lib/mysql

ENV PATH=/usr/local/mysql/bin:"$PATH"
VOLUME /var/lib/mysql
ENTRYPOINT ["mysqld"]
EXPOSE 3306 33060 33062 8080
USER 10000:10000
