# Bugfix with regression tests

## Purpose

Fix a bug: **reproduction test** first, then **fix**, then **full test suite**, then a **short report**.

**You must describe the bug** (expected vs actual, how to reproduce, issue text). **`/rpa-bugfix` alone is not enough.**

## When to use

- User runs **`/rpa-bugfix`** and provides **bug details**.

## Workflow (summary)

1. Write failing test that reproduces the bug.
2. Fix code; confirm test passes.
3. Run all tests; run linter or pre-commit when configured.
4. Short factual report.

## Contents

| File       | Role                    |
|------------|-------------------------|
| `SKILL.md` | Metadata and full steps |

## Install

This skill is distributed through the **rpa-skills** catalog (a Claude Code plugin marketplace), and also installs as a plain **skill folder** (Cursor, OpenAI Codex, Kimi Code CLI, and others).

**As a plugin (Claude Code)** — add the catalog once, then install this skill from it:

```text
/plugin marketplace add EvilFreelancer/rpa-skills
/plugin install rpa-bugfix@rpa-skills
```

**As a plain skill folder** — copy or symlink this repository into a skill root (its `SKILL.md` lives at the repo root):

| Tool          | Path                          |
|---------------|-------------------------------|
| Claude Code   | `~/.claude/skills/rpa-bugfix/`      |
| Cursor        | `~/.cursor/skills/rpa-bugfix/`      |
| OpenAI Codex  | `~/.codex/skills/rpa-bugfix/`       |
| Kimi Code CLI | `~/.kimi/skills/rpa-bugfix/`        |

The directory name must match the `name` field in `SKILL.md`.

## How to invoke

- **Slash command** — type `/rpa-bugfix` in agent chat.
- **`@` context** — attach the skill folder or `SKILL.md` to ground the message in these instructions.
- **Automatic** — the agent may load the skill on its own when your request matches the `description` in `SKILL.md`.

## Source & attribution

Part of **[rpa-skills](https://github.com/EvilFreelancer/rpa-skills)** — [Pavel Rykov](https://t.me/evilfreelancer)'s agent-skills collection (see [notes on vibe coding](https://t.me/evilfreelancer/1485)).

Packaged from the prompt collection **[cursor-vibe-prompts](https://github.com/EvilFreelancer/cursor-vibe-prompts)** — the same vibe-coding workflow, turned into a reusable skill.

Licensed under the MIT License — see [LICENSE](LICENSE).
