#!/usr/bin/env python3
"""Hooks: user-scope recorder hooks see a real run_command, a project hook is held until trusted."""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

from cli_tui_driver import CoddyTUI, coddy_bin, ok, prepare_home

PROJECT_FILE = ".coddy/hooks.json"
MARKER_ONE = "hooks-e2e-cli-first"
MARKER_TWO = "hooks-e2e-cli-second"


def install_hooks(home: Path, work: Path, events: Path, marker: Path) -> None:
    """The user-scope recorder and the project-scope marker hook."""
    hooks_dir = home / "hooks"
    hooks_dir.mkdir(parents=True, exist_ok=True)
    recorder = hooks_dir / "record.sh"
    recorder.write_text(
        "#!/bin/sh\n"
        f'cat >> "{events}"\n'
        f'printf "\\n" >> "{events}"\n'
        "exit 0\n"
    )
    recorder.chmod(0o755)
    (home / "hooks.json").write_text(
        json.dumps(
            {
                "hooks": {
                    "PreToolUse": [
                        {"matcher": "run_command", "hooks": [{"type": "command", "command": str(recorder)}]}
                    ],
                    "PostToolUse": [{"hooks": [{"type": "command", "command": str(recorder)}]}],
                }
            },
            indent=2,
        )
    )
    project = work / ".coddy"
    project.mkdir(parents=True, exist_ok=True)
    (project / "hooks.json").write_text(
        json.dumps(
            {
                "hooks": {
                    "PreToolUse": [
                        {
                            "matcher": "run_command",
                            "hooks": [{"type": "command", "command": f'printf "ran\\n" >> "{marker}"'}],
                        }
                    ]
                }
            },
            indent=2,
        )
    )


def hooks_cli(home: Path, work: Path, *args: str) -> str:
    env = dict(os.environ)
    env["CODDY_HOME"] = str(home)
    res = subprocess.run(
        [coddy_bin(), "hooks", *args, "--cwd", str(work)],
        cwd=str(work),
        env=env,
        capture_output=True,
        text=True,
        timeout=60,
    )
    if res.returncode != 0:
        sys.stderr.write(res.stderr)
        raise AssertionError(f"coddy hooks {' '.join(args)} failed with {res.returncode}")
    return res.stdout


def read_events(path: Path) -> list[dict]:
    if not path.exists():
        return []
    out: list[dict] = []
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            out.append(json.loads(line))
        except json.JSONDecodeError:
            continue
    return out


def prompt_for(marker: str) -> str:
    return (
        f"Call run_command once with the command: echo {marker}. Then reply with one short "
        "sentence that repeats what the command printed and stop. Do not call any other tool "
        "and do not run the command a second time."
    )


def main() -> int:
    home, work = prepare_home("hooks")
    events = home / "hook-events.jsonl"
    marker = work / "project-hook.marker"
    install_hooks(home, work, events, marker)

    listing = hooks_cli(home, work, "list")
    if PROJECT_FILE not in listing or "needs_approval" not in listing:
        raise AssertionError(f"coddy hooks list does not report the held project file:\n{listing}")

    tui = CoddyTUI("hooks", home=str(home), workdir=str(work))
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.prompt(prompt_for(MARKER_ONE))
        tui.wait_tool_call("run_command", timeout=300)
        tui.wait_idle(timeout=300)

        recorded = read_events(events)
        pre = [e for e in recorded if e.get("hook_event_name") == "PreToolUse" and e.get("tool_name") == "run_command"]
        post = [e for e in recorded if e.get("hook_event_name") == "PostToolUse" and e.get("tool_name") == "run_command"]
        if not pre or not post:
            raise AssertionError(f"recorder hooks did not see run_command (pre={len(pre)} post={len(post)} events={len(recorded)})")
        session_dir = tui.single_session_dir()
        if pre[0].get("session_id") != session_dir.name or pre[0].get("cwd") != str(work):
            raise AssertionError(f"PreToolUse payload lacks the session and workspace: {json.dumps(pre[0])[:300]}")
        if MARKER_ONE not in str((pre[0].get("tool_input") or {}).get("command", "")):
            raise AssertionError(f"PreToolUse tool_input does not carry the command: {pre[0].get('tool_input')!r}")
        if MARKER_ONE not in str(post[0].get("tool_response", "")):
            raise AssertionError(f"PostToolUse tool_response lacks the command output: {post[0].get('tool_response')!r}")
        if marker.exists():
            raise AssertionError("the unapproved project hook ran (marker exists)")

        ui_log = session_dir / "ui_log.json"
        if not ui_log.exists() or PROJECT_FILE not in ui_log.read_text():
            raise AssertionError("the session's ui_log.json carries no notice about the held project file")

        # Approve the project file; definitions are re-read on the next turn.
        approval = hooks_cli(home, work, "trust", PROJECT_FILE)
        if "Approved." not in approval:
            raise AssertionError(f"coddy hooks trust did not report the approval:\n{approval}")
        tui.prompt(prompt_for(MARKER_TWO))
        tui.wait_for(MARKER_TWO, timeout=300)
        tui.wait_idle(timeout=300)
        if not marker.exists():
            raise AssertionError("the approved project hook did not run (no marker)")
        return ok("cli_e2e_hooks")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
