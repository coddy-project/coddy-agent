#!/usr/bin/env python3
"""Skills: the fixture skill appears in the slash catalog and its token lands."""

from __future__ import annotations

import sys
import tempfile
from pathlib import Path

from cli_tui_driver import CR, CoddyTUI, install_skill_fixture, ok

DEMO_TOKEN = "DEMO_SKILL_TOKEN:z7k9-demo-slash"


def main() -> int:
    home = Path(tempfile.mkdtemp(prefix="coddy-cli-skills-home-"))
    install_skill_fixture(home)
    tui = CoddyTUI("skills", home=str(home))
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.wait_for("coddy_slash_demo", timeout=20)  # header [Skills] section
        tui.type_text("/coddy_slash_demo run the demo")
        tui.send(CR)
        tui.wait_busy(timeout=30)
        tui.wait_idle(timeout=240)
        if DEMO_TOKEN not in tui.assistant_text():
            raise AssertionError("demo skill token missing from the assistant reply")
        return ok("cli_e2e_skills_slash")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
