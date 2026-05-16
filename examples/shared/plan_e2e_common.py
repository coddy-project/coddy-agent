"""Shared constants and waits for plan-mode e2e harnesses (ACP and HTTP)."""

from __future__ import annotations

import time
from pathlib import Path

PLAN_SLUG = "e2e-plan"
PLAN_MARKER = "PLAN_E2E_MARKER_v1"
RUN_ARTIFACT = "plan_e2e_done.txt"
RUN_MARKER = "PLAN_RUN_E2E_OK"


def plan_prompt_text(work: Path) -> str:
    return f"""Working directory MUST be: {work}

You are in plan mode. Do exactly this:

1. Call plan_write once with slug exactly "{PLAN_SLUG}".
2. The full file content must be valid YAML frontmatter plus markdown body.
   Frontmatter must include name: E2E plan
   The markdown body must include this exact line on its own: {PLAN_MARKER}
3. The body must also instruct that when the plan is implemented in agent mode,
   the agent must create file {RUN_ARTIFACT} in the working directory containing exactly: {RUN_MARKER}
4. Do not call write, edit, coddy_todo_*, or plan_exit.
5. When done, reply with one line starting with PLAN_WRITE_E2E_OK.
"""


def wait_for_plan_file(session_dir: Path, slug: str, marker: str, timeout_s: float = 120) -> Path:
    plan_path = session_dir / "plans" / f"{slug}.plan.md"
    deadline = time.time() + timeout_s
    while time.time() < deadline:
        if plan_path.is_file():
            text = plan_path.read_text(encoding="utf-8", errors="replace")
            if marker in text:
                return plan_path
        time.sleep(0.35)
    raise RuntimeError(f"plan file missing or marker not found: {plan_path}")


def wait_for_run_artifact(work: Path, name: str, marker: str, timeout_s: float = 240) -> Path:
    path = work / name
    deadline = time.time() + timeout_s
    while time.time() < deadline:
        if path.is_file():
            text = path.read_text(encoding="utf-8", errors="replace")
            if marker in text:
                return path
        time.sleep(0.35)
    raise RuntimeError(f"run artifact missing or marker not found: {path}")
