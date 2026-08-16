#!/usr/bin/env python3
"""Background tasks: run_command background:true leaves meta and output on disk."""

from __future__ import annotations

import json
import sys
import time

from cli_tui_driver import CoddyTUI, ok


def main() -> int:
    tui = CoddyTUI("background")
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.prompt(
            "Run this exact command as a background task with run_command "
            "background=true and expected_seconds=3: "
            "sh -c 'echo BG_E2E_START; sleep 2; echo BG_E2E_DONE'. "
            "Then wait for it with background_wait and report its output."
        )
        tui.wait_tool_call("run_command", timeout=300)
        tui.wait_idle(timeout=420)
        deadline = time.time() + 60
        seen = False
        while time.time() < deadline and not seen:
            for d in tui.session_dirs():
                bg = d / "background"
                if not bg.exists():
                    continue
                for task in bg.iterdir():
                    meta = task / "meta.json"
                    out = task / "output.log"
                    if meta.exists() and out.exists():
                        status = json.loads(meta.read_text()).get("status", "")
                        if status in ("succeeded", "stopped") and "BG_E2E_DONE" in out.read_text():
                            seen = True
            time.sleep(0.5)
        if not seen:
            raise AssertionError("background task artifacts missing or incomplete")
        return ok("cli_e2e_background")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
