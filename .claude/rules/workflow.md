---
description: BDD-style workflow, UI screenshots in the PR, HTTP OpenAPI and config schema sync before lint, final checks
paths:
  - "**/*.go"
  - "docs/**/*.md"
  - "README.md"
  - "external/ui/**/*"
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
5. **UI screenshots in the PR** - if the change touches the SPA (**`external/ui/**`**: `.tsx`, `styles.css`, rendered markup), attach screenshots of **every** changed surface to the PR description. Per edit, not one image per PR.
   - Screenshot the **running build** you already verified per **`.claude/rules/ui-verification.md`** (**`npx vite`** in **`external/ui`**, browser tools). Never a mockup, a hand-drawn approximation, or a re-used older image.
   - One image per affected **view and state** - a new dialog needs open *and* the surface it returns to; a changed row needs the row in each state the edit reaches.
   - Post **before/after** pairs for surfaces that already existed, so the visual diff is readable without checking out the branch.
   - Add **narrow (390px)** and **wide (1280px)** when layout differs between them, and **light** plus **dark** when the change adds or edits colors (the repo ships 7 themes; cover any whose tokens the change touches).
   - If a surface genuinely cannot be captured (no browser available, backend-gated screen), say so **explicitly** in the PR and state what was verified instead - do not silently omit it.
6. **HTTP OpenAPI narrative** - If you changed the optional OpenAI-compatible HTTP API (routes, methods, headers, request or response bodies, status codes, or anything reflected in the served spec), update **`external/httpserver/openapi.go`** (`openAPISpec`) so it matches **`external/httpserver/server.go`** handlers and tests. Align **`docs/http-api.md`** (and **`README.md`** HTTP bullets) when user-facing descriptions change.
7. **Config schema sync** - If you changed the YAML config surface (**`internal/config`** structs: added, renamed, retyped, or removed a yaml-tagged field, enum value, or default), update **`docs/config.schema.json`** and the tables in **`docs/config-reference.md`** to match. **`TestDocsConfigSchemaMatchesStructs`** (**`internal/config/docs_schema_test.go`**) catches key/type drift, but descriptions, defaults, enums-in-prose, and the reference tables are not auto-checked - keep them accurate by hand. Mirror user-facing fields in **`config.example.yaml`** and **`UISchemaMap()`** (**`internal/config/ui_schema.go`**) as well. The same change must also update the bundled self-configuration skill **`internal/skills/bundled/configure-coddy/SKILL.md`** (its "Configuration areas" catalog and command examples are the agent-facing view of the schema) - schema edits that skip the skill ship an agent that configures against a stale surface.

8. **Publish the schema to the site** - **`docs/config.schema.json`** is served at **`https://coddy.dev/config.schema.json`**, the address Coddy writes as a modeline into every config it saves, out of the site repository **`coddy-project.github.io`** as a **verbatim copy**. A schema change that stops in this repository leaves every editor validating saved configs against a schema the binary no longer matches.

   ```bash
   make site-schema        # copy into the site checkout (SITE_REPO=... if it is elsewhere)
   make site-schema-check  # report drift without writing; non-zero when stale
   ```

   Then commit in the site repository. **Do not push it ahead of the release** when the change **renamed or removed** a key: the schema sets **`additionalProperties: false`**, so the new file marks the old key as an error in every config already on disk. Adding an optional key is safe to publish immediately. `gh pr create` returns 404 in that repository - commits go to `main`, or open a PR through a compare link.

9. **Documentation, examples, comments and bundled instructions** - a rename or a behavior
   change is not finished when `docs/` reads correctly. Nothing in the build catches the old
   spelling anywhere else: `config.yaml` is decoded non-strictly, so an `enabled:` left over
   from before `enable` is silently ignored rather than rejected, and a superseded command
   name inside a comment or a UI string is never compiled at all. What is left behind keeps
   teaching itself to the next reader, human or agent.

   Sweep the whole tree for the old spelling and move every hit in the **same** commit:

   ```bash
   git grep -nI '<old spelling>'
   ```

   The places that get missed, roughly in that order:
   - **`docs/**`** - prose, the YAML blocks inside it, and the field tables of **`docs/config-reference.md`**;
   - **`config.example.yaml`**, the sample configs under **`examples/`** (`examples/httpserver/config.*.yaml`), and the config snippets embedded in the harness scripts (`examples/**/*.py`, `*.sh`);
   - **`README.md`** and the package READMEs (**`external/memory/README.md`**, ...);
   - **Go comments** - a doc comment is what `go doc` prints, and `// ... when compaction.enabled is false` outlives the key it names;
   - **user-visible strings in the SPA**, every dictionary under **`external/ui/src/ui/i18n/messages/`** included: a message naming a config key, a flag or a subcommand is documentation with a locale attached, and `messagesParity.test.ts` only checks that the keys match, never that the text is still true;
   - **`internal/skills/bundled/`** - **`configure-coddy/SKILL.md`** is the agent-facing copy of the config surface;
   - **`.claude/rules/`**, **`.cursor/rules/`**, **`AGENTS.md`** and **`CLAUDE.md`** - what the next agent reads before it writes the next example;
   - the **`description`** strings inside **`docs/config.schema.json`**, which travel to `coddy.dev` with step 8.

   **`docs/plans/**`** and **`docs/remote-control.md`** are design records of decisions as they
   were taken; they are **not** rewritten to match a later rename.

10. Run **`make lint`** (`golangci-lint`). Fix reported issues.

Then report briefly: goal, tests added or changed, `make test` and `make lint` outcome, files touched.

## Bug fixes

1. Add a regression test that fails on the broken code.
2. Fix the code; confirm the new test passes.
3. Run **`make test`**.
4. If the fix changes anything the user sees in the SPA, complete step 5 (UI screenshots) from the feature flow - a visual bug fix without before/after images in the PR is not reviewable.
5. If the bug or fix touches the HTTP API surface, complete step 6 (OpenAPI and docs) from the feature flow.
6. If it touches **`internal/config`** yaml-tagged structs, complete steps 7 and 8 (config schema sync, and publishing it to the site) from the feature flow.
7. If the fix renamed, removed or replaced anything an operator types - a config key, a subcommand, a flag - complete step 9 (documentation, examples, comments and bundled instructions) from the feature flow. A fix that leaves the old spelling standing in an example ships a second bug.
8. Run **`make lint`**.

## Before calling work done

- **`make test`** green.
- **Screenshots of every changed UI surface attached to the PR** when **`external/ui/**`** changed, or an explicit note saying why a surface could not be captured.
- OpenAPI and HTTP docs updated when the HTTP API changed.
- **`docs/config.schema.json`**, **`docs/config-reference.md`**, and **`internal/skills/bundled/configure-coddy/SKILL.md`** updated when `internal/config` yaml fields changed, and **`make site-schema-check`** clean so the copy published at **`coddy.dev/config.schema.json`** is not stale.
- **No stale spelling of anything renamed**: `git grep -nI '<old name>'` comes back empty outside **`docs/plans/**`** and **`docs/remote-control.md`** - docs, `config.example.yaml`, `examples/`, Go comments, every `external/ui/src/ui/i18n/messages/` dictionary and `internal/skills/bundled/` included.
- **`make lint`** clean.
- **Rules sync** — if any `.claude/rules/*.md` file was added or changed, propagate to `.cursor/rules/`: copy the content body, replace `paths:` with Cursor-compatible `globs:`/`alwaysApply:`, rename to `.mdc`. Files without `paths:` get `alwaysApply: true`.
