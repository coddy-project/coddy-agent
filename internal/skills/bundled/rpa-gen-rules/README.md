# Agent project rules

## Purpose

Create or refresh project instructions with root `AGENTS.md` as the common baseline, native Cursor and Claude Code rule trees, and an optional Codex project hook that attaches Cursor rules without duplicating their bodies.

The skill can produce:

- root `AGENTS.md` with repository-wide architecture, workflow, verification, and security invariants;
- a root `CLAUDE.md` symlink or short compatibility import when Claude Code is used;
- Cursor `.cursor/rules/*.mdc` files with correct `alwaysApply` and `globs` semantics;
- Claude Code `.claude/rules/*.md` files with correct `paths` semantics;
- `.codex/hooks.json` and `.codex/hooks/attach_rules.py` when Codex support is requested;
- synchronized layered-cake and BDD/TDD guidance across deliberate mirrors.

The Codex hook reads `.cursor/rules/*.mdc` directly. The skill does not generate a separate human-readable Codex rule index. Critical constraints remain in root `AGENTS.md`, code, tests, permissions, or CI because project hooks can be disabled or untrusted.

## When to use

Use `/rpa-gen-rules` when creating, refreshing, auditing, or synchronizing project instructions, especially when Cursor and Claude Code rule trees have drifted.

No separate brief is required. The agent infers the project contract from code, tests, documentation, build files, and CI, and asks only when a real product or architecture decision is missing.

## Core behavior

- Repository-wide policy stays in root `AGENTS.md`;
- layered architecture is implemented from inner dependencies outward;
- new behavior and bug fixes follow RED-GREEN-REFACTOR;
- Cursor and Claude Code mirrors keep equivalent bodies and native frontmatter;
- Claude `paths` map to Cursor `globs` with `alwaysApply: false`;
- the Codex hook consumes Cursor rules and is probed before the result is reported;
- unsupported host mechanisms are reported, not invented.

See `SKILL.md` for the workflow and `references/bdd-and-agents.md` for detailed host semantics and synchronization guidance.

## Bundled references

| Path | Contents |
|---|---|
| `references/bdd-and-agents.md` | BDD/TDD wording, host capabilities, limits, and Rules Sync |
| `references/cursor-examples/.cursor/rules/` | Cursor `.mdc` templates |
| `references/claude-examples/.claude/` | Claude Code `CLAUDE.md` and modular rule templates |
| `references/codex-examples/.codex/hooks.json` | Codex hook configuration template |
| `references/codex-examples/.codex/hooks/attach_rules.py` | Codex hook that reads Cursor rules |

## Install

This skill is distributed through the [rpa-skills](https://github.com/EvilFreelancer/rpa-skills) catalog and can also be installed as a plain skill folder.

### Claude Code plugin

```text
/plugin marketplace add EvilFreelancer/rpa-skills
/plugin install rpa-gen-rules@rpa-skills
```

### Plain skill folder

Copy or symlink this repository into a supported skill root:

| Tool | Path |
|---|---|
| Claude Code | `~/.claude/skills/rpa-gen-rules/` |
| Cursor | `~/.cursor/skills/rpa-gen-rules/` |
| OpenAI Codex | `~/.codex/skills/rpa-gen-rules/` |
| Kimi Code CLI | `~/.kimi/skills/rpa-gen-rules/` |

The directory name must match the `name` field in `SKILL.md`.

## Source and attribution

Part of [rpa-skills](https://github.com/EvilFreelancer/rpa-skills), Pavel Rykov's agent-skills collection.

Packaged from [cursor-vibe-prompts](https://github.com/EvilFreelancer/cursor-vibe-prompts) and maintained as a reusable multi-host skill.

Licensed under the MIT License. See [LICENSE](LICENSE).
