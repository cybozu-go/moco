# Integration Patterns

## Isolate External State in Parallel Suites

- Ginkgo parallel workers are separate processes, but they can still collide through Kubernetes namespaces, databases, files, and other external resources.
- Shard those resources with `GinkgoParallelProcess()`, for example `fmt.Sprintf("repl-p%d", GinkgoParallelProcess())`.
- Keep the spec tree identical on all workers. Choose worker-specific resources during setup, not by conditionally registering different specs.
- Also reset state between specs on the same worker. A process-specific namespace alone is not spec isolation.
- Prefer independent resources when their cost is reasonable. Use `Serial` only for a real constraint that cannot be isolated.

## Limited Exception: Expensive Integration Scenarios

If separate specs would repeat a costly cluster creation or instance reinitialization, related checks of one workflow may stay in one `It`.

- Use `By("[Act] ...")` and `By("[Assert] ...")` to expose the sequence. These labels are a local convention, not a Ginkgo requirement.
- Keep the workflow bounded. Do not merge unrelated behaviors into a giant scenario.
- Remember that the first failure skips later steps; the checks lose independent failure reporting and filtering.
- `By` documents execution; it does not create independent specs or provide cleanup.
- Preserve the reason for merging and every assertion. Do not apply this exception to fast specs by default.

## Limited Exception: Reusing Expensive Fixtures

Reuse a per-process cluster only with an explicit initial-state contract:

1. Prepare or restore it before each consuming spec.
2. Check the invariants that matter, such as credentials, replication threads, role labels, and data.
3. Restore and verify changed state after the spec, including failure paths.
4. Give scenarios with irreversible configuration changes dedicated fixtures.
5. Delete retained fixtures at suite end and release them earlier when no longer needed.

Do not silently recover a broken fixture in a way that hides a failed restoration. Each spec must remain runnable without another spec preparing its state.

`SpecPriority` can improve scheduling of expensive specs, but it does not establish a dependency or guarantee completion order across workers. Use it only as a performance hint.

## Examples and Framework References

- Ginkgo guide:
  [By steps](https://onsi.github.io/ginkgo/#documenting-complex-specs-by),
  [parallel integration specs](https://onsi.github.io/ginkgo/#patterns-for-parallel-integration-specs), and
  [spec priorities](https://onsi.github.io/ginkgo/#prioritizing-specs).
