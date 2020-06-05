# Build the moco-controller binary
FROM golang:1.13 as builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY api/ api/
COPY controllers/ controllers/
COPY cmd/ cmd/
COPY constants.go constants.go
COPY version.go version.go

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o moco-controller ./cmd/moco-controller/main.go

# Use distroless as minimal base image to package the moco-controller binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/moco-controller ./
USER nonroot:nonroot

ENTRYPOINT ["/moco-controller"]
