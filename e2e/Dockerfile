ARG MYSQL_VERSION
FROM quay.io/cybozu/golang:1.15-focal as builder

WORKDIR /workspace

COPY ./ .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o entrypoint ./cmd/entrypoint

FROM quay.io/cybozu/moco-mysql:${MYSQL_VERSION}

COPY --from=builder /workspace/entrypoint /entrypoint
