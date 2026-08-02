# Project rules

Coddy discovers project rules from the session working directory and injects them into the system prompt via **`{{.Rules}}`**, separate from skills (**`{{.Skills}}`**).

## Prompt order

From top to bottom in the rendered system message:

1. Tools
2. Skills
3. Plan context / todo list (mode-dependent)
4. **Rules** (project docs + active rules)
5. Session memory
6. Current UTC time

## Discovery

When `rules.auto_discover` is true (default), Coddy scans:

| Source | Path |
|--------|------|
| Coddy | `.coddy/rules/` |
| Cursor | `.cursor/rules/` |
| Claude | `.claude/rules/` |
| Codex | `.codex/rules/` (markdown only in v1) |
| Agents | nested `**/AGENTS.md` ([agents.md](https://agents.md/) convention; hidden dirs, `node_modules`, `vendor` skipped) — discovered eagerly, **loaded on demand** (see Activation) |

Duplicate rule files (same basename) resolve with precedence: **coddy > cursor > claude > codex > agents**. Nested `AGENTS.md` files are keyed by full path, so they never collapse into each other.

Nested `AGENTS.md` files are **directory-scoped**. Discovery finds them all, but a body enters **`{{.Rules}}`** only the first time a filesystem tool call or an attached `file://` path targets its directory or anything below it — then it **sticks** for the rest of the session. Every `AGENTS.md` on the ancestor chain of a touched path activates together, so reading `a/b/c/f.go` pulls in `a/AGENTS.md`, `a/b/AGENTS.md`, and `a/b/c/AGENTS.md`. `run_command` does not activate anything: a shell string cannot be attributed to a directory reliably.

This is what keeps a repo with vendored sibling checkouts usable — 45 nested files loaded unconditionally cost ~131k tokens of system prompt before the first question. Bodies over 256 KB are truncated, as with the project docs preamble.

The **root** `AGENTS.md` is not part of this set — it already enters the prompt unconditionally as a project docs preamble (below).

CLI: `coddy rules list [--cwd DIR]` prints the discovered catalog.

## Activation

| Frontmatter | Behavior |
|-------------|----------|
| `alwaysApply: true` + `globs` / `paths` | Body enters **`{{.Rules}}`** only after the first glob match on a turn (from `file://` context in the user message). Then **sticks** for the rest of the session |
| `alwaysApply: true` without globs | Active immediately for the session |
| `alwaysApply: false` | **Never** auto-included, even if globs would match. Body only when **`@ruleName`** appears in the user message |
| No frontmatter | Treated as auto; active immediately |
| Nested `AGENTS.md` (no frontmatter) | Directory-scoped. Body enters **`{{.Rules}}`** after the first filesystem tool call or `file://` path inside its directory, then **sticks** for the session |

Mention-only rules use **`@name`** (file stem). They are **not** slash commands and do not appear in the skills catalog.

## Project docs preamble

On every turn, if present in session CWD:

- **`AGENTS.md`** (subsection `### AGENTS.md`)
- **`DESIGN.md`** (subsection `### DESIGN.md`)

These are unconditional; they do not use `alwaysApply` or `@mention`.

## Generating rules

Use the bundled skill **`/generate-rules`**. It is always available (embedded in the binary) and guides the agent to write focused `.coddy/rules/*.mdc` files via filesystem tools.

There is no `coddy rules generate` CLI subcommand.

## Context breakdown (UI)

After each agent turn, Coddy estimates tokens per category (`systemPrompt`, `toolDefinitions`, `rules`, `skills`, `mcp`, `conversation`) and exposes them on **`GET /coddy/sessions/{id}/stats`** as `contextBreakdown`. The composer context ring opens a breakdown popover on click.

## Configuration

```yaml
rules:
  auto_discover: true
  systems: []   # optional filter: coddy, cursor, claude, codex, agents
```

## References

- [Cursor Rules](https://cursor.com/docs/rules)
- [Claude `.claude/rules`](https://code.claude.com/docs/en/memory#organize-rules-with-clauderules)
- [Codex Rules](https://developers.openai.com/codex/rules)
- Implementation: `internal/rules/*` (directory scoping in `scope.go`), wiring in `internal/session`, `internal/agent/system_prompt.go`; tool-path activation in `internal/agent/rules_activation.go` and `internal/tools/fs/toolpaths.go`
- Spec: `features/agents_md_scoping.feature`
