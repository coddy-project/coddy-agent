#!/usr/bin/env python3
"""Startup: the console draws its chrome in a real pty, takes keys and exits.

No model is contacted. The script proves the terminal path on the host it
runs on - raw mode, the first frame, the keyboard, the resume hint printed
after the terminal is restored - which is what the macOS and Linux CI jobs
check with the real binary (the Go suite drives the app over a fake
terminal and never opens a pty).
"""

from __future__ import annotations

import os
import signal
import sys
import tempfile
import time
from pathlib import Path

import pexpect

from cli_tui_driver import CTRL_C, CoddyTUI, ok

# A provider row of config.demo.yaml that reports no account usage, so the
# session start sends nothing over the network.
STARTUP_MODEL = "rpa/qwen3.6-35b-a3b"


def first_frame_keys_and_exit() -> None:
    """The console draws, takes keys and leaves through double ctrl+c."""
    tui = CoddyTUI("startup", model=STARTUP_MODEL)
    try:
        # The first frame: header, hints, the editor between its rules.
        tui.wait_for("coddy v", timeout=30)
        tui.wait_for("escape interrupt", timeout=10)
        # Keys reach the editor through the pty.
        tui.type_text("startup probe")
        tui.wait_for("startup probe", timeout=5)
        # ctrl+c on a filled editor clears it; on an empty one it asks for a
        # second press, and that one ends the console.
        tui.send(CTRL_C)
        tui.wait_gone("startup probe", timeout=5)
        tui.send(CTRL_C)
        tui.wait_for("Press ctrl+c again to exit", timeout=5)
        tui.send(CTRL_C)
        # The resume hint lands in the scrollback once the terminal is back
        # in cooked mode.
        tui.wait_for("continue: coddy cli --session-id", timeout=10)
        tui.child.expect(pexpect.EOF, timeout=10)
        tui.child.close()
        if tui.child.exitstatus != 0:
            raise AssertionError(f"console exited with {tui.child.exitstatus} (signal {tui.child.signalstatus})")
    finally:
        tui.close()


def interrupt_during_startup() -> None:
    """A second interrupt ends a console whose startup is stuck.

    The footer reads the git branch before the first frame; a git on PATH
    that never answers holds the startup there. The first SIGINT cancels the
    startup context (nothing in that step watches it), the second must end
    the process the default way instead of being swallowed - which is what
    a user saw on macOS as a row of ^C with nothing happening.
    """
    fake = Path(tempfile.mkdtemp(prefix="coddy-cli-startup-git-"))
    marker = fake / "git-started"
    git = fake / "git"
    git.write_text(f"#!/bin/sh\n: > '{marker}'\nexec sleep 30\n")
    git.chmod(0o755)
    tui = CoddyTUI(
        "startup-interrupt",
        model=STARTUP_MODEL,
        env_extra={"PATH": f"{fake}{os.pathsep}{os.environ.get('PATH', '')}"},
    )
    try:
        deadline = time.time() + 10
        while not marker.exists():
            if time.time() > deadline:
                raise AssertionError("the console never ran git during startup")
            tui.pump(0.05)
        tui.child.kill(signal.SIGINT)
        tui.pump(0.3)
        tui.child.kill(signal.SIGINT)
        sent = time.time()
        tui.child.expect(pexpect.EOF, timeout=5)
        tui.child.close()
        took = time.time() - sent
        if tui.child.signalstatus != signal.SIGINT:
            raise AssertionError(
                f"console did not end on the second interrupt: exit={tui.child.exitstatus} signal={tui.child.signalstatus}"
            )
        if took > 2:
            raise AssertionError(f"console took {took:.1f}s to end after the second interrupt")
    finally:
        tui.close()


def main() -> int:
    first_frame_keys_and_exit()
    interrupt_during_startup()
    return ok("cli_e2e_startup")


if __name__ == "__main__":
    sys.exit(main())
