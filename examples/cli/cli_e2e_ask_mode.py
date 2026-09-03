#!/usr/bin/env python3
"""Ask mode: /mode ask reads the note but never writes; /mode agent then writes."""

from __future__ import annotations

import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "shared"))

from ask_e2e_common import check_ask_turn, seed_note, ask_prompt_text, wait_for_artifact
from cli_tui_driver import CR, CoddyTUI, ok


def wait_assistant(tui: CoddyTUI, needle: str, timeout: float) -> None:
    """The reply lands in messages.json a beat after the loader stops."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        tui.pump(0.3)
        if needle in tui.assistant_text():
            return
    tui.dump(f"assistant text never contained {needle!r}")
    raise AssertionError(f"assistant reply lacks {needle!r}")


def main() -> int:
    tui = CoddyTUI("ask")
    try:
        seed_note(tui.workdir)
        tui.wait_for("coddy v", timeout=30)
        tui.type_text("/mode ask")
        tui.send(CR)
        tui.wait_for("• ask", timeout=15)

        tui.prompt(ask_prompt_text(tui.workdir))
        tui.wait_idle(timeout=420)
        tui.wait_tool_call("read", timeout=60)
        wait_assistant(tui, "ASK_E2E_DONE", timeout=60)
        check_ask_turn(tui.tool_call_names(), tui.assistant_text(), tui.workdir)

        tui.type_text("/mode agent")
        tui.send(CR)
        tui.wait_gone("• ask", timeout=15)
        tui.prompt(ask_prompt_text(tui.workdir))
        tui.wait_idle(timeout=420)
        wait_for_artifact(tui.workdir, timeout_s=60)
        return ok("cli_e2e_ask_mode")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
