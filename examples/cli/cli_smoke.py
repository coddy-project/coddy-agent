#!/usr/bin/env python3
"""Smoke: boot the console, exchange one prompt, verify chrome + persistence."""

from __future__ import annotations

import sys

from cli_tui_driver import CoddyTUI, ok


def main() -> int:
    tui = CoddyTUI("smoke")
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.wait_for("escape interrupt", timeout=10)
        tui.prompt("Reply with exactly: CLI_SMOKE_OK")
        tui.wait_busy(timeout=30)
        tui.wait_idle(timeout=240)
        tui.wait_session_file("messages.json", timeout=30)
        msgs = tui.messages()
        roles = [m.get("role") for m in msgs]
        if "user" not in roles or "assistant" not in roles:
            raise AssertionError(f"messages.json roles = {roles}")
        if "CLI_SMOKE_OK" not in tui.assistant_text():
            raise AssertionError("assistant reply with the token was not persisted")
        # Double ctrl+c must exit promptly and print the resume hint.
        tui.send("\x03")
        tui.pump(0.2)
        tui.send("\x03")
        tui.wait_for("continue: coddy cli --session-id", timeout=10)
        return ok("cli_smoke")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
