ARG MYSQL_VERSION=8.0.20.1
FROM quay.io/cybozu/golang:1.13-bionic as builder

WORKDIR /workspace

COPY ./ .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o entrypoint ./cmd/entrypoint

FROM quay.io/yokaze/moco-mysql:${MYSQL_VERSION}

COPY --from=builder /workspace/entrypoint /entrypoint
