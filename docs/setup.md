Setup
=====

## Quick setup

You can choose between two installation methods.

MOCO depends on cert-manager. If cert-manager is not installed on your cluster, install it as follows:

```console
$ curl -fsLO https://github.com/jetstack/cert-manager/releases/latest/download/cert-manager.yaml
$ kubectl apply -f cert-manager.yaml
```

### Install using raw manifests:

```console
$ curl -fsLO https://github.com/cybozu-go/moco/releases/latest/download/moco.yaml
$ kubectl apply -f moco.yaml
```

### Install using Helm chart:

```console
$ helm repo add moco https://cybozu-go.github.io/moco/
$ helm repo update
$ helm install --create-namespace --namespace moco-system moco moco/moco
```

## Customize manifests

If you want to edit the manifest, [`config/`](https://github.com/cybozu-go/moco/tree/main/config) directory contains the source YAML for [kustomize](https://kustomize.io/).

## Next step

Read [`usage.md`](usage.md) and create your first MySQL cluster!
