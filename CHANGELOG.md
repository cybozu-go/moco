# Change Log

All notable changes to this project will be documented in this file.
This project adheres to [Semantic Versioning](http://semver.org/).

## [Unreleased]


## [0.31.0] - 2025-12-09

### Notice
GitHub Discussions has been closed. Please open an Issue for any questions, feature requests, or discussions going forward.

### Changed
- Document the Developer Certificate of Origin sign-off policy in contributor docs [#863](https://github.com/cybozu-go/moco/pull/863)

### Added
- Support for Kubernetes v1.34, including updated tooling, CRDs, and CI workflows [#860](https://github.com/cybozu-go/moco/pull/860) [#853](https://github.com/cybozu-go/moco/pull/853)
- Support MySQL 8.0.44 and 8.4.7, with corresponding container images [#858](https://github.com/cybozu-go/moco/pull/858) [#856](https://github.com/cybozu-go/moco/pull/856)
- Added `(agent,fluentbit,mysqldExporter).image.(repository,tag)` values, to set `--agent-image`, `--fluent-bit-image`, `--mysqld-exporter-image` args on controller [#827](https://github.com/cybozu-go/moco/pull/827)

### Fixed
- Compare replicas and partition numbers correctly when confirming StatefulSet rollout readiness in the partition controller [#857](https://github.com/cybozu-go/moco/pull/857)
- Scope PVC resize targets to the MySQL cluster namespace to avoid unintended updates [#857](https://github.com/cybozu-go/moco/pull/857)

### Contributors
- @dmaes

## [0.30.0] - 2025-09-26

### Notice
GitHub Discussions will be closed in the next release. Please open an Issue for any questions, feature requests, or discussions going forward.

### Changed
- Allow setting user and group security context for additional containers [#835](https://github.com/cybozu-go/moco/pull/835)
- Upgrade Kubernetes 1.33 (includes related dependency updates) [#831](https://github.com/cybozu-go/moco/pull/831)

### Fixed
- Increase test stability for SemiSyncMasterWaitSessions updates [#825](https://github.com/cybozu-go/moco/pull/825)
- Download assets without setup-envtest [#838](https://github.com/cybozu-go/moco/pull/838)

## [0.29.0] - 2025-09-09

### Added
- Build mysqld_exporter and flunet-bit container image [#828](https://github.com/cybozu-go/moco/pull/828)

### Changed
- Long wait for SetReadOnly to complete [#830](https://github.com/cybozu-go/moco/pull/830)
    - Due to this change, huge transactions will now be killed if they exist during a switchover. 

## [0.28.0] - 2025-08-06

### Added
- Support for Kubernetes v1.33 [#819](https://github.com/cybozu-go/moco/pull/819)
- Support MySQL 8.4.6/8.0.43 [#821](https://github.com/cybozu-go/moco/pull/821) [#822](https://github.com/cybozu-go/moco/pull/822)
- Add InitializeTimezoneData to populate timezone data with moco-init [#818](https://github.com/cybozu-go/moco/pull/818)
- Add slowQueryLogConfigTmpl options to use custom slow-log config [#810](https://github.com/cybozu-go/moco/pull/810)
- Prometheus PodMonitors configured by Helm chart [#815](https://github.com/cybozu-go/moco/pull/815)

### Fixed
- docs: Add install aqua procedure before `make start` [#807](https://github.com/cybozu-go/moco/pull/807)
- Exclude replicas with uncommitted trx during failover [#816](https://github.com/cybozu-go/moco/pull/816)

### Contributors
- @kdambekalns
- @kevinrudde
- @vholer

## [0.27.1] - 2025/05/28
### Fixed
- Add setup-aqua to release workflow [#806](https://github.com/cybozu-go/moco/pull/806)

## [0.27.0] - 2025-05-27
### Breaking Changes
- Support MySQL 8.4.5/8.0.42 [#794](https://github.com/cybozu-go/moco/pull/794), [#798](https://github.com/cybozu-go/moco/pull/798) 

### Changed
- Manage CLI tools by aquaproj/aqua [#793](https://github.com/cybozu-go/moco/pull/793)

### Fixed
- Improved log messages when replication is stopped due to errant_replica [#799](https://github.com/cybozu-go/moco/pull/799)
- Dead pods should not be labeled with role=replica [#802](https://github.com/cybozu-go/moco/pull/802)

## [0.26.0] - 2025-03-04
### Breaking Changes

### ⚠️ Breaking Changes
- Support MySQL 8.4.4/8.0.41 [#775](https://github.com/cybozu-go/moco/pull/775), [#773](https://github.com/cybozu-go/moco/pull/773), [#774](https://github.com/cybozu-go/moco/pull/774)
- Support k8s 1.32 [#770](https://github.com/cybozu-go/moco/pull/770)

### ⚠️ End support for older versions
 - MySQL versions supported after this release are 8.0.28, 8.0.39, 8.0.40, 8.0.41 and 8.4.4
 - K8s versions supported after this release are 1.30, 1.31, 1.32

### Changed
- Update mysqld_exporter image [#765](https://github.com/cybozu-go/moco/pull/765)
- Update moco-agent [#770](https://github.com/cybozu-go/moco/pull/770)
- Run e2e test in parallel [#767](https://github.com/cybozu-go/moco/pull/767)
- Update dependencies [#760](https://github.com/cybozu-go/moco/pull/760) [#770](https://github.com/cybozu-go/moco/pull/770) [#783](https://github.com/cybozu-go/moco/pull/783) 

### Added
- Allow overwriteContainers to specify a securityContext [#778](https://github.com/cybozu-go/moco/pull/778)

### Fixed
- update known issues [#769](https://github.com/cybozu-go/moco/pull/769)
- Narrow down the target of StatefulSetPartitionReconciler [#781](https://github.com/cybozu-go/moco/pull/781)
- docs: fix spelling in summary.md [#785](https://github.com/cybozu-go/moco/pull/785)
   - Our thanks to [@sebastian-philipp](https://github.com/sebastian-philipp) for the contribution

## [0.25.1] - 2024-12-03

### Breaking Changes
[#751](https://github.com/cybozu-go/moco/pull/751), included in this release, needs ValidatingAdmissionPolicy. But this feature is not enabled default when K8s version < 1.30. So you have to enable the feature manually when you use those version.

### Changed
- Set timeoutSeconds to 50sec [#746](https://github.com/cybozu-go/moco/pull/746)
- Migrate to Ginkgo v2 [#750](https://github.com/cybozu-go/moco/pull/750)
- pods are deleted without waiting for completion when switchover takes a long time [#751](https://github.com/cybozu-go/moco/pull/751)

### Added
- build MySQL and mysqld_exporter images [#754](https://github.com/cybozu-go/moco/pull/754)
- Add log-rotation-size [#762](https://github.com/cybozu-go/moco/pull/762)

### Fixed
- issue-759: Select the appropriate apiVersion [#760](https://github.com/cybozu-go/moco/pull/760)


## [0.25.0] - 2024-11-20

This release was aborted due to a bug.
https://github.com/cybozu-go/moco/issues/759

## [0.24.1] - 2024-09-20

### Changed
- Update fluent-bit image to 3.1.7 [#738](https://github.com/cybozu-go/moco/pull/738)
- Add support for k8s 1.30 and 1.31 [#727](https://github.com/cybozu-go/moco/pull/727)
- Bump google.golang.org/grpc from 1.64.0 to 1.64.1 [719](https://github.com/cybozu-go/moco/pull/719)

### Added
- Add mysql-configmap-history-limit flags [#733](https://github.com/cybozu-go/moco/pull/733)
- rebuild mysql image and add mysqld 8.0.39, 8.4.2 [#728](https://github.com/cybozu-go/moco/pull/728)
- Add statefulset partition controller [#633](https://github.com/cybozu-go/moco/pull/633),[#628](https://github.com/cybozu-go/moco/pull/628)
- Load only the named schema from the dump files [#726](https://github.com/cybozu-go/moco/pull/726)
- Add procedure to set cluster to read-only [#724](https://github.com/cybozu-go/moco/pull/724)
- Allows customization of service ports [#723](https://github.com/cybozu-go/moco/pull/723)

### Fixed
- Add a line break to kubectl-moco start/stop command's message [#737](https://github.com/cybozu-go/moco/pull/737)
- Cannot replicate because the master purged required binary logs [#731](https://github.com/cybozu-go/moco/pull/731)
- Add missing assertion method [#709](https://github.com/cybozu-go/moco/pull/709)

## [0.23.2] - 2024-07-02

### Fixed
- Do not kill system user [#710](https://github.com/cybozu-go/moco/issues/710)

## [0.23.1] - 2024-06-25

### ⚠️ Breaking Changes
 - Support MySQL 8.4.0 [#686](https://github.com/cybozu-go/moco/issues/686)
   - With MySQL 8.4 support, MOCO no longer runs on MySQL 8.0.25 or earlier

### ⚠️ End support for older versions
 - MySQL versions supported after this release are 8.0.28, 8.0.36, 8.0.37 and 8.4.0

## Added
 - Add kubectl moco stop/start clustering description [#649](https://github.com/cybozu-go/moco/pull/649)

 - MySQLClusters can now be taken offline without deleting data [#659](https://github.com/cybozu-go/moco/issues/659)
   - Our thanks to [@vsliouniaev](https://github.com/vsliouniaev) for the contribution

 - mysqld_exporter now supports MySQL 8.4 [#686](https://github.com/cybozu-go/moco/issues/686)

## [0.22.1] - 2024-06-21

This release was canceled due to an operational error.

## [0.21.1] - 2024-06-13

### Notable changed:
Due to an update of the github.com/aws/aws-sdk-go-v2 library, users must specify the region of the bucket in their BackupPolicy if the bucketConfig.backendType is s3 or not specified. If the region is not specified, the backup/restore job will crash.
ref: aws/aws-sdk-go-v2#2502

### Added
- Add mysql-admin port to the headless service [#658](https://github.com/cybozu-go/moco/pull/658)
- Add option to to use localhost instead of pod name to CRD [#662](https://github.com/cybozu-go/moco/pull/662) (@vsliouniaev)
- Add support for MySQL 8.0.36,8.0.37 and K8s 1.28,1.29 [#676](https://github.com/cybozu-go/moco/pull/676),[#667](https://github.com/cybozu-go/moco/pull/667),[#671](https://github.com/cybozu-go/moco/pull/671)
- Add to prevent disabling of binlog in MySQL cnf generator [#678](https://github.com/cybozu-go/moco/pull/678)
- Add check for limit cluster name length [#679](https://github.com/cybozu-go/moco/pull/679)
- Add ARM64 processor adaptation [#681](https://github.com/cybozu-go/moco/pull/681) (@vholer)

### Changed
- Bump google.golang.org/protobuf from 1.31.0 to 1.33.0 [#654](https://github.com/cybozu-go/moco/pull/654)
- Bump golang.org/x/net from 0.18.0 to 0.23.0 [#663](https://github.com/cybozu-go/moco/pull/663)
- Bump fluent-bit 3.0.2 for MOCO [#665](https://github.com/cybozu-go/moco/pull/665)
- Bump mysql_exporter v0.15.1 [#666](https://github.com/cybozu-go/moco/pull/666)
- Bump moco-backup tools version [#672](https://github.com/cybozu-go/moco/pull/672)
- Update go.mod for the latest moco-agent [#690](https://github.com/cybozu-go/moco/pull/690)


## 0.21.0 - 2024-06-13

This release was canceled due to occur conflict.


## [0.20.2] - 2024-03-08

### Fixed
- Add / delimiter to the end of path in calcPrefix [#648](https://github.com/cybozu-go/moco/pull/648)


## [0.20.1] - 2024-01-24

### Fixed
- issue-604: Added backward compatibility [#640](https://github.com/cybozu-go/moco/pull/640)
- Use net.JoinHostPort when constructing ip address: [#631](https://github.com/cybozu-go/moco/pull/631)

### Changed
- ci: Convert e2e to workflow_dispatch [#623](https://github.com/cybozu-go/moco/pull/623)
- Migrate to ghcr.io [#635](https://github.com/cybozu-go/moco/pull/635)

## [0.20.0] - 2023-12-19

### Breaking Changes

#### ⚠️ Removal of Deprecated APIs
The MOCO API version v1beta1 have been removed.
You must ensure that all MOCO custom resources are stored in etcd at v1beta2 before upgrading.

### Added
- Support MySQL v8.0.35 [#614](https://github.com/cybozu-go/moco/pull/614) [#617](https://github.com/cybozu-go/moco/pull/617)
- Upgrade fluent-bit container to 2.2.0.1 [#615](https://github.com/cybozu-go/moco/pull/615) [#618](https://github.com/cybozu-go/moco/pull/618)
- Sync PVC labels and annotations [#613](https://github.com/cybozu-go/moco/pull/613)

### Fixed
- fix wrong error message [#607](https://github.com/cybozu-go/moco/pull/607)
- Fix binlog backup algorithm not to be missing [#604](https://github.com/cybozu-go/moco/pull/604)
- Set PDB maxUnavailable equal to 0 when executing backup jobs [#612](https://github.com/cybozu-go/moco/pull/612)
- Stop reconciliation and clustering[#578](https://github.com/cybozu-go/moco/pull/578)

### Changed

- The API version v1beta1 is removed. [#602](https://github.com/cybozu-go/moco/pull/602)
- CI: Enable cancel-in-progress. [#619](https://github.com/cybozu-go/moco/pull/619)
- Update dependencies [#616](https://github.com/cybozu-go/moco/pull/616) [#620] (https://github.com/cybozu-go/moco/pull/620) [#621](https://github.com/cybozu-go/moco/pull/621)

## [0.19.0] - 2023-11-14

### Breaking Changes

⚠️ The MOCO API version v1beta1 is no longer served.
If you deploy a custom resource that contains v1beta1, it will fail.
Please update the version of your custom resources before upgrading MOCO to v0.19.0.

### Fixed

- Pass password via command arguments on backup to avoid partial log line. [#605](https://github.com/cybozu-go/moco/pull/605)

### Changed

- The API version v1beta1 is no longer served. [#608](https://github.com/cybozu-go/moco/pull/608)

## [0.18.1] - 2023-11-06

The release workflow for 0.18.0 failed due to an error, and will be corrected and re-released as 0.18.1.

### Changed
- Bump goreleaser-action and goreleaser [#599](https://github.com/cybozu-go/moco/pull/599)

## [0.18.0] - 2023-11-02

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

[Unreleased]: https://github.com/cybozu-go/moco/compare/v0.31.0...HEAD
[0.31.0]: https://github.com/cybozu-go/moco/compare/v0.30.0...v0.31.0
[0.30.0]: https://github.com/cybozu-go/moco/compare/v0.29.0...v0.30.0
[0.29.0]: https://github.com/cybozu-go/moco/compare/v0.28.0...v0.29.0
[0.28.0]: https://github.com/cybozu-go/moco/compare/v0.27.1...v0.28.0
[0.27.1]: https://github.com/cybozu-go/moco/compare/v0.27.0...v0.27.1
[0.27.0]: https://github.com/cybozu-go/moco/compare/v0.26.0...v0.27.0
[0.26.0]: https://github.com/cybozu-go/moco/compare/v0.25.1...v0.26.0
[0.25.1]: https://github.com/cybozu-go/moco/compare/v0.25.0...v0.25.1
[0.25.0]: https://github.com/cybozu-go/moco/compare/v0.24.1...v0.25.0
[0.24.1]: https://github.com/cybozu-go/moco/compare/v0.23.2...v0.24.1
[0.23.2]: https://github.com/cybozu-go/moco/compare/v0.23.1...v0.23.2
[0.23.1]: https://github.com/cybozu-go/moco/compare/v0.21.1...v0.23.1
[0.22.1]: https://github.com/cybozu-go/moco/compare/v0.21.1...v0.22.1
[0.21.1]: https://github.com/cybozu-go/moco/compare/v0.20.2...v0.21.1
[0.20.2]: https://github.com/cybozu-go/moco/compare/v0.20.1...v0.20.2
[0.20.1]: https://github.com/cybozu-go/moco/compare/v0.20.0...v0.20.1
[0.20.0]: https://github.com/cybozu-go/moco/compare/v0.19.0...v0.20.0
[0.19.0]: https://github.com/cybozu-go/moco/compare/v0.18.1...v0.19.0
[0.18.1]: https://github.com/cybozu-go/moco/compare/v0.18.0...v0.18.1
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
