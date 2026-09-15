---
name: rpa-feat
version: 1.0.0
description: >
  Run when the user invokes /rpa-feat together with a clear feature description (for example issue text).
  BDD workflow: plan, failing tests, implementation, green tests, full suite, docs and examples, linter at the end.
  Will not work correctly without the user supplying what to build.
---

# RPA feature work (BDD)

## When this skill applies

The user must include **`/rpa-feat`** and a **concrete feature description** in the same message or immediately after (scope, acceptance criteria, edge cases, issue text). If there is no actionable description, ask once for the minimum needed or stop.

## Principles

Behavior is anchored in tests. The loop is plan, **red**, **green**, full regression, documentation, then quality tools.

## Workflow

1. **Understand** existing code, docs, and tests. Use `.venv` and project conventions. Respect `.cursor/rules/` or `.claude/rules/` if present.
2. **Plan** briefly: modules touched, scenarios, edge cases, public API or config changes.
3. **Write tests** that specify the new behavior. Match project test style.
4. **Run the new tests** and confirm they **fail** for the right reason.
5. **Implement** until those tests pass.
6. **Run the full test suite**. Fix regressions.
7. **Update documentation and examples** wherever behavior, configuration, or public API changed.
8. **Run linter / quality gate** at the end when the repository uses it (for example `pre-commit run -a`). Fix what it reports.

## Report

Short summary: what shipped, tests added or changed, full suite result, docs touched, linter result.
