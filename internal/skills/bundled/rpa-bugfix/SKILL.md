---
name: rpa-bugfix
version: 1.0.0
description: >
  Run when the user invokes /rpa-bugfix together with a clear bug description (expected vs actual, reproduction).
  Reproduction test first, then fix, full test suite, short report. Will not work correctly without the bug details.
---

# RPA bugfix (test-first)

## When this skill applies

The user must include **`/rpa-bugfix`** and a **bug description** (expected vs actual, steps to reproduce, environment if relevant). Paste issue text when possible. If reproduction is impossible without one missing fact, ask that single blocking question.

## Principles

Encode the intended behavior in a **regression test** so the defect cannot silently return.

## Workflow

1. **Understand** the affected area using the report and existing tests. Use `.venv` and project test commands.
2. **Write a test** that fails on the current code and reproduces the bug.
3. **Run** that test and confirm the failure matches the bug.
4. **Fix** with the smallest reasonable change. Respect architecture and project rules.
5. **Run** the new test until it passes.
6. **Run the full test suite**.
7. **Run linter / pre-commit** when the repository uses it.

## Report

Brief: root cause, fix, test added or changed, full suite outcome, files touched.
