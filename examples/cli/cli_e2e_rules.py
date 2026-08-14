#!/usr/bin/env python3
"""Rules: project glob rules fire for touched files through the console."""

from __future__ import annotations

import sys
import tempfile
from pathlib import Path

from cli_tui_driver import CoddyTUI, install_rules_fixture, ok

GLOB_TOKEN = "RULE_GLOB_TOKEN:e2e-glob"


def main() -> int:
    # The rules fixture must exist before the session is created at app
    # start, so the workdir is prepared before the process spawns.
    workdir = Path(tempfile.mkdtemp(prefix="coddy-cli-rules-work-"))
    install_rules_fixture(workdir)
    tui = CoddyTUI("rules", workdir=str(workdir))
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.prompt(
            "Create a Go file named demo_resource.go containing a package main "
            "declaration and one exported function returning the string \"ok\". "
            "Follow every project rule that applies to Go files, then state the "
            "rule token you applied."
        )
        tui.wait_idle(timeout=420)
        joined = "\n".join(m.get("content", "") for m in tui.messages())
        if GLOB_TOKEN not in joined:
            raise AssertionError("glob rule token missing from the transcript")
        return ok("cli_e2e_rules")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
