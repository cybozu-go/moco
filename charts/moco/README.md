# MOCO Helm Chart

## How to use MOCO Helm repository

You need to add this repository to your Helm repositories:

```console
$ helm repo add moco https://cybozu-go.github.io/moco/
$ helm repo update
```

## Quick start

### Installing cert-manager

```console
$ curl -fsL https://github.com/jetstack/cert-manager/releases/latest/download/cert-manager.yaml | kubectl apply -f -
```

### Installing the Chart

> NOTE:
>
> This installation method requires cert-manager to be installed beforehand.
> To install the chart with the release name `moco` using a dedicated namespace(recommended):

```console
$ helm install --create-namespace --namespace moco-system moco moco/moco
```

Specify parameters using `--set key=value[,key=value]` argument to `helm install`.

Alternatively a YAML file that specifies the values for the parameters can be provided like this:

```console
$ helm install --create-namespace --namespace moco-system moco -f values.yaml moco/moco
```

## Values

| Key              | Type   | Default                    | Description                   |
| ---------------- | ------ | -------------------------- | ----------------------------- |
| image.repository | string | `"ghcr.io/cybozu-go/moco"` | MOCO image repository to use. |
| image.tag        | string | `{{ .Chart.AppVersion }}`  | MOCO image tag to use.        |

## Generate Manifests

You can use the `helm template` command to render manifests.

```console
$ helm template --namespace moco-system moco moco/moco
```

## Upgrade CRDs

There is no support at this time for upgrading or deleting CRDs using Helm.
Users must manually upgrade the CRD if there is a change in the CRD used by MOCO.

https://helm.sh/docs/chart_best_practices/custom_resource_definitions/#install-a-crd-declaration-before-using-the-resource

## Release Chart

See [RELEASE.md](../../RELEASE.md#bump-chart-version).
