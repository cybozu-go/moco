---
name: ginkgo-gomega-patterns
description: 'Use when writing, refactoring, or reviewing Ginkgo specs and Gomega assertions or test helpers. Not for standard-library TestXxx table-driven tests.'
argument-hint: 'Describe the Ginkgo specs or test helpers to write, refactor, or review'
---

# Ginkgo and Gomega Patterns

Write or review independent behavior tests with retry-aware assertions and reliable cleanup. For standard-library table-driven tests, use the separate [table-driven AAA skill](../table-driven-aaa-tests/SKILL.md).

## Workflow

1. Read the affected spec tree, hooks, helpers, and runner settings. Identify the initial state, action, observable results, and cleanup contract.
2. Read the relevant references below. Choose the simplest independent spec shape; preserve the tested behavior and assertions when refactoring.
3. Implement the change or report review findings. Keep mutable state local to a spec or reset it before each spec.
4. For changes, follow the repository's [focused-test instructions](../../instructions/focused-tests.instructions.md). For MOCO E2E changes, use the existing E2E Makefile and CI workflow, not a raw `go test` detour.

## Read as Needed

| Work | Reference |
| ---- | --------- |
| Spec structure, asynchronous assertions, helpers, or cleanup | [Spec patterns](./references/spec-patterns.md) |
| Parallel integration tests, slow scenarios, or shared fixtures | [Integration patterns](./references/integration-patterns.md) |

Read both when a change crosses these concerns. Performance exceptions are not defaults for fast specs.
