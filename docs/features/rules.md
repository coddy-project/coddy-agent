# Rules and instructions

Coddy reads standing instructions from two places and injects them into the system prompt, separate from skills (**`{{.Skills}}`**).

- The **workspace**: the rule folders of the session working directory and its `AGENTS.md`, which travel with the checkout and apply to whoever opens it;
- your **agent home** (`~/.coddy`): `AGENTS.md`, `DESIGN.md` and `rules/` next to `config.yaml`, which belong to the person running Coddy and apply in every workspace.

Nothing has to be configured for either: both `AGENTS.md` files and every rule folder reach the model through **`{{.Rules}}`**, yours first. **`{{.Instructions}}`** carries whatever else `instructions.files` names.

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
| `user` | `~/.coddy/rules/` | **Yours**, not the project's: read in every workspace, never part of a checkout. Lowest precedence, so a project file of the same name overrides it |
| `coddy` | `.coddy/rules/` | Coddy's own folder |
| `agents-dir` | `.agents/rules/` | The tool-neutral `.agents/` tree, next to `.agents/skills`: rules meant for every agent that works on the project |
| `cursor` | `.cursor/rules/` | |
| `claude` | `.claude/rules/` | |
| `codex` | `.codex/rules/` | Markdown only in v1; Codex's own `*.rules` policy files are not prompt rules |
| `agents` | nested `**/AGENTS.md` and `**/DESIGN.md` | [agents.md](https://agents.md/) convention, plus the design note beside it; **read on demand**, only from the folders a tool enters, never walked (see Activation); hidden dirs, `node_modules`, `vendor` are not entered |

Every markdown root is scanned recursively for `.md` and `.mdc` files; those roots are small rule folders, and nothing else of the workspace is read at session start. Duplicate rule files (same file name, extension included) resolve with precedence: **coddy > agents-dir > cursor > claude > codex > agents > user**. A project that keeps its rules in `.agents/rules/` therefore overrides tool-specific copies of the same file, and `.coddy/rules/` overrides everything. Nested documents are keyed by full path, so they never collapse into each other.

Nested `AGENTS.md` files are **read on demand**, the way Codex reads the `AGENTS.md` chain of the folder it works in, and a `DESIGN.md` beside one is read with it - a folder that describes itself is read whichever of the two it wrote, the same pair the workspace root and the agent home are read for. Nothing walks the tree to find them: the first time a filesystem tool call or an attached `file://` path targets a path, every such document on the chain of folders from the project root (exclusive) down to that path's directory is read and enters **`{{.Rules}}`** — then it **sticks** for the rest of the session. Reading `a/b/c/f.go` pulls in `a/AGENTS.md`, `a/b/AGENTS.md` and `a/b/c/AGENTS.md`, plus whichever of those folders also has a `DESIGN.md`; a sibling folder nobody enters is never opened, and a file written after the session started is picked up the moment a tool enters its folder. Hidden directories, `node_modules` and `vendor` end the chain. `run_command` does not activate anything: a shell string cannot be attributed to a directory reliably.

This is what keeps a repo with vendored sibling checkouts usable — 45 nested files loaded unconditionally cost ~131k tokens of system prompt before the first question — and what keeps a session anchored on a home directory from stalling: a walk over `~` (a macOS home carries hundreds of thousands of entries under `~/Library` alone, read cold and behind folder-access prompts) once held the console before its first frame. Bodies over 256 KB are truncated, as with the project docs preamble.

The **root** `AGENTS.md` is not part of this set — it already enters the prompt unconditionally as a project docs preamble (below).

CLI: `coddy rules list [--cwd DIR]` prints the discovered catalog: the source folder (`SOURCE`), the dialect each file was read with (`FORMAT`), the activation mode (`APPLY`: `auto` or `mention`), whether the rule is in every prompt (`ALWAYS`: an auto rule with no patterns and no directory scope) and what activates the others (`ACTIVATES ON`). Nested `AGENTS.md` and `DESIGN.md` files are not in the table, since listing them would mean walking the workspace; a line under the table says they are read on demand from the folders a tool enters.

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
| Nested `AGENTS.md`, `DESIGN.md` | Read on demand. The first filesystem tool call or `file://` path inside its directory reads it (with every such document on the chain of folders above it) into **`{{.Rules}}`**, then it **sticks** for the session; nothing is read for folders no tool enters |

Mention-only rules use **`@name`** (file stem). They are **not** slash commands and do not appear in the skills catalog. `run_command` activates nothing: a shell string cannot be attributed to a path reliably.

## Project docs preamble

Two directories describe themselves the same way, with the same pair of files, and both are read on every turn when present - yours first, so the operator speaks before the checkout:

- **`~/.coddy/AGENTS.md`**, then **`~/.coddy/DESIGN.md`** - the agent home
- **`AGENTS.md`**, then **`DESIGN.md`** - the session CWD

Whichever of the four exists is read; a home with only a `DESIGN.md` contributes that one and nothing else.

These are unconditional; they do not use `alwaysApply` or `@mention`, and none of them is named in `config.yaml`. A file that enters the prompt here is not sent a second time as an instruction file, so the default `instructions.files` can name `AGENTS.md` without paying for it twice.

## Your own instructions and rules

A checkout describes itself: how it is built, what its conventions are, which commands are dangerous *in it*. What you want from every session - the language to answer in, the style you carry between projects, the commands you never want run - is not the project's business and does not belong in its files. Those live in your agent home, `~/.coddy`, and are read in every workspace:

| File | Reaches the prompt as | Read when |
|------|-----------------------|-----------|
| `~/.coddy/AGENTS.md`, `~/.coddy/DESIGN.md` | **`{{.Rules}}`**, the first preamble sections, above the project's own pair | every turn, in every workspace |
| `~/.coddy/rules/*.md`, `*.mdc` | **`{{.Rules}}`**, like any project rule | per their own frontmatter (always on, glob-gated or `@mention`) |

Nothing is configured for either. Writing the file is the switch, deleting it is the off switch, and no key in `config.yaml` mentions them:

```bash
mkdir -p ~/.coddy/rules
$EDITOR ~/.coddy/AGENTS.md
```

Your pair is read first, so what you want is in front of the model before the checkout starts describing itself, and the project's own `AGENTS.md` and `DESIGN.md` follow below - they join, neither replaces the other. Write one of the two or both; the file that is not there costs nothing.

Your rules are ordinary rule files and follow the same dialects, globs and activation modes as a project's - a `go.mdc` with `globs: **/*.go` under `~/.coddy/rules/` waits for a Go file in whichever project the session opened, because globs are anchored at the workspace, not at the folder the rule came from. `coddy rules list` shows them with source `user`, and names the folder under the table. The project still has the last word: when a project file and one of yours share a file name, the project's wins.

Nothing here is trusted differently from a project file: both are text that steers the model, and neither can run anything by itself (see [Security](../operate/security.md)). The difference is who wrote it - your own folder needs no trust receipt because nothing arrives in it with a `git clone`.

### More instruction files

`instructions.files` names what else a session reads into **`{{.Instructions}}`**, on top of the preamble above. It defaults to `["AGENTS.md", "DESIGN.md"]` - the workspace's own pair, which the preamble already carries, so out of the box that block is empty - and a team or a machine can add its own:

```yaml
instructions:
  files:
    - "AGENTS.md"                   # the project's own, relative to the workspace
    - "DESIGN.md"
    - "/srv/agents/house-style.md"  # an absolute path, shared by a fleet
    - "${CWD}/docs/conventions.md"  # this workspace
    - "${CODDY_HOME}/team.md"       # next to your config.yaml
```

A leading `~` expands, and so do the two placeholders the config understands: `${CODDY_HOME}` is the agent home (`~/.coddy`) and `${CWD}` the workspace of the session reading the file. An absolute entry is read as it stands and a relative one resolves against the session working directory. Files are read in the order listed, and one that does not exist is silently skipped - which is how a single list can serve workspaces that do not all carry the same files. A file the preamble already carries is not read twice.

## Generating rules

Use the bundled skill **`/generate-rules`**. It is always available (embedded in the binary) and guides the agent to write focused rule files via filesystem tools, into `.cursor/rules/` when the project already has one, into `.agents/rules/` when the project keeps shared agent configuration there, otherwise into `.coddy/rules/`.

There is no `coddy rules generate` CLI subcommand.

## Context breakdown (UI)

After each agent turn, Coddy estimates tokens per category (`systemPrompt`, `toolDefinitions`, `rules`, `skills`, `mcp`, `conversation`) and exposes them on **`GET /coddy/sessions/{id}/stats`** as `contextBreakdown`. The composer context ring opens a breakdown popover on click.

## Configuration

```yaml
instructions:
  # Read in the order listed. ${CODDY_HOME}, ${CWD} and ~ expand;
  # an absolute entry is read as it stands, a relative one resolves against the
  # session working directory. This is the default.
  files:
    - "${CODDY_HOME}/AGENTS.md"
    - "AGENTS.md"

rules:
  auto_discover: true
  systems: []   # optional filter: user, coddy, agents-dir, cursor, claude, codex, agents
```

## References

- [Cursor Rules](https://cursor.com/docs/rules)
- [Claude `.claude/rules`](https://code.claude.com/docs/en/memory#organize-rules-with-clauderules)
- [Codex Rules](https://developers.openai.com/codex/rules)
- Implementation: `internal/rules/*` (roots in `factory.go`, dialects and glob matching in `markdown.go`, directory scoping in `scope.go`), instruction files in `internal/session/instructions_load.go`, wiring in `internal/session`, `internal/agent/system_prompt.go`; tool-path activation in `internal/agent/rules_activation.go` and `internal/tools/fs/toolpaths.go`
- Specs: `features/rules_agents_dir.feature`, `features/agents_md_scoping.feature`, `features/global_instructions.feature`
- End-to-end against a real model: `examples/cli/cli_e2e_rules.py` (a project glob rule through the console) and `examples/cli/cli_e2e_global_instructions.py` (an `AGENTS.md` and a rule in an isolated `CODDY_HOME`, a workspace with nothing in it)
