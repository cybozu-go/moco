---
name: Focused Test Execution
description: "Use when running, debugging, or suggesting focused tests in MOCO. Covers Makefile targets, FOCUS, envtest, CI workflow/action patterns, and avoiding raw go test detours."
applyTo: "**/*.go"
---

# Focused test execution

When running a specific MOCO test case, first use the same entry points as CI and the Makefile. Do not start with raw `go test` unless there is a clear reason.

## Preferred focused commands

- Controller envtest: `make controller-envtest FOCUS='part of spec name'`
- Clustering envtest: `make clustering-envtest FOCUS='part of spec name'`
- API envtest: `make api-envtest FOCUS='part of spec name'`
- Backup envtest: `make backup-envtest FOCUS='part of spec name'`
- Full envtest set: `make envtest`
- Small package tests/install/vet/gofmt: `make test`
- MySQL integration tests: `make test-bkop MYSQL_VERSION='8.4.8'` or `make test-dbop MYSQL_VERSION='8.4.8'`
- E2E and upgrade tests should follow `.github/actions/e2e/action.yaml` and `.github/actions/upgrade/action.yaml`; use the `e2e/Makefile` targets from inside `e2e/`.

## Why Make targets first

- `Makefile` exports `ENVTEST_KUBERNETES_VERSION` and `ENVTEST_ASSETS_DIR`; raw controller/envtest commands can fail with `could not parse "v" as version` if these are missing.
- The `*-envtest` targets run with the repo's expected flags: `-race`, `-count 1`, `-ginkgo.randomize-all`, `-ginkgo.v`, and usually `-ginkgo.fail-fast`.
- `controller-envtest` also sets `DEBUG_CONTROLLER=1`.
- CI calls `make lint`, `make test`, `make check-generate`, and `make envtest` after `.github/actions/setup-aqua`, so local verification should mirror those paths when practical.

## If raw `go test` is necessary

Only use raw `go test` for a quick diagnostic. For envtest packages, carry over the Makefile environment explicitly:

```sh
ENVTEST_KUBERNETES_VERSION=1.35.0 ENVTEST_ASSETS_DIR="$PWD/bin" \
  go test ./controllers -run TestAPIs -ginkgo.focus 'part of spec name'
```
