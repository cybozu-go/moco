SUDO=sudo
GOFLAGS=-mod=vendor
export GOFLAGS

GINKGO=$(GOPATH)/bin/ginkgo
KUBECTL=/usr/local/bin/kubectl
KIND_VERSION=0.8.1
KUSTOMIZE_VERSION=3.7.0
KUSTOMIZE=/usr/local/bin/kustomize
KIND_CLUSTER_NAME=moco-e2e

KUBERNETES_VERSION=1.17.11
KUBEADM_APIVERSION=kubeadm.k8s.io/v1beta2

GO_FILES := $(shell find .. -path ../vendor -prune -o -path ../e2e -prune -o -name '*.go' -print)

launch-kind:
	sed -e "s|@KUBERNETES_VERSION@|$(KUBERNETES_VERSION)|" \
		-e "s|@KUBEADM_APIVERSION@|$(KUBEADM_APIVERSION)|" kind-cluster.yaml > /tmp/kind-cluster.yaml
	kind create cluster --name=$(KIND_CLUSTER_NAME) --config /tmp/kind-cluster.yaml --image kindest/node:v$(KUBERNETES_VERSION)

shutdown-kind:
	kind delete cluster --name=$(KIND_CLUSTER_NAME) || true

test: load-image
	kubectl config use-context kind-$(KIND_CLUSTER_NAME)
	kustomize build --load_restrictor='none' ./manifests | kubectl apply -f -
	env E2ETEST=1 $(GINKGO) --failFast -v .

telepresence:
	telepresence --namespace moco-system --swap-deployment moco-controller-manager --run go run -mod=vendor ../cmd/moco-controller/main.go --conf-init-container-image=quay.io/cybozu/moco-conf-gen:0.2.1

load-image: moco.img mysql.img
	kind load image-archive --name=$(KIND_CLUSTER_NAME) moco.img
	kind load image-archive --name=$(KIND_CLUSTER_NAME) mysql.img

moco.img: $(GO_FILES) ../Dockerfile
	docker build .. -f ../Dockerfile -t controller:dev
	docker save -o $@ controller:dev

mysql.img: $(GO_FILES) Dockerfile
	docker build .. -f Dockerfile -t mysql:dev
	docker save -o $@ mysql:dev

$(KUBECTL):
	$(SUDO) curl -sfL https://storage.googleapis.com/kubernetes-release/release/v$(KUBERNETES_VERSION)/bin/linux/amd64/kubectl -o $(KUBECTL)
	$(SUDO) chmod 755 $(KUBECTL)

setup: $(KUBECTL)
	GO111MODULE=off go get -u github.com/onsi/ginkgo/ginkgo
	curl -sSLf https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize/v$(KUSTOMIZE_VERSION)/kustomize_v$(KUSTOMIZE_VERSION)_linux_amd64.tar.gz | tar xzf - > kustomize
	$(SUDO) mv kustomize $(KUSTOMIZE)
	cd /tmp; env GOFLAGS= GO111MODULE=on go get sigs.k8s.io/kind@v$(KIND_VERSION)

clean:
	rm -f moco.img mysql.img

.PHONY: launch-kind shutdown-kind test load-image setup clean
