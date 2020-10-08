# End-to-end test suites

## Strategy

We try to maximize coverage of unit tests even if it needs "real" MySQL instances.  The tests using real MySQL instances can be divided into the unit tests and the e2e tests as follows:
- test by unit test
  - initialization of MySQL instance
  - status retrieval
  - cloning
  - metrics collection
  - log file management (rotate)
- test by e2e test
  - failover/switchover
  - replication
  - backup/restore
  - point-in-time recovery
  - upgrade
  - blackout recovery
  - failure injection

The unit tests are written in each `_test.go` file.

The remains are:
- YAML manifests
- `main` functions of each programs

## Analysis

### Feature functions

The following feature functions should be examined by checking the status of `MySQLCluster` custom resource and MySQL instances.
- failover/switchover
- replication
- backup/restore
- point-in-time recovery
- upgrade
- blackout recovery
- failure injection

### YAML manifests

`MySQLCluster` custom resource accepts some flexible settings such as the templates of Pod, Volume, and Service.  So, we should examine the settings are correctly applied to the resources made by MOCO.

Functions represented by MOCO's custom resource (i.e., `MySQLBackup`, `MySQLSwitchOver`) also should be confirmed.

### `main` functions

#### `moco-controller`

- Manager setup
- Metrics server

#### `agent`

- Read password files
- HTTP server for agent APIs


## How to test

TBD
