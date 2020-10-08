# End-to-end test suites

## Strategy

We try to maximize coverage of unit tests even if it needs "real" MySQL instances.
The tests using real MySQL instances can be divided into the small tests and the e2e tests as follows:

- by small test
  - initialization of MySQL instance
  - status retrieval
  - cloning
  - metrics collection
  - log file management (rotate)
- by e2e test
  - failover
  - switchover
  - replication
  - backup/restore
  - point-in-time recovery
  - upgrade
  - blackout recovery
  - failure injection

The small tests are written in each `_test.go` file.

The remains are:
- YAML manifests
- `main` functions of each programs

## Analysis

### Feature functions

The following feature functions should be examined by checking the status of `MySQLCluster` custom resource and MySQL instances.

- failover
- switchover
- replication
- backup/restore
- point-in-time recovery
- upgrade
- blackout recovery
- failure injection

### YAML manifests

MOCO's custom resources accept some flexible settings such as the templates of Pod, Volume, and Service.
These manifests should be tested with small tests.

The other manifests to deploy MOCO are mostly tested together with e2e tests.

### `main` functions

#### `moco-controller`

- Manager setup
- Metrics server

#### `agent`

- Read password files
- HTTP server for agent APIs


## How to test

TBD
