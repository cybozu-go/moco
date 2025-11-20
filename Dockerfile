# Build the moco-controller binary
FROM --platform=$BUILDPLATFORM ghcr.io/cybozu/golang:1.24-noble as builder

ARG TARGETARCH

# Copy the go source
COPY ./ .

# Build
RUN GOARCH=${TARGETARCH} CGO_ENABLED=0 go build -ldflags="-w -s" -o moco-controller ./cmd/moco-controller
RUN GOARCH=${TARGETARCH} go build -ldflags="-w -s" -o moco-backup ./cmd/moco-backup

# the controller image
FROM --platform=$TARGETPLATFORM scratch as controller
LABEL org.opencontainers.image.source=https://github.com/cybozu-go/moco

COPY --from=builder /work/moco-controller ./
USER 10000:10000

ENTRYPOINT ["/moco-controller"]

# For MySQL binaries
FROM --platform=$TARGETPLATFORM ghcr.io/cybozu-go/moco/mysql:8.4.7.1 as mysql

# the backup image
FROM --platform=$TARGETPLATFORM ghcr.io/cybozu/ubuntu:24.04
LABEL org.opencontainers.image.source=https://github.com/cybozu-go/moco

ARG MYSQLSH_VERSION=8.4.7
ARG MYSQLSH_GLIBC_VERSION=2.28
ARG TARGETARCH

COPY --from=builder /work/moco-backup /moco-backup

COPY --from=mysql /usr/local/mysql/LICENSE         /usr/local/mysql/LICENSE
COPY --from=mysql /usr/local/mysql/bin/mysqlbinlog /usr/local/mysql/bin/mysqlbinlog
COPY --from=mysql /usr/local/mysql/bin/mysql       /usr/local/mysql/bin/mysql

RUN apt-get update \
  && apt-get install -y --no-install-recommends zstd python3 libpython3.10 s3cmd libgoogle-perftools4 \
  && rm -rf /var/lib/apt/lists/* \
  && if [ "${TARGETARCH}" = 'amd64' ]; then MYSQLSH_ARCH='x86-64'; fi \
  && if [ "${TARGETARCH}" = 'arm64' ]; then MYSQLSH_ARCH='arm-64'; fi \
  && curl -o /tmp/mysqlsh.tar.gz -fsL "https://cdn.mysql.com/Downloads/MySQL-Shell/mysql-shell-${MYSQLSH_VERSION}-linux-glibc${MYSQLSH_GLIBC_VERSION}-${MYSQLSH_ARCH:-unknown}bit.tar.gz" \
  && mkdir /usr/local/mysql-shell \
  && tar -xf /tmp/mysqlsh.tar.gz -C /usr/local/mysql-shell --strip-components=1 \
  && rm -f /tmp/mysqlsh.tar.gz

ENV PATH=/usr/local/mysql/bin:/usr/local/mysql-shell/bin:"$PATH"
USER 10000:10000
ENTRYPOINT ["/moco-backup"]
