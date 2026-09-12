#!/usr/bin/env python3
"""Global instructions: CODDY_HOME's own AGENTS.md and rules reach a session in a bare workdir."""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

from cli_tui_driver import CoddyTUI, coddy_bin, ok, prepare_home

AGENTS_TOKEN = "USER_AGENTS_TOKEN:e2e-home"
RULE_TOKEN = "USER_RULE_TOKEN:e2e-home"


def install_home_files(home: Path) -> None:
    """The operator's own instructions and rule folder, next to config.yaml.

    Neither is configured anywhere: instructions.files defaults to
    ${CODDY_HOME}/AGENTS.md before the project's own, and ${CODDY_HOME}/rules
    is the `user` rules root. The workdir stays empty, so anything the model
    repeats came from the agent home.
    """
    (home / "AGENTS.md").write_text(
        "# Operator instructions\n\n"
        f"Every reply must include the verification token {AGENTS_TOKEN} exactly once.\n"
    )
    rules = home / "rules"
    rules.mkdir(parents=True, exist_ok=True)
    (rules / "house.mdc").write_text(
        "---\n"
        "alwaysApply: true\n"
        "description: E2E user rule\n"
        "---\n"
        f"Every reply must also include the verification token {RULE_TOKEN} exactly once.\n"
    )


def rules_list(home: Path, work: Path) -> str:
    env = dict(os.environ)
    env["CODDY_HOME"] = str(home)
    res = subprocess.run(
        [coddy_bin(), "rules", "list", "--cwd", str(work)],
        cwd=str(work),
        env=env,
        capture_output=True,
        text=True,
        timeout=60,
    )
    if res.returncode != 0:
        sys.stderr.write(res.stderr)
        raise AssertionError(f"coddy rules list failed with {res.returncode}")
    return res.stdout


def main() -> int:
    home, work = prepare_home("global-instructions")
    install_home_files(home)

    listing = rules_list(home, work)
    if "user" not in listing or "house" not in listing:
        raise AssertionError(f"coddy rules list does not report the user rule:\n{listing}")

    tui = CoddyTUI("global-instructions", home=str(home), workdir=str(work))
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.prompt(
            "Reply with one short sentence about what this workspace is for, "
            "following every instruction and rule that applies to you. "
            "Do not call any tool."
        )
        tui.wait_idle(timeout=420)
        reply = tui.assistant_text()
        if AGENTS_TOKEN not in reply:
            raise AssertionError("the agent home's AGENTS.md token is missing from the assistant reply")
        if RULE_TOKEN not in reply:
            raise AssertionError("the agent home's rule token is missing from the assistant reply")
        return ok("cli_e2e_global_instructions")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
