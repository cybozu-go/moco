ARG MYSQL_VERSION
FROM quay.io/cybozu/golang:1.15-focal as builder

WORKDIR /workspace

COPY ./ .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o entrypoint ./cmd/entrypoint

FROM mysql:${MYSQL_VERSION}

COPY --from=builder /workspace/entrypoint /entrypoint
COPY --from=builder /workspace/ping.sh /ping.sh

# 999 is the user ID of `mysql` user in the docker official MySQL container.
RUN mkdir -p /var/lib/mysql-files \
    && chown -R 999:999 /var/lib/mysql-files

ENTRYPOINT ["mysqld"]
