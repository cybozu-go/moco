SUDO=sudo
GOFLAGS=-mod=vendor
export GOFLAGS

GINKGO=$(GOPATH)/bin/ginkgo
KUBECTL=/usr/local/bin/kubectl
KIND_VERSION=0.8.1
KUSTOMIZE_VERSION=3.5.4
KUSTOMIZE=/usr/local/bin/kustomize

KUBERNETES_VERSION=1.17.2
KUBEADM_APIVERSION=kubeadm.k8s.io/v1beta2

GO_FILES := $(shell find .. -path ../vendor -prune -o -path ../e2e -prune -o -name '*.go' -print)

launch-kind:
	sed -e "s|@KUBERNETES_VERSION@|$(KUBERNETES_VERSION)|" \
		-e "s|@KUBEADM_APIVERSION@|$(KUBEADM_APIVERSION)|" kind-cluster.yaml > /tmp/kind-cluster.yaml
	kind create cluster --config /tmp/kind-cluster.yaml --image kindest/node:v$(KUBERNETES_VERSION)

shutdown-kind:
	kind delete cluster || true

test: myso.img mysql.img
	-kubectl delete -n e2e-test mysqlcluster --all
	-kubectl delete -n e2e-test pvc --all
	kubectl delete crd --all
	kustomize build --load_restrictor='none' ./manifests | kubectl apply -f -
	kubectl delete -n myso-system pod --all
	env E2ETEST=1 $(GINKGO) --failFast -v .

myso.img:
	docker build .. -f ../Dockerfile -t controller:dev
	docker save -o $@ controller:dev
	kind load image-archive myso.img
	rm -f $@

mysql.img:
	docker build .. -f ../e2e/Dockerfile -t mysql:dev
	docker save -o $@ mysql:dev
	kind load image-archive mysql.img
	rm -f $@

$(KUBECTL):
	$(SUDO) curl -sfL https://storage.googleapis.com/kubernetes-release/release/v$(KUBERNETES_VERSION)/bin/linux/amd64/kubectl -o $(KUBECTL)
	$(SUDO) chmod 755 $(KUBECTL)

setup: $(KUBECTL)
	GO111MODULE=off go get -u github.com/onsi/ginkgo/ginkgo
	curl -sSLf https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize/v$(KUSTOMIZE_VERSION)/kustomize_v$(KUSTOMIZE_VERSION)_linux_amd64.tar.gz | tar xzf - > kustomize
	$(SUDO) mv kustomize $(KUSTOMIZE)
	cd /tmp; env GOFLAGS= GO111MODULE=on go get sigs.k8s.io/kind@v$(KIND_VERSION)

clean:
	rm -f myso.img mysql.img

.PHONY: launch-kind shutdown-kind test setup clean
