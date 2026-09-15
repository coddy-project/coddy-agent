# Workflow (global rule)

This file has **no** `paths` frontmatter, so it loads at session start like `.claude/CLAUDE.md`. See [modular rules](https://code.claude.com/docs/en/memory#organize-rules-with-claude-rules).

## Documentation and examples (optional convention)

If the project uses paired API docs:

- Narrative examples in `docs/examples.md`
- Runnable HTTP in `docs/examples.http`

Keep them in sync when you add or change an example.

## New features (TDD)

When the user asks for a new feature (including words like "feature", "add", "implement", "фича", "добавить"), use this order. Do not skip steps.

1. Write a test that describes the new behavior in `tests/test_*.py`. The test must **fail** (red) for the right reason.
2. Run only that test and confirm it fails.
3. Implement the smallest change that makes the test pass (green).
4. Run the full test suite. All tests must pass.
5. Run `pre-commit run -a` if the project uses pre-commit. Fix reported issues.
6. Update docs where public behavior, config, or API changed.

## Bug fixes (TDD)

1. Write a regression test that reproduces the bug. It must **fail** on the broken code.
2. Fix the implementation.
3. Confirm the new test passes, then run the full suite.
4. Run `pre-commit run -a` if configured.

## Before the final answer

- Full test run is green.
- Optional linter or pre-commit is clean when the repo expects it.
- **Rules sync** (see section below): if any rule under `.claude/rules/` or `.cursor/rules/` (or `AGENTS.md`) changed in this task, mirror the change to the other agent's tree in the same commit.
- Report what changed, which tests were added, results, and which rule files were synced (if any).

## Relationship to `implementation-order.md`

That file describes **layers and dependencies**. For **new behavior**, this file wins on order (test first, then code). Use `implementation-order.md` to choose where code belongs in the stack.

## Rules Sync

**MANDATORY** - if any rule file is added or changed in this task, mirror the change to every other agent's rule tree in the same PR. Do not leave one tree ahead of the other.

1. Identify every rule tree in the repo: `.claude/rules/`, `.cursor/rules/`, root `AGENTS.md` / `CLAUDE.md`, the Codex bridge (`.codex/`), plus any other agent roots present (`.kimi/`, `.github/copilot-instructions.md`).
2. For each edited file, locate or create its counterpart in every other tree under the same topic name (`workflow`, `testing`, `architecture`, `code-style`, `implementation-order`, `api-layer`, `core-modules`).
3. Copy the body verbatim, then adapt frontmatter and inline links:
   - Claude rule **without** `paths:` (always loads) <-> Cursor `globs: <broad>` + `alwaysApply: true`.
   - Claude `paths: ["src/**/*.py"]` <-> Cursor `globs: src/**/*.py` + `alwaysApply: true`.
   - Claude `.claude/rules/file.md` references <-> Cursor `@file.mdc` references.
   - File extension `.md` <-> `.mdc`.
4. Keep the language identical across trees. Rule files and `AGENTS.md` are written in **English** unless the user asked for another language for this project.
5. If `AGENTS.md` changed, verify `CLAUDE.md` resolves to the same content (`ls -la CLAUDE.md` shows a symlink to `AGENTS.md`; if not, refresh it or re-create the symlink: `ln -sf AGENTS.md CLAUDE.md`).
6. If the repo has the Codex bridge and a rule file was added, renamed, or removed, refresh the index in `.codex/rules.md`; the hook (`.codex/hooks.json`, `.codex/hooks/attach_rules.py`) reads `.cursor/rules/` directly and needs no update.
7. Commit both sides together. The report must list every rule file synced.

Skip only when the topic is genuinely tool-specific. When skipping, add a one-line comment in the file that diverges so the divergence is intentional and visible.
