---
paths:
  - "src/**/*.py"
---

# Implementation order

Applies when editing application code under `src/` (change the `paths` pattern to match your tree).

## One unit at a time

1. Identify the layer from your architecture doc.
2. Add or change tests first when adding **new behavior** (see global `workflow.md`).
3. Implement the smallest surface on that layer, then integrate upward.

## Forbidden

- Implementing several unrelated classes in one step without tests.
- Skipping tests for new behavior.

## Allowed

- Stubs or fakes for dependencies while a lower layer is not ready, if tests document the contract.
