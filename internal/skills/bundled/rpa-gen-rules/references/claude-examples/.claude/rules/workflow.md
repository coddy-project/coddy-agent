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

**MANDATORY** - if any rule file is added or changed in this task, update every deliberate representation of the same policy in the same PR. Do not leave one tree ahead of the other.

1. Inventory root `AGENTS.md`, `CLAUDE.md`, `.cursor/rules/`, `.claude/rules/`, the Codex hook, and any other verified agent integration already present.
2. Pair Cursor and Claude Code files only when the project supports both hosts and the files represent the same topic.
3. Keep the Markdown body equivalent, then adapt native metadata and links:
   - Claude rule without `paths:` <-> Cursor always-on `alwaysApply: true`.
   - Claude `paths:` <-> Cursor path-scoped `globs:` with `alwaysApply: false`.
   - Claude `.claude/rules/file.md` references <-> Cursor `@file.mdc` references.
4. Keep the language identical across deliberate mirrors. Rule files and `AGENTS.md` are written in **English** unless the project deliberately uses another language.
5. If `AGENTS.md` changed, verify `CLAUDE.md` still resolves to the same content or remains an intentional short compatibility file.
6. The Codex hook reads `.cursor/rules/*.mdc` directly. Update it only when its parser or event contract changes; do not maintain a separate manual rule index.
7. Commit every counterpart together and list all synchronized files and unsupported host limitations in the final report.

Skip a counterpart when the project does not support that host or when the topic is genuinely host-specific. Label an intentional divergence in the differing file.
