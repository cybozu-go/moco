# Build the moco-controller binary
FROM --platform=$BUILDPLATFORM ghcr.io/cybozu/golang:1.21-focal as builder

ARG TARGETARCH

# Copy the go source
COPY ./ .

# Build
RUN GOARCH=${TARGETARCH} CGO_ENABLED=0 go build -ldflags="-w -s" -o moco-controller ./cmd/moco-controller
RUN GOARCH=${TARGETARCH} go build -ldflags="-w -s" -o moco-backup ./cmd/moco-backup

# the controller image
FROM --platform=$TARGETPLATFORM scratch as controller
LABEL org.opencontainers.image.source https://github.com/cybozu-go/moco

COPY --from=builder /work/moco-controller ./
USER 10000:10000

ENTRYPOINT ["/moco-controller"]

# For MySQL binaries
FROM --platform=$TARGETPLATFORM ghcr.io/cybozu-go/moco/mysql:8.0.32.2 as mysql

# the backup image
FROM --platform=$TARGETPLATFORM quay.io/cybozu/ubuntu:20.04
LABEL org.opencontainers.image.source https://github.com/cybozu-go/moco

ARG MYSQLSH_VERSION=8.0.30-1

COPY --from=builder /work/moco-backup /moco-backup

COPY --from=mysql /usr/local/mysql/LICENSE         /usr/local/mysql/LICENSE
COPY --from=mysql /usr/local/mysql/bin/mysqlbinlog /usr/local/mysql/bin/mysqlbinlog
COPY --from=mysql /usr/local/mysql/bin/mysql       /usr/local/mysql/bin/mysql

RUN apt-get update \
  && apt-get install -y --no-install-recommends libjemalloc2 zstd python3 libpython3.8 s3cmd \
  && rm -rf /var/lib/apt/lists/* \
  && curl -o /tmp/mysqlsh.deb -fsL https://dev.mysql.com/get/Downloads/MySQL-Shell/mysql-shell_${MYSQLSH_VERSION}ubuntu20.04_amd64.deb \
  && dpkg -i /tmp/mysqlsh.deb \
  && rm -f /tmp/mysqlsh.deb

ENV PATH=/usr/local/mysql/bin:"$PATH"
USER 10000:10000
ENTRYPOINT ["/moco-backup"]
