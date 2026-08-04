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
- **Touching a file behind `//go:build windows`, or any signature it shares with `_other.go` / the rest of the tree: also run `make check-windows` and `make lint-windows`.** `make test` and `make lint` run on the host, so the Windows half of **`internal/platform`** (and anything else with a per-OS file) is never compiled by them. `check-windows` cross-builds and `go vet`s every non-`ui` tag combination, test files included. CI runs both (**`.github/workflows/tests-on-pr.yaml`**), plus **`go test`** on a real `windows-latest` runner for **`internal/platform`**, **`internal/bgtask`**, and **`internal/tools/shell`** — the packages whose behavior differs by OS.
- HTTP server core is **`//go:build http`**. Embedded UI tests compile with **`go test -tags=http,ui ./external/httpserver`**. SPA-free **`http`** build uses **`//go:build http && !ui`** handlers under **`external/httpserver`**. Session memory REST uses **`//go:build http && memory`** (**`memory_http.go`**); without **`memory`**, **`memory_http_stub.go`** registers no routes.

## Conventions

- Prefer table-driven tests where it clarifies cases.
- Keep tests deterministic; avoid real network unless the test is explicitly integration-style and documented.
- New HTTP behavior belongs in **`external/httpserver/server_test.go`** (and related files) with **`http` build tag parity.

## BDD feature specs (`features/`)

- **Happy-path behavior** of a feature (and the reproduction of a bug) is an executable Gherkin
  spec in the **repo-root `features/`** directory, run by a godog harness (e.g.
  **`external/httpserver/bdd_remote_test.go`**, **`bdd_workspace_test.go`**) whose
  **`Options.Paths`** points at **`../../features/<name>.feature`**.
- **Edge / boundary / error cases** are ordinary **unit tests** next to the code, **not** scenarios
  in `features/`. Keep `.feature` files to the correct-behavior story.
- Feature suites run under the tag that owns the behavior (e.g. **`-tags http`**) and are part of
  **`make test`**. Step definitions may use a stub runner to stay deterministic and LLM-free.

## References

@Makefile
@code-style.mdc
@architecture.mdc
