#!/usr/bin/env python3
"""Scheduler: the model creates a job through scheduler tools; the job file lands."""

from __future__ import annotations

import sys
import time

from cli_tui_driver import CoddyTUI, ok


def main() -> int:
    tui = CoddyTUI("scheduler")
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.prompt(
            "Use coddy_scheduler_job_create to create a scheduler job with id "
            "cli_e2e_tick, cron schedule '*/5 * * * *', and the body: echo tick. "
            "Then confirm the job id."
        )
        tui.wait_tool_call("coddy_scheduler_job_create", timeout=300)
        tui.wait_idle(timeout=300)
        sched = tui.home / "scheduler"
        deadline = time.time() + 60
        found = False
        while time.time() < deadline and not found:
            if sched.exists():
                for p in sched.glob("*.md"):
                    if "cli_e2e_tick" in p.name or "cli_e2e_tick" in p.read_text():
                        found = True
                        break
            time.sleep(0.5)
        if not found:
            raise AssertionError("scheduler job markdown not found under home/scheduler")
        return ok("cli_e2e_scheduler_agent")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
