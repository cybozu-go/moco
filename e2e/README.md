# End-to-end test suites

## Strategy

The automated tests are divided into three categories:

1. Unit Tests: that can be written with just Golang.
1. Small Tests: that use **real** MySQL instances and envtest (kube-apiserver + etcd).
1. End-to-end (e2e) Test: that use MOCO and MySQL instances as a container deployed on a Kubernetes cluster using the kind.

We write tests according to the following policies:

- Test early: We maximize Unit tests and Small tests coverage as much as possible.
- Avoid using mocks: If you want to interact with kube-apiserver or MySQL, use the **real** components instead of using mock.
- Fast test: Avoid slow tests.

For example, the following tests could be written in Small tests:

- Initialization of MySQL instance
- Retrieval of MySQL instance
- Cloning MySQL instance
- Metrics collection
- Log file management

This directory contains just e2e tests.
Unit tests and Small tests exist in each package directory.

## Analysis

As explained above, tests that cannot be covered by Unit tests and Small tests are conducted by e2e tests.

- `main` function
- Complex scenarios

### `main` functions

#### `moco-controller`

- Manager setup
- Leader election
- Admission Webhook
- Metrics server
- Event recording
- Garbage collector

#### `entrypoint`

- Read password files
- HTTP server for agent APIs

### Scenarios

The following feature functions should interact with MySQL on a Kubernetes cluster, so they need to be examined by e2e tests.

- Failover
- Switchover
- Intermediate Primary
- Backup/Restore
- Point-in-Tme recovery
- Upgrade MySQL version
- Increase replicas
- Recovery from blackout
- Failure injection

## How to test

TBD
