KUBEBUILDER_VERSION = 2.3.1
CTRLTOOLS_VERSION = 0.2.9

# For Go
GO111MODULE = on
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)
GOFLAGS     = -mod=vendor
export GO111MODULE GOFLAGS

SUDO=sudo

all: test

test:
	test -z "$$(gofmt -s -l . | grep -v '^vendor' | tee /dev/stderr)"
	test -z "$$(golint $$(go list -mod=vendor ./... | grep -v /vendor/) | tee /dev/stderr)"
	test -z "$$(nilerr ./... 2>&1 | tee /dev/stderr)"
	test -z "$$(restrictpkg -packages=html/template,log ./... 2>&1 | tee /dev/stderr)"
	go build -mod=vendor ./...
	go test -mod=vendor -race -v ./...
	go vet -mod=vendor ./...
	ineffassign .

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

.PHONY:	all test mod setup
