# For Go
GO111MODULE = on
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)
GOFLAGS := -mod=vendor
export GO111MODULE GOFLAGS

SUDO=sudo

# Image URL to use all building/pushing image targets
IMG ?= controller:latest
# Produce CRDs with apiextensions.k8s.io/v1
CRD_OPTIONS ?= "crd:crdVersions=v1"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

KUBEBUILDER_VERSION := 2.3.1
CTRLTOOLS_VERSION := 0.2.9

all: build/moco-controller

# Run tests
test:
	cd /tmp; GO111MODULE=on GOFLAGS= go install github.com/cybozu/neco-containers/golang/analyzer/cmd/custom-checker
	test -z "$$(gofmt -s -l . | grep -v '^vendor' | tee /dev/stderr)"
	test -z "$$(golint $$(go list -mod=vendor ./... | grep -v /vendor/) | tee /dev/stderr)"
	test -z "$$(nilerr ./... 2>&1 | tee /dev/stderr)"
	test -z "$$(custom-checker -restrictpkg.packages=html/template,log $$(go list -tags='$(GOTAGS)' ./... | grep -v /vendor/ ) 2>&1 | tee /dev/stderr)"
	ineffassign .
	go build -mod=vendor ./...
	go test -race -v -coverprofile cover.out ./...
	go vet ./...
	test -z "$$(go vet ./... | grep -v '^vendor' | tee /dev/stderr)"

# Build moco-controller binary
build/moco-controller: generate
	mkdir -p build
	GO111MODULE=on go build -o $@ ./cmd/moco-controller/main.go

# Build entrypoint binary
build/entrypoint:
	mkdir -p build
	GO111MODULE=on go build -o $@ ./cmd/entrypoint

# Generate manifests e.g. CRD, RBAC etc.
manifests:
	controller-gen $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases
	cp config/crd/bases/moco.cybozu.com_mysqlclusters.yaml deploy/crd.yaml

# Generate code
generate:
	controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."

mod:
	go mod tidy
	go mod vendor
	git add -f vendor
	git add go.mod

setup:
	$(SUDO) apt-get update
	curl -sL https://go.kubebuilder.io/dl/$(KUBEBUILDER_VERSION)/$(GOOS)/$(GOARCH) | tar -xz -C /tmp/
	$(SUDO) rm -rf /usr/local/kubebuilder
	$(SUDO) mv /tmp/kubebuilder_$(KUBEBUILDER_VERSION)_$(GOOS)_$(GOARCH) /usr/local/kubebuilder
	$(SUDO) curl -o /usr/local/kubebuilder/bin/kustomize -sL https://go.kubebuilder.io/kustomize/$(GOOS)/$(GOARCH)
	$(SUDO) chmod a+x /usr/local/kubebuilder/bin/kustomize
	cd /tmp; GO111MODULE=on GOFLAGS= go get sigs.k8s.io/controller-tools/cmd/controller-gen@v$(CTRLTOOLS_VERSION)

.PHONY:	all test manifests generate mod setup
