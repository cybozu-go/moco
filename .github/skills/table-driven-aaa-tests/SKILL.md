---
name: table-driven-aaa-tests
description: 'Refactor or implement Go table-driven tests using case-specific Arrange and Assert functions with one shared Act. Use when consolidating duplicated tests, fake-client fixtures, setup helpers, resource builders, expected-state checks, or subtests while preserving behavior and coverage.'
argument-hint: 'Describe the Go test file or test cases to refactor'
---

# Table-Driven Arrange/Act/Assert Tests

Use this workflow to structure Go tests so each table entry owns its initial state and expected outcome while the test loop owns one common operation under test.

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
        tt.arrangeFunc(t, dependency)

        subject := loadSubject(t, dependency)
        if err := act(t.Context(), dependency, subject); err != nil {
            t.Fatalf("act failed: %v", err)
        }

        tt.assertFunc(t, dependency)
    })
}
```

Adapt the fields for per-case identifiers, expected errors, metrics, events, or configuration. Keep the operation being tested in the common loop.

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

- `arrangeFunc` creates all case-specific resources and initial values.
- `assertFunc` reloads results and performs case-specific checks.
- Expected errors or optional side effects remain fields when their checking logic is common.
- Do not keep parallel `wantX` fields if only one case-specific assertion closure consumes them more clearly.

### Build Fresh Common Infrastructure

Inside each `t.Run`:

- create a fresh scheme, fake client, database, registry, clock, or other mutable dependency
- register common types or common resources
- run `arrangeFunc`
- reload the subject from the dependency when persistence is part of the contract
- construct the reconciler or service with common configuration

Fresh dependencies prevent state and metrics from leaking between subtests. Move genuinely invariant setup into the common loop; leave resources whose values define a scenario in its Arrange closure.

### Extract Narrow Helpers

Create helpers only for repeated mechanical work, such as:

- building resource objects
- deriving child objects from templates
- creating a list of objects in a fake client
- loading and comparing one resource
- returning a fresh expected label or annotation map

Helpers should expose the arranged state instead of hiding an entire scenario. Prefer:

```go
objects := newObjectsForStatefulSet(cluster, statefulSet, sizes)
createObjects(t, client, objects...)
```

over a helper that silently creates the scheme, client, all resources, and scenario-specific overrides before returning only the client.
