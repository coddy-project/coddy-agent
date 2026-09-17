---
name: rpa-init
version: 1.0.0
description: >
  Run when the user invokes /rpa-init or asks to onboard or warm up context on a repository.
  The agent studies code, reads documentation and test code, sets up the dev environment as the project expects,
  runs tests, and writes a short project report. No extra user brief is required.
---

# RPA project initialization (context warm-up)

## Intent

Treat automated tests as the project long-term memory. Initialization maps **documented intent**, **behavior encoded in tests**, and **implementation**. Prefer learning from tests and docs before inferring only from production code.

## Preconditions you must verify yourself

1. Read **application source**, **docs**, and **tests** (test code is part of the specification).
2. **Dev environment** - install dependencies and prepare the environment the repo documents (for example `python -m venv .venv`, `pip install -e .`, `uv sync`, `npm ci`, or commands from `README` / CI). If the stack is not Python, follow that ecosystem's norms.
3. Locate how tests are run. Default for Python: `pytest` via `.venv` when present. Respect `pytest.ini`, `pyproject.toml`, or `tox` / `nox` if present.
4. Identify entrypoints: `README`, `docs/`, package layout, `main` modules, CLI.

## Workflow

1. **Scan** repository structure (layout, monorepo packages if any).
2. **Read** user-facing documentation and specs.
3. **Study test code** (naming, fixtures, markers, integration vs unit). This is the BDD-facing view of expected behavior.
4. **Set up dev environment** so tests can run (create venv, install deps, any documented bootstrap). Note blockers if setup cannot be completed.
5. **Run the test suite**, for example:

   ```bash
   .venv/bin/pytest -q
   ```

   If tests fail, record where and why (do not fix unless the user asked).

6. **Summarize** in a concise report (see template). Use English for code-related terms if the codebase uses English; respond in the user's language for narrative.

## Report template

Use this structure (adapt if needed):

```markdown
## Project snapshot
- Purpose (one paragraph)
- Main packages and boundaries

## Dev environment
- What was installed or configured (venv, package manager, key commands)

## How to run tests
- Commands actually used

## Behavior from tests
- Scenarios covered by tests (bullets)
- Gaps (important paths with weak or missing tests)

## Risks and notes
- Flaky tests, secrets, external services

## Suggested next steps
- 1 to 3 follow-ups
```

## Constraints

- Comments in any new code: English only.
- Do not add secrets or keys.
- If the repository is not Python, still follow the same pattern: official install and test commands, then report.
