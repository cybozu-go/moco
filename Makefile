

# For Go
GO111MODULE = on
GOFLAGS     = -mod=vendor
export GO111MODULE GOFLAGS

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

.PHONY:	all test mod
