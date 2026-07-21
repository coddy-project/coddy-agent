---
description: BDD-style workflow, HTTP OpenAPI and config schema sync before lint, final checks
paths:
  - "**/*.go"
  - "docs/**/*.md"
  - "README.md"
---

# Workflow (features, bugs, finish)

## Specifications live in `features/` (BDD)

Executable specifications for **feature behavior** and the **happy path of a bug fix** are
Gherkin `.feature` files in the **repository-root `features/`** directory, run by a godog harness
(step definitions in the package that owns the behavior, e.g. `external/httpserver/bdd_*_test.go`,
pointing `Options.Paths` at `../../features/<name>.feature`).

- **New feature** → add or extend the feature's `.feature` spec in `features/` describing the
  scenario as it works when correct (the happy path), so the behavior is reproducible.
- **Bug fix** → add a scenario (or feature) in `features/` that reproduces the problem as a
  happy-path expectation, then make it pass.
- **Boundary / edge / error cases** → do **not** put these in `features/`; cover them with ordinary
  **unit tests** next to the code. Keep `.feature` files focused on the correct-behavior story.
- Keep specs deterministic and LLM-free where possible (use a stub runner, as the existing
  harnesses do). Step definitions may live near the code; the `.feature` specs stay in `features/`.

## New behavior (TDD / BDD)

When adding or changing behavior (including words like feature, add, implement, фича, добавить):

1. Add or extend the **happy-path `.feature` spec** in `features/` (and/or a **failing** unit test)
   that asserts the observable outcome (red). Edge cases go in unit tests, not the spec.
2. Run the narrowest test scope that proves the failure is real.
3. Implement the smallest change that makes the test pass (green).
4. Run **`make test`** (default, **`http`**, **`scheduler`**, **`ui-build`** then **`http,ui`**, combined scheduler tags). Everything must pass.
5. **HTTP OpenAPI narrative** - If you changed the optional OpenAI-compatible HTTP API (routes, methods, headers, request or response bodies, status codes, or anything reflected in the served spec), update **`external/httpserver/openapi.go`** (`openAPISpec`) so it matches **`external/httpserver/server.go`** handlers and tests. Align **`docs/http-api.md`** (and **`README.md`** HTTP bullets) when user-facing descriptions change.
6. **Config schema sync** - If you changed the YAML config surface (**`internal/config`** structs: added, renamed, retyped, or removed a yaml-tagged field, enum value, or default), update **`docs/config.schema.json`** and the tables in **`docs/config-reference.md`** to match. **`TestDocsConfigSchemaMatchesStructs`** (**`internal/config/docs_schema_test.go`**) catches key/type drift, but descriptions, defaults, enums-in-prose, and the reference tables are not auto-checked - keep them accurate by hand. Mirror user-facing fields in **`config.example.yaml`** and **`UISchemaMap()`** (**`internal/config/ui_schema.go`**) as well.
7. Update documentation and specs if needed.
8. Run **`make lint`** (`golangci-lint`). Fix reported issues.

Then report briefly: goal, tests added or changed, `make test` and `make lint` outcome, files touched.

## Bug fixes

1. Add a regression test that fails on the broken code.
2. Fix the code; confirm the new test passes.
3. Run **`make test`**.
4. If the bug or fix touches the HTTP API surface, complete step 5 (OpenAPI and docs) from the feature flow.
5. If it touches **`internal/config`** yaml-tagged structs, complete step 6 (config schema sync) from the feature flow.
6. Run **`make lint`**.

## Before calling work done

- **`make test`** green.
- OpenAPI and HTTP docs updated when the HTTP API changed.
- **`docs/config.schema.json`** and **`docs/config-reference.md`** updated when `internal/config` yaml fields changed.
- **`make lint`** clean.
- **Rules sync** — if any `.claude/rules/*.md` file was added or changed, propagate to `.cursor/rules/`: copy the content body, replace `paths:` with Cursor-compatible `globs:`/`alwaysApply:`, rename to `.mdc`. Files without `paths:` get `alwaysApply: true`.
