---
name: rpa-gen-rules
metadata:
  version: 1.3.4
description: >
  Use when the user invokes /rpa-gen-rules or asks to create, refresh, audit, or synchronize project instructions
  such as root AGENTS.md, Cursor .cursor/rules, or Claude Code .claude/rules, especially when existing rule trees
  have drifted.
---

# Generate project rules

## Goal

Create project instructions for the coding-agent hosts the repository actually supports.

- Keep repository-wide policy in root `AGENTS.md`;
- enforce layered implementation from lower dependencies upward;
- require RED-GREEN-REFACTOR for behavior changes;
- use native Cursor and Claude Code formats accurately;
- let the Codex project hook attach Cursor rules without duplicating their bodies;
- never promise another host equivalent scoped delivery without a test.

Infer the contract from code, tests, docs, builds, and CI. Ask only when a real product decision is missing. Generated instruction files default to English unless the user or repository defines another language.

## Read progressively

- `references/bdd-and-agents.md` - BDD wording, host behavior, synchronization, and limitations;
- `references/cursor-examples/.cursor/rules/` and `references/claude-examples/.claude/` - native templates;
- `references/codex-examples/.codex/hooks.json` and `hooks/attach_rules.py` - Codex adapter templates.

## Workflow

1. Inventory root instructions, rule trees, architecture docs, tests, build files, and CI;
2. choose only hosts the repository uses or the user requests;
3. write architecture boundaries, real verification commands, security invariants, and documentation duties in repository terms;
4. create scoped native rules without widening their activation;
5. synchronize deliberate mirrors and install the Codex hook when Codex is in scope;
6. run repository checks, probe adapters, verify symlinks, and report limitations honestly.

## Quick reference

| Surface | Generated output | Role |
|---|---|---|
| Common | root `AGENTS.md` | repository-wide baseline |
| Claude compatibility | `CLAUDE.md` symlink or short import | same top-level policy |
| Cursor | `.cursor/rules/*.mdc` | native Cursor rules and Codex hook input |
| Claude Code | `.claude/rules/*.md` | native Claude Code rules |
| Codex | root `AGENTS.md`, `.codex/hooks.json`, `.codex/hooks/attach_rules.py` | common policy plus project adapter |
| Other hosts | existing, documented integration | no invented adapter |

## Cursor and Claude Code mapping

- Claude rule without `paths` -> Cursor `alwaysApply: true` without a narrowing glob;
- Claude `paths` -> Cursor `globs` with `alwaysApply: false`;
- Cursor `globs` with `alwaysApply: false` -> Claude `paths`;
- keep paired bodies equivalent and adapt only metadata and host-specific links.

Never map a path-scoped rule to `alwaysApply: true`.

## Codex (`AGENTS.md` + project hook)

Root `AGENTS.md` carries common policy. The project hook reads `.cursor/rules/*.mdc` and attaches rules for supported Codex events and edit payloads.

1. Copy the bundled `hooks.json` and `hooks/attach_rules.py` templates;
2. keep Cursor rules as the hook input and do not copy their bodies under `.codex`;
3. Do not generate `.codex/rules.md`; it is not model context and becomes a stale index;
4. probe scalar and YAML-list `globs` plus add, update, delete, rename, `Edit`, and `Write` payloads;
5. tell the user to run `/hooks` once per clone and after either hook file changes.

The hook attaches always-on rules at `SessionStart` and recognized scoped rules at `PreToolUse`. It is fail-open project code, not native Codex rule discovery or a security boundary. Keep critical constraints in root policy, permissions, code, tests, or CI. Codex execpolicy `.rules` are unrelated command policy.

## Rules Sync

Keep root `AGENTS.md` aligned with its compatibility entrypoints. Update deliberate Cursor and Claude Code mirrors together, with equivalent bodies and native metadata. The Codex hook reads the Cursor representation directly and needs no manual index. Label intentional host-specific differences.

Every generated workflow rule must carry this contract.

## Common mistakes

| Mistake | Correction |
|---|---|
| Generate `.codex/rules.md` | Do not generate it; the hook reads Cursor rules directly |
| Copy Cursor bodies into `.codex` | Keep one Cursor body |
| Call a vendor tree universally canonical | Describe its native role and explicit adapters |
| Map Claude `paths` to `alwaysApply: true` | Use Cursor `globs` with `alwaysApply: false` |
| Put a security boundary only in scoped model context | Enforce it outside the scoped rule too |
| Invent OpenCode, ZCode, `.agents`, or `.coddy` delivery | Preserve a tested project integration or research it separately |
| Copy historical PR state into standing rules | Extract and verify the current invariant |
