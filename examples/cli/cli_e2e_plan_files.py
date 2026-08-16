#!/usr/bin/env python3
"""Plan mode: /mode plan writes a plan file with the shared e2e marker."""

from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "shared"))

from cli_tui_driver import CR, CoddyTUI, ok
from plan_e2e_common import PLAN_MARKER, PLAN_SLUG, plan_prompt_text


def main() -> int:
    tui = CoddyTUI("plan")
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.type_text("/mode")
        tui.send(CR)
        tui.wait_for("Select mode", timeout=15)
        tui.send("\x1b[B")  # down to plan
        tui.send(CR)
        tui.wait_for("• plan", timeout=15)
        tui.prompt(plan_prompt_text("document the demo workflow"))
        tui.wait_idle(timeout=420)
        plan = tui.wait_session_file(f"plans/{PLAN_SLUG}.plan.md", timeout=60)
        if PLAN_MARKER not in plan.read_text():
            raise AssertionError("plan file lacks the e2e marker")
        return ok("cli_e2e_plan_files")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
