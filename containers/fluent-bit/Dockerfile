# Fluent Bit container image

# Stage1: build from source
FROM quay.io/cybozu/ubuntu-dev:22.04 AS builder

ARG FLUENT_BIT_VERSION=2.1.8

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# https://github.com/fluent/fluent-bit/blob/master/dockerfiles/Dockerfile
ARG FLB_TARBALL=https://github.com/fluent/fluent-bit/archive/v$FLUENT_BIT_VERSION.tar.gz
ENV FLB_SOURCE $FLB_TARBALL
RUN mkdir -p /fluent-bit/bin /fluent-bit/etc /fluent-bit/log /tmp/fluent-bit-master/

ENV DEBIAN_FRONTEND noninteractive

RUN apt-get update && apt-get -y install --no-install-recommends \
    build-essential \
    curl \
    ca-certificates \
    cmake \
    make \
    tar \
    libssl-dev \
    libsasl2-dev \
    pkg-config \
    zlib1g-dev \
    libpq-dev \
    flex \
    bison \
    && curl -L -o "/tmp/fluent-bit.tar.gz" ${FLB_SOURCE} \
    && cd tmp/ && mkdir fluent-bit \
    && tar zxfv fluent-bit.tar.gz -C ./fluent-bit --strip-components=1 \
    && cd fluent-bit/build/ \
    && rm -rf /tmp/fluent-bit/build/*

WORKDIR /tmp/fluent-bit/build/
RUN cmake -DFLB_RELEASE=On \
          -DFLB_TRACE=Off \
          -DFLB_JEMALLOC=On \
          -DFLB_TLS=On \
          -DFLB_SHARED_LIB=Off \
          -DFLB_EXAMPLES=Off \
          -DFLB_HTTP_SERVER=On \
          -DFLB_IN_SYSTEMD=Off \
          -DFLB_OUT_KAFKA=On \
          -DFLB_CONFIG_YAML=No \
          .. \
    && make -j $(getconf _NPROCESSORS_ONLN) \
    && strip bin/fluent-bit \
    && mkdir -p /fluent-bit/bin \
    && cp bin/fluent-bit /fluent-bit/bin/ \
    && mkdir -p /fluent-bit/etc \
    && cd /tmp/fluent-bit \
    && cp -a conf/fluent-bit.conf \
        conf/parsers.conf \
        conf/parsers_ambassador.conf \
        conf/parsers_java.conf \
        conf/parsers_extra.conf \
        conf/parsers_openstack.conf \
        conf/parsers_cinder.conf \
        conf/plugins.conf \
        /fluent-bit/etc/ \
    && cp LICENSE /fluent-bit/

# Stage2: setup runtime container
FROM quay.io/cybozu/ubuntu:22.04

COPY --from=builder /fluent-bit /fluent-bit

EXPOSE 2020

ENTRYPOINT ["/fluent-bit/bin/fluent-bit"]
CMD ["-q", "-c", "/fluent-bit/etc/fluent-bit.conf"]
