include common.mk

# For Go
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)

KUBEBUILDER_ASSETS := $(PWD)/bin
export KUBEBUILDER_ASSETS

CONTROLLER_GEN := $(PWD)/bin/controller-gen
KUBEBUILDER := $(PWD)/bin/kubebuilder
PROTOC := $(PWD)/bin/protoc
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
CTRLTOOLS_VERSION := 0.5.0
MOCO_AGENT_VERSION := 0.4.0
PROTOC_VERSION := 3.14.0

.PHONY: all
all: build

.PHONY: validate
validate: setup
	test -z "$$(gofmt -s -l . | tee /dev/stderr)"
	staticcheck ./...
	test -z "$$(nilerr ./... 2>&1 | tee /dev/stderr)"
	test -z "$$(custom-checker -restrictpkg.packages=html/template,log $$(go list -tags='$(GOTAGS)' ./... ) 2>&1 | tee /dev/stderr)"
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
	go build -o $@ ./cmd/moco-controller/main.go

# Build kubectl-moco binary
build/kubectl-moco: $(GO_FILES)
	mkdir -p build
	go build -o $@ ./cmd/kubectl-moco

.PHONY: release-build
release-build: build/kubectl-moco-linux-amd64 build/kubectl-moco-windows-amd64.exe build/kubectl-moco-darwin-amd64

# Build kubectl-moco binary for linux (release build)
build/kubectl-moco-linux-amd64: $(GO_FILES)
	mkdir -p build
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $@ ./cmd/kubectl-moco

# Build kubectl-moco binary for Windows (release build)
build/kubectl-moco-windows-amd64.exe: $(GO_FILES)
	mkdir -p build
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o $@ ./cmd/kubectl-moco

# Build kubectl-moco binary for Mac OS (release build)
build/kubectl-moco-darwin-amd64: $(GO_FILES)
	mkdir -p build
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o $@ ./cmd/kubectl-moco

# Generate manifests e.g. CRD, RBAC etc.
.PHONY: manifests
manifests: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases

# Generate code
.PHONY: generate
generate: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: agentrpc
agentrpc: $(PROTOC)
	curl -sfL https://github.com/cybozu-go/moco-agent/releases/download/v$(MOCO_AGENT_VERSION)/agentrpc.proto -O
	mv agentrpc.proto agentrpc/
	$(PROTOC) --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative agentrpc/agentrpc.proto

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
	env GOBIN=$(PWD)/bin GOFLAGS= go install sigs.k8s.io/controller-tools/cmd/controller-gen@v$(CTRLTOOLS_VERSION)

$(PROTOC):
	mkdir -p bin
	curl -sfL -O https://github.com/protocolbuffers/protobuf/releases/download/v$(PROTOC_VERSION)/protoc-$(PROTOC_VERSION)-linux-x86_64.zip
	unzip -p protoc-$(PROTOC_VERSION)-linux-x86_64.zip bin/protoc > bin/protoc
	chmod +x bin/protoc
	rm protoc-$(PROTOC_VERSION)-linux-x86_64.zip

.PHONY: setup
setup: custom-checker staticcheck nilerr

.PHONY: custom-checker
custom-checker:
	if ! which custom-checker >/dev/null; then \
		env GOFLAGS= go install github.com/cybozu/neco-containers/golang/analyzer/cmd/custom-checker@latest; \
	fi

.PHONY: staticcheck
staticcheck:
	if ! which staticcheck >/dev/null; then \
		env GOFLAGS= go install honnef.co/go/tools/cmd/staticcheck@latest; \
	fi

.PHONY: nilerr
nilerr:
	if ! which nilerr >/dev/null; then \
		env GOFLAGS= go install github.com/gostaticanalysis/nilerr/cmd/nilerr@latest; \
	fi

.PHONY: clean
clean:
	rm -rf build
	rm -rf bin
