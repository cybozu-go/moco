# Change Log

All notable changes to this project will be documented in this file.
This project adheres to [Semantic Versioning](http://semver.org/).

## [Unreleased]

## [0.10.2] - 2024-03-08
### Changed
- Bump appVersion to 0.20.2 [#650](https://github.com/cybozu-go/moco/pull/650)

## [0.10.1] - 2024-01-24
### Changed
- Bump appVersion to 0.20.1 [#642](https://github.com/cybozu-go/moco/pull/642)

## [0.10.0] - 2023-12-20
### Changed
- Bump appVersion to 0.20.0 [#627](https://github.com/cybozu-go/moco/pull/627)

## [0.9.0] - 2023-11-14
### Changed
- Bump appVersion to 0.19.0 [#610](https://github.com/cybozu-go/moco/pull/610)

## [0.8.0] - 2023-11-02
### Changed
- Bump appVersion to 0.18.1 [#598](https://github.com/cybozu-go/moco/pull/598)

## [0.7.0] - 2023-09-12

### Changed
- Bump appVersion to 0.17.0. [#569](https://github.com/cybozu-go/moco/pull/569)

### Fixed
- Set kubeVersion compatible with EKS [#536](https://github.com/cybozu-go/moco/pull/536)

### Contributors
- @fgeorgeanybox

## [0.6.0] - 2023-04-06

### Changed
- Bump appVersion to 0.16.1. [#521](https://github.com/cybozu-go/moco/pull/521)

## [0.5.0] - 2023-02-24

### Changed
- Bump appVersion to 0.15.0. [#511](https://github.com/cybozu-go/moco/pull/511)

## [0.4.1] - 2022-12-09

### Changed
- Relax the kubeVersion constraints in Chart.yaml [#487](https://github.com/cybozu-go/moco/pull/487)
- Bump appVersion to 0.14.1. [#489](https://github.com/cybozu-go/moco/pull/489)

## [0.4.0] - 2022-11-29

### Added
- Add topologySpreadConstraints helm chart value [#455](https://github.com/cybozu-go/moco/pull/455)
- Add extraArgs to helm values [#466](https://github.com/cybozu-go/moco/pull/466)
- Specify minimum Kubernetes version [#468](https://github.com/cybozu-go/moco/pull/468)

## [0.3.0] - 2022-09-12

This release has breaking changes to the helm chart.
If you installed the chart with a release name other than `moco`, please migrate following [this procedure](./README.md#migrate-to-v030).

### Added
- Add helm chart values (#450)

### Changed
- Bump appVersion to 0.13.0.
- Rename helm resources (#432, #440)

## [0.2.3] - 2022-04-26

### Changed
- Bump appVersion to 0.12.1.

## [0.2.2] - 2022-04-22

### Changed
- Bump appVersion to 0.12.0.

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

[Unreleased]: https://github.com/cybozu-go/moco/compare/chart-v0.10.2...HEAD
[0.10.2]: https://github.com/cybozu-go/moco/compare/chart-v0.10.1...chart-v0.10.2
[0.10.1]: https://github.com/cybozu-go/moco/compare/chart-v0.10.0...chart-v0.10.1
[0.10.0]: https://github.com/cybozu-go/moco/compare/chart-v0.9.0...chart-v0.10.0
[0.9.0]: https://github.com/cybozu-go/moco/compare/chart-v0.8.0...chart-v0.9.0
[0.8.0]: https://github.com/cybozu-go/moco/compare/chart-v0.7.0...chart-v0.8.0
[0.7.0]: https://github.com/cybozu-go/moco/compare/chart-v0.6.0...chart-v0.7.0
[0.6.0]: https://github.com/cybozu-go/moco/compare/chart-v0.5.0...chart-v0.6.0
[0.5.0]: https://github.com/cybozu-go/moco/compare/chart-v0.4.1...chart-v0.5.0
[0.4.1]: https://github.com/cybozu-go/moco/compare/chart-v0.4.0...chart-v0.4.1
[0.4.0]: https://github.com/cybozu-go/moco/compare/chart-v0.3.0...chart-v0.4.0
[0.3.0]: https://github.com/cybozu-go/moco/compare/chart-v0.2.3...chart-v0.3.0
[0.2.3]: https://github.com/cybozu-go/moco/compare/chart-v0.2.2...chart-v0.2.3
[0.2.2]: https://github.com/cybozu-go/moco/compare/chart-v0.2.1...chart-v0.2.2
[0.2.1]: https://github.com/cybozu-go/moco/compare/chart-v0.2.0...chart-v0.2.1
[0.2.0]: https://github.com/cybozu-go/moco/compare/chart-v0.1.2...chart-v0.2.0
[0.1.2]: https://github.com/cybozu-go/moco/compare/chart-v0.1.1...chart-v0.1.2
[0.1.1]: https://github.com/cybozu-go/moco/compare/chart-v0.1.0...chart-v0.1.1
[0.1.0]: https://github.com/cybozu-go/moco/releases/tag/chart-v0.1.0
