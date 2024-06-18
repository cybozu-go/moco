# mysqld_exporter container

# Stage1: build from source
FROM ghcr.io/cybozu/golang:1.22-jammy AS build

ARG MYSQLD_EXPORTER_VERSION=v0.15.1

COPY mysql84.patch .

RUN git clone -b ${MYSQLD_EXPORTER_VERSION} --depth 1 https://github.com/prometheus/mysqld_exporter \
    && patch -d mysqld_exporter -Np1 < mysql84.patch \
    && make -C mysqld_exporter build

# Stage2: setup runtime container
FROM scratch

COPY --from=build /work/mysqld_exporter/LICENSE /LICENSE
COPY --from=build /work/mysqld_exporter/mysqld_exporter /mysqld_exporter

USER 10000:10000
EXPOSE 9104

ENTRYPOINT ["/mysqld_exporter"]
