# Change Log

All notable changes to this project will be documented in this file.
This project adheres to [Semantic Versioning](http://semver.org/).

## [Unreleased]

## [0.2.1] - 2022-03-16

### Changed
- Bump appVersion to 0.11.1.

## [0.2.0] - 2022-03-07

### Support MySQLCluster v1beta2 API

The addition of the API adds a conversion webhook to the CRD.
Starting with this version, the CRD will be included in the Helm Chart template in MOCO
because the definition of the CRD conversion webhook needs to be changed
based on the namespace where the Helm chart is installed.

> :note: Helm standard CRD management does not support CRD configuration. (refs: [helm/helm#7735](https://github.com/helm/helm/issues/7735))

This will cause an error when upgrading from the previous chart, requiring a manual operation.

```console
$ helm upgrade -n moco-system moco moco/moco
Error: UPGRADE FAILED: rendered manifests contain a resource that already exists. Unable to continue with update: CustomResourceDefinition "backuppolicies.moco.cybozu.com" in namespace "" exists and cannot be imported into the current release: invalid ownership metadata; label validation error: missing key "app.kubernetes.io/managed-by": must be set to "Helm"; annotation validation error: missing key "meta.helm.sh/release-name": must be set to "moco"; annotation validation error: missing key "meta.helm.sh/release-namespace": must be set to "moco-system"
```

Exec the following command to add annotations and labels to the CRD:

```console
$ kubectl annotate crd mysqlclusters.moco.cybozu.com meta.helm.sh/release-name='<YOUR RELEASE NAME>'
$ kubectl annotate crd backuppolicies.moco.cybozu.com meta.helm.sh/release-name='<YOUR RELEASE NAME>'
$ kubectl annotate crd mysqlclusters.moco.cybozu.com meta.helm.sh/release-namespace='<YOUR RELEASE NAMESPACE>'
$ kubectl annotate crd backuppolicies.moco.cybozu.com meta.helm.sh/release-namespace='<YOUR RELEASE NAMESPACE>'
$ kubectl label crd mysqlclusters.moco.cybozu.com app.kubernetes.io/managed-by='Helm'
$ kubectl label crd backuppolicies.moco.cybozu.com app.kubernetes.io/managed-by='Helm'
```

If the labels and annotations are set properly, the upgrade will be successful.

```console
$ helm upgrade -n moco-system moco moco/moco
Release "moco" has been upgraded. Happy Helming!
NAME: moco
LAST DEPLOYED: Tue Dec  7 22:43:30 2021
NAMESPACE: moco-system
STATUS: deployed
REVISION: 2
TEST SUITE: None
```

### Changed
- Bump appVersion to 0.11.0.

## [0.1.2] - 2021-11-18

### Changed
- Bump appVersion to 0.10.9.

## [0.1.1] - 2021-11-12

### Changed
- Bump appVersion to 0.10.8.

## [0.1.0] - 2021-11-02

This is the first release.

[Unreleased]: https://github.com/cybozu-go/moco/compare/chart-v0.2.1...HEAD
[0.2.1]: https://github.com/cybozu-go/moco/compare/chart-v0.2.0...chart-v0.2.1
[0.2.0]: https://github.com/cybozu-go/moco/compare/chart-v0.1.2...chart-v0.2.0
[0.1.2]: https://github.com/cybozu-go/moco/compare/chart-v0.1.1...chart-v0.1.2
[0.1.1]: https://github.com/cybozu-go/moco/compare/chart-v0.1.0...chart-v0.1.1
[0.1.0]: https://github.com/cybozu-go/moco/releases/tag/chart-v0.1.0
