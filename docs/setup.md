Setup
=====

## Quick setup

1. Download and install the latest [cert-manager](https://cert-manager.io/).

    ```console
    $ curl -fsLO https://github.com/jetstack/cert-manager/releases/latest/download/cert-manager.yaml
    $ kubectl apply -f cert-manager.yaml
    ```

2. Download and install the latest MOCO.

    ```console
    $ curl -fsLO https://github.com/cybozu-go/moco/releases/latest/download/moco.yaml
    $ kubectl apply -f moco.yaml
    ```

That's all!

## Customize manifests

If you want to edit the manifest, [`config/`](https://github.com/cybozu-go/moco/tree/main/config) directory contains the source YAML for [kustomize](https://kustomize.io/).

## Next step

Read [`usage.md`](usage.md) and create your first MySQL cluster!
