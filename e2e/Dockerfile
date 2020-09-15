FROM quay.io/cybozu/golang:1.13-bionic as builder

WORKDIR /workspace

COPY go.mod go.mod
COPY vendor/ vendor/
COPY cmd/ cmd/
COPY agent/ agent/
COPY api/ api/
COPY accessor/ accessor/
COPY constants.go constants.go
COPY utils.go utils.go
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -mod=vendor -a -o entrypoint ./cmd/entrypoint

FROM quay.io/cybozu/moco-mysql:8.0.20.4

COPY --from=builder /workspace/entrypoint /entrypoint
