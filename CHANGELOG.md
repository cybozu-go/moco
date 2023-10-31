# Change Log

All notable changes to this project will be documented in this file.
This project adheres to [Semantic Versioning](http://semver.org/).

## [Unreleased]

## [0.18.0] - 2023-10-31

### Notification

With this release, the storage version of CRD has been changed from `v1beta1` to `v1beta2`. [#586](https://github.com/cybozu-go/moco/pull/586)
In addition, `v1beta1` has been deprecated and will be removed in the next version. [#592](https://github.com/cybozu-go/moco/pull/592)

### Added
- Add MySQL 8.0.33 an 8.0.34 containers [#574](https://github.com/cybozu-go/moco/pull/574)
- Support MySQL 8.0.33 and 8.0.34 [#575](https://github.com/cybozu-go/moco/pull/575)
- Enable cpu specification in jobConfig [#582](https://github.com/cybozu-go/moco/pull/582)
- Add tls settings for BackupPolicy [#580](https://github.com/cybozu-go/moco/pull/580)

### Changed
- api: change the storage version to v1beta2 [#586](https://github.com/cybozu-go/moco/pull/586)
- api: add deprecatedversion marker [#592](https://github.com/cybozu-go/moco/pull/592)
- Build moco-controller with go 1.21 [#571](https://github.com/cybozu-go/moco/pull/571)
- Build moco-controller and moco-backup with ubuntu-22.04 [#575](https://github.com/cybozu-go/moco/pull/575)
- Bump golang.org/x/net from 0.10.0 to 0.17.0 [#579](https://github.com/cybozu-go/moco/pull/579)
- Bump google.golang.org/grpc from 1.55.0 to 1.56.3 [#594](https://github.com/cybozu-go/moco/pull/594)

### Fixed
- Use Gomega matcher in Eventually and check pods' labels [#584](https://github.com/cybozu-go/moco/pull/584)
- Remove copy code of FindTopRunner [#585](https://github.com/cybozu-go/moco/pull/585)
- Deduplicate SSA related code [#583](https://github.com/cybozu-go/moco/pull/583)
- Kill old connections when demoting primary [#561](https://github.com/cybozu-go/moco/pull/561)
- Fix documentations [#590](https://github.com/cybozu-go/moco/pull/590), [#589](https://github.com/cybozu-go/moco/pull/589)
- Fix deprecated Goreleaser's options [#591](https://github.com/cybozu-go/moco/pull/591)
- Run e2e tests on larger runners [#593](https://github.com/cybozu-go/moco/pull/593)
- Revert "Fix flaky test" [#595](https://github.com/cybozu-go/moco/pull/595)
- Kill existing connections when changing roles [#587](https://github.com/cybozu-go/moco/pull/587)

## [0.17.0] - 2023-09-11

### Breaking Changes

#### Migrate image registry
We migrated the image repository of mysql, fluent-bit, and mysqld_exporter to `ghcr.io`.
From MOCO v0.17.0, please use the following images.

- [ghcr.io/cybozu-go/moco/mysql](https://github.com/cybozu-go/moco/pkgs/container/moco%2Fmysql)
- [ghcr.io/cybozu-go/moco/fluent-bit](https://github.com/cybozu-go/moco/pkgs/container/moco%2Ffluent-bit)
- [ghcr.io/cybozu-go/moco/mysqld_exporter](https://github.com/cybozu-go/moco/pkgs/container/moco%2Fmysqld_exporter)

The [`quay.io/cybozu/mysql`](https://quay.io/repository/cybozu/mysql), [`quay.io/cybozu/fluent-bit`](https://quay.io/repository/cybozu/fluent-bit), and [`quay.io/cybozu/mysqld_exporter`](https://quay.io/repository/cybozu/mysqld_exporter) will not be updated in the future.

In addition, from this time, the mysql image does not contain the moco-init binary.
Therefore, MOCO v0.13.0 or lower cannot use `ghcr.io/cybozu-go/moco/mysql`.

#### Dropped metrics
The default mysqld_exporter has been upgraded to [v0.15.0](https://github.com/prometheus/mysqld_exporter/releases/tag/v0.15.0).
Accordingly, the following metrics will no longer be output.
  - `mysql_exporter_scrapes_total`
  - `mysql_exporter_scrape_errors_total`
  - `mysql_last_scrape_failed`

### Added
- Support Kubernetes 1.27 [#525](https://github.com/cybozu-go/moco/pull/525)
- Support redunce volume size [#538](https://github.com/cybozu-go/moco/pull/538), [#552](https://github.com/cybozu-go/moco/pull/552)
- Upgrade fluent-bit v2.1.8 and mysqld-exporter v0.15.0 [#553](https://github.com/cybozu-go/moco/pull/553), [#554](https://github.com/cybozu-go/moco/pull/554)
- Add Updated Conditon in Status [#546](https://github.com/cybozu-go/moco/pull/546)

### Changed
- Disable innodb_undo_log_truncate [#526](https://github.com/cybozu-go/moco/pull/526)
- Migrate image registry from qury.io to ghcr.io [#528](https://github.com/cybozu-go/moco/pull/528), [#529](https://github.com/cybozu-go/moco/pull/529), [#533](https://github.com/cybozu-go/moco/pull/533), [#535](https://github.com/cybozu-go/moco/pull/535), [#542](https://github.com/cybozu-go/moco/pull/542), [#555](https://github.com/cybozu-go/moco/pull/555)
- incorporate moco-agent v0.10.0 [#551](https://github.com/cybozu-go/moco/pull/551)
- Fix retry message when gathering mysqld status [#559](https://github.com/cybozu-go/moco/pull/559)

### Fixed
- backup: PITR did not work sometimes [#565](https://github.com/cybozu-go/moco/pull/565)
- Set kubeVersion compatible with EKS [#536](https://github.com/cybozu-go/moco/pull/536)
- MySQLClusterSpec.BackupPolicyName should be annotated omitempty [#532](https://github.com/cybozu-go/moco/pull/532)
- Fix broken links [#541](https://github.com/cybozu-go/moco/pull/541)
- Fix log message when demote annotation is added [#560](https://github.com/cybozu-go/moco/pull/560)

### Contributors
- @fgeorgeanybox

## [0.16.1] - 2023-04-07

### Added
- Add podAntiAffinity to MySQL Cluster StatefulSet. [#513](https://github.com/cybozu-go/moco/pull/513)
- Support Google Cloud Storage [#493](https://github.com/cybozu-go/moco/pull/493) [#501](https://github.com/cybozu-go/moco/pull/501)
- Add qps flag [#518](https://github.com/cybozu-go/moco/pull/518)

### Changed
- Bump golang.org/x/net from 0.3.1-0.20221206200815-1e63c2f08a10 to 0.7.0 [#514](https://github.com/cybozu-go/moco/pull/514)

### Fixed
- Wait until all Pods are deleted in E2E [#510](https://github.com/cybozu-go/moco/pull/510)
- Fix flaky test [#515](https://github.com/cybozu-go/moco/pull/515)
- Disable the delay check if .spec.maxDelaySeconds == 0 [#516](https://github.com/cybozu-go/moco/pull/516)
- Stop releasing ARM64 image [#522](https://github.com/cybozu-go/moco/pull/522)

## 0.16.0 - 2023-03-28

This release was canceled because the release workflow was incorrect.

## [0.15.0] - 2023-02-21

### Added
- Support Kubernetes v1.26 [#495](https://github.com/cybozu-go/moco/pull/495)
- Support MySQL 8.0.32 [#505](https://github.com/cybozu-go/moco/pull/505)
- Add default affinity for BackupPolicySpec [#471](https://github.com/cybozu-go/moco/pull/471)
- Add max-concurrent-reconciles option [#498](https://github.com/cybozu-go/moco/pull/498)
- Add metrics to measure processing time [#499](https://github.com/cybozu-go/moco/pull/499)
- Add pprof server to moco-controller [#500](https://github.com/cybozu-go/moco/pull/500)

### Changed
- Update fluent-bit v2.0.9 [#508](https://github.com/cybozu-go/moco/pull/508)
- Add id to cluster manager's log [#503](https://github.com/cybozu-go/moco/pull/503)
- Build moco-controller with go 1.19 [#506](https://github.com/cybozu-go/moco/pull/506)

### Fixed
- Ignore primary's event when detecting errant replicas [#491](https://github.com/cybozu-go/moco/pull/491)
- Use ExecutedGtidSet and RetrievedGtidSet when determining the primary [#474](https://github.com/cybozu-go/moco/pull/474)
- Enable go.mod cache [#504](https://github.com/cybozu-go/moco/pull/504)

## [0.14.1] - 2022-12-08

### Fixed
- Downgrade mysqlsh to 8.0.30 [#485](https://github.com/cybozu-go/moco/pull/485)

## [0.14.0] - 2022-11-28

### Breaking Changes
This release allows MOCO users to leave the moco-init binary out of a custom mysqld container image.
Now the moco-init binary is copied from the moco-agent container through an emptyDir volume.

To maintain backward compatibility, the [`mysql` images](https://quay.io/repository/cybozu/mysql?tab=tags) provided by Cybozu still contain the old moco-init binary.

### Added
- Copy moco-init binary from moco-agent image [#461](https://github.com/cybozu-go/moco/pull/461)
- Support Kubernetes v1.25 [#467](https://github.com/cybozu-go/moco/pull/467)
- Support MySQL 8.0.31 [#479](https://github.com/cybozu-go/moco/pull/479)
- Introduce completion [#470](https://github.com/cybozu-go/moco/pull/470), [#473](https://github.com/cybozu-go/moco/pull/473)
- Uses a probe defined by the user [#472](https://github.com/cybozu-go/moco/pull/472)

### Fixed
- Ignore MySQL error 1094 when killing connections [#476](https://github.com/cybozu-go/moco/pull/476)
- Detect differences of service appropriately [#457](https://github.com/cybozu-go/moco/pull/457)
- Normalize time values in Certificate resource [#460](https://github.com/cybozu-go/moco/pull/460)

### Changed
- Use reusing workflow [#465](https://github.com/cybozu-go/moco/pull/465)
- Stop using set-output [#469](https://github.com/cybozu-go/moco/pull/469)
- Update dependencies [#478](https://github.com/cybozu-go/moco/pull/478)

## [0.13.0] - 2022-09-12

### Added
- Support apply PVC template changes to StatefulSet (#403, #412, #417, #420, #424)
- Support kubernetes v1.24 (#429)
- Support MySQL 8.0.30 (#444)

### Changed
- Make sure partial_revokes is enabled (#414)
- Use Docker action (#416, #418)
- Disable the delay check if .spec.maxDelaySeconds == 0 in MySQLCluster (#434)
- Remove ineffective default setting (max_connect_errors) (#446)
- Add fsGroup and fsGroupChangePolicy to Pod specs (#443)
- Migrate batch/v1beta1.CronJob to batch/v1.CronJob (#447)
- Migrate from policyv1beta1.PodDisruptionBudget to policyv1.PodDisruptionBudget (#452)
- Update fluent-bit and mysqld_exporter container (#453)
- Delete cluster after E2E to reduce flaky test failures (#448)
- Update release procedure (#423)

### Contributors

- @inductor
- @jelmer

## [0.12.1] - 2022-04-26

### Fixed
- Increase memory limit and request for moco-init (#409)

## [0.12.0] - 2022-04-22

### Added
- Allow users to customize containers resources (#394, #395)
- Clarify a known issue about multi-threaded replication (#400, #401)

### Changed
- Update mysqld_exporter and Fluent Bit (#402)
- Support k8s 1.23 (#399)
- Update actions (#398)

### Fixed
- Fix broken helm chart (#397)

## [0.11.1] - 2022-03-16

### Changed
- Using SSA to create Secret (#387)
- Validate that container name is non-nil in validation webhook (#388)

### Fixed
- Fixed duplicate key in structured log (#386)
- Add securityContext to the user-supplied containers (#390)

## [0.11.0] - 2022-03-07

### Added
- Add MySQLCluster v1beta2 (#350, #359, #376)
- Support MySQL 8.0.28 (#369)

### Changed
- Using SSA with moco-controller (#364, #372, #373, #374, #379)
- Update krew description (#351)
- Update klog to v2 (#355)
- Update Fluent Bit container image to v1.8.11 (#357)

### Fixed
- Stop using Reconcile's context for ClusterManager (#353)
- backup: calculate disk usage accurately (#356)
- Do not stop gathering cluster status if one or more Pods are pending (#366)
- Add nil check when restoring errant replica status (#367)
- Update how-to doc to grant PROXY when replicating cluster from outside (#381)

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

[Unreleased]: https://github.com/cybozu-go/moco/compare/v0.18.0...HEAD
[0.18.0]: https://github.com/cybozu-go/moco/compare/v0.17.0...v0.18.0
[0.17.0]: https://github.com/cybozu-go/moco/compare/v0.16.1...v0.17.0
[0.16.1]: https://github.com/cybozu-go/moco/compare/v0.15.0...v0.16.1
[0.15.0]: https://github.com/cybozu-go/moco/compare/v0.14.1...v0.15.0
[0.14.1]: https://github.com/cybozu-go/moco/compare/v0.14.0...v0.14.1
[0.14.0]: https://github.com/cybozu-go/moco/compare/v0.13.0...v0.14.0
[0.13.0]: https://github.com/cybozu-go/moco/compare/v0.12.1...v0.13.0
[0.12.1]: https://github.com/cybozu-go/moco/compare/v0.12.0...v0.12.1
[0.12.0]: https://github.com/cybozu-go/moco/compare/v0.11.1...v0.12.0
[0.11.1]: https://github.com/cybozu-go/moco/compare/v0.11.0...v0.11.1
[0.11.0]: https://github.com/cybozu-go/moco/compare/v0.10.9...v0.11.0
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
