#!/usr/bin/env python3
"""Models: the /model command switches the session model and the footer follows."""

from __future__ import annotations

import json
import sys

from cli_tui_driver import CR, CoddyTUI, ok


def main() -> int:
    tui = CoddyTUI("models")
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.wait_for("qwen3.8-27b", timeout=10)  # footer shows the default
        # Switch by explicit id (validated by the manager against YAML models).
        tui.type_text("/model rpa/gpt-oss:20b")
        tui.send(CR)
        tui.send(CR)
        tui.wait_for("(rpa) gpt-oss:20b", timeout=20)
        session = tui.single_session_dir() / "session.json"
        data = json.loads(session.read_text())
        blob = json.dumps(data)
        if "gpt-oss:20b" not in blob:
            raise AssertionError("session.json does not record the switched model")
        return ok("cli_e2e_models")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
