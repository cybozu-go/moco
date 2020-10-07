# Change Log

All notable changes to this project will be documented in this file.
This project adheres to [Semantic Versioning](http://semver.org/).

## [Unreleased]

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

[Unreleased]: https://github.com/cybozu-go/moco/compare/v0.2.0...HEAD
[0.1.1]: https://github.com/cybozu-go/moco/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/cybozu-go/moco/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/cybozu-go/moco/compare/5256088a31e70f2d29649b8b69b0c8e208eb1c70...v0.1.0
