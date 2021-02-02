include common.mk

# For Go
GO111MODULE = on
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)

KUBEBUILDER_ASSETS := $(PWD)/bin
export KUBEBUILDER_ASSETS

CONTROLLER_GEN := $(PWD)/bin/controller-gen
KUBEBUILDER := $(PWD)/bin/kubebuilder
# Produce CRDs with apiextensions.k8s.io/v1
CRD_OPTIONS ?= "crd:crdVersions=v1"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN := $(shell go env GOPATH)/bin
else
GOBIN := $(shell go env GOBIN)
endif

GO_FILES := $(shell find . -path ./e2e -prune -o -name '*.go' -print)

KUBEBUILDER_VERSION := 2.3.1
CTRLTOOLS_VERSION := 0.4.0

.PHONY: all
all: build

.PHONY: validate
validate: setup
	test -z "$$(gofmt -s -l . | tee /dev/stderr)"
	staticcheck ./...
	test -z "$$(nilerr ./... 2>&1 | tee /dev/stderr)"
	test -z "$$(custom-checker -restrictpkg.packages=html/template,log $$(go list -tags='$(GOTAGS)' ./... ) 2>&1 | tee /dev/stderr)"
	ineffassign .
	go build ./...
	go vet ./...
	test -z "$$(go vet ./... | tee /dev/stderr)"

# Run tests
.PHONY: test
test: $(KUBEBUILDER)
	MYSQL_VERSION=$(MYSQL_VERSION) go test -race -v -coverprofile cover.out ./...

# Build all binaries
.PHONY: build
build: build/moco-controller build/kubectl-moco

# Build moco-controller binary
build/moco-controller: generate $(GO_FILES)
	mkdir -p build
	GO111MODULE=on go build -o $@ ./cmd/moco-controller/main.go

# Build kubectl-moco binary
build/kubectl-moco: $(GO_FILES)
	mkdir -p build
	GO111MODULE=on go build -o $@ ./cmd/kubectl-moco

# Generate manifests e.g. CRD, RBAC etc.
.PHONY: manifests
manifests: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases

# Generate code
.PHONY: generate
generate: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: mod
mod:
	go mod tidy
	git add go.mod

$(KUBEBUILDER):
	rm -rf tmp && mkdir -p tmp
	mkdir -p bin
	curl -sfL https://go.kubebuilder.io/dl/$(KUBEBUILDER_VERSION)/$(GOOS)/$(GOARCH) | tar -xz -C tmp/
	mv tmp/kubebuilder_$(KUBEBUILDER_VERSION)_$(GOOS)_$(GOARCH)/bin/* bin/
	curl -sfL https://github.com/kubernetes/kubernetes/archive/v$(KUBERNETES_VERSION).tar.gz | tar zxf - -C tmp/
	mv tmp/kubernetes-$(KUBERNETES_VERSION) tmp/kubernetes
	cd tmp/kubernetes; make all WHAT="cmd/kube-apiserver"
	mv tmp/kubernetes/_output/bin/kube-apiserver bin/
	rm -rf tmp

$(CONTROLLER_GEN):
	mkdir -p bin
	env GOBIN=$(PWD)/bin GOFLAGS= go install sigs.k8s.io/controller-tools/cmd/controller-gen

.PHONY: setup
setup: custom-checker staticcheck nilerr ineffassign

.PHONY: custom-checker
custom-checker:
	if ! which custom-checker >/dev/null; then \
		cd /tmp; env GOFLAGS= GO111MODULE=on go get github.com/cybozu/neco-containers/golang/analyzer/cmd/custom-checker; \
	fi

.PHONY: staticcheck
staticcheck:
	if ! which staticcheck >/dev/null; then \
		cd /tmp; env GOFLAGS= GO111MODULE=on go get honnef.co/go/tools/cmd/staticcheck; \
	fi

.PHONY: nilerr
nilerr:
	if ! which nilerr >/dev/null; then \
		cd /tmp; env GOFLAGS= GO111MODULE=on go get github.com/gostaticanalysis/nilerr/cmd/nilerr; \
	fi

.PHONY: ineffassign
ineffassign:
	if ! which ineffassign >/dev/null; then \
		cd /tmp; env GOFLAGS= GO111MODULE=on go get github.com/gordonklaus/ineffassign; \
	fi

.PHONY: clean
clean:
	rm -rf build
	rm -rf bin
