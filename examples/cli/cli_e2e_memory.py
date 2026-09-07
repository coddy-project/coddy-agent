#!/usr/bin/env python3
"""Memory: the copilot persists a note under the coddy home memory dir."""

from __future__ import annotations

import sys
import time

from cli_tui_driver import CoddyTUI, ok


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
        return ok("cli_e2e_memory")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
