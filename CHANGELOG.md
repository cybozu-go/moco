# Change Log

All notable changes to this project will be documented in this file.
This project adheres to [Semantic Versioning](http://semver.org/).

## [Unreleased]

## [0.10.9] - 2021-11-18

### Fixed
- Pass spec.startupWaitSeconds for Clone request (#345)
- Update moco-agent to v0.7.1 (#345)

## [0.10.8] - 2021-11-12

### Added
- Add Kubernetes 1.22 Support (#338)

### Fixed
- Fix backup failure due to incorrect part size calculation (#339)
- Fix installation procedure of kubectl-moco (#341)

## [0.10.7] - 2021-11-02

### Added
- Add MySQL 8.0.27 Support (#330)
- Add Helm charts (#317)
- Support krew release (#312)

### Fixed
- Fix available status when all replicas are not ready (#323)
- Dynamically adjust part size when uploading backup files (#319) 
- Disable replication_sender_observe_commit_only (#333)
- kubectl-moco: Fix crash bug when kubeconfig file does not exist (#327)
- kubeclt-moco: Import auth plugin (#328)
- kubectl-moco: Remove redundant error message (#329)

## [0.10.6] - 2021-10-05

### Added
- Add MySQL 8.0.26 Support (#302)
- Supports more than 5 MySQL instances (#303)

### Fixed
- Fix a dead link in GitHub pages (#301)
- Set TMPDIR for mysqlbinlog (#309)

## [0.10.5] - 2021-07-29

### Changed
- Create ServiceAccount for moco controller (#281)
- Change LICENSE from MIT to Apache 2 (#291)

### Fixed
- Random failure when restoring a database (#284)
- Failed to restore data due to missing PROXY privilege (#287)

## [0.10.4] - 2021-07-07

### Fixed
- Fix delete BackupPolicy error (#276)
- Fix not to be affected by the system time zone (#277)

### Added
- Add History Limit to BackupPolicySpec (#279)

## [0.10.3] - 2021-07-06

### Fixed
- Watch ConfigMap for customizing my.cnf (#271)

## [0.10.2] - 2021-06-24

### Changed
- Update moco-agent to 0.6.8 (#269)

## [0.10.1] - 2021-06-22

### Changed
- Update moco-agent to 0.6.7 (#266)

## [0.10.0] - 2021-06-13

### Changed
- Migrate the official MySQL image repository to [quay.io/cybozu/mysql](https://quay.io/cybozu/mysql) (#262)
- Update controller-runtime to 0.9.0 (#262)
- Update fluent-bit to 1.7.8 (#263)
- Update mysqld_exporter to 0.13.0 (#263)

## [0.9.5] - 2021-06-04

### Fixed
- Timeout while cloning large data (#261)
- Better handling of Cloning state (#261)
- Manual switch over did not take place immediately (#261)

### Changed
- Update `moco-agent` to 0.6.6 (#261)

## [0.9.4] - 2021-06-03

### Fixed
- Automatic switchover did not take place immediately (#260)

## [0.9.3] - 2021-06-02

### Fixed
- Failed to update Service if `spec.externalTrafficPolicy` was set to `Local` (#259)

## [0.9.2] - 2021-06-01

### Changed
- Fix RBAC for BackupPolicy (#258)

## [0.9.1] - 2021-05-31

### Changed
- Change the way to shrink MySQLCluster CRD (#257)

## [0.9.0] - 2021-05-31

### Added
- Backup and Point-in-Time Recovery feature (#247)
    - To use the new backup feature, clusters created with MOCO v0.8 needs to be re-created.
- Support for Kubernetes 1.21 (#251)
- Allow opaque `my.cnf` configurations (#252)
- Documentation site on https://cybozu-go.github.io/moco/ (#254)

### Changed
- Update `moco-agent` to v0.6.5 (#247)

### Fixed
- Controller failed to update Service if `spec.externalTrafficPolicy` is set to Local (#250)

## [0.8.3] - 2021-05-12

### Changed
- Set UID/GID of containers to 10000:10000 (#243)

## [0.8.2] - 2021-05-10

### Changed
- gRPC communication between moco-controller and moco-agent is protected with mTLS (#241)

## [0.8.1] - 2021-05-06

### Added
- built-in `mysqld_exporter` to expose `mysqld` metrics (#237)

### Changed
- binlog filename now has a proper prefix `binlog.` (#237)

## [0.8.0] - 2021-04-27

### Changed
- Everything.  There is no backward compatibility. (#228)
- The older release must be uninstalled before installing this version.

## [0.7.0] - 2021-02-22

Since v0.7.0, MOCO will no longer use CronJob for log rotation.
Please remove existing CronJobs manually after upgrading MOCO.

### Changed

- Stop using CronJob for log rotation. (#190, moco-agent#10)
- Update moco-agent to v0.2.1. (#202)

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

[Unreleased]: https://github.com/cybozu-go/moco/compare/v0.10.9...HEAD
[0.10.9]: https://github.com/cybozu-go/moco/compare/v0.10.8...v0.10.9
[0.10.8]: https://github.com/cybozu-go/moco/compare/v0.10.7...v0.10.8
[0.10.7]: https://github.com/cybozu-go/moco/compare/v0.10.6...v0.10.7
[0.10.6]: https://github.com/cybozu-go/moco/compare/v0.10.5...v0.10.6
[0.10.5]: https://github.com/cybozu-go/moco/compare/v0.10.4...v0.10.5
[0.10.4]: https://github.com/cybozu-go/moco/compare/v0.10.3...v0.10.4
[0.10.3]: https://github.com/cybozu-go/moco/compare/v0.10.2...v0.10.3
[0.10.2]: https://github.com/cybozu-go/moco/compare/v0.10.1...v0.10.2
[0.10.1]: https://github.com/cybozu-go/moco/compare/v0.10.0...v0.10.1
[0.10.0]: https://github.com/cybozu-go/moco/compare/v0.9.5...v0.10.0
[0.9.5]: https://github.com/cybozu-go/moco/compare/v0.9.4...v0.9.5
[0.9.4]: https://github.com/cybozu-go/moco/compare/v0.9.3...v0.9.4
[0.9.3]: https://github.com/cybozu-go/moco/compare/v0.9.2...v0.9.3
[0.9.2]: https://github.com/cybozu-go/moco/compare/v0.9.1...v0.9.2
[0.9.1]: https://github.com/cybozu-go/moco/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/cybozu-go/moco/compare/v0.8.3...v0.9.0
[0.8.3]: https://github.com/cybozu-go/moco/compare/v0.8.2...v0.8.3
[0.8.2]: https://github.com/cybozu-go/moco/compare/v0.8.1...v0.8.2
[0.8.1]: https://github.com/cybozu-go/moco/compare/v0.8.0...v0.8.1
[0.8.0]: https://github.com/cybozu-go/moco/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/cybozu-go/moco/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/cybozu-go/moco/compare/v0.5.1...v0.6.0
[0.5.1]: https://github.com/cybozu-go/moco/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/cybozu-go/moco/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/cybozu-go/moco/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/cybozu-go/moco/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/cybozu-go/moco/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/cybozu-go/moco/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/cybozu-go/moco/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/cybozu-go/moco/compare/5256088a31e70f2d29649b8b69b0c8e208eb1c70...v0.1.0
