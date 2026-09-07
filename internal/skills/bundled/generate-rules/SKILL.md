---
name: generate-rules
description: "Scan the codebase and generate or refresh focused project rules under .coddy/rules/"
---

# Generate project rules

Use this skill when the user invokes `/generate-rules`. Behave like Cursor's built-in "Generate Cursor Rules": **analyse the codebase first**, derive rules from what you find, then write them without asking the user to describe the project.

## Phase 1 — scan (do this before writing anything)

Read in order, skimming for patterns:

1. `README.md`, `AGENTS.md`, `DESIGN.md` — stated purpose, tech stack, conventions
2. Build / package manifest: `go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`, `Makefile` — languages, dependencies, build commands, test commands
3. Top-level directory layout (`ls`) — identify main packages / layers
4. `internal/` or `src/` root — two or three representative source files per package (not all files)
5. `*_test.go` / `*.test.*` / `tests/` — understand test strategy and naming
6. Existing rules under `.cursor/rules/`, `.coddy/rules/`, `.agents/rules/`, `.claude/rules/` — read every file to avoid duplicates and understand what is already covered
7. CI config (`.github/workflows/`, `Dockerfile`, `.pre-commit-config.yaml`) if present

Skip binary, generated, or vendored files.

## Phase 2 — plan

After scanning, output a **brief plan** (not the rules yet): a table listing each proposed file, its globs/type, and one-line purpose. Ask the user to confirm, adjust scope, or skip topics before writing.

Example:

| File | Type | Purpose |
|------|------|---------|
| `architecture.mdc` | always, `**/*.go` | layer dependencies and import direction |
| `code-style.mdc` | always, `**/*.go` | formatting, lint, comment language |
| `testing.mdc` | always, `**/*_test.go` | test commands, table-driven conventions |
| `api-layer.mdc` | manual | HTTP handler patterns and OpenAPI sync |

Update or skip existing files that already cover a topic well.

## Phase 3 — write

Write each confirmed file. Default target directory:
- `.cursor/rules/` if it already exists in the repo
- `.agents/rules/` if the repo already keeps shared agent configuration under `.agents/` (skills, plugins) or the user asks for rules every agent can read
- Otherwise `.coddy/rules/`
- Use `.claude/rules/` only if the user explicitly asks

The extension selects the dialect coddy reads the file with: `.mdc` is a Cursor rule (frontmatter below), `.md` is a Claude Code rule (`paths:` list instead of `globs`/`alwaysApply`; a `.md` without `paths` is loaded unconditionally). Write `.mdc` unless the target is `.claude/rules/`.

### Frontmatter

```yaml
---
description: One-line summary (used when the rule is fetched manually)
globs: comma-separated glob patterns   # omit if no meaningful file filter
alwaysApply: true   # or false for manual/reference rules
---
```

**`alwaysApply: true`** — rules a model needs on every task (style, architecture, test commands). Pair with tight `globs` to avoid bloating context: with globs the rule enters the prompt once a matching file is attached or read, without globs it is on from the first turn.

**`alwaysApply: false`** — reference rules activated via `@ruleName`, or auto-attached when a file matching `globs` is attached or read. Use for deep-dive docs (API patterns, DB schema, deployment). A `.mdc` with a description and no `globs` is reachable only through `@ruleName`.

### Body

- Title = `# Topic`
- Short paragraphs or numbered lists — no walls of prose
- Prefer referencing real files over pasting code: `see [auth.go](mdc:internal/auth/auth.go)`
- Cross-link related rules in a `## References` section using `@ruleName.mdc`
- English only
- Keep each file under 150 lines

### Good rule categories to derive from code

| Category | What to look for |
|----------|-----------------|
| Architecture / layers | Package import graph, forbidden cross-layer calls |
| Code style | Existing naming, error handling, import grouping patterns |
| Testing | Test helpers, table-driven style, build tags, `make test` targets |
| API / HTTP layer | Handler registration pattern, OpenAPI sync, middleware order |
| Data / persistence | ORM or raw SQL patterns, migration conventions |
| Build & CI | Lint gates, build tags, required pre-push checks |
| Domain concepts | Key types and their invariants (e.g. session state machine) |

## After writing

Report: files created or updated, their type (`always` / `manual`), and how to verify — e.g. `coddy rules list` (the `FORMAT` column shows the dialect each file was read with, `ACTIVATES ON` its globs) or open a matching file in chat to confirm the rule is picked up.
