# End-to-end tests for MOCO

This directory contains test suites that runs MOCO on a real Kubernetes using [kind][].

## Strategy

We adopt [Test Pyramid](https://martinfowler.com/bliki/TestPyramid.html) and [Test Sizes](https://testing.googleblog.com/2010/12/test-sizes.html).

The end-to-end (e2e) tests are positioned at the top of the pyramid and "Large" ones.

MOCO has small and medium tests in package directories, so all packages are ensured to work by themselves.
Therefore, we include the following tests in the e2e suite.

- Manifests.
- MySQL cluster lifecycle (create, update, delete).
- Access mysqld via Services.
- Garbage collection after deleting MySQLCluster.
- Slow logs from a sidecar container.
- Metrics of `moco-controller`.
- `kubectl-moco` plugin features.
- Backup and restore features.

## How to run e2e tests

1. Prepare a Linux with Docker.
2. Run the following commands in this directory.

    ```console
    $ make start
    $ make test
    $ make test-upgrade
    ```

3. After the test, run the following command to stop `kind` cluster.

    ```console
    $ make stop
    ```

## How to test with a development version of moco-agent

1. Prepare the source directory of moco-agent.
2. Run `make start AGENT_DIR=<dir>` with the directory path of moco-agent.

[kind]: https://kind.sigs.k8s.io/
