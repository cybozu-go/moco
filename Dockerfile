# Build the moco-controller binary
FROM quay.io/cybozu/golang:1.16-focal as builder

WORKDIR /workspace

# Copy the go source
COPY ./ .

# Build
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o moco-controller ./cmd/moco-controller/main.go

# stage2
FROM scratch
LABEL org.opencontainers.image.source https://github.com/cybozu-go/moco

WORKDIR /
COPY --from=builder /workspace/moco-controller ./
USER 10000:10000

ENTRYPOINT ["/moco-controller"]
