#!/usr/bin/env python3
"""Startup: the console draws its chrome in a real pty, takes keys and exits.

No model is contacted. The script proves the terminal path on the host it
runs on - raw mode, the first frame, the keyboard, a clean exit that leaves
no session behind when nothing was sent - which is what the macOS and Linux
CI jobs check with the real binary (the Go suite drives the app over a fake
terminal and never opens a pty).
"""

from __future__ import annotations

import os
import signal
import socket
import sys
import tempfile
import threading
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
        # Everything the console writes up to its exit, the scrollback it
        # leaves once the terminal is back in cooked mode included.
        tui.pump(10)
        tui.child.expect(pexpect.EOF, timeout=10)
        tui.child.close()
        if tui.child.exitstatus != 0:
            raise AssertionError(f"console exited with {tui.child.exitstatus} (signal {tui.child.signalstatus})")
        # Nothing was sent, so there is nothing to resume: no resume hint and
        # no session folder (coddy-project/coddy-agent#357).
        if "continue: coddy cli" in tui.written():
            raise AssertionError("a console closed without a message printed a resume hint")
        if tui.session_dirs():
            raise AssertionError(f"a console closed without a message left {[d.name for d in tui.session_dirs()]}")
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


def first_frame_with_hung_mcp() -> None:
    """A configured stdio server that never answers does not hold the first frame.

    The console connects its MCP servers after it has drawn (coddy-project/coddy-agent#319):
    the footer counts them while they come up, and the console still leaves
    through double ctrl+c while one is stuck in its handshake.
    """
    hung = {"name": "hung", "type": "stdio", "command": "sleep", "args": ["600"]}
    tui = CoddyTUI("startup-hung-mcp", model=STARTUP_MODEL, mcp_servers=[hung])
    try:
        started = time.time()
        tui.wait_for("coddy v", timeout=5)
        took = time.time() - started
        if took > 5:
            raise AssertionError(f"first frame took {took:.1f}s with a hung MCP server")
        tui.wait_for("escape interrupt", timeout=5)
        tui.wait_for("MCP 0/1", timeout=5)
        tui.send(CTRL_C)
        tui.wait_for("Press ctrl+c again to exit", timeout=5)
        tui.send(CTRL_C)
        tui.child.expect(pexpect.EOF, timeout=10)
        tui.child.close()
        if tui.child.exitstatus != 0:
            raise AssertionError(f"console exited with {tui.child.exitstatus} (signal {tui.child.signalstatus})")
    finally:
        tui.close()


class SilentSource:
    """A skill source that accepts connections and never answers them."""

    def __init__(self) -> None:
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.sock.bind(("127.0.0.1", 0))
        self.sock.listen(16)
        self.url = f"http://127.0.0.1:{self.sock.getsockname()[1]}/marketplace.json"
        self.accepted = 0
        self.held: list[socket.socket] = []
        threading.Thread(target=self._accept, daemon=True).start()

    def _accept(self) -> None:
        while True:
            try:
                conn, _ = self.sock.accept()
            except OSError:
                return
            self.accepted += 1
            self.held.append(conn)

    def close(self) -> None:
        self.sock.close()
        for conn in self.held:
            conn.close()


def first_frame_with_many_skills_and_a_silent_source() -> None:
    """Issue #319: 300 installed skills and a skill source that never answers
    do not keep the console from drawing and taking keys, and the start does
    not contact the source at all.

    A stdio MCP server that never answers is configured as well, the thing
    that did hang the start: the console draws before it, counts it in the
    footer, and takes keys while it is stuck.
    """
    home = Path(tempfile.mkdtemp(prefix="coddy-cli-startup-skills-home-"))
    fixture = home / "skills_fixture"
    for i in range(300):
        skill = fixture / f"synthetic-{i:03d}"
        skill.mkdir(parents=True)
        (skill / "SKILL.md").write_text(
            f"---\nname: synthetic-{i:03d}\ndescription: Synthetic skill number {i}.\n---\n\nDo step {i}.\n"
        )
    source = SilentSource()
    hung = {"name": "hung", "type": "stdio", "command": "sleep", "args": ["600"]}
    tui = CoddyTUI(
        "startup-skills",
        model=STARTUP_MODEL,
        home=str(home),
        mcp_servers=[hung],
        skill_sources=[source.url],
    )
    try:
        started = time.time()
        # The header names 300 skills, so it scrolls off the screen: what
        # the console wrote is searched, not the screen alone.
        tui.wait_written("coddy v", timeout=5)
        took = time.time() - started
        if took > 2:
            raise AssertionError(f"first frame took {took:.1f}s with 300 skills and a silent skill source")
        tui.wait_written("escape interrupt", timeout=5)
        tui.wait_for("MCP 0/1", timeout=5)
        tui.type_text("skills probe")
        tui.wait_for("skills probe", timeout=5)
        tui.pump(0.5)
        if source.accepted:
            raise AssertionError(f"the console start contacted the skill source {source.accepted} time(s)")
        tui.send(CTRL_C)
        tui.wait_gone("skills probe", timeout=5)
        tui.send(CTRL_C)
        tui.wait_for("Press ctrl+c again to exit", timeout=5)
        tui.send(CTRL_C)
        tui.child.expect(pexpect.EOF, timeout=10)
        tui.child.close()
        print(f"first frame with 300 skills, a silent skill source and a hung MCP server: {took * 1000:.0f} ms")
    finally:
        tui.close()
        source.close()


def main() -> int:
    first_frame_keys_and_exit()
    interrupt_during_startup()
    first_frame_with_hung_mcp()
    first_frame_with_many_skills_and_a_silent_source()
    return ok("cli_e2e_startup")


if __name__ == "__main__":
    sys.exit(main())
