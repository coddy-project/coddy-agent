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

Duplicate rule files (same basename) resolve with precedence: **coddy > cursor > claude > codex**.

CLI: `coddy rules list [--cwd DIR]` prints the discovered catalog.

## Activation

| Frontmatter | Behavior |
|-------------|----------|
| `alwaysApply: true` + `globs` / `paths` | Body enters **`{{.Rules}}`** only after the first glob match on a turn (from `file://` context or tool paths). Then **sticks** for the rest of the session |
| `alwaysApply: true` without globs | Active immediately for the session |
| `alwaysApply: false` | **Never** auto-included, even if globs would match. Body only when **`@ruleName`** appears in the user message |
| No frontmatter | Treated as auto; active immediately |

Mention-only rules use **`@name`** (file stem). They are **not** slash commands and do not appear in the skills catalog.

## Project docs preamble

On every turn, if present in session CWD:

- **`AGENTS.md`** (subsection `### AGENTS.md`)
- **`DESIGN.md`** (subsection `### DESIGN.md`)

These are unconditional; they do not use `alwaysApply` or `@mention`.

## Generating rules

Use the bundled skill **`/coddy-generate-rules`**. It is always available (embedded in the binary) and guides the agent to write focused `.coddy/rules/*.mdc` files via filesystem tools.

There is no `coddy rules generate` CLI subcommand.

## Context breakdown (UI)

After each agent turn, Coddy estimates tokens per category (`systemPrompt`, `toolDefinitions`, `rules`, `skills`, `mcp`, `conversation`) and exposes them on **`GET /coddy/sessions/{id}/stats`** as `contextBreakdown`. The composer context ring opens a breakdown popover on click.

## Configuration

```yaml
rules:
  auto_discover: true
  systems: []   # optional filter: coddy, cursor, claude, codex
```

## References

- [Cursor Rules](https://cursor.com/docs/rules)
- [Claude `.claude/rules`](https://code.claude.com/docs/en/memory#organize-rules-with-clauderules)
- [Codex Rules](https://developers.openai.com/codex/rules)
- Implementation: `internal/rules/*`, wiring in `internal/session`, `internal/agent/system_prompt.go`
