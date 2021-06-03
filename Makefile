# Tool versions
CTRL_TOOLS_VERSION=0.5.0
CTRL_RUNTIME_VERSION := $(shell awk '/sigs.k8s.io\/controller-runtime/ {print substr($$2, 2)}' go.mod)
KUSTOMIZE_VERSION = 4.1.3
CRD_TO_MARKDOWN_VERSION = 0.0.3
MYSQLSH_VERSION = 8.0.25-1
MDBOOK_VERSION = 0.4.9
OS_VERSION := $(shell . /etc/os-release; echo $$VERSION_ID)

# Test tools
BIN_DIR := $(shell pwd)/bin
STATICCHECK := $(BIN_DIR)/staticcheck
NILERR := $(BIN_DIR)/nilerr
SUDO = sudo

# Set the shell used to bash for better error handling.
SHELL = /bin/bash
.SHELLFLAGS = -e -o pipefail -c

# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS = "crd:crdVersions=v1"

# for Go
GOOS = $(shell go env GOOS)
GOARCH = $(shell go env GOARCH)
SUFFIX =

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases
	@echo "Shrinking CRD size..."
	sed -i -E 's/^(                        +description: ).*$$/\1"omitted"/' config/crd/bases/moco.cybozu.com_mysqlclusters.yaml

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: apidoc
apidoc: crd-to-markdown $(wildcard api/*/*_types.go)
	$(CRD_TO_MARKDOWN) --links docs/links.csv -f api/v1beta1/mysqlcluster_types.go -f api/v1beta1/job_types.go -n MySQLCluster > docs/crd_mysqlcluster.md
	$(CRD_TO_MARKDOWN) --links docs/links.csv -f api/v1beta1/backuppolicy_types.go -f api/v1beta1/job_types.go -n BackupPolicy > docs/crd_backuppolicy.md

.PHONY: book
book: mdbook
	rm -rf docs/book
	cd docs; $(MDBOOK) build

.PHONY: check-generate
check-generate:
	$(MAKE) manifests generate apidoc
	git diff --exit-code --name-only

ENVTEST_ASSETS_DIR := $(shell pwd)/testbin
.PHONY: envtest
envtest:
	mkdir -p ${ENVTEST_ASSETS_DIR}
	test -f ${ENVTEST_ASSETS_DIR}/setup-envtest.sh || curl -sSLo ${ENVTEST_ASSETS_DIR}/setup-envtest.sh https://raw.githubusercontent.com/kubernetes-sigs/controller-runtime/v$(CTRL_RUNTIME_VERSION)/hack/setup-envtest.sh
	source ${ENVTEST_ASSETS_DIR}/setup-envtest.sh; \
		fetch_envtest_tools $(ENVTEST_ASSETS_DIR); \
		setup_envtest_env $(ENVTEST_ASSETS_DIR); \
		export MOCO_CHECK_INTERVAL=100ms; \
		export MOCO_WAIT_INTERVAL=100ms; \
		go test -v -count 1 -race ./clustering -ginkgo.progress -ginkgo.v -ginkgo.failFast
	source ${ENVTEST_ASSETS_DIR}/setup-envtest.sh; \
		fetch_envtest_tools $(ENVTEST_ASSETS_DIR); \
		setup_envtest_env $(ENVTEST_ASSETS_DIR); \
		export DEBUG_CONTROLLER=1; \
		go test -v -count 1 -race ./controllers -ginkgo.progress -ginkgo.v -ginkgo.failFast
	source ${ENVTEST_ASSETS_DIR}/setup-envtest.sh; \
		fetch_envtest_tools $(ENVTEST_ASSETS_DIR); \
		setup_envtest_env $(ENVTEST_ASSETS_DIR); \
		go test -v -count 1 -race ./api/... -ginkgo.progress -ginkgo.v
	source ${ENVTEST_ASSETS_DIR}/setup-envtest.sh; \
		fetch_envtest_tools $(ENVTEST_ASSETS_DIR); \
		setup_envtest_env $(ENVTEST_ASSETS_DIR); \
		go test -v -count 1 -race ./backup -ginkgo.progress -ginkgo.v -ginkgo.failFast

.PHONY: test-dbop
test-dbop:
	-docker network create test-moco
	TEST_MYSQL=1 MYSQL_VERSION=$(MYSQL_VERSION) go test -v -count 1 -race ./pkg/dbop -ginkgo.v

.PHONY: test-bkop
test-bkop:
	@if which mysqlsh; then : ; else echo 'Run "make setup" to prepare test tools.'; exit 1; fi
	-docker network create test-moco
	TEST_MYSQL=1 MYSQL_VERSION=$(MYSQL_VERSION) go test -v -count 1 -race ./pkg/bkop -ginkgo.v -ginkgo.progress

.PHONY: test
test: test-tools
	go test -v -count 1 -race ./pkg/...
	go install ./...
	go vet ./...
	test -z $$(gofmt -s -l . | tee /dev/stderr)
	$(STATICCHECK) ./...
	$(NILERR) ./...

##@ Build

.PHONY: build
build:
	mkdir -p bin
	GOBIN=$(shell pwd)/bin go install ./cmd/...

.PHONY: release-build
release-build: kustomize
	mkdir -p build
	$(MAKE) kubectl-moco GOOS=windows GOARCH=amd64 SUFFIX=.exe
	$(MAKE) kubectl-moco GOOS=darwin GOARCH=amd64
	$(MAKE) kubectl-moco GOOS=darwin GOARCH=arm64
	$(MAKE) kubectl-moco GOOS=linux GOARCH=amd64
	$(MAKE) kubectl-moco GOOS=linux GOARCH=arm64
	$(KUSTOMIZE) build . > build/moco.yaml

.PHONY: kubectl-moco
kubectl-moco: build/kubectl-moco-$(GOOS)-$(GOARCH)$(SUFFIX)

build/kubectl-moco-$(GOOS)-$(GOARCH)$(SUFFIX):
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o $@ ./cmd/kubectl-moco

##@ Tools

CONTROLLER_GEN := $(shell pwd)/bin/controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	$(call go-get-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@v$(CTRL_TOOLS_VERSION))

KUSTOMIZE := $(shell pwd)/bin/kustomize
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.

$(KUSTOMIZE):
	mkdir -p bin
	curl -fsL https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2Fv$(KUSTOMIZE_VERSION)/kustomize_v$(KUSTOMIZE_VERSION)_linux_amd64.tar.gz | \
	tar -C bin -xzf -

CRD_TO_MARKDOWN := $(shell pwd)/bin/crd-to-markdown
.PHONY: crd-to-markdown
crd-to-markdown: ## Download crd-to-markdown locally if necessary.
	$(call go-get-tool,$(CRD_TO_MARKDOWN),github.com/clamoriniere/crd-to-markdown@v$(CRD_TO_MARKDOWN_VERSION))

MDBOOK := $(shell pwd)/bin/mdbook
.PHONY: mdbook
mdbook: ## Donwload mdbook locally if necessary
	mkdir -p bin
	curl -fsL https://github.com/rust-lang/mdBook/releases/download/v$(MDBOOK_VERSION)/mdbook-v$(MDBOOK_VERSION)-x86_64-unknown-linux-gnu.tar.gz | tar -C bin -xzf -

# go-get-tool will 'go get' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-get-tool
@[ -f $(1) ] || { \
set -e ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin go install $(2) ;\
}
endef

.PHONY: test-tools
test-tools: $(STATICCHECK) $(NILERR)

$(STATICCHECK):
	mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install honnef.co/go/tools/cmd/staticcheck@latest

$(NILERR):
	mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install github.com/gostaticanalysis/nilerr/cmd/nilerr@latest

.PHONY: setup
setup:
	$(SUDO) apt-get update
	$(SUDO) apt-get install -y --no-install-recommends mysql-client zstd python3 libpython3.8 mysql-server-core-8.0
	curl -o /tmp/mysqlsh.deb -fsL https://dev.mysql.com/get/Downloads/MySQL-Shell/mysql-shell_$(MYSQLSH_VERSION)ubuntu$(OS_VERSION)_amd64.deb
	$(SUDO) dpkg -i /tmp/mysqlsh.deb
	rm -f /tmp/mysqlsh.deb
