---
description: Component-scoped logger levels and hierarchy
paths:
  - "internal/logger/**/*.go"
  - "internal/config/**/*logger*.go"
  - "internal/session/**/*.go"
  - "internal/agent/**/*.go"
  - "external/gateway/**/*.go"
  - "external/scheduler/**/*.go"
---
# Logger components

- Tag a subsystem logger once with `logger.Component`; `WithAttrs` inherits the component.
- An inline record attribute cannot affect `Enabled`.
- Component names are dotted paths, and overrides resolve by the longest configured prefix.
- Open the inner handler at the lowest requested level; `componentHandler` performs per-component filtering.
- Every new configurable subsystem gets a named component constant and hierarchy tests.
