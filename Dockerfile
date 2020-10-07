# Build the moco-controller binary
FROM quay.io/cybozu/golang:1.13-bionic as builder

WORKDIR /workspace

# Copy the go source
COPY ./ .

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -mod=vendor -a -o moco-controller ./cmd/moco-controller/main.go

FROM quay.io/cybozu/ubuntu:18.04
LABEL org.opencontainers.image.source https://github.com/cybozu-go/moco

WORKDIR /
COPY --from=builder /workspace/moco-controller ./
USER 10000:10000

ENTRYPOINT ["/moco-controller"]
