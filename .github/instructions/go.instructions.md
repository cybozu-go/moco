---
name: Go coding guidelines
description: "Use when editing Go files in this repository."
applyTo: "**/*.go"
---

- This repository uses Go 1.26+, so `new(expr)` can be used to allocate a new variable initialized with the value of an expression and return its pointer.
  - This also works with literals such as `new(42)`, `new("go")`, and `new(Person{name: "alice"})`.