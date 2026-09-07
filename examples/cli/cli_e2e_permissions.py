#!/usr/bin/env python3
"""Permissions: ask mode renders the modal; allowing it runs the command."""

from __future__ import annotations

import sys

from cli_tui_driver import CR, CoddyTUI, ok

PROMPT = (
    "Run exactly this shell command with the run_command tool and show its "
    "output: echo PERM_E2E_TOKEN"
)


def main() -> int:
    tui = CoddyTUI("permissions", permission_mode="ask")
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.prompt(PROMPT)
        try:
            tui.wait_for("Permission required", timeout=240)
        except AssertionError:
            # One retry: live models occasionally answer without the tool.
            tui.prompt(PROMPT)
            tui.wait_for("Permission required", timeout=240)
        tui.send(CR)  # allow (first highlighted option)
        tui.wait_idle(timeout=300)
        tui.wait_tool_call("run_command", timeout=60)
        found = False
        for d in tui.session_dirs():
            for call in (d / "tool_calls").iterdir():
                result = call / "result.md"
                if result.exists() and "PERM_E2E_TOKEN" in result.read_text():
                    found = True
        if not found:
            raise AssertionError("allowed command output not persisted")
        return ok("cli_e2e_permissions")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
