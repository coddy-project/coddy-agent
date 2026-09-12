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

| System | Path | Notes |
|--------|------|-------|
| `coddy` | `.coddy/rules/` | Coddy's own folder |
| `agents-dir` | `.agents/rules/` | The tool-neutral `.agents/` tree, next to `.agents/skills`: rules meant for every agent that works on the project |
| `cursor` | `.cursor/rules/` | |
| `claude` | `.claude/rules/` | |
| `codex` | `.codex/rules/` | Markdown only in v1; Codex's own `*.rules` policy files are not prompt rules |
| `agents` | nested `**/AGENTS.md` | [agents.md](https://agents.md/) convention; **read on demand**, only from the folders a tool enters, never walked (see Activation); hidden dirs, `node_modules`, `vendor` are not entered |

Every markdown root is scanned recursively for `.md` and `.mdc` files; those roots are small rule folders, and nothing else of the workspace is read at session start. Duplicate rule files (same file name, extension included) resolve with precedence: **coddy > agents-dir > cursor > claude > codex > agents**. A project that keeps its rules in `.agents/rules/` therefore overrides tool-specific copies of the same file, and `.coddy/rules/` overrides everything. Nested `AGENTS.md` files are keyed by full path, so they never collapse into each other.

Nested `AGENTS.md` files are **read on demand**, the way Codex reads the `AGENTS.md` chain of the folder it works in. Nothing walks the tree to find them: the first time a filesystem tool call or an attached `file://` path targets a path, every `AGENTS.md` on the chain of folders from the project root (exclusive) down to that path's directory is read and enters **`{{.Rules}}`** — then it **sticks** for the rest of the session. Reading `a/b/c/f.go` pulls in `a/AGENTS.md`, `a/b/AGENTS.md`, and `a/b/c/AGENTS.md`; a sibling folder nobody enters is never opened, and a file written after the session started is picked up the moment a tool enters its folder. Hidden directories, `node_modules` and `vendor` end the chain. `run_command` does not activate anything: a shell string cannot be attributed to a directory reliably.

This is what keeps a repo with vendored sibling checkouts usable — 45 nested files loaded unconditionally cost ~131k tokens of system prompt before the first question — and what keeps a session anchored on a home directory from stalling: a walk over `~` (a macOS home carries hundreds of thousands of entries under `~/Library` alone, read cold and behind folder-access prompts) once held the console before its first frame. Bodies over 256 KB are truncated, as with the project docs preamble.

The **root** `AGENTS.md` is not part of this set — it already enters the prompt unconditionally as a project docs preamble (below).

CLI: `coddy rules list [--cwd DIR]` prints the discovered catalog: the source folder (`SOURCE`), the dialect each file was read with (`FORMAT`), the activation mode (`APPLY`: `auto` or `mention`), whether the rule is in every prompt (`ALWAYS`: an auto rule with no patterns and no directory scope) and what activates the others (`ACTIVATES ON`). Nested `AGENTS.md` files are not in the table, since listing them would mean walking the workspace; a line under the table says they are read on demand from the folders a tool enters.

## Rule file formats

The file extension selects the dialect a rule is read with. Both dialects are accepted in every root, so `.agents/rules/` can hold Cursor and Claude Code rules side by side and a file copied from `.cursor/rules/` or `.claude/rules/` keeps its meaning.

| Extension | Format | Frontmatter keys | Header without `alwaysApply` and without patterns |
|-----------|--------|------------------|----------------------------------------------------|
| `.mdc` | Cursor ([docs](https://cursor.com/docs/rules)) | `description`, `globs` (comma-separated string or a list), `alwaysApply` | Manual: Cursor defaults `alwaysApply` to false, so the body enters only on **`@name`** |
| `.md` | Claude Code ([docs](https://code.claude.com/docs/en/memory#organize-rules-with-clauderules)) | `paths` (list of globs), optional `description` | Loaded unconditionally, as Claude Code does |

Shared by both dialects:

- A file without frontmatter is active from the first turn.
- Patterns (`globs` or `paths`; either key is accepted in either dialect) gate the rule: the body enters the prompt the first time a matching file comes into play, then sticks. This is Cursor's auto-attach and Claude Code's path scoping; Coddy applies the same gate to `alwaysApply: true` rules that carry patterns, to keep the prompt small.
- An explicit `alwaysApply` is the master switch for a header without patterns: `true` is active immediately, `false` is mention-only.
- CRLF line endings, quoted or unquoted values, `[flow, lists]`, `- item` lists and trailing `# comments` are all read.

Cursor's `.mdc` headers are frequently not valid YAML: `globs: **/*.go` starts with the YAML alias character. Coddy reads a header as YAML first and falls back to a line reader that knows these keys, so such a file keeps its description, globs and `alwaysApply` instead of being treated as if it had no frontmatter.

```markdown
---
description: HTTP layer conventions
globs: external/httpserver/**/*.go, docs/reference/http-api.md
alwaysApply: false
---
```

```markdown
---
description: HTTP layer conventions
paths:
  - "external/httpserver/**/*.go"
  - "docs/reference/http-api.md"
---
```

The two headers above mean the same thing in `http-layer.mdc` and `http-layer.md`: auto-attached once one of those files is attached or touched.

### Glob patterns

Patterns follow Cursor and Claude Code: `*` matches within one path segment, `**` any number of directories, `{a,b}` alternatives, `[abc]` character classes. They are anchored at the project root (the session cwd): a file is matched by its path relative to the root, so `internal/**/*.go` matches `internal/agent/react.go` but not `external/x.go`, `*.md` names markdown files in the root only (use `**/*.md` for any depth), and a file outside the workspace, such as a sibling checkout or a module cache, never matches anything. In `.mdc` files several patterns are separated by commas (`docs/**/*.md, README.md`); a comma inside braces (`*.{ts,tsx}`) does not split.

### Upgrading from earlier releases

Rule files already on disk may change mode after this release; `coddy rules list` shows the result.

- `alwaysApply: false` together with `globs` is auto-attached once a matching file is attached or read. Earlier releases kept such a rule mention-only. Drop the `globs` to keep a rule manual.
- `.md` rules without `alwaysApply` follow Claude Code: unconditional without `paths`, path-gated with them. Earlier releases treated them as mention-only. Write `alwaysApply: false` to keep a `.md` rule manual.
- A directory-less pattern such as `*.go` matches files in the project root only. Earlier releases matched it against the file name at any depth; write `**/*.go` for that.
- Cursor headers that are not valid YAML (`globs: **/*.go`) are now read. Earlier releases dropped the whole header, so those rules were always on with no globs; they now honour their `alwaysApply` and wait for their globs like any other rule.

## Activation

| Rule | Behavior |
|------|----------|
| Patterns (`globs` / `paths`), any `alwaysApply` | Body enters **`{{.Rules}}`** the first time a matching file comes into play: a `file://` attachment in the user message, or a filesystem tool call (`read`, `edit`, `grep`, ...) targeting a matching path. Then **sticks** for the rest of the session |
| `alwaysApply: true` without patterns | Active immediately for the session |
| `alwaysApply: false` without patterns | **Never** auto-included. Body only when **`@ruleName`** appears in the user message |
| No `alwaysApply`, no patterns, `.mdc` | Mention-only (Cursor's default) |
| No `alwaysApply`, no patterns, `.md` | Active immediately (Claude Code loads it unconditionally) |
| No frontmatter | Active immediately |
| Nested `AGENTS.md` | Read on demand. The first filesystem tool call or `file://` path inside its directory reads it (with every `AGENTS.md` on the chain of folders above it) into **`{{.Rules}}`**, then it **sticks** for the session; nothing is read for folders no tool enters |

Mention-only rules use **`@name`** (file stem). They are **not** slash commands and do not appear in the skills catalog. `run_command` activates nothing: a shell string cannot be attributed to a path reliably.

## Project docs preamble

On every turn, if present in session CWD:

- **`AGENTS.md`** (subsection `### AGENTS.md`)
- **`DESIGN.md`** (subsection `### DESIGN.md`)

These are unconditional; they do not use `alwaysApply` or `@mention`.

## Generating rules

Use the bundled skill **`/generate-rules`**. It is always available (embedded in the binary) and guides the agent to write focused rule files via filesystem tools, into `.cursor/rules/` when the project already has one, into `.agents/rules/` when the project keeps shared agent configuration there, otherwise into `.coddy/rules/`.

There is no `coddy rules generate` CLI subcommand.

## Context breakdown (UI)

After each agent turn, Coddy estimates tokens per category (`systemPrompt`, `toolDefinitions`, `rules`, `skills`, `mcp`, `conversation`) and exposes them on **`GET /coddy/sessions/{id}/stats`** as `contextBreakdown`. The composer context ring opens a breakdown popover on click.

## Configuration

```yaml
rules:
  auto_discover: true
  systems: []   # optional filter: coddy, agents-dir, cursor, claude, codex, agents
```

## References

- [Cursor Rules](https://cursor.com/docs/rules)
- [Claude `.claude/rules`](https://code.claude.com/docs/en/memory#organize-rules-with-clauderules)
- [Codex Rules](https://developers.openai.com/codex/rules)
- Implementation: `internal/rules/*` (dialects and glob matching in `markdown.go`, directory scoping in `scope.go`), wiring in `internal/session`, `internal/agent/system_prompt.go`; tool-path activation in `internal/agent/rules_activation.go` and `internal/tools/fs/toolpaths.go`
- Specs: `features/rules_agents_dir.feature`, `features/agents_md_scoping.feature`
