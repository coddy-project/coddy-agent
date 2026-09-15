---
name: rpa-gen-rules
version: 1.3.0
description: >
  Run when the user invokes /rpa-gen-rules or asks to create or refresh agent project rules (Cursor .mdc,
  Claude Code CLAUDE.md and .claude/rules, Codex .codex hook bridge). Infers from specs, docs, and code.
  Rules use a layered-cake model (implement inner layers with no dependencies first, then the next layer)
  and BDD-style delivery. Generated rules and AGENTS.md are written in English unless the user asks for
  another language. Generated rules include a mandatory Rules Sync step so changes to one agent's tree are
  mirrored to every other agent's tree (Cursor <-> Claude <-> Codex <-> AGENTS.md) in the same commit.
  For Codex the skill installs a hook bridge that auto-attaches Cursor rules by glob, like Cursor and
  Claude Code do natively. No separate user brief required.
---

# RPA generate project rules (layered + BDD, multi-agent)

## Goal

Produce or update **versioned** instructions so the agent stays inside architecture, style, and **BDD-style** discipline (behavior specified in tests, tests before new behavior, full suite before calling work done).

- **Layered cake** - rules must tell the agent to build **by layers**: first units that depend on nothing inside the project, then layers that depend only on lower layers, and so on. Mirror this in `architecture` and `implementation-order` style files.
- **Cursor** - `.cursor/rules/*.mdc` ([MDC](https://github.com/nuxt-content/mdc), `globs`, `alwaysApply`, `@` links).
- **Claude Code** - `CLAUDE.md` and `.claude/rules/*.md` with optional YAML `paths:` ([memory](https://code.claude.com/docs/en/memory#organize-rules-with-claude/rules/)).
- **Codex** - root `AGENTS.md` plus a `.codex/` hook bridge (`hooks.json`, `hooks/attach_rules.py`, `rules.md` index) that injects `.cursor/rules/*.mdc` into Codex sessions: `alwaysApply` rules at `SessionStart`, glob-matched rules on `PreToolUse` - the same behavior Cursor and Claude Code have natively.

## Language of generated files: English by default

Everything this skill writes into the target project is **written in English** unless the user explicitly asks for another language:

- top-level briefs - `AGENTS.md`, root `CLAUDE.md`, `.claude/CLAUDE.md`, `.github/copilot-instructions.md`;
- rule files and their frontmatter - `.cursor/rules/*.mdc`, `.claude/rules/*.md`, and any other agent tree;
- headings, bullets, examples, and comments inside generated code snippets.

Boundaries of the rule:

- Chat language is a separate thing. Keep answering the user in the language they write in, and keep whatever `Respond in <language>` convention already lives in the project's `code-style` rule.
- If the user does ask for another language, apply it to **every** tree at once. Mixed-language trees make the Rules Sync diff unreadable.
- If the repo already has rules in another language and the user said nothing, keep that language in the files you touch, do not translate them on your own initiative, and mention the mismatch in the final report.
- The English default also applies to later edits, so state it inside the generated `code-style` rule, not only here.

## Read these references from this skill (progressive disclosure)

1. **`references/bdd-and-agents.md`**  
   BDD-oriented rules, Cursor vs Claude vs others, avoiding duplicate drifting copies.

2. **`references/cursor-examples/.cursor/rules/workflow.mdc`**  
   Feature and bugfix flow (red, green, all tests, linter, report). Adapt globs and `@` links.

3. **`references/cursor-examples/.cursor/rules/testing.mdc`**  
   pytest naming, fixtures, commands.

4. **Other files in `references/cursor-examples/.cursor/rules/`**  
   `architecture.mdc`, `code-style.mdc`, `implementation-order.mdc`, `api-layer.mdc`, `core-modules.mdc` - templates from [cursor-vibe-prompts](https://github.com/EvilFreelancer/cursor-vibe-prompts/tree/main/cursor-rules). Replace placeholders with the real project.

5. **`references/claude-examples/.claude/`**  
   Example `CLAUDE.md` and modular `rules/*.md` with optional `paths:`.

6. **`references/codex-examples/.codex/`**  
   The Codex hook bridge: `hooks.json`, `hooks/attach_rules.py` (copy both verbatim), and a `rules.md` index template (regenerate per project).

## Discover first

Without asking for hand-written specs unless the repo has none:

1. Read `README`, `docs/`, `ARCHITECTURE*`, OpenAPI, `pyproject.toml` / `package.json`, CI.
2. Skim representative code.
3. Read `tests/` and linters (`ruff.toml`, `pre-commit-config.yaml`).
4. In monorepos, plan per-package `.cursor/rules/` or `.claude/rules/` when layouts split.

## Cursor (`.cursor/rules/*.mdc`)

YAML front matter example:

```markdown
---
description: Short label for this rule set
globs: path/glob/**/*.py
alwaysApply: true
---

# Title

Bullets and @references

@path/to/file.py
@docs/spec.md
```

Use `alwaysApply: true` for workflow and style that must always apply. Use `alwaysApply: false` or narrow `globs` for optional topics.

**Docs:** [Cursor Rules](https://cursor.com/docs/context/rules).

## Claude Code (`CLAUDE.md`, `.claude/rules/`)

Keep `CLAUDE.md` short. Put layered architecture and BDD workflow in topic files under `.claude/rules/`. Confirm `paths:` behavior against your Claude Code version. [CLAUDE.md overview](https://docs.anthropic.com/en/docs/claude-code/claude-md).

## Codex (`AGENTS.md` + `.codex/` hook bridge)

Codex resolves its instruction chain **once per session**, walking from the repository root down to the launch directory, at most one `AGENTS.md` per directory. A session started at the root never loads nested briefs, and there is no glob-based attachment at all. So `AGENTS.md` alone cannot reproduce Cursor's `globs` / `alwaysApply` behavior, and "read the relevant rule file before editing" is advisory and gets skipped.

Generate the **hook bridge** instead ([Codex hooks](https://developers.openai.com/codex/hooks)):

1. Copy `references/codex-examples/.codex/hooks.json` and `references/codex-examples/.codex/hooks/attach_rules.py` into the target repo **verbatim** - no per-project edits. The script parses `.cursor/rules/*.mdc` frontmatter itself, so `.cursor/rules/` stays the single source of truth and rule bodies are **not** duplicated into a third tree.
2. Regenerate `.codex/rules.md` from the template - a human-readable index of every rule with what it applies to and how it attaches (always vs globs). This index is project-specific.
3. Check every generated `.mdc`: a rule is reachable from Codex **only** if its frontmatter carries `globs` or `alwaysApply: true`.

How the bridge behaves: `SessionStart` (startup, resume, clear, compact) injects every `alwaysApply: true` rule; `PreToolUse` on `apply_patch` / `Edit` / `Write` injects rules whose `globs` cover the files in the pending patch, at most once per rule per session; it fails open, so a malformed rule never blocks an edit. Requires `python3` on the developer machine - note it in the report if the target project cannot assume that.

Keep the always-on set small (typically `workflow`, `code-style`, `architecture`, `testing`) and let the rest attach by glob: Codex caps model-visible hook output (about 2500 tokens by default; `additionalContextLimit` in `hooks.json` raises the ceiling to 8000 at session start and 6000 per patch).

**Trust:** Codex tracks hooks by content hash and silently skips untrusted ones. The final report must tell the user to run `/hooks` in Codex once per clone, and again after any edit to `attach_rules.py` or `hooks.json`.

Do not confuse this with Codex **execpolicy** `.rules` files (Starlark, command sandboxing) - an unrelated mechanism this skill does not touch.

## Files to create or align in the target project

| Topic | Contents |
|-------|----------|
| `workflow` | BDD/TDD steps for features and bugs, final checks, `pre-commit` when used |
| `testing` | pytest layout, naming, fixtures |
| `architecture` | Layers, modules, allowed dependencies (supports layered cake) |
| `code-style` | Formatter, types, language of comments and of the rule files themselves |
| `implementation-order` | Explicit layer-by-layer order |
| `api-layer` | If HTTP or RPC exists |
| `core-modules` | Domain modules |

## Content rules

- **Layered cake** in rules: forbid jumping ahead to high-level code before lower layers exist and are tested.
- **BDD** in rules: new behavior starts with tests that describe observable outcomes.
- **Rules sync is mandatory** (see next section): every workflow rule you generate must end with a "Rules Sync" step.
- **English by default** (see the language section above): rule bodies, `AGENTS.md`, and comments in generated code snippets are English unless the user asked for another language. Repeat the rule inside the generated `code-style` file so it survives later edits.
- Ask questions only when blocked.

## Rules sync across agents (mandatory)

Projects in this workflow keep parallel rule trees - typically `.cursor/rules/*.mdc` and `.claude/rules/*.md`, plus a top-level `AGENTS.md` / `CLAUDE.md`. The two **must not drift**. Whenever the agent touches one tree, it must propagate the change to the others in the same task. The Codex bridge deliberately adds **no third copy of rule bodies** - `attach_rules.py` reads `.cursor/rules/` directly - but its `.codex/rules.md` index does list every rule and must follow the same sync discipline.

Bake this into every project you generate:

### 1. Top-level: one file, many symlinks

- Keep the canonical project brief in **`AGENTS.md`** at the repository root.
- Make **`CLAUDE.md`** a symlink to `AGENTS.md` (`ln -sf AGENTS.md CLAUDE.md`) so Claude Code, OpenCode, Cursor, Codex, and any other agent that reads either filename get the same text. Reference projects: `getconf`, `getconf-ui`, `sdm-client-proxy`.
- `.claude/CLAUDE.md` may exist as a short Claude-specific addendum; keep substantive content in the root `AGENTS.md`.

### 2. Per-topic modular rules: paired files, paired edits

For each topic (`workflow`, `testing`, `architecture`, `code-style`, `implementation-order`, `api-layer`, `core-modules`), generate **both** sides:

| Cursor | Claude Code |
|--------|-------------|
| `.cursor/rules/<topic>.mdc` | `.claude/rules/<topic>.md` |
| YAML: `description`, `globs`, `alwaysApply` | YAML: `description`, optional `paths:` |
| Inline links: `@architecture.mdc`, `@docs/foo.md` | Inline links: `.claude/rules/architecture.md`, `@docs/foo.md` |

Translation rules between the two formats:

- `paths: ["src/**/*.py"]` (Claude) ↔ `globs: src/**/*.py` + `alwaysApply: true` (Cursor); a Claude rule **without** `paths:` (loads every session) ↔ Cursor `alwaysApply: true`.
- `.claude/rules/architecture.md` references ↔ `@architecture.mdc` references.
- Body content stays the same; only frontmatter and inline link syntax change.

Codex needs no third column here: the `.codex/` bridge delivers the **Cursor** side of each pair (`alwaysApply` ↔ SessionStart, `globs` ↔ PreToolUse). Only the `.codex/rules.md` index changes when topics are added or removed.

### 3. The "Rules Sync" section every workflow rule must carry

The generated `workflow.mdc` and `workflow.md` **must** end with a `## Rules Sync` section that makes propagation a mandatory step of every TDD/BDD pass. Use wording like this (adjust paths to the project's real layout):

```markdown
## Rules Sync

**MANDATORY** - if any rule file is added or changed in this task, mirror the change to every other agent's rule tree in the same PR:

1. Identify which trees exist in the repo: `.cursor/rules/`, `.claude/rules/`, root `AGENTS.md` / `CLAUDE.md`, the Codex bridge (`.codex/`), plus any other agent roots (`.kimi/`, `.github/copilot-instructions.md`).
2. For each edited file, locate or create its counterpart in every other tree (same topic name).
3. Copy the body verbatim, then adapt the frontmatter and inline link syntax:
   - Cursor `globs:` + `alwaysApply:` <-> Claude `paths:` (or omit `paths:` for always-on).
   - Cursor `@file.mdc` <-> Claude `.claude/rules/file.md`.
4. Keep the language identical across trees. Rule files and `AGENTS.md` are written in English unless this project is deliberately documented in another language; never translate one tree and leave the other behind.
5. If `AGENTS.md` changed, verify `CLAUDE.md` resolves to the same content (symlink intact, or copy refreshed).
6. If the repo has the Codex bridge and a rule file was added, renamed, or removed, refresh the index in `.codex/rules.md`. The hook itself (`.codex/hooks.json`, `.codex/hooks/attach_rules.py`) needs no update - it reads `.cursor/rules/` directly.
7. Include the synced files in the same commit. Do not leave one tree ahead of the other.

Skip only when the topic is genuinely tool-specific (e.g. a Cursor `@`-context trick with no Claude equivalent). When skipping, add a one-line comment in the file that diverges.
```

This section is non-optional. If the project uses only one agent today, still generate the sync block - it activates the moment the second agent's tree is added.

## Deliverables

1. Create or update rules (Cursor and Claude Code paired, plus any other agent roots the repo uses).
2. Ensure every `workflow` rule contains the **Rules Sync** section above.
3. Ensure top-level `AGENTS.md` exists and `CLAUDE.md` is a symlink to it (or note why not).
4. Install the Codex bridge: `.codex/hooks.json` and `.codex/hooks/attach_rules.py` copied verbatim from `references/codex-examples/`, `.codex/rules.md` index regenerated for this project's rules.
5. Ensure everything written is in English unless the user asked otherwise, and that the generated `code-style` rule states this for future edits.
6. Summarize files added or changed, how `alwaysApply` / `paths` / `globs` are set, which language the files use, and confirm all trees were updated in lockstep. Remind the user to run `/hooks` in Codex to trust the bridge (once per clone, and after any edit to the hook files).
