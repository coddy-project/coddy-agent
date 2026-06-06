---
description: Layered implementation order (cake model)
paths:
  - "cmd/**/*.go"
  - "internal/**/*.go"
  - "external/**/*.go"
  - "lib/**/*.go"
  - "tools/**/*.go"
---

# Implementation order

For **new behavior**, **`workflow.md` wins** on red-green order. Use this file to decide **where** code lives and which layer to extend first.

1. Extend the **lowest** layer that can own the change with no new upward dependencies.
2. Add tests at that layer before or with the change (see **`testing.md`**).
3. Wire through **`internal/agent`** or **`internal/acp`** only after lower pieces exist and pass tests.
4. Optional HTTP surface - last mile in **`external/httpserver`**, then refresh **`openapi.go`** and docs per **`api-layer.md`** and **`workflow.md`**.

## Forbidden

- Skipping tests for new behavior.
- Placing domain logic in **`cmd/`** or duplicating it in HTTP handlers instead of reusing **`internal/`** APIs.

## References

@architecture.md
@workflow.md
@api-layer.md
