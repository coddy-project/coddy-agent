#!/usr/bin/env python3
"""Memory: the memory subagent persists a note under the coddy home memory dir,
and its run is a child session bundle under the session's subagents folder."""

from __future__ import annotations

import json
import sys
import time
from pathlib import Path

from cli_tui_driver import CoddyTUI, ok


def _is_memory_child(session_json: Path) -> bool:
    """True for the session.json of a child bundle the memory subagent ran in."""
    try:
        meta = json.loads(session_json.read_text(encoding="utf-8", errors="replace"))
    except (OSError, ValueError):
        return False
    return meta.get("subagentName") == "memory"


def main() -> int:
    tui = CoddyTUI("memory")
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.prompt(
            "Remember durably for future sessions: my favorite demo constant is "
            "MEMORY_E2E_OMEGA. Confirm briefly."
        )
        tui.wait_idle(timeout=300)
        deadline = time.time() + 120
        found = False
        while time.time() < deadline and not found:
            mem = tui.home / "memory"
            if mem.exists():
                for p in mem.rglob("*.md"):
                    if "MEMORY_E2E_OMEGA" in p.read_text():
                        found = True
                        break
            tui.pump(0.3)
            time.sleep(0.5)
        if not found:
            raise AssertionError("no memory markdown captured the token")
        # The run's record: a child session named memory inside the session bundle.
        children = [
            p
            for p in (tui.home / "sessions").rglob("subagents/*/session.json")
            if _is_memory_child(p)
        ]
        if not children:
            raise AssertionError("no memory child session bundle under the session's subagents folder")
        return ok("cli_e2e_memory")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
