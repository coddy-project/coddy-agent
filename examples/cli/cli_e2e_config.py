#!/usr/bin/env python3
"""Config: staged self-configuration through the console (twins: acp/http _e2e_config)."""

from __future__ import annotations

import re
import sys

from cli_tui_driver import CoddyTUI, ok

ORIGINAL_MAX_TURNS = 35
TARGET_MAX_TURNS = 19


def max_turns_in(tui: CoddyTUI) -> int:
    text = (tui.home / "config.yaml").read_text(encoding="utf-8")
    m = re.search(r"max_turns:\s*(\d+)", text)
    if not m:
        raise AssertionError("max_turns not found in the active config")
    return int(m.group(1))


def main() -> int:
    tui = CoddyTUI("config")
    snapshot = tui.home / "config.yaml.prev"
    try:
        tui.wait_for("coddy v", timeout=30)

        # 1. Stage only: the active file must not change yet.
        tui.prompt(
            f"Change your own Coddy configuration: set agent.max_turns to {TARGET_MAX_TURNS}. "
            "Use your config tools to STAGE the edit, show me the staged commands, and ask me "
            "whether to save. Do NOT commit or apply anything until I answer."
        )
        tui.wait_idle(timeout=420)
        if max_turns_in(tui) != ORIGINAL_MAX_TURNS:
            raise AssertionError("staging already changed the active config")

        # 2. Confirm in Russian: the agent must commit and hot-reload.
        tui.prompt("Да, сохраняй.")
        tui.wait_idle(timeout=420)
        if max_turns_in(tui) != TARGET_MAX_TURNS:
            raise AssertionError("confirmation did not commit the staged change")
        if not snapshot.is_file() or f"max_turns: {ORIGINAL_MAX_TURNS}" not in snapshot.read_text(encoding="utf-8"):
            raise AssertionError("pre-commit snapshot missing or wrong")

        # 3. Roll back to the previous configuration from the snapshot.
        tui.prompt(
            "Now roll back your configuration to the previous version from the snapshot backup. "
            "I understand the warning about losing the change - proceed."
        )
        tui.wait_idle(timeout=420)
        if max_turns_in(tui) != ORIGINAL_MAX_TURNS:
            raise AssertionError("rollback did not restore the previous configuration")

        return ok("cli_e2e_config")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
