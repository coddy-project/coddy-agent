---
description: Go tests, tags, and commands for this repo
paths:
  - "**/*_test.go"
  - "**/*.go"
---

# Testing (Go)

## Commands

- Full suite (what agents should run before finishing): **`make test`**
  - Runs **`go test ./...`**, **`-tags=memory`**, **`-tags=http`**, **`-tags=http,memory`**, **`-tags=scheduler`**, **`-tags=scheduler,memory`**, then **`make ui-build`**, then **`http,ui`**, **`http,ui,memory`**, **`http,scheduler`**, **`http,scheduler,memory`**, **`http,scheduler,ui`**, **`http,scheduler,ui,memory`** (see **`Makefile`** **`test`** target).
- Targeted run while iterating: **`go test ./path/to/pkg -run TestName -count=1`**
- HTTP server core is **`//go:build http`**. Embedded UI tests compile with **`go test -tags=http,ui ./external/httpserver`**. SPA-free **`http`** build uses **`//go:build http && !ui`** handlers under **`external/httpserver`**. Session memory REST uses **`//go:build http && memory`** (**`memory_http.go`**); without **`memory`**, **`memory_http_stub.go`** registers no routes.

## Conventions

- Prefer table-driven tests where it clarifies cases.
- Keep tests deterministic; avoid real network unless the test is explicitly integration-style and documented.
- New HTTP behavior belongs in **`external/httpserver/server_test.go`** (and related files) with **`http` build tag parity.

## References

@Makefile
@code-style.mdc
@architecture.mdc
