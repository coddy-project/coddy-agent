# Project onboarding

## Purpose

Warm up context on a repository: study **code**, read **documentation** and **test code**, **set up the dev environment** the project expects, **run tests**, write a **short report** about the project. No extra brief from the user is required.

## When to use

- User runs **`/rpa-init`** or asks to onboard / understand a codebase.

## What the agent does

1. Read layout, docs, and tests.
2. Install or configure dev tooling (venv, deps, documented bootstrap).
3. Run the test suite.
4. Summarize purpose, behavior implied by tests, gaps, and next steps.

## Contents

| File       | Role                          |
|------------|-------------------------------|
| `SKILL.md` | Metadata and full workflow    |


## Install

This skill is distributed through the **rpa-skills** catalog (a Claude Code plugin marketplace), and also installs as a plain **skill folder** (Cursor, OpenAI Codex, Kimi Code CLI, and others).

**As a plugin (Claude Code)** — add the catalog once, then install this skill from it:

```text
/plugin marketplace add EvilFreelancer/rpa-skills
/plugin install rpa-init@rpa-skills
```

**As a plain skill folder** — copy or symlink this repository into a skill root (its `SKILL.md` lives at the repo root):

| Tool          | Path                          |
|---------------|-------------------------------|
| Claude Code   | `~/.claude/skills/rpa-init/`      |
| Cursor        | `~/.cursor/skills/rpa-init/`      |
| OpenAI Codex  | `~/.codex/skills/rpa-init/`       |
| Kimi Code CLI | `~/.kimi/skills/rpa-init/`        |

The directory name must match the `name` field in `SKILL.md`.

## How to invoke

- **Slash command** — type `/rpa-init` in agent chat.
- **`@` context** — attach the skill folder or `SKILL.md` to ground the message in these instructions.
- **Automatic** — the agent may load the skill on its own when your request matches the `description` in `SKILL.md`.

## Source & attribution

Part of **[rpa-skills](https://github.com/EvilFreelancer/rpa-skills)** — [Pavel Rykov](https://t.me/evilfreelancer)'s agent-skills collection (see [notes on vibe coding](https://t.me/evilfreelancer/1485)).

Packaged from the prompt collection **[cursor-vibe-prompts](https://github.com/EvilFreelancer/cursor-vibe-prompts)** — the same vibe-coding workflow, turned into a reusable skill.

Licensed under the MIT License — see [LICENSE](LICENSE).
