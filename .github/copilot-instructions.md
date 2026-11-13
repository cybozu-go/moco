# Copilot instructions for MOCO

MOCO is a Kubernetes operator for MySQL. It uses GTID-based semi-sync replication (not group replication) with one writable primary and read-only replicas.

## Build, test, and lint

Tools are managed by [aqua](https://aquaproj.github.io/). Make targets run `aqua install` automatically.

- Build all binaries into `bin/`: `make build`
- Lint: `make lint` (golangci-lint)
- Regenerate code and check that nothing changed: `make check-generate`

For test commands and focused test execution, see [focused-tests.instructions.md](instructions/focused-tests.instructions.md).

MySQL integration tests need Docker. `test-bkop` also needs `mysqlsh`; run `make setup`.

The clustering envtest target sets `MOCO_CHECK_INTERVAL`.

End-to-end tests run on kind: `make start`, `make test` (or `make test FOCUS='...'`), `make test-upgrade`, `make stop`. They need `RUN_E2E=1`, which the Makefile sets. To test an unreleased moco-agent, run `make start AGENT_DIR=<path>`.

## Generated files

Do not edit these files by hand. Change the source and regenerate. CI fails if they are stale.

- `api/**/zz_generated.deepcopy.go`: `make generate`
- CRDs, RBAC, and webhook configs under `config/`, and `charts/moco/templates/generated/`: `make manifests` (from `+kubebuilder` markers)
- `docs/crd_*.md`: `make apidoc`

## Architecture

There are three binaries in `cmd/`: `moco-controller`, `moco-backup` (runs in backup and restore Jobs), and `kubectl-moco` (kubectl plugin).

Inside `moco-controller`, two loops work together:

- **`MySQLClusterReconciler`** (`controllers/mysqlcluster_controller.go`) creates and updates Kubernetes resources: Secrets, certificates, ConfigMaps (`my.cnf` from `pkg/mycnf`), Services, StatefulSet, PDB, and backup/restore CronJobs/Jobs. It uses server-side apply with `ApplyConfiguration` types and a fixed field manager. After each reconcile, it calls `ClusterManager.Update()`.
- **`ClusterManager`** (`clustering/`) runs one goroutine per MySQLCluster. It reads MySQL state through `pkg/dbop` (direct SQL) and moco-agent (gRPC), then decides the cluster state (`Healthy`, `Degraded`, `Failed`, `Lost`, `Offline`, and so on). It also does switchover, failover, replication setup, role labels (`moco.cybozu.com/role`), and status conditions. See `docs/clustering.md`, `clustering/status.go`, `clustering/process.go`, and `clustering/operations.go`.

Read `.github/instructions/clustering.instructions.md` before you change `clustering/`. It lists the invariants that prevent split-brain and data loss.

StatefulSet rollouts are controlled step by step:

- `api/v1beta2/statefulset_webhhok.go` is a mutating webhook. It sets `updateStrategy.rollingUpdate.partition` on MOCO-owned StatefulSets so that Pods do not restart all at once.
- `controllers/partition_controller.go` lowers the partition one Pod at a time when the cluster is ready for the next restart.
- The annotation `moco.cybozu.com/force-rolling-update` skips this process.

Reconciler versions (`docs/reconcile.md`): `Reconcile()` keeps using the reconciler version stored in `status.reconcileInfo` until the user changes the MySQLCluster `spec` (generation). This prevents a MOCO upgrade from restarting mysqld. If a change in StatefulSet generation logic would restart existing clusters, think about whether it needs a new reconciler version.

Other parts:

- Webhooks and CRD types are in `api/v1beta2`. The update webhook rejects some changes, for example decreasing `spec.replicas` or setting `spec.replicationSourceSecretName` on an existing cluster.
- Backup and restore: `backup/` (logic run by `moco-backup`), `pkg/bkop` (mysqlsh dump/load, binlog), `pkg/bucket` (S3, GCS, Azure Blob).
- Shared names, labels, annotations, ports, and user names are in `pkg/constants`. Use them; do not repeat string literals.
- Operator control annotations: `moco.cybozu.com/clustering-stopped`, `moco.cybozu.com/reconciliation-stopped`, and `moco.cybozu.com/prevent-delete`.
- `moco-agent` is an external module (`github.com/cybozu-go/moco-agent`) used through gRPC. Its image tag comes from `go.mod`. Update it with `go get github.com/cybozu-go/moco-agent@latest`.
- Sidecar images (fluent-bit, mysqld_exporter) are set in `version.go`. `containers/` builds the MySQL and sidecar images.
- Design documents for larger features are in `docs/designdoc/`.

## Conventions

- Tests use Ginkgo v2 and Gomega. envtest suites do not run MySQL. `clustering/mock_test.go` fakes `dbop` and the agent, and `controllers/mock_test.go` fakes the ClusterManager. Only `pkg/dbop` and `pkg/bkop` integration tests (`TEST_MYSQL=1`) run real MySQL in Docker.
- e2e specs run in parallel (`ginkgo --procs 5 --randomize-all`). Make each spec safe to run in parallel: use separate namespaces, and do not depend on spec order unless the container is `Ordered`. In e2e helpers, call `GinkgoHelper()` instead of `*WithOffset`.
- In e2e, the MySQLCluster `Healthy` condition can stay `True` for a short time after switchover or failover. Also check data, role labels, or replication state.
- When you add or drop a supported MySQL or Kubernetes version, update the matrices in `.github/workflows/ci.yaml` and `README.md`. See `DEVELOP.md`.
- All commits must be signed off: `git commit -s` (DCO).
- Write English Markdown at CEFR B2 level: clear, direct, and with common words.
