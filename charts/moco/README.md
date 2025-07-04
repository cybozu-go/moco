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

| Key                                   | Type   | Default                                       | Description                                                                  |
| ------------------------------------- | ------ | --------------------------------------------- | ---------------------------------------------------------------------------- |
| replicaCount                          | number | `2`                                           | Number of controller replicas.                                               |
| image.repository                      | string | `"ghcr.io/cybozu-go/moco"`                    | MOCO image repository to use.                                                |
| image.pullPolicy                      | string | `IfNotPresent`                                | MOCO image pulling policy.                                                   |
| image.tag                             | string | `{{ .Chart.AppVersion }}`                     | MOCO image tag to use.                                                       |
| imagePullSecrets                      | list   | `[]`                                          | Secrets for pulling MOCO image from private repository.                      |
| resources                             | object | `{"requests":{"cpu":"100m","memory":"20Mi"}}` | resources used by moco-controller.                                           |
| crds.enabled                          | bool   | `true`                                        | Install and update CRDs as part of the Helm chart.                           |
| extraArgs                             | list   | `[]`                                          | Additional command line flags to pass to moco-controller binary.             |
| nodeSelector                          | object | `{}`                                          | nodeSelector used by moco-controller.                                        |
| affinity                              | object | `{}`                                          | affinity used by moco-controller.                                            |
| tolerations                           | list   | `[]`                                          | tolerations used by moco-controller.                                         |
| topologySpreadConstraints             | list   | `[]`                                          | topologySpreadConstraints used by moco-controller.                           |
| priorityClassName                     | string | `""`                                          | PriorityClass used by moco-controller.                                       |
| monitoring.enabled                    | bool   | `false`                                       | Enable monitoring configuration. Requires Prometheus (CRDs) to be installed. |
| monitoring.podMonitors.enabled        | bool   | `true`                                        | Create Prometheus pod monitors.                                              |
| monitoring.podMonitors.interval       | string | `""`                                          | Custom Prometheus scrape interval.                                           |
| monitoring.podMonitors.scrapeTimeout  | string | `""`                                          | Custom Prometheus scrape timeout.                                            |

## Generate Manifests

You can use the `helm template` command to render manifests.

```console
$ helm template --namespace moco-system moco moco/moco
```

## CRD considerations

### Installing or updating CRDs

MOCO Helm Chart installs or updates CRDs by default. If you want to manage CRDs on your own, turn off the `crds.enabled` parameter.

### Removing CRDs

Helm does not remove the CRDs due to the [`helm.sh/resource-policy: keep` annotation](https://helm.sh/docs/howto/charts_tips_and_tricks/#tell-helm-not-to-uninstall-a-resource).
When uninstalling, please remove the CRDs manually.

## Migrate to v0.11.0 or higher

Chart version v0.11.0 introduces the `crds.enabled` parameter.

When updating to a new chart from chart v0.10.x or lower, you **MUST** leave this parameter `true` (the default value).
If you turn off this option when updating, the CRD will be removed, causing data loss.

## Migrate to v0.3.0

Chart version v0.3.0 has breaking changes.
The `.metadata.name` of the resource generated by Chart is changed.

e.g.

* `{{ template "moco.fullname" . }}-foo-resources` -> `moco-foo-resources`

Related Issue: [cybozu-go/moco#426](https://github.com/cybozu-go/moco/issues/426)

If you are using a release name other than `moco`, you need to migrate.

The migration steps involve deleting and recreating each MOCO resource once, except CRDs.
Since the CRDs are not deleted, the pods running existing MySQL clusters are not deleted, so there is no downtime.
However, the migration process should be completed in a short time since the moco-controller will be temporarily deleted and no control over the cluster will be available.

<details>

<summary>migration steps</summary>

1. Show the installed chart

    ```console
    $ helm list -n <YOUR NAMESPACE>
    NAME    NAMESPACE       REVISION        UPDATED                                 STATUS          CHART           APP VERSION
    moco    moco-system     1               2022-08-17 11:28:23.418752 +0900 JST    deployed        moco-0.2.3      0.12.1
    ```

2. Render the manifests

    ```console
    $ helm template --namespace moco-system --version <YOUR CHART VERSION> <YOUR INSTALL NAME> moco/moco > render.yaml
    ```

3. Setup kustomize

    ```console
    $ cat > kustomization.yaml <<'EOF'
    resources:
      - render.yaml
    patches:
      - crd-patch.yaml
    EOF

    $ cat > crd-patch.yaml <<'EOF'
    $patch: delete
    apiVersion: apiextensions.k8s.io/v1
    kind: CustomResourceDefinition
    metadata:
      name: backuppolicies.moco.cybozu.com
    ---
    $patch: delete
    apiVersion: apiextensions.k8s.io/v1
    kind: CustomResourceDefinition
    metadata:
      name: mysqlclusters.moco.cybozu.com
    EOF
    ```

4. Delete resources

    ```console
    $ kustomize build ./ | kubectl delete -f -
    serviceaccount "moco-controller-manager" deleted
    role.rbac.authorization.k8s.io "moco-leader-election-role" deleted
    clusterrole.rbac.authorization.k8s.io "moco-backuppolicy-editor-role" deleted
    clusterrole.rbac.authorization.k8s.io "moco-backuppolicy-viewer-role" deleted
    clusterrole.rbac.authorization.k8s.io "moco-manager-role" deleted
    clusterrole.rbac.authorization.k8s.io "moco-mysqlcluster-editor-role" deleted
    clusterrole.rbac.authorization.k8s.io "moco-mysqlcluster-viewer-role" deleted
    rolebinding.rbac.authorization.k8s.io "moco-leader-election-rolebinding" deleted
    clusterrolebinding.rbac.authorization.k8s.io "moco-manager-rolebinding" deleted
    service "moco-webhook-service" deleted
    deployment.apps "moco-controller" deleted
    certificate.cert-manager.io "moco-controller-grpc" deleted
    certificate.cert-manager.io "moco-grpc-ca" deleted
    certificate.cert-manager.io "moco-serving-cert" deleted
    issuer.cert-manager.io "moco-grpc-issuer" deleted
    issuer.cert-manager.io "moco-selfsigned-issuer" deleted
    mutatingwebhookconfiguration.admissionregistration.k8s.io "moco-mutating-webhook-configuration" deleted
    validatingwebhookconfiguration.admissionregistration.k8s.io "moco-validating-webhook-configuration" deleted
    ```

5. Delete Secret

    ```console
    $ kubectl delete secret sh.helm.release.v1.<YOUR INSTALL NAME>.v1 -n <YOUR NAMESPACE>
    ```

6. Re-install the v0.3.0 chart

    ```console
    $ helm install --create-namespace --namespace moco-system --version 0.3.0 moco moco/moco
    ```

</details>

## Release Chart

See [RELEASE.md](../../RELEASE.md#bump-chart-version).
