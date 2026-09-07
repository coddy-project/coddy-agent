#!/usr/bin/env python3
"""Skills: the fixture skill appears in the slash catalog and its token lands.

The console runs from a workdir that also carries ``.coddy/skills/coddy_project_demo``
(``examples/skills_fixture/coddy_project_demo``), so the ``${CWD}/.coddy/skills`` entry of
the demo config must list that project-local skill in the header ``[Skills]`` section
next to the home-installed ``coddy_slash_demo`` (coddy-project/coddy-agent#146).
"""

from __future__ import annotations

import shutil
import sys
import tempfile
from pathlib import Path

from cli_tui_driver import CR, REPO_ROOT, CoddyTUI, install_skill_fixture, ok

DEMO_TOKEN = "DEMO_SKILL_TOKEN:z7k9-demo-slash"
PROJECT_SLASH_NAME = "coddy_project_demo"


def install_project_skill_fixture(workdir: Path) -> None:
    """Copy the project-local demo skill under <workdir>/.coddy/skills/."""
    src = REPO_ROOT / "examples" / "skills_fixture" / PROJECT_SLASH_NAME
    dst = workdir / ".coddy" / "skills" / PROJECT_SLASH_NAME
    if src.exists():
        shutil.copytree(src, dst, dirs_exist_ok=True)


def main() -> int:
    home = Path(tempfile.mkdtemp(prefix="coddy-cli-skills-home-"))
    work = Path(tempfile.mkdtemp(prefix="coddy-cli-skills-work-"))
    install_skill_fixture(home)
    install_project_skill_fixture(work)
    tui = CoddyTUI("skills", home=str(home), workdir=str(work))
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.wait_for("coddy_slash_demo", timeout=20)  # header [Skills] section
        tui.wait_for(PROJECT_SLASH_NAME, timeout=20)  # project-local skill from <workdir>/.coddy/skills
        tui.type_text("/coddy_slash_demo run the demo")
        tui.send(CR)
        tui.wait_turn_started(timeout=30)
        tui.wait_idle(timeout=240)
        if DEMO_TOKEN not in tui.assistant_text():
            raise AssertionError("demo skill token missing from the assistant reply")
        return ok("cli_e2e_skills_slash")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
