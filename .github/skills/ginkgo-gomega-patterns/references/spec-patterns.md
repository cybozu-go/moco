# Spec Patterns

## Make the Spec Tree Explain the Behavior

- Use `Describe` for the subject, `Context` for relevant initial conditions, and `When` for a meaningful situation or event. Name each `It` after an observable result.
- Put shared Arrange work in `BeforeEach`; put case-specific Arrange work in the closest nested setup hook.
- Keep the Act and Assert visible. For a small spec, perform the action in `It` and assert its result there.
- When several independent specs check results of the same action, a scoped `BeforeEach` can perform that action and each `It` can check one result. That action runs again for every `It`; setup is not shared execution.
- Use `JustBeforeEach` only when the action must run after all nested `BeforeEach` configuration. Do not add it just to make the tree deeper.
- Setup assertions may check successful preparation. Keep the behavior being tested in the subject assertions.
- Declare shared mutable variables in containers, but initialize them in setup hooks. Do not perform I/O or assertions while constructing the tree.
- Do not use `Ordered`, `Serial`, or priorities to hide accidental dependencies between otherwise independent specs.

For example, the following schematic tree makes each result independently runnable:

```text
Describe subject
  BeforeEach: arrange common state
  Context relevant initial condition
    BeforeEach: arrange this condition
    When action occurs
      BeforeEach: perform action and capture result
      It: asserts one observable result
      It: asserts another observable result
```

## Poll Observations, Not Stale Snapshots

- Use `Eventually` for convergence and `Consistently` for a state that must remain true throughout a defined observation window.
- Pass a function that fetches fresh state on every attempt, not the result of a one-time fetch.
- Keep mutations outside polling callbacks unless retrying that operation is intentional and safe after partial completion.
- For a simple observation, return a value or an error and use a matcher that explains the expected result.
- For related checks, accept `g Gomega` and use `g.Expect` for all retryable assertions inside that callback:

```go
Eventually(func(g Gomega) {
    status, err := readStatus()
    g.Expect(err).NotTo(HaveOccurred())
    g.Expect(status.Role).To(Equal("primary"))
    g.Expect(status.Ready).To(BeTrue())
}).Should(Succeed())
```

Here, `readStatus` represents the existing state reader. Do not introduce a wrapper just to match this example.

- A global `Expect` inside a polling callback fails the spec immediately; it does not ask `Eventually` to retry. Do not call assertion-bearing helpers that use global `Expect` for a retryable observation. Return an error or pass the local `Gomega` through.
- Prefer `(value, error)` or descriptive errors over `bool` results that discard the failed read or observed value.
- Include resource identity, observed state, and expected state in failure messages.
- Use the suite defaults where appropriate. Set explicit timeouts and polling intervals when the behavior needs a different budget.
- Give `Consistently` an intentional duration. A successful sample does not prove stability, and a bounded check does not prove that a state can never occur.
- Avoid fixed sleeps as a substitute for observing the required condition.

### Verify the Real Contract

For asynchronous systems, a cached status condition can describe the old state. Check the actual contract, not just a convenient health flag.

In the replication specs, primary convergence includes the expected primary index and the pod's primary role label as well as `Healthy=True`. Data checks and replication-thread checks establish further invariants where needed. Choose observations for the behavior under test; do not copy all readiness checks into every spec.

## Keep Helpers Narrow and Failures at the Caller

- Extract repeated mechanical work such as command execution, waiting for a pod, or reading a replication source. Do not hide an entire scenario behind one helper.
- Call `GinkgoHelper()` in assertion-bearing helpers and helper wrappers so failure locations point to the spec.
- Use plain `Expect` and `Eventually` with helper marking. Do not also add manual stack offsets such as `ExpectWithOffset(1, ...)` for the same helper layer.
- Separate error-returning operations from fail-fast helpers. Use error-returning operations inside retry callbacks and when failure is part of the behavior being tested.
- Keep helper arguments explicit enough to identify the resource and expected state.
- Keep domain-specific matchers and contextual failure messages; do not reduce every check to `BeTrue()`.

## Register Cleanup Beside Setup

- Use `DeferCleanup` close to resource acquisition or a reversible state change. Register it before later work can fail, including before a mutation that might partly succeed.
- Capture resource names and original state when registering cleanup. Restore the prior state rather than assuming a default.
- For a cleanup function returning an error, `DeferCleanup` checks the final error return. Otherwise, make failures visible through assertions.
- Cleanup should tolerate a resource already being absent when that is expected. Do not turn arbitrary read, deletion, or restoration errors into success.
- Use per-spec cleanup for temporary resources and changed state. Use suite cleanup only for resources intentionally kept across specs.
- Verify restoration before another spec can reuse shared state; issuing a cleanup command alone may not establish the invariant.

## Examples and Framework References

- [Ginkgo guide](https://github.com/onsi/ginkgo/blob/master/docs/index.md):
  [tree construction and spec pollution](https://onsi.github.io/ginkgo/#mental-model-how-ginkgo-traverses-the-spec-hierarchy),
  [JustBeforeEach](https://onsi.github.io/ginkgo/#separating-creation-and-configuration-justbeforeeach),
  [cleanup](https://onsi.github.io/ginkgo/#spec-cleanup-aftereach-and-defercleanup),
  [helper failure locations](https://onsi.github.io/ginkgo/#mental-model-how-ginkgo-handles-failure), and
  [asynchronous testing](https://onsi.github.io/ginkgo/#patterns-for-asynchronous-testing).
