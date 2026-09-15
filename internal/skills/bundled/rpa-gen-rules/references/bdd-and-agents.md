# BDD-style agent rules and multiple IDEs

This file explains how to encode **behavior** (examples, acceptance checks, regression discipline) in project rules, and how **Cursor**, **Claude Code**, **OpenAI Codex**, and other tools differ while serving the same goal.

## What "BDD rules" means here

Classic BDD often uses Gherkin (`Given` / `When` / `Then`) and tools like `pytest-bdd`. In this RPA workflow, **BDD-oriented rules** mean:

1. **Executable behavior** lives in tests. Rules tell the agent to treat tests as the long-term memory of the project (red-green-refactor, full suite before done).
2. **`workflow.mdc` (in this skill)** is the main rail: trigger phrases for features and bugs, mandatory order (failing test → fix or implement → green → all tests → optional `pre-commit run -a` → short report).
3. **`testing.mdc`** defines how tests read: naming, Arrange-Act-Assert, fixtures, integration vs unit, commands (`pytest`, coverage).

If the repo already uses Gherkin or `pytest-bdd`, extend `testing.mdc` and `workflow.mdc` with paths to `features/`, step definitions, and how to run those tests. Otherwise, plain pytest examples are enough.

**Important:** In `implementation-order.mdc`, steps sometimes show a class before tests for illustration of **layers**. For **new behavior**, always follow **`workflow.mdc`**: write or extend the failing test first, then minimal code (TDD). Layering still applies when choosing *where* code lives.

## Cursor

- **Location:** `.cursor/rules/` with `.mdc` files.
- **Format:** YAML front matter ([MDC](https://github.com/nuxt-content/mdc)) with `description`, optional `globs`, optional `alwaysApply`.
- **Context:** Use `@path/to/file` inside a rule to pull files into context when the rule applies.
- **Docs:** [Cursor Rules](https://cursor.com/docs/context/rules).

**Mapping**

| Concern | Typical file |
|---------|----------------|
| TDD and bugfix workflow | `workflow.mdc` |
| pytest structure and examples | `testing.mdc` |
| Layers and modules | `architecture.mdc` |
| Style and tooling | `code-style.mdc` |
| Dependency order | `implementation-order.mdc` (often `alwaysApply: false`) |

## Claude Code

Official reference: [How Claude remembers your project](https://code.claude.com/docs/en/memory), including [modular rules with `.claude/rules/`](https://code.claude.com/docs/en/memory#organize-rules-with-claude/rules/).

Claude Code uses a **similar** idea (persistent instructions + optional modular files) but **different paths and discovery**.

- **`CLAUDE.md`:** Project instructions loaded for sessions. Prefer under 200 lines; split using `@imports` or `.claude/rules/`. See the same memory page for load order and `CLAUDE.local.md`.
- **`.claude/rules/`:** Markdown files, one topic per file (`testing.md`, `workflow.md`, …). Discovered recursively; you can nest `frontend/`, `backend/`. Rules **without** YAML frontmatter load at launch (same priority as `.claude/CLAUDE.md`). Rules **with** `paths:` in frontmatter load when Claude works on matching files (see [path-specific rules](https://code.claude.com/docs/en/memory#path-specific-rules)). Use glob patterns like `src/**/*.py`, `tests/**/*.py`, `**/*.{ts,tsx}`.
- **Imports in `CLAUDE.md`:** `@README.md`, `@docs/guide.md`, up to five hops deep. First-time imports may require user approval in the IDE.
- **`AGENTS.md`:** Claude Code reads `CLAUDE.md`, not `AGENTS.md` by default. You can `import` with `@AGENTS.md` at the top of `CLAUDE.md` to share one text with other agents (documented on the memory page).
- **Skills (Agent Skills):** Portable folders with `SKILL.md` and optional `scripts/`. For heavy procedures that should not sit in every session context, prefer skills over huge rules. [agentskills.io](https://agentskills.io/), [anthropics/skills](https://github.com/anthropics/skills).

### Bundled example in this skill

Copy or adapt `references/claude-examples/.claude/` into a real project root. It demonstrates:

- Short `.claude/CLAUDE.md` with imports and pointers to modular rules.
- Global `rules/workflow.md` (no `paths`, TDD for features and bugs).
- Path-scoped `rules/testing.md`, `code-style.md`, `architecture.md`, `implementation-order.md`, `api-layer.md` with YAML `paths:` per the official docs.

**Practical alignment**

- Port the **same substance** as Cursor rules: one file for **workflow** (TDD, bug reproduction, full suite), one for **testing** conventions, one for **architecture**.
- Use Markdown in `.claude/rules/` without assuming MDC-only features; link `@` to repo files if your Claude Code build supports it.
- **Plugins vs repo rules:** Skills are portable packages; `CLAUDE.md` and `.claude/rules/` are project-local. Use both when skills encode reusable procedure and repo rules encode **this** codebase.

## OpenAI Codex

Codex reads instructions differently from Cursor and Claude Code, and the difference is what the `.codex/` hook bridge exists for.

### Why plain `AGENTS.md` is not enough

Codex resolves its instruction chain **once per session**, walking from the repository root down to the directory it was launched in and concatenating at most one `AGENTS.md` per directory ([Custom instructions with AGENTS.md](https://developers.openai.com/codex/guides/agents-md)). Two consequences:

- a session started at the repository root never loads a nested `AGENTS.md`, no matter which files it goes on to edit;
- there is no glob-based attachment - Codex has no equivalent of `globs` in `.cursor/rules/*.mdc`, so scope can only be expressed by where a file sits in the tree.

Pointing Codex at an index file and asking it to "open the relevant rule before editing" is advisory, and it gets skipped.

### The hook bridge

[Codex hooks](https://developers.openai.com/codex/hooks) close the gap. This skill ships a ready-made bridge in `references/codex-examples/.codex/`:

| File | Role | Per-project edits |
|------|------|-------------------|
| `hooks.json` | Wires both handlers to `attach_rules.py` | none - copy verbatim |
| `hooks/attach_rules.py` | Parses `.mdc` frontmatter, injects rule bodies | none - copy verbatim |
| `rules.md` | Human-readable index of every rule and its attachment | regenerate per project |

What fires when:

| Event | Matcher | What is injected |
|-------|---------|------------------|
| `SessionStart` | `startup\|resume\|clear\|compact` | every rule whose frontmatter has `alwaysApply: true` |
| `PreToolUse` | `^(apply_patch\|Edit\|Write)$` | rules whose `globs` cover a file in the pending patch, once per rule per session |

Implementation points worth knowing (all already handled by the bundled script):

- **Glob matching is hand-rolled.** `fnmatch` is unusable because its `*` also crosses `/`, which makes `src/api/**/*.py` miss `src/api/server.py`. The script translates globs to a regex where `**/` means "zero or more directories" and `*` stays inside one segment.
- **Paths come from the patch itself.** `*** Add File:`, `*** Update File:`, `*** Delete File:` and the `*** Move to:` destination are all read; absolute paths are made repo-relative before matching.
- **Each rule is injected at most once per session.** Dedup state lives under the system temp directory, keyed by repository path and session id; `SessionStart` with `source: "clear"` resets it.
- **It fails open.** Malformed JSON on stdin, an unparsable rule file, or an unwritable state directory all exit 0 with no output. A broken rule can never block an edit.
- **No third copy of rule bodies.** The script reads `.cursor/rules/*.mdc` directly, so the bridge adds no new drift surface; only the `rules.md` index needs syncing when rules are added, renamed, or removed.

### Context budget

Codex caps model-visible hook output at roughly 2500 tokens by default, then spills the remainder to a temp file and shows the model a head-and-tail preview. The bundled `hooks.json` raises `additionalContextLimit` to 8000 at session start and 6000 per patch - ceilings, not reservations. Keep the always-on set small (`workflow`, `code-style`, `architecture`, `testing`) and let topic rules attach by glob; oversized always-on context degrades the model instead of helping it.

### Trust

Codex requires explicit approval before a non-managed command hook runs, records the approval against the **hash of the hook definition**, and skips an unapproved hook without a hard error - which looks exactly like a hook that is not firing. Tell the user to run `/hooks` once per clone, and again after any edit to `attach_rules.py` or `hooks.json`.

### Verifying without a Codex session

The script reads JSON on stdin and writes JSON on stdout:

```bash
echo '{"hook_event_name":"SessionStart","session_id":"probe","source":"startup"}' | python3 .codex/hooks/attach_rules.py
echo '{"hook_event_name":"PreToolUse","session_id":"probe","tool_input":{"command":"*** Begin Patch\n*** Update File: src/api/server.py\n*** End Patch"}}' | python3 .codex/hooks/attach_rules.py
```

Empty output means: no glob matched, the rule was already sent in this session, or the input was not understood.

### Not to be confused with Codex execpolicy `.rules`

Codex uses the word "rules" for a second, unrelated mechanism: Starlark `.rules` files under a `rules/` directory next to a config layer, which decide which shell commands may run outside the sandbox. That is an execution policy, not instructions; this skill does not generate it.

## Other agents and editors

| Tool | Where rules usually live | Notes |
|------|---------------------------|--------|
| **OpenCode / skills repos** | `SKILL.md`, `AGENTS.md` | Same progressive disclosure idea as Agent Skills standard. |
| **GitHub Copilot** | `.github/copilot-instructions.md` (or policy UI) | Keep instructions short; mirror key bullets from `workflow` and `testing`. |
| **Generic** | `CONTRIBUTING.md`, `docs/dev-guide.md` | Humans and any agent can read these; duplicate critical agent constraints here if your team does not use Cursor or Claude.

## Language: English by default

Rule files and top-level briefs (`AGENTS.md`, `CLAUDE.md`, `.cursor/rules/*.mdc`, `.claude/rules/*.md`, `.github/copilot-instructions.md`) are written in **English** unless the user explicitly asks for another language. Reasons:

- rule trees are mirrored across agents, and a diff between an English `.mdc` and a translated `.md` is impossible to review;
- most agents are prompted and evaluated in English, so English rules behave more predictably;
- contributors joining the repo later read one language, not two.

This is about **files**, not about conversation. The chat stays in the user's language, and a `Respond in <language>` line inside `code-style` stays exactly as the project wants it. If the user asks for rules in another language, switch every tree at once. If the repo already carries rules in another language and the user gave no instruction, keep that language in the files you touch and report the mismatch instead of translating on your own initiative.

## Single source of truth and mandatory sync

Different agents read different paths, but the **content** must stay identical. Two mechanisms keep that true:

### Top-level brief: `AGENTS.md` + `CLAUDE.md` symlink

Keep one canonical brief at repo root and make every other agent's top-level filename point at it.

```bash
# at repo root
ln -sf AGENTS.md CLAUDE.md
# verify
ls -la CLAUDE.md   # should print "CLAUDE.md -> AGENTS.md"
```

Used in production by `getconf`, `getconf-ui`, and `sdm-client-proxy`. Claude Code reads `CLAUDE.md`, OpenCode / Codex / others read `AGENTS.md`, and Cursor picks up `AGENTS.md` via its docs context - all see the same text.

If a project genuinely needs a Claude-only addendum, put a **short** `.claude/CLAUDE.md` with an `@AGENTS.md` import at the top and only the Claude-specific lines below.

### Per-topic rules: paired files, paired edits

For every topic file you create, generate **both** `.cursor/rules/<topic>.mdc` and `.claude/rules/<topic>.md`. The body is identical; only frontmatter and inline link syntax differ:

| Cursor (`.mdc`) | Claude Code (`.md`) |
|-----------------|---------------------|
| `globs: src/**/*.py` + `alwaysApply: true` | `paths: ["src/**/*.py"]` |
| `alwaysApply: true` (no `globs`) | rule **without** `paths:` (loads every session) |
| `@architecture.mdc` | `.claude/rules/architecture.md` |
| `.mdc` extension | `.md` extension |

Codex is served by the **Cursor** side of each pair through the `.codex/` hook bridge (`alwaysApply` maps to `SessionStart`, `globs` to `PreToolUse`), so no third copy of the body exists. The delivery equivalence across the three agents:

| Agent | Always-on rules | Scoped rules |
|-------|-----------------|--------------|
| Cursor | `alwaysApply: true` in `.cursor/rules/*.mdc` | `globs` in the same frontmatter |
| Claude Code | `.claude/rules/*.md` without `paths:` | `paths:` in frontmatter |
| Codex | `AGENTS.md` plus the `SessionStart` hook | the `PreToolUse` hook |

### "Rules Sync" section is mandatory

Every `workflow` rule must end with a `## Rules Sync` section that makes propagation a mandatory final step of every feature / bugfix pass. The two reference workflow files in this skill (`references/cursor-examples/.cursor/rules/workflow.mdc` and `references/claude-examples/.claude/rules/workflow.md`) already include it - copy that section verbatim and adjust paths.

The agent must, in the same commit / PR:

1. Identify every rule tree present in the repo: `.cursor/rules/`, `.claude/rules/`, root `AGENTS.md` / `CLAUDE.md`, the Codex bridge (`.codex/`), plus any `.kimi/`, `.github/copilot-instructions.md`.
2. Locate or create the counterpart of each edited file in every other tree.
3. Copy the body verbatim; translate frontmatter and links per the table above.
4. Keep both sides in the same language (English by default, see the section above).
5. Confirm the `CLAUDE.md -> AGENTS.md` symlink is still valid when `AGENTS.md` changed.
6. Refresh the `.codex/rules.md` index when a rule file was added, renamed, or removed; the hook script itself needs no update.
7. List every synced file in the task report.

Drift is a bug. If one tree leads the other for more than a single commit, the agents start giving contradictory advice on the same codebase.

### Other constants to keep aligned

Point all rules at the **same** test commands (`pytest`, `uv run`, `go test`, `behave`, etc.) and the **same** config files (`ruff.toml`, `pytest.ini`, `pre-commit-config.yaml`). When one of those constants changes, the sync step above must touch every rule file that names it.

## Bundled examples in this skill

- **`references/cursor-examples/.cursor/rules/`** — full **reference copies** of [cursor-vibe-prompts](https://github.com/EvilFreelancer/cursor-vibe-prompts/tree/main/cursor-rules) (`.mdc` templates), laid out like a real Cursor project (same path as production `.cursor/rules/`).
- **`references/claude-examples/.claude/`** — parallel **Claude Code** layout (`CLAUDE.md` + `rules/*.md` with and without `paths`), aligned with [code.claude.com memory docs](https://code.claude.com/docs/en/memory#organize-rules-with-claude/rules/).
- **`references/codex-examples/.codex/`** — the **Codex** hook bridge (`hooks.json` + `hooks/attach_rules.py`, both copied into projects verbatim, and the `rules.md` index template).

Read these when generating or updating project rules so structure and BDD/TDD wording stay consistent across tools.
