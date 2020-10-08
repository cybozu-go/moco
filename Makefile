include common.mk

# For Go
GO111MODULE = on
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)
GOFLAGS := -mod=vendor
export GO111MODULE GOFLAGS

KUBEBUILDER_ASSETS := $(PWD)/bin
export KUBEBUILDER_ASSETS

CONTROLLER_GEN := $(PWD)/bin/controller-gen
KUBEBUILDER := $(PWD)/bin/kubebuilder
# Produce CRDs with apiextensions.k8s.io/v1
CRD_OPTIONS ?= "crd:crdVersions=v1"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

KUBEBUILDER_VERSION := 2.3.1
CTRLTOOLS_VERSION := 0.4.0
MYSQL_VERSION := 8.0.21

.PHONY: all
all: build/moco-controller

.PHONY: validate
validate: setup
	test -z "$$(gofmt -s -l . | grep -v '^vendor' | tee /dev/stderr)"
	staticcheck ./...
	test -z "$$(nilerr ./... 2>&1 | tee /dev/stderr)"
	test -z "$$(custom-checker -restrictpkg.packages=html/template,log $$(go list -tags='$(GOTAGS)' ./... | grep -v /vendor/ ) 2>&1 | tee /dev/stderr)"
	ineffassign .
	go build -mod=vendor ./...
	go vet ./...
	test -z "$$(go vet ./... | grep -v '^vendor' | tee /dev/stderr)"

# Run tests
.PHONY: test
test: $(KUBEBUILDER)
	go test -race -v -coverprofile cover.out ./...

.PHONY: start-mysqld
start-mysqld:
	if [ "$(shell docker inspect moco-test-mysqld --format='{{ .State.Running }}')" != "true" ]; then \
		docker run --name moco-test-mysqld --rm -d -p 3306:3306 -e MYSQL_ROOT_PASSWORD=test-password mysql:$(MYSQL_VERSION); \
	fi
	docker network inspect moco-mysql-net > /dev/null; \
	if [ $$? -ne 0 ] ; then \
		docker network create moco-mysql-net; \
	fi
	if [ "$(shell docker inspect moco-test-mysqld-donor --format='{{ .State.Running }}')" != "true" ]; then \
		docker run --name moco-test-mysqld-donor --network=moco-mysql-net --rm -d -p 3307:3307 -v $(PWD)/my.cnf:/etc/mysql/conf.d/my.cnf -e MYSQL_ROOT_PASSWORD=test-password mysql:$(MYSQL_VERSION) --port=3307; \
	fi
	if [ "$(shell docker inspect moco-test-mysqld-replica --format='{{ .State.Running }}')" != "true" ]; then \
		docker run --name moco-test-mysqld-replica --restart always --network=moco-mysql-net -d -p 3308:3308 -v $(PWD)/my.cnf:/etc/mysql/conf.d/my.cnf -e MYSQL_ROOT_PASSWORD=test-password mysql:$(MYSQL_VERSION) --port=3308; \
	fi
	echo "127.0.0.1\tmoco-test-mysqld-donor\n127.0.0.1\tmoco-test-mysqld-replica\n" | sudo tee -a /etc/hosts > /dev/null

.PHONY: stop-mysqld
stop-mysqld:
	if [ "$(shell docker inspect moco-test-mysqld --format='{{ .State.Running }}')" = "true" ]; then \
		docker stop moco-test-mysqld; \
	fi
	if [ "$(shell docker inspect moco-test-mysqld-donor --format='{{ .State.Running }}')" = "true" ]; then \
		docker stop moco-test-mysqld-donor; \
	fi
	if [ "$(shell docker inspect moco-test-mysqld-replica --format='{{ .State.Running }}')" = "true" ]; then \
		docker stop moco-test-mysqld-replica; \
		docker rm moco-test-mysqld-replica; \
	fi
	docker network inspect moco-mysql-net > /dev/null; \
	if [ $$? -eq 0 ] ; then \
		docker network rm moco-mysql-net; \
	fi
	sudo sed -i -e '/moco-test-mysqld-/d' /etc/hosts

# Build moco-controller binary
build/moco-controller: generate
	mkdir -p build
	GO111MODULE=on go build -o $@ ./cmd/moco-controller/main.go

# Build entrypoint binary
build/entrypoint:
	mkdir -p build
	GO111MODULE=on go build -o $@ ./cmd/entrypoint

# Generate manifests e.g. CRD, RBAC etc.
.PHONY: manifests
manifests: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases

	# workaround for CRD issue with k8s 1.18 & controller-gen 0.4
	# ref: https://github.com/kubernetes/kubernetes/issues/91395
	# sed -i -r 's/^( +)  or SCTP\. Defaults to "TCP"\./\0\n\1default: TCP/' \
	sed -i -r 's/^( +)description: Protocol for port\. Must be UDP, TCP, or SCTP\. Defaults to "TCP"\./\0\n\1default: TCP/' \
	  config/crd/bases/moco.cybozu.com_mysqlclusters.yaml
	sed  -i -r 's/^( +)description: The IP protocol for this port\. Supports "TCP", "UDP", and "SCTP"\. Default is TCP\./\0\n\1default: TCP/' \
	  config/crd/bases/moco.cybozu.com_mysqlclusters.yaml
	cp config/crd/bases/moco.cybozu.com_mysqlclusters.yaml deploy/crd.yaml

# Generate code
.PHONY: generate
generate: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: mod
mod:
	go mod tidy
	go mod vendor
	git add -f vendor
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
		cd /tmp; env GOFLAGS= GO111MODULE=on go install github.com/cybozu/neco-containers/golang/analyzer/cmd/custom-checker; \
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
