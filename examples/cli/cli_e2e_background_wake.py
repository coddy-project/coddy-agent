#!/usr/bin/env python3
"""Console e2e for the background wake (issue #305), in a real pty.

Self-boots the scripted model of cmd/tgfake (examples/shared/wake_e2e_common.py),
so no key and no network. The model starts a failing command in the background
with notify_on_finish; when it ends, the agent is woken:

- local console: the waker of the bare `coddy` runs the turn, which shows
  nothing of its own - no note and not the instruction it started from - only
  the answer, and `/tasks` says the task `woke the agent`; with a model that
  fixes what woke it, the permission modal opens inside the woken turn and
  Enter allows the command;
- console over --remote: `coddy serve` wakes the agent on the server, and the
  console, which reads only the turns it starts, follows the woken turn on the
  composer relay - the answer and the permission modal - and its `/tasks`
  reads the server's mark;
- console over --remote opened after the wake: the turn was started over HTTP,
  the agent woke while no console was attached and waits on a permission
  prompt; a console opened on that session (--session-id) learns from the
  server that a woken turn runs, follows it and answers the prompt.

Linux-only (pty). Requires pexpect and pyte (examples/cli/requirements.txt).
Environment: CODDY_BIN (default build/coddy); KEEP=1 leaves the stands' files.
"""

from __future__ import annotations

import json
import os
import sys
import time
import urllib.request
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "shared"))

from cli_tui_driver import CR, CoddyTUI, ok  # noqa: E402
from wake_e2e_common import (  # noqa: E402
    ANSWER_FIXED,
    ANSWER_STARTED,
    ANSWER_WOKEN,
    START_PROMPT,
    WAKE_INSTRUCTION,
    WAKE_TITLE,
    WakeStand,
)


class WakeTUI(CoddyTUI):
    """The console driver, pointed at a wake stand instead of the demo config."""

    def __init__(self, name: str, stand: WakeStand, extra_args: list[str] | None = None) -> None:
        self._stand = stand
        super().__init__(name, model="stub/coddy-demo", extra_args=extra_args,
                         home=str(stand.home), workdir=str(stand.work))

    def _render_config(self) -> None:
        # The stand wrote the config already; the demo template stays out.
        pass

    def _seed_env_file(self) -> None:
        pass


def run_note(name: str, stand: WakeStand, extra_args: list[str] | None = None) -> None:
    tui = WakeTUI(name, stand, extra_args)
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.prompt(START_PROMPT)
        tui.wait_for(ANSWER_STARTED, timeout=60)
        tui.wait_for(ANSWER_WOKEN, timeout=60)
        text = tui.text()
        for unwanted, why in ((WAKE_INSTRUCTION, "the wake instruction is rendered as the operator's message"),
                              (WAKE_TITLE, "the woken turn opens with a note")):
            if unwanted in text:
                tui.dump(why)
                raise AssertionError(why)
        # The task list is where the console says what woke the agent.
        tui.prompt("/tasks")
        tui.wait_for("woke the agent", timeout=30)
    finally:
        tui.close()


def run_permission(name: str, stand: WakeStand, extra_args: list[str] | None = None) -> None:
    tui = WakeTUI(name, stand, extra_args)
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.prompt(START_PROMPT)
        tui.wait_for(ANSWER_STARTED, timeout=60)
        tui.wait_for("Permission required", timeout=60)
        tui.send(CR)  # the highlighted option allows it
        tui.wait_for(ANSWER_FIXED, timeout=60)
        if not (stand.work / "fixed.txt").exists():
            raise AssertionError("the command allowed in the woken turn did not run")
    finally:
        tui.close()


def post_turn(base: str, session_id: str, text: str) -> None:
    """Run one turn over the HTTP API, as a browser would, to its end."""
    body = json.dumps({"model": "agent", "input": text, "stream": True}).encode()
    req = urllib.request.Request(f"{base}/v1/responses", data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("X-Coddy-Session-ID", session_id)
    with urllib.request.urlopen(req, timeout=120) as resp:
        resp.read()


def wait_woken_turn(base: str, session_id: str, timeout: float = 60) -> None:
    """Wait until the server runs the session's woken turn."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        req = urllib.request.Request(f"{base}/coddy/sessions/{session_id}/activity")
        req.add_header("X-Coddy-Session-ID", session_id)
        try:
            with urllib.request.urlopen(req, timeout=10) as resp:
                activity = json.loads(resp.read())
            if activity.get("turnActive") and activity.get("backgroundWake"):
                return
        except OSError:
            pass
        time.sleep(0.2)
    raise AssertionError("the server never ran the woken turn")


def run_resume_permission(name: str, stand: WakeStand) -> None:
    base = stand.serve()
    session_id = "cli-e2e-wake-resume"
    post_turn(base, session_id, START_PROMPT)
    wait_woken_turn(base, session_id)
    # The woken turn now waits on its permission prompt with nobody attached.
    time.sleep(1.5)
    tui = WakeTUI(name, stand, ["--remote", base, "--session-id", session_id])
    try:
        # The session's history fills the screen, so the banner is gone by now.
        tui.wait_for("Permission required", timeout=60)
        tui.send(CR)  # the highlighted option allows it
        tui.wait_for(ANSWER_FIXED, timeout=60)
        if not (stand.work / "fixed.txt").exists():
            raise AssertionError("the command allowed in the woken turn did not run")
    finally:
        tui.close()


def main() -> int:
    keep = bool(os.environ.get("KEEP"))

    stand = WakeStand("cli-note")
    try:
        run_note("wake-local", stand)
    finally:
        stand.close(keep)
    print("ok: the local console wakes into a turn that carries on the work, and /tasks marks the task")

    stand = WakeStand("cli-perm", fixes=True)
    try:
        run_permission("wake-local-perm", stand)
    finally:
        stand.close(keep)
    print("ok: the woken turn's permission prompt opens the modal in the local console")

    stand = WakeStand("cli-remote", http=True)
    try:
        base = stand.serve()
        run_note("wake-remote", stand, ["--remote", base])
    finally:
        stand.close(keep)
    print("ok: a console over --remote follows the turn the server woke")

    stand = WakeStand("cli-remote-perm", fixes=True, http=True)
    try:
        base = stand.serve()
        run_permission("wake-remote-perm", stand, ["--remote", base])
    finally:
        stand.close(keep)
    print("ok: the server's woken turn asks the console following it")

    stand = WakeStand("cli-remote-resume", fixes=True, http=True)
    try:
        run_resume_permission("wake-remote-resume", stand)
    finally:
        stand.close(keep)
    print("ok: a console opened after the wake follows the woken turn and answers its prompt")

    return ok("cli_e2e_background_wake")


if __name__ == "__main__":
    sys.exit(main())
