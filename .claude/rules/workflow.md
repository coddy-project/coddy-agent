---
description: BDD-style workflow, UI screenshots in the PR, HTTP OpenAPI, config schema and CLI packaging sync before lint, final checks
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
4. Run **`make test`** - the express run: **`ui-build`**, then one **`go test`** over the whole tree with every optional module compiled in (**`http,ui,scheduler,memory,cli,gateway,swarm`**). Everything must pass. Do **not** walk the tag combinations locally: that matrix (**`make test-matrix`**) runs on GitHub Actions for every pull request, one job per combination. When the change moved a build-tag boundary (a **`_stub.go`**, an **`Available`** const, a **`//go:build`** line), run that one combination by hand - **`go test -tags=<set> ./...`** - and leave the rest to CI.
5. **UI screenshots in the PR** - if the change touches the SPA (**`external/ui/**`**: `.tsx`, `styles.css`, rendered markup), attach screenshots of **every** changed surface to the PR description. Per edit, not one image per PR.
   - Screenshot the **running build** you already verified per **`.claude/rules/ui-verification.md`** (**`npx vite`** in **`external/ui`**, browser tools). Never a mockup, a hand-drawn approximation, or a re-used older image.
   - One image per affected **view and state** - a new dialog needs open *and* the surface it returns to; a changed row needs the row in each state the edit reaches.
   - Post **before/after** pairs for surfaces that already existed, so the visual diff is readable without checking out the branch.
   - Add **narrow (390px)** and **wide (1280px)** when layout differs between them, and **light** plus **dark** when the change adds or edits colors (the repo ships 7 themes; cover any whose tokens the change touches).
   - If a surface genuinely cannot be captured (no browser available, backend-gated screen), say so **explicitly** in the PR and state what was verified instead - do not silently omit it.
6. **HTTP OpenAPI narrative** - If you changed the optional OpenAI-compatible HTTP API (routes, methods, headers, request or response bodies, status codes, or anything reflected in the served spec), update **`external/httpserver/openapi.go`** (`openAPISpec`) so it matches **`external/httpserver/server.go`** handlers and tests. Align **`docs/reference/http-api.md`** (and **`README.md`** HTTP bullets) when user-facing descriptions change.
7. **Config schema sync** - If you changed the YAML config surface (**`internal/config`** structs: added, renamed, retyped, or removed a yaml-tagged field, enum value, or default), update **`internal/config/config.schema.json`** (embedded into the binary: it is what `coddy -t` validates against and what the site publishes) and run **`make docs`**: the field tables of **`docs/reference/config.md`** are generated from the schema's `description` strings and the loader's defaults, so a key without a description ships an empty row. **`TestDocsConfigSchemaMatchesStructs`** (**`internal/config/docs_schema_test.go`**) catches key/type drift; the prose in the Notes section of that page and the guide **`docs/getting-started/configuration.md`** are kept by hand. Mirror user-facing fields in **`config.example.yaml`** and **`UISchemaMap()`** (**`internal/config/ui_schema.go`**) as well. The same change must also update the bundled self-configuration skill **`internal/skills/bundled/configure-coddy/SKILL.md`** (its "Configuration areas" catalog and command examples are the agent-facing view of the schema) - schema edits that skip the skill ship an agent that configures against a stale surface.

8. **Publish to the site** - two things leave this repository for **`coddy-project.github.io`** and must be pushed there in step with the change that touched them.

   **The schema.** **`internal/config/config.schema.json`** is served at **`https://coddy.dev/config.schema.json`**, the address Coddy writes as a modeline into every config it saves, as a **verbatim copy**. A schema change that stops in this repository leaves every editor validating saved configs against a schema the binary no longer matches.

   ```bash
   make site-schema        # copy into the site checkout (SITE_REPO=... if it is elsewhere)
   make site-schema-check  # report drift without writing; non-zero when stale
   ```

   Then commit in the site repository. **Do not push it ahead of the release** when the change **renamed or removed** a key: the schema sets **`additionalProperties: false`**, so the new file marks the old key as an error in every config already on disk. Adding an optional key is safe to publish immediately.

   **The documentation layer.** Every page of **`docs/nav.yaml`** has a stable address on the site, **`coddy.dev/docs/<slug>`**, and nothing is duplicated there: the site's `404.html` loads the generated **`docs-redirect.js`**, which sends a person to the page on GitHub (fragment kept) and turns **`coddy.dev/docs/<slug>.md`** into the raw Markdown; **`llms.txt`** and **`llms-full.txt`** at the site root point at the raw Markdown on `main`. The binary (`coddy -t` hints, `--dry-run` findings), the schema descriptions and the bundled `configure-coddy` skill print those addresses. **`internal/docsgen`** renders the three files; whenever a page, `nav.yaml`, the schema or anything a generated file depends on changes, publish the layer with the same pull request:

   ```bash
   make site-docs          # write docs-redirect.js, llms.txt and llms-full.txt into the site checkout
   make site-docs-check    # report drift without writing; non-zero when stale
   ```

   `llms.txt` names pages by their path on `main`, so the site commit follows the merge of the coddy-agent change, not the other way round. Links that leave the repository - the binary, the schema, the skill, the site, posts - use the **`coddy.dev/docs/<slug>`** form, never a GitHub path.

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
   - **`docs/**`** - prose, the YAML blocks inside it, and the field tables of **`docs/reference/config.md`**;
   - **`config.example.yaml`**, the sample configs under **`examples/`** (`examples/httpserver/config.*.yaml`), and the config snippets embedded in the harness scripts (`examples/**/*.py`, `*.sh`);
   - **`README.md`** and the package READMEs (**`external/memory/README.md`**, ...);
   - **Go comments** - a doc comment is what `go doc` prints, and `// ... when compaction.enabled is false` outlives the key it names;
   - **user-visible strings in the SPA**, every dictionary under **`external/ui/src/ui/i18n/messages/`** included: a message naming a config key, a flag or a subcommand is documentation with a locale attached, and `messagesParity.test.ts` only checks that the keys match, never that the text is still true;
   - **`internal/skills/bundled/`** - **`configure-coddy/SKILL.md`** is the agent-facing copy of the config surface;
   - **`.claude/rules/`**, **`.cursor/rules/`**, **`AGENTS.md`** and **`CLAUDE.md`** - what the next agent reads before it writes the next example;
   - the **`description`** strings inside **`internal/config/config.schema.json`**, which travel to `coddy.dev` with step 8 and are printed as the `doc:` line of a `coddy -t` finding.

   **`docs/plans/**`** are design records of decisions as they were taken; they are **not**
   rewritten to match a later rename (only a link target is repaired when a page moves).

10. **Documentation of the change** - a capability a user will notice, or a change to anything a
    page describes, is not finished until the documentation says so, in the same pull request. The
    layout is **`docs/nav.yaml`** (the map: every page with a one-line summary), **`docs/<group>/<page>.md`**
    (`getting-started`, `surfaces`, `operate`, `features`, `reference`, `contributing`, `plans`) and
    **`docs/assets/`** (what the pages embed); **`docs/contributing/documentation.md`** has the page
    types, the capture recipes and the checks.
    - **New capability** - a page under **`docs/features/`**, **`docs/surfaces/`** or **`docs/operate/`**
      (or a section of the page that owns the area), an entry in **`docs/nav.yaml`**, a line in the
      README "What it does" list when it is something a newcomer chooses Coddy for, and a row in the
      reference that lists things of its kind (**`docs/reference/tools.md`** for a tool,
      **`slash-commands.md`** for a command, **`environment-variables.md`** for a variable,
      **`keyboard.md`** for a key).
    - **Changed behaviour** - the page that describes it, found with `git grep -n '<key or command>' docs/`,
      updated in the same commit. A config key also flows through step 7, a CLI flag through step 11.
    - **Web UI and console features** - the page shows the feature, not only describes it: a screenshot
      of the real surface (Playwright against **`coddy serve`**, or the Konsole stand for the TUI)
      embedded next to the paragraph that explains it, Dark theme 1280 px wide as the default, named
      `<feature>-<state>-<theme>-<width>.png`, under **`docs/assets/<page>/`** when the page needs
      several and at the top level of **`docs/assets/`** otherwise, with a one-line italic caption. A demo video goes to **`docs/assets/video/`** (H.264, 1280 px wide, no audio, under 4 MB)
      and is linked from the page through a poster image.
    - **Assets** - every file under **`docs/assets/`** is referenced by a page, the README, **`DESIGN.md`**
      or the **`Dockerfile`**; **`make docs-check`** fails on one that is not, so a screenshot leaves
      together with the feature it showed. Screenshots that only prove a pull request go to the pull
      request (drag and drop, or the orphan **`screenshots`** branch), never under **`docs/assets/`**.
    - **Generated pages** - **`make docs`** regenerates **`docs/README.md`**, **`docs/llms.txt`**,
      **`docs/llms-full.txt`**, the field tables of **`docs/reference/config.md`**, the help screens of
      **`docs/reference/cli.md`** and the inventory of **`docs/assets/INDEX.md`**; commit the result.
      **`make docs-check`** (the CI job **Documentation**, and the pre-commit hook for documentation
      commits) fails on drift, on a page missing from the map, on a broken relative link or anchor and
      on an unused asset. **`make docs-changelog`** refreshes **`docs/getting-started/changelog.md`**
      from the GitHub Releases.
    - **Design records** - **`docs/plans/**`** keep decisions as they were taken and are not rewritten
      to match a later rename; only their link targets are repaired when a page moves. A page that
      moves leaves a redirect stub at its old path (first line `<!-- docs-stub`), because the binary,
      the schema and the site still print the old address.

11. **CLI command set** - if the change added, renamed or removed a subcommand, a **`serve`** verb
    or a flag that **`printUsage`** (**`cmd/coddy/main.go`**) lists, carry the same change into
    **`packaging/man/coddy.1`**, **`packaging/completions/coddy.bash`** and
    **`packaging/completions/coddy.zsh`**, and into the **`topLevelCommands`** list of
    **`cmd/coddy/usage_test.go`**. Those three files are what every install route ships as the
    description of the command set - the release archive (**`install.sh`**, the Homebrew cask,
    **`coddy update`**), the **`.deb`** and the **`.rpm`**, the Homebrew formula - and nothing
    generates them from the code. The test asserts the top-level commands and the `serve` verbs
    against the usage text, in both directions, but a flag lives in the completions and the man
    page by hand only. A completer that offers **`http`** while the binary answers **`serve`** is a
    user-visible bug (issue #188), so this belongs to the change, not to the release.

12. Run **`make lint`** (`golangci-lint`). Fix reported issues.

Then report briefly: goal, tests added or changed, `make test` and `make lint` outcome, files touched, and the CI matrix verdict once the pull request is up.

## Bug fixes

1. Add a regression test that fails on the broken code.
2. Fix the code; confirm the new test passes.
3. Run **`make test`** (the express run; the tag matrix is CI's).
4. If the fix changes anything the user sees in the SPA, complete step 5 (UI screenshots) from the feature flow - a visual bug fix without before/after images in the PR is not reviewable.
5. If the bug or fix touches the HTTP API surface, complete step 6 (OpenAPI and docs) from the feature flow.
6. If it touches **`internal/config`** yaml-tagged structs, complete steps 7 and 8 (config schema sync, and publishing it to the site) from the feature flow.
7. If the fix renamed, removed or replaced anything an operator types - a config key, a subcommand, a flag - complete step 9 (documentation, examples, comments and bundled instructions) from the feature flow. A fix that leaves the old spelling standing in an example ships a second bug.
8. If the fix touched what **`printUsage`** lists - a subcommand, a **`serve`** verb, a flag - complete step 11 (man page and completions) from the feature flow.
9. If the fix changes anything a documentation page describes, complete step 10 (documentation of the change) from the feature flow.
10. Run **`make lint`**.

## Before calling work done

- **`make test`** green locally. The tag matrix is not a local step: after the push, read the **Tests on PR** run (**`gh pr checks`**) and fix whichever combination it names.
- **Screenshots of every changed UI surface attached to the PR** when **`external/ui/**`** changed, or an explicit note saying why a surface could not be captured.
- OpenAPI and HTTP docs updated when the HTTP API changed.
- **`internal/config/config.schema.json`**, **`docs/reference/config.md`**, and **`internal/skills/bundled/configure-coddy/SKILL.md`** updated when `internal/config` yaml fields changed, and **`make site-schema-check`** clean so the copy published at **`coddy.dev/config.schema.json`** is not stale.
- **`make site-docs-check`** clean when a documentation page, **`docs/nav.yaml`** or the schema changed: the interceptor script and the llms files on coddy.dev follow the repository.
- **No stale spelling of anything renamed**: `git grep -nI '<old name>'` comes back empty outside **`docs/plans/**`** - docs, `config.example.yaml`, `examples/`, Go comments, every `external/ui/src/ui/i18n/messages/` dictionary and `internal/skills/bundled/` included.
- **Man page and completions match the usage text** when the CLI surface changed: `go test ./cmd/coddy -run 'TestUsage|TestPackaging'` green, and the flags of the changed command present in **`packaging/completions/*`** and **`packaging/man/coddy.1`**.
- **`make docs-check`** clean: every new page in **`docs/nav.yaml`**, the generated pages regenerated with **`make docs`**, no broken relative link or anchor, no asset without a page. The page of every user-visible change updated, with a screenshot on the page when the change is visible in the web UI or the console.
- **`make lint`** clean.
- **Rules sync** — if any `.claude/rules/*.md` file was added or changed, propagate to `.cursor/rules/`: copy the content body, replace `paths:` with Cursor-compatible `globs:`/`alwaysApply:`, rename to `.mdc`. Files without `paths:` get `alwaysApply: true`.
