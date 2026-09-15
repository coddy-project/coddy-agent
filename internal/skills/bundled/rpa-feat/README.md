# New feature (BDD)

## Purpose

Add a feature **by BDD**: **plan**, tests (**red**), code (**green**), **full test run**, **docs and examples**, **linter / pre-commit** at the end when the repo uses them.

**You must pass what to build** (for example issue text, acceptance criteria). **`/rpa-feat` alone is not enough.**

## When to use

- User runs **`/rpa-feat`** and supplies a **feature description** in the same turn or right after.

## Workflow (summary)

1. Study code, docs, tests.
2. Plan (components, contracts, edge cases).
3. Add failing tests, then implementation.
4. Run all tests; update docs and examples.
5. Run linter or `pre-commit` when configured.

## Contents

| File       | Role                    |
|------------|-------------------------|
| `SKILL.md` | Metadata and full steps |


## Install

This skill is distributed through the **rpa-skills** catalog (a Claude Code plugin marketplace), and also installs as a plain **skill folder** (Cursor, OpenAI Codex, Kimi Code CLI, and others).

**As a plugin (Claude Code)** — add the catalog once, then install this skill from it:

```text
/plugin marketplace add EvilFreelancer/rpa-skills
/plugin install rpa-feat@rpa-skills
```

**As a plain skill folder** — copy or symlink this repository into a skill root (its `SKILL.md` lives at the repo root):

| Tool          | Path                          |
|---------------|-------------------------------|
| Claude Code   | `~/.claude/skills/rpa-feat/`      |
| Cursor        | `~/.cursor/skills/rpa-feat/`      |
| OpenAI Codex  | `~/.codex/skills/rpa-feat/`       |
| Kimi Code CLI | `~/.kimi/skills/rpa-feat/`        |

The directory name must match the `name` field in `SKILL.md`.

## How to invoke

- **Slash command** — type `/rpa-feat` in agent chat.
- **`@` context** — attach the skill folder or `SKILL.md` to ground the message in these instructions.
- **Automatic** — the agent may load the skill on its own when your request matches the `description` in `SKILL.md`.

## Source & attribution

Part of **[rpa-skills](https://github.com/EvilFreelancer/rpa-skills)** — [Pavel Rykov](https://t.me/evilfreelancer)'s agent-skills collection (see [notes on vibe coding](https://t.me/evilfreelancer/1485)).

Packaged from the prompt collection **[cursor-vibe-prompts](https://github.com/EvilFreelancer/cursor-vibe-prompts)** — the same vibe-coding workflow, turned into a reusable skill.

Licensed under the MIT License — see [LICENSE](LICENSE).
