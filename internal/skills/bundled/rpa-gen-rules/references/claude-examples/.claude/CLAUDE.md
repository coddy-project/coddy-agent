# Project instructions (example for Claude Code)

This file is a **template**. Copy the `.claude/` tree into a real repository root or merge with output from `/init`.

## Imports

Pull canonical project facts from versioned docs (adjust paths to your repo):

```text
See @README.md for purpose and layout. Use @pyproject.toml or @package.json for install and script commands.
```

## Tests and quality

- Python virtual environment: `.venv` when present.
- Default test command: `.venv/bin/pytest tests/ -v` (override if the project uses `uv run`, `tox`, or `nox`).
- Before finishing a feature or bugfix, run the full suite and fix regressions.

## Language

Documentation for agents - this file, `AGENTS.md`, `.claude/rules/*.md`, `.cursor/rules/*.mdc` - is written in **English** unless the project owner asks for another language. Chat replies follow the user's language; only the files are fixed. If the language does change, change it in every rule tree at once so Rules Sync keeps working.

## Modular rules

Long procedures live under `.claude/rules/`. Files **without** YAML `paths` load every session. Files **with** `paths` apply when Claude works on matching files. See [How Claude remembers your project - modular rules](https://code.claude.com/docs/en/memory#organize-rules-with-claude-rules).

Topic files in this example:

- `rules/workflow.md` — TDD for features and bugs (always loaded).
- `rules/testing.md` — pytest conventions (scoped to `tests/**`).
- `rules/code-style.md` — Python style (scoped to `**/*.py`).
- `rules/architecture.md` — layers and boundaries (scoped to application code).
- `rules/implementation-order.md` — one unit at a time (scoped to `src/**`).
- `rules/api-layer.md` — thin HTTP layer (scoped to `main.py`).

## Claude-specific note

If the repo also uses `AGENTS.md` for other agents, you can deduplicate:

```markdown
@AGENTS.md

## Claude Code

Add only Claude-specific behavior here (for example plan mode for risky paths).
```
