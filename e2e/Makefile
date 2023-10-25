KIND_VERSION = 0.19.0
KUBERNETES_VERSION = 1.27.1
CERT_MANAGER_VERSION = 1.12.1
MYSQL_VERSION = 8.0.34

KIND := $(dir $(shell pwd))/bin/kind
KUBECTL := $(dir $(shell pwd))/bin/kubectl
KUSTOMIZE := $(dir $(shell pwd))/bin/kustomize
KUBECTL_MOCO := $(dir $(shell pwd))/bin/kubectl-moco
KUBECONFIG := $(shell pwd)/.kubeconfig
export MYSQL_VERSION KUBECTL KUBECONFIG

## to test development version of moco-agent, run make with AGENT_DIR=<path>
AGENT_DIR =
AGENT_IMAGE := ghcr.io/cybozu-go/moco-agent:$(shell awk '/moco-agent/ {print substr($$2, 2)}' ../go.mod)

## to test development version of mysqld, run make with USE_LOCAL_MYSQL_IMAGE=1
USE_LOCAL_MYSQL_IMAGE =

## We need to switch the configuration of kind on GitHub Actions
KIND_CONFIG = kind-config.yaml

.PHONY: help
help:
	@echo "Choose one of the following target"
	@echo
	@echo "start      Start kind cluster and install MOCO"
	@echo "test       Run e2e tests"
	@echo "logs       Save logs as logs.tar.gz"
	@echo "stop       Stop the kind cluster"

.PHONY: start
start: $(KIND) $(KUBECTL) $(KUSTOMIZE) $(KUBECTL_MOCO)
	$(KIND) create cluster --name=moco --config=$(KIND_CONFIG) --image=kindest/node:v$(KUBERNETES_VERSION) --wait 1m
	cd ..; docker buildx build --no-cache --load --target controller -t moco:dev .
	$(KIND) load docker-image moco:dev --name=moco
	cd ..; docker buildx build --load -t moco-backup:dev .
	$(KIND) load docker-image moco-backup:dev --name=moco
ifdef AGENT_DIR
	cd $(AGENT_DIR); docker buildx build --load --no-cache -t $(AGENT_IMAGE) .
	$(KIND) load docker-image $(AGENT_IMAGE) --name=moco
endif
ifdef USE_LOCAL_MYSQL_IMAGE
	$(KIND) load docker-image ghcr.io/cybozu-go/moco/mysql:$(MYSQL_VERSION) --name=moco
endif
	$(KUBECTL) apply -f https://github.com/jetstack/cert-manager/releases/download/v$(CERT_MANAGER_VERSION)/cert-manager.yaml
	$(KUBECTL) -n cert-manager wait --for=condition=available --timeout=180s --all deployments
	$(KUSTOMIZE) build . | $(KUBECTL) apply -f -
	$(KUBECTL) -n moco-system wait --for=condition=available --timeout=180s --all deployments
	$(KUBECTL) apply -f minio.yaml
	$(KUBECTL) apply -f minio-tls.yaml
	$(KUBECTL) apply -f fake-gcs-server.yaml
	$(KUBECTL) wait --timeout=60s --for=condition=Ready --all pods

.PHONY: test
test:
	env PATH="$$(pwd)/../bin:$$PATH" RUN_E2E=1 \
		go test -v -race -timeout 60m . -ginkgo.progress -ginkgo.v -ginkgo.failFast

.PHONY: test-upgrade
test-upgrade:
	env PATH=$$(pwd)/../bin:$$PATH RUN_E2E=1 UPGRADE=1 \
		go test -v -race -timeout 15m . -ginkgo.progress -ginkgo.v -ginkgo.failFast

.PHONY: logs
logs:
	rm -rf logs.tar.gz logs
	$(KIND) export logs --name=moco ./logs
	tar czf logs.tar.gz logs
	rm -rf logs

.PHONY: stop
stop: $(KIND) 
	$(KIND) delete cluster --name=moco
	-docker image rm moco:dev
	-docker image rm moco-backup:dev
	-docker image prune -f

$(KIND):
	mkdir -p ../bin
	curl -sfL -o $@ https://github.com/kubernetes-sigs/kind/releases/download/v$(KIND_VERSION)/kind-linux-amd64
	chmod a+x $@

$(KUBECTL):
	mkdir -p ../bin
	curl -sfL -o $@ https://dl.k8s.io/release/v$(KUBERNETES_VERSION)/bin/linux/amd64/kubectl
	chmod a+x $@

$(KUSTOMIZE):
	$(MAKE) -C .. kustomize

$(KUBECTL_MOCO):
	mkdir -p ../bin
	cd ..; GOBIN=$$(pwd)/bin go install ./cmd/kubectl-moco
