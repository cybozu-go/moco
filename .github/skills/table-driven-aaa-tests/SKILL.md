---
name: table-driven-aaa-tests
description: 'Implement or refactor Go standard-library TestXxx table-driven tests when Arrange or Assert varies by case, using case-specific functions and one shared Act. Do not use for Ginkgo specs or tables with common Arrange and Assert logic.'
argument-hint: 'Describe the Go test file or test cases to refactor'
---

# Table-Driven Arrange/Act/Assert Tests

Use this workflow only for Go standard-library tests declared as `func TestXxx(t *testing.T)`. It does not apply to Ginkgo specs.

## Choose the Test Shape

- Use case-specific `arrangeFunc` and `assertFunc` when Arrange or Assert behavior varies between test cases.
- Keep setup and assertions that every case needs directly in the `t.Run` body. Do not repeat common work in case functions.
- When Arrange and Assert are both common, write a conventional table-driven test. Put inputs, options, and expected values in fields such as `input`, `want`, or `wantErr`, and keep the shared Arrange and Assert logic in the test loop.

Different parameter or expected values alone do not justify `arrangeFunc` or `assertFunc`. Use them when the cases need different setup or assertion behavior.

## Target Shape

```go
testCases := []struct {
    name        string
    arrangeFunc func(*testing.T, Dependency)
    assertFunc  func(*testing.T, Dependency)
}{
    {
        name: "scenario",
        arrangeFunc: func(t *testing.T, dependency Dependency) {
            // Create this case's initial state.
        },
        assertFunc: func(t *testing.T, dependency Dependency) {
            // Read and verify this case's resulting state.
        },
    },
}

for _, tt := range testCases {
    t.Run(tt.name, func(t *testing.T) {
        dependency := newDependency(t)
        // common setup for every case...
        tt.arrangeFunc(t, dependency)

        subject := loadSubject(t, dependency)
        if err := act(t.Context(), dependency, subject); err != nil {
            t.Fatalf("act failed: %v", err)
        }

        // common assertions for every case...
        tt.assertFunc(t, dependency)
    })
}
```

## Recommended

### Define Case Responsibilities

Use fields similar to:

```go
struct {
    name        string
    subjectName string
    arrangeFunc func(*testing.T, Dependency)
    assertFunc  func(*testing.T, Dependency)
    wantErr     string
    wantMetrics string
}
```

- `arrangeFunc` arranges case-specific resources and initial values.
- `assertFunc` performs case-specific checks.
- Keep expected errors or optional side effects as fields when their checking logic is common.

### Build Fresh Common Infrastructure

Inside each `t.Run`:

- create a fresh scheme, fake client, database, registry, clock, or other mutable dependency
- run `arrangeFunc`
- reload the subject from the dependency when persistence is part of the contract
- construct the reconciler or service with common configuration

Fresh dependencies prevent state and metrics from leaking between subtests. Keep invariant setup in the common loop and scenario-specific state in `arrangeFunc`.

### Extract Narrow Helpers

Create helpers only for repeated mechanical work. Helpers should expose the arranged state instead of hiding an entire scenario. Prefer:

```go
objects := newObjectsForStatefulSet(cluster, statefulSet, sizes)
createObjects(t, client, objects...)
```
