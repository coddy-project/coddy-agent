# Agent project rules

## Purpose

Create or update **agent rules** for **Cursor** (`.cursor/rules/*.mdc`), **Claude Code** (`.claude/` with modular `rules/`), and **OpenAI Codex** (a `.codex/` hook bridge that auto-attaches Cursor rules by glob, since Codex has no native equivalent). Bundled examples live under `references/`.

Rules follow a **layered-cake** idea: implement **inner layers first** (no internal project dependencies), then the next layer, and so on. They also describe **BDD-style** work (behavior driven by tests). No long user brief is required; the agent infers from the repo.

Generated rules, `AGENTS.md`, and every other agent brief are written **in English** unless you explicitly ask for another language - mirrored trees are only reviewable when both sides speak the same language. The chat itself stays in your language.

Generated rules **must** include a mandatory **Rules Sync** step: any change to a rule for one agent (for example a Cursor `.mdc`) has to be mirrored to every other agent's tree (Claude `.md`, root `AGENTS.md` / `CLAUDE.md`, the `.codex/rules.md` index, etc.) in the **same** commit. The skill also recommends keeping `CLAUDE.md` as a symlink to `AGENTS.md` at repo root (see `getconf`, `getconf-ui`, `sdm-client-proxy` for reference). See `SKILL.md` and `references/bdd-and-agents.md` for the exact wording and translation table.

For Codex the skill installs a ready-made hook bridge (`.codex/hooks.json` + `.codex/hooks/attach_rules.py`, copied verbatim): `alwaysApply: true` rules are injected at `SessionStart`, glob-matched rules on `PreToolUse` when a patch touches covered files - the same behavior Cursor and Claude Code provide natively. Rule bodies are **not** duplicated; the hook reads `.cursor/rules/*.mdc` directly. After install, run `/hooks` in Codex once to trust the hook.

## When to use

- User runs **`/rpa-gen-rules`** or asks to generate or refresh rules.

## Bundled references

| Path | Contents |
|------|----------|
| `references/bdd-and-agents.md` | BDD rules meaning, Cursor vs Claude vs Codex |
| `references/cursor-examples/.cursor/rules/` | `.mdc` templates |
| `references/claude-examples/.claude/` | `CLAUDE.md` + `rules/*.md` examples |
| `references/codex-examples/.codex/` | Codex hook bridge: `hooks.json`, `hooks/attach_rules.py`, `rules.md` index template |

## Contents

| File / dir | Role |
|------------|------|
| `SKILL.md` | Metadata and deliverables |
| `references/` | Templates and notes |


## Install

This skill is distributed through the **rpa-skills** catalog (a Claude Code plugin marketplace), and also installs as a plain **skill folder** (Cursor, OpenAI Codex, Kimi Code CLI, and others).

**As a plugin (Claude Code)** — add the catalog once, then install this skill from it:

```text
/plugin marketplace add EvilFreelancer/rpa-skills
/plugin install rpa-gen-rules@rpa-skills
```

**As a plain skill folder** — copy or symlink this repository into a skill root (its `SKILL.md` lives at the repo root):

| Tool          | Path                          |
|---------------|-------------------------------|
| Claude Code   | `~/.claude/skills/rpa-gen-rules/`      |
| Cursor        | `~/.cursor/skills/rpa-gen-rules/`      |
| OpenAI Codex  | `~/.codex/skills/rpa-gen-rules/`       |
| Kimi Code CLI | `~/.kimi/skills/rpa-gen-rules/`        |

The directory name must match the `name` field in `SKILL.md`.

## How to invoke

- **Slash command** — type `/rpa-gen-rules` in agent chat.
- **`@` context** — attach the skill folder or `SKILL.md` to ground the message in these instructions.
- **Automatic** — the agent may load the skill on its own when your request matches the `description` in `SKILL.md`.

## Source & attribution

Part of **[rpa-skills](https://github.com/EvilFreelancer/rpa-skills)** — [Pavel Rykov](https://t.me/evilfreelancer)'s agent-skills collection (see [notes on vibe coding](https://t.me/evilfreelancer/1485)).

Packaged from the prompt collection **[cursor-vibe-prompts](https://github.com/EvilFreelancer/cursor-vibe-prompts)** — the same vibe-coding workflow, turned into a reusable skill.

Licensed under the MIT License — see [LICENSE](LICENSE).
