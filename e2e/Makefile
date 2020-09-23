GOFLAGS=-mod=vendor
export GOFLAGS

GINKGO=$(GOPATH)/bin/ginkgo
KUBECTL := $(PWD)/bin/kubectl
KIND_VERSION=0.8.1
KUSTOMIZE_VERSION=3.7.0
KUSTOMIZE := $(PWD)/bin/kustomize
KIND_CLUSTER_NAME=moco-e2e

MOCO_CONF_GEN_VERSION=0.2.1
KUBERNETES_VERSION=1.17.11
KUBEADM_APIVERSION=kubeadm.k8s.io/v1beta2

GO_FILES := $(shell find .. -path ../vendor -prune -o -path ../e2e -prune -o -name '*.go' -print)

launch-kind: kind
	if [ ! "$(shell kind get clusters | grep $(KIND_CLUSTER_NAME))" ]; then \
		sed -e "s|@KUBERNETES_VERSION@|$(KUBERNETES_VERSION)|" \
			-e "s|@KUBEADM_APIVERSION@|$(KUBEADM_APIVERSION)|" kind-cluster.yaml > /tmp/kind-cluster.yaml; \
		kind create cluster --name=$(KIND_CLUSTER_NAME) --config /tmp/kind-cluster.yaml --image kindest/node:v$(KUBERNETES_VERSION); \
	fi

shutdown-kind: kind
	if [ "$(shell kind get clusters | grep $(KIND_CLUSTER_NAME))" ]; then \
		kind delete cluster --name=$(KIND_CLUSTER_NAME) || true; \
	fi

test: launch-kind load-image $(KUBECTL) $(KUSTOMIZE) ginkgo
	$(KUBECTL) config use-context kind-$(KIND_CLUSTER_NAME)
	$(KUSTOMIZE) build --load_restrictor='none' ./manifests | $(KUBECTL) apply -f -
	env E2ETEST=1 $(GINKGO) --failFast -v .

.PHONY: telepresence
telepresence:
	telepresence --namespace moco-system --swap-deployment moco-controller-manager --run go run -mod=vendor ../cmd/moco-controller/main.go --conf-init-container-image=quay.io/cybozu/moco-conf-gen:$(MOCO_CONF_GEN_VERSION)

.PHONY: load-image
load-image: load-moco-image load-mysql-image

.PHONY: load-moco-image
load-moco-image: build-moco-image
	ID=$$(docker image inspect --format='{{.ID}}' controller:dev); \
	if [ ! "$$(docker exec -it $(KIND_CLUSTER_NAME)-control-plane ctr --namespace=k8s.io images list | grep $$ID)" ]; then \
		kind load docker-image --name=$(KIND_CLUSTER_NAME) controller:dev; \
	fi

.PHONY: load-mysql-image
load-mysql-image: build-mysql-image
	ID=$$(docker image inspect --format='{{.ID}}' mysql:dev); \
	if [ ! "$$(docker exec -it $(KIND_CLUSTER_NAME)-control-plane ctr --namespace=k8s.io images list | grep $$ID)" ]; then \
		kind load docker-image --name=$(KIND_CLUSTER_NAME) mysql:dev; \
	fi

.PHONY: build-moco-image
build-moco-image: $(GO_FILES) ../Dockerfile
	docker build .. -f ../Dockerfile -t controller:dev

.PHONY: build-mysql-image
build-mysql-image: $(GO_FILES) Dockerfile
	docker build .. -f Dockerfile -t mysql:dev

$(KUBECTL):
	mkdir -p bin
	curl -sfL https://storage.googleapis.com/kubernetes-release/release/v$(KUBERNETES_VERSION)/bin/linux/amd64/kubectl -o $(KUBECTL)
	chmod 755 $(KUBECTL)

$(KUSTOMIZE):
	mkdir -p bin
	curl -sSLf https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize/v$(KUSTOMIZE_VERSION)/kustomize_v$(KUSTOMIZE_VERSION)_linux_amd64.tar.gz | tar xzf - > kustomize
	mv kustomize $(KUSTOMIZE)

.PHONY: kind
kind:
	if [ ! "$(shell kind version | grep $(KIND_VERSION))" ]; then \
		cd /tmp; env GOFLAGS= GO111MODULE=on go get sigs.k8s.io/kind@v$(KIND_VERSION); \
	fi

.PHONY: ginkgo
ginkgo:
	if ! which ginkgo >/dev/null; then \
		cd /tmp; env GOFLAGS= GO111MODULE=on go get -u github.com/onsi/ginkgo/ginkgo; \
	fi

.PHONY: clean
clean:
	rm -rf bin
