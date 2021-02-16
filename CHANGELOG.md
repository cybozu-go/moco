# Change Log

All notable changes to this project will be documented in this file.
This project adheres to [Semantic Versioning](http://semver.org/).

## [Unreleased]

## [0.6.0] - 2021-02-16

**Caution**

- Since MOCO v0.6.0, MySQL data volumes (PVC) will be automatically deleted when the parent MySQLCluster resource is deleted.
  If you want to keep the volumes, please  delete the owner reference manually from the volumes (PVCs) resource.
  https://github.com/cybozu-go/moco/blob/main/docs/design.md#how-to-delete-resources-garbage-collection
- The `volumeClaimTemplates` field in the generated StatefulSet will be changed. This change will not be applied automatically.
  After upgrading moco from v0.5.x, please delete the existing StatefulSet manually.

### Added

- Add kubectl-moco command for Mac OS (darwin/amd64). (#184)
- Add PVC auto deletion. (#189)

### Changed

- Generate secrets for cluster from ControllerSecret. (#185, #192)

## [0.5.1] - 2021-02-04

### Changed

- Download e2e test tools for the host OS. (#182)

## [0.5.0] - 2021-02-03

**Breaking change**  
The `MySQLCluster` created by MOCO `< v0.5.0` has no compatibility with `>= v0.5.0` caused by the naming method of k8s resources (PR #161). Please recreate the cluster.

### Added

- Add kubectl-moco release workflow for windows. (#169)

### Changed

- Support official MySQL container image. (#165)
- Add 'moco-' prefix to resource name and remove UUID suffix (#161)

## [0.4.0] - 2021-01-26

### Added

- Add document about how to build MySQL container image. (#122)
- Add document about example of MySQLCluster CR. (#124)

### Changed

- Support MySQL 8.0.18 and 8.0.20 (#125, #141, #142)
- Support Kubernetes 1.19 and 1.20 (#157, #160)
- Update Go to 1.15 and Ubuntu base image to 20.04 (#153)

### Fixed

- Publish editor/viewer ClusterRoles with aggregation labels. (#116)
- Fix the login user option of `kubectl-moco`. (#117)
- Fix agent process crash bug. (#143)
- Prevent unnecessary reconciliation. (#146)
- Add `loose_` prefix to the `innodb_numa_interleave` system variable. (#158)

## [0.3.1] - 2020-11-11

### Added

- Add support for [klog](https://github.com/kubernetes/klog) options to `kubectl-moco` plugin (#110).
- Add `logRotationSecurityContext` field to `MySQLCluster` CRD to give PodSecurityContext for the log rotation CronJob (#111).

### Fixed

- Fix the location of an annotation in the deployment manifest (#107).
- Fix the behavior of `-it` option for `kubectl-moco` plugin (#109).
- Fix the default value of `-u` option for `kubectl-moco` plugin (#109).
- Add `moco-` prefix to the names in the deployment manifest (#112).  **You need to delete `moco-controller-manager` Deployment to apply the updated manifest.**
- Remove the resource limits for the controller from the deployment manifest (#115).

## [0.3.0] - 2020-11-05

### Added

- Use ServiceTemplate. (#65, #92)
- Configure intermediate primary (#74, #87)
- Add metrics for controller (#81)
- Add metrics for agents (#83)
- Add Event recording. (#84)
- kubectl-moco plugin (#93, #95)
- create PodDisruptionBudget (#99)

### Changed

- Modify manifests for deployment. (#97)

## [0.2.0] - 2020-10-07

### Added

- Generate MySQL configuration file with merging configmap resource (#39, #42)
- Add periodic log rotation mechanism (#43)
- Setup MySQL cluster with primary-replica (#50)
- Add Service resources to connect primary and replicas (#52)
- Do failover when a replica becomes unavailable (#53)
- Add token mechanism to call agent APIs (#55)
- Do failover when a primary becomes unavailable (#58)
- Support for Kubernetes 1.18 (#61)

## [0.1.1] - 2020-06-18

### Fixed

- Fix a build target bug (#36).

## [0.1.0] - 2020-06-18

### Added

- Bootstrap a vanilla MySQL cluster with no replicas (#2).

[Unreleased]: https://github.com/cybozu-go/moco/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/cybozu-go/moco/compare/v0.5.1...v0.6.0
[0.5.1]: https://github.com/cybozu-go/moco/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/cybozu-go/moco/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/cybozu-go/moco/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/cybozu-go/moco/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/cybozu-go/moco/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/cybozu-go/moco/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/cybozu-go/moco/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/cybozu-go/moco/compare/5256088a31e70f2d29649b8b69b0c8e208eb1c70...v0.1.0
