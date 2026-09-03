#!/usr/bin/env python3
"""Subagents: spawn_agent runs a trusted project definition as a child session."""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

from cli_tui_driver import REPO_ROOT, CoddyTUI, coddy_bin, ok, prepare_home

AGENT = "marker-reporter"
MARKER = "MARKER: coddy-subagent-e2e-cli"


def install_agents_fixture(workdir: Path) -> None:
    src = REPO_ROOT / "examples" / "agents_fixture" / ".coddy" / "agents"
    dst = workdir / ".coddy" / "agents"
    shutil.copytree(src, dst, dirs_exist_ok=True)


def trust_agent(home: Path, workdir: Path) -> None:
    """Record the workspace receipt the default ask policy demands."""
    env = dict(os.environ)
    env["CODDY_HOME"] = str(home)
    res = subprocess.run(
        [coddy_bin(), "agents", "trust", AGENT, "--cwd", str(workdir)],
        cwd=str(workdir),
        env=env,
        capture_output=True,
        text=True,
        timeout=60,
    )
    if res.returncode != 0:
        sys.stderr.write(res.stderr)
        raise AssertionError(f"coddy agents trust {AGENT} failed with {res.returncode}")


def parent_session_dir(tui: CoddyTUI) -> Path:
    dirs = [d for d in tui.session_dirs() if not d.name.startswith("sub_")]
    if len(dirs) != 1:
        raise AssertionError(f"expected one parent session dir, found {[d.name for d in dirs]}")
    return dirs[0]


def messages_text(session_dir: Path, role: str | None = None) -> str:
    path = session_dir / "messages.json"
    if not path.exists():
        return ""
    data = json.loads(path.read_text())
    msgs = data.get("messages", []) if isinstance(data, dict) else data
    return "\n".join(
        str(m.get("content") or "")
        for m in msgs
        if isinstance(m, dict) and (role is None or m.get("role") == role)
    )


def wait_agent_task(parent: Path, timeout: float = 60.0) -> dict:
    """Poll the parent bundle for a finished task of kind agent."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        bg = parent / "background"
        if bg.exists():
            for task in sorted(bg.iterdir()):
                meta = task / "meta.json"
                if not meta.exists():
                    continue
                try:
                    snap = json.loads(meta.read_text())
                except json.JSONDecodeError:
                    continue
                if snap.get("kind") == "agent" and snap.get("status") not in ("queued", "running"):
                    return snap
        time.sleep(0.5)
    raise AssertionError("no finished task of kind agent under the parent session")


def main() -> int:
    # The definition and the receipt must exist before the spawn, and the
    # receipt is keyed by the temp home, so both are prepared before the
    # console process starts.
    home, work = prepare_home("subagents")
    install_agents_fixture(work)
    (work / "notes.txt").write_text(MARKER + "\n")
    trust_agent(home, work)

    tui = CoddyTUI("subagents", home=str(home), workdir=str(work))
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.prompt(
            f"Call spawn_agent once with agent \"{AGENT}\", description \"read the marker\", "
            "background false, and this prompt: Read the file notes.txt in the current "
            "directory and reply with exactly the line that starts with MARKER:. Wait for "
            "the child's report, then reply with one short sentence that repeats the marker "
            "line the subagent reported, verbatim. Do not read notes.txt yourself and do not "
            "call any other tool."
        )
        tui.wait_tool_call("spawn_agent", timeout=300)
        tui.wait_idle(timeout=600)

        parent = parent_session_dir(tui)
        task = wait_agent_task(parent)
        agent = task.get("agent") or {}
        child_id = str(agent.get("session_id") or "")
        if not child_id.startswith("sub_") or agent.get("name") != AGENT:
            raise AssertionError(f"agent task does not name a sub_ child session for {AGENT}: {task}")
        if task.get("status") != "succeeded":
            raise AssertionError(f"subagent task ended as {task.get('status')!r}, want succeeded")

        child = tui.sessions_root() / child_id
        session_json = child / "session.json"
        if not session_json.exists():
            raise AssertionError(f"child session bundle missing at {child}")
        meta = json.loads(session_json.read_text())
        if meta.get("subagentRun") is not True or meta.get("parentSessionId") != parent.name:
            raise AssertionError(f"child session.json lacks the parent link: {meta}")
        if MARKER not in messages_text(child):
            raise AssertionError("child messages.json does not contain the marker")
        if MARKER not in messages_text(parent, role="assistant"):
            raise AssertionError("parent assistant reply does not repeat the marker")
        return ok("cli_e2e_subagents")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
