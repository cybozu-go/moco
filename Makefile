# Tool versions
MYSQLSH_VERSION = 8.4.6-1
OS_VERSION := $(shell . /etc/os-release; echo $$VERSION_ID)

# Test tools
BIN_DIR := $(shell pwd)/bin
NILERR := $(BIN_DIR)/nilerr
STATICCHECK := $(BIN_DIR)/staticcheck
SUDO = sudo

KUSTOMIZE := kustomize
HELM := helm
GORELEASER := goreleaser
YQ := yq
CONTROLLER_GEN := controller-gen
SETUP_ENVTEST := setup-envtest
CRD_TO_MARKDOWN := crd-to-markdown
MDBOOK := mdbook

PKG_LIST := zstd python3 libpython3.8
ifneq ($(CI),true)
  # Don't install the mysql packages in GitHub Actions.
  # refs: https://github.com/actions/virtual-environments/pull/4674
  PKG_LIST := $(PKG_LIST) mysql-client mysql-server-core-8.0
endif

# Set the shell used to bash for better error handling.
SHELL = /bin/bash
.SHELLFLAGS = -e -o pipefail -c

# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS = "crd:crdVersions=v1,maxDescLen=50"

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
manifests: aqua-install ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	rm -rf charts/moco/templates/generated/
	mkdir -p charts/moco/templates/generated/crds/
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases
	$(KUSTOMIZE) build config/crd -o config/crd/tests # Outputs static CRDs for use with Envtest.
	$(KUSTOMIZE) build config/kustomize-to-helm/overlays/templates | $(YQ) e ". | del(select(.kind==\"ValidatingAdmissionPolicy\" or .kind==\"ValidatingAdmissionPolicyBinding\").metadata.namespace)" - > charts/moco/templates/generated/generated.yaml # Manually remove namespaces because the API version supported by kustomize is out of date.
	echo '{{- if .Values.crds.enabled }}' > charts/moco/templates/generated/crds/moco_crds.yaml
	$(KUSTOMIZE) build config/kustomize-to-helm/overlays/crds | $(YQ) e "." - >> charts/moco/templates/generated/crds/moco_crds.yaml
	echo '{{- end }}' >> charts/moco/templates/generated/crds/moco_crds.yaml

.PHONY: generate
generate: aqua-install ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: apidoc
apidoc: aqua-install $(wildcard api/*/*_types.go)
	$(CRD_TO_MARKDOWN) --links docs/links.csv -f api/v1beta2/mysqlcluster_types.go -f api/v1beta2/job_types.go -n MySQLCluster > docs/crd_mysqlcluster_v1beta2.md
	$(CRD_TO_MARKDOWN) --links docs/links.csv -f api/v1beta2/backuppolicy_types.go -f api/v1beta2/job_types.go -n BackupPolicy > docs/crd_backuppolicy_v1beta2.md

.PHONY: book
book: aqua-install
	rm -rf docs/book
	cd docs; $(MDBOOK) build

.PHONY: check-generate
check-generate:
	$(MAKE) manifests generate apidoc
	go mod tidy
	git diff --exit-code --name-only

.PHONY: envtest
envtest: aqua-install
	source <($(SETUP_ENVTEST) use -p env); \
		export MOCO_CHECK_INTERVAL=100ms; \
		export MOCO_CLONE_WAIT_DURATION=100ms; \
		go test -v -count 1 -race ./clustering -ginkgo.randomize-all -ginkgo.v -ginkgo.fail-fast
	source <($(SETUP_ENVTEST) use -p env); \
		export DEBUG_CONTROLLER=1; \
		go test -v -count 1 -race ./controllers -ginkgo.randomize-all -ginkgo.v -ginkgo.fail-fast
	source <($(SETUP_ENVTEST) use -p env); \
		go test -v -count 1 -race ./api/... -ginkgo.randomize-all -ginkgo.v
	source <($(SETUP_ENVTEST) use -p env); \
		go test -v -count 1 -race ./backup -ginkgo.randomize-all -ginkgo.v -ginkgo.fail-fast

.PHONY: test-dbop
test-dbop:
	-docker network create test-moco
	TEST_MYSQL=1 MYSQL_VERSION=$(MYSQL_VERSION) go test -v -count 1 -race ./pkg/dbop -ginkgo.v -ginkgo.randomize-all

.PHONY: test-bkop
test-bkop:
	@if which mysqlsh; then : ; else echo 'Run "make setup" to prepare test tools.'; exit 1; fi
	-docker network create test-moco
	TEST_MYSQL=1 MYSQL_VERSION=$(MYSQL_VERSION) go test -v -count 1 -race ./pkg/bkop -ginkgo.v -ginkgo.randomize-all

.PHONY: test
test: test-tools aqua-install
	go test -v -count 1 -race ./pkg/...
	go install ./...
	go vet ./...
	test -z $$(gofmt -s -l . | tee /dev/stderr)
	$(STATICCHECK) ./...
	# Disabled temporary due to a false positive with nilerr 0.1.1 built with Go 1.17
	# https://github.com/cybozu-go/moco/runs/4221024784?check_suite_focus=true
	# $(NILERR) ./...

##@ Build

.PHONY: build
build:
	mkdir -p bin
	GOBIN=$(shell pwd)/bin go install ./cmd/...

.PHONY: release-build
release-build: aqua-install
	$(GORELEASER) build --snapshot --clean

.PHONY: release-manifests-build
release-manifests-build: aqua-install
	rm -rf build
	mkdir -p build
	$(KUSTOMIZE) build . > build/moco.yaml

.PHONY: aqua-install
aqua-install: ## Install tools managed by aqua
	aqua install

.PHONY: test-tools
test-tools: $(NILERR) $(STATICCHECK)

$(NILERR):
	mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install github.com/gostaticanalysis/nilerr/cmd/nilerr@latest

.PHONY: $(STATICCHECK)
$(STATICCHECK):
	mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install honnef.co/go/tools/cmd/staticcheck@latest

.PHONY: setup
setup:
	$(SUDO) apt-get update
	$(SUDO) apt-get install -y --no-install-recommends $(PKG_LIST)
	curl -o /tmp/mysqlsh.deb -fsL https://dev.mysql.com/get/Downloads/MySQL-Shell/mysql-shell_$(MYSQLSH_VERSION)ubuntu$(OS_VERSION)_amd64.deb
	$(SUDO) dpkg -i /tmp/mysqlsh.deb
	rm -f /tmp/mysqlsh.deb
