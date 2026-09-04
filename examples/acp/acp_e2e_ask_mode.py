#!/usr/bin/env python3
"""ACP e2e: ``session/set_mode`` ``ask`` reads but never writes; back in ``agent`` the write happens.

Verifies on disk and on the wire:

- ``session/set_mode`` with ``modeId: ask`` succeeds and the ``current_mode_update`` names ``ask``
- during the ask turn every ``tool_call`` update names a read-only tool, the assistant text
  quotes the note marker, and ``ask_e2e_written.txt`` does not appear in the session cwd
- after ``session/set_mode`` back to ``agent`` the same prompt creates the artifact

Mirrors ``examples/httpserver/http_e2e_ask_mode.py``.

Environment: ``CODDY_BIN``, ``CODDY_CONFIG``, ``SESSION_ROOT``, ``SESSION_ID``.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "shared"))
sys.path.insert(0, os.path.dirname(__file__))

from acp_e2e_plan_files import default_coddy_bin, default_config, extract_tool_titles, jd, rpc_call  # noqa: E402
from ask_e2e_common import (  # noqa: E402
    check_ask_turn,
    seed_note,
    ask_prompt_text,
    wait_for_artifact,
)


def current_mode_updates(backlog: list[dict[str, Any]]) -> list[str]:
    modes: list[str] = []
    for m in backlog:
        if m.get("method") != "session/update":
            continue
        u = m.get("params", {}).get("update") or {}
        if u.get("sessionUpdate") == "current_mode_update":
            modes.append(str(u.get("currentModeId") or ""))
    return modes


def assistant_text(session_dir: Path) -> str:
    path = session_dir / "messages.json"
    if not path.is_file():
        return ""
    data = json.loads(path.read_text(encoding="utf-8"))
    rows = data.get("messages", []) if isinstance(data, dict) else data
    return "\n".join(str(m.get("content") or "") for m in rows if m.get("role") == "assistant")


def set_mode(proc: subprocess.Popen[str], sid: str, mode: str, nid: list[int]) -> None:
    r, backlog = rpc_call(proc, "session/set_mode", {"sessionId": sid, "modeId": mode}, nid)
    if "error" in r:
        raise RuntimeError(f"session/set_mode {mode} error: {jd(r)}")
    seen = current_mode_updates(backlog)
    if seen and seen[-1] != mode:
        raise RuntimeError(f"current_mode_update names {seen[-1]!r}, want {mode!r}")


def main() -> int:
    binary = os.environ.get("CODDY_BIN", default_coddy_bin())
    cfg = os.environ.get("CODDY_CONFIG", default_config())
    session_root = Path(os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp-ask")).resolve()
    session_id = os.environ.get("SESSION_ID", "example-acp-ask")

    work = Path(tempfile.mkdtemp(prefix="coddy-acp-ask-")).resolve()
    session_root.mkdir(parents=True, exist_ok=True)
    sdir = session_root / session_id
    if sdir.is_dir():
        shutil.rmtree(sdir)
    seed_note(work)

    proc = subprocess.Popen(
        [
            "stdbuf",
            "-oL",
            "-eL",
            binary,
            "acp",
            "--config",
            cfg,
            "--sessions-dir",
            str(session_root),
            "--session-id",
            session_id,
            "--cwd",
            str(work),
            "--log-level",
            "warn",
        ],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=sys.stderr,
        text=True,
        bufsize=1,
    )
    assert proc.stdin is not None

    nid = [1]
    try:
        r0, _ = rpc_call(
            proc,
            "initialize",
            {
                "protocolVersion": 1,
                "clientCapabilities": {"terminal": True},
                "clientInfo": {"name": "acp-ask-e2e", "title": "ask", "version": "1.0.0"},
            },
            nid,
        )
        if "error" in r0:
            print("initialize error", jd(r0), file=sys.stderr)
            return 1

        r1, _ = rpc_call(proc, "session/new", {"cwd": str(work)}, nid)
        if "error" in r1:
            print("session/new error", jd(r1), file=sys.stderr)
            return 1
        sid = (r1.get("result") or {}).get("sessionId") or ""
        if not sid:
            print("missing sessionId", jd(r1), file=sys.stderr)
            return 1
        advertised = [m.get("id") for m in ((r1.get("result") or {}).get("modes") or {}).get("availableModes") or []]
        if advertised and "ask" not in advertised:
            print(f"session/new does not advertise ask: {advertised}", file=sys.stderr)
            return 1

        set_mode(proc, sid, "ask", nid)

        r_ask, ask_backlog = rpc_call(
            proc,
            "session/prompt",
            {"sessionId": sid, "prompt": [{"type": "text", "text": ask_prompt_text(work)}]},
            nid,
            timeout_s=420,
        )
        if "error" in r_ask:
            print("ask session/prompt error", jd(r_ask), file=sys.stderr)
            return 1
        titles = extract_tool_titles(ask_backlog)
        try:
            check_ask_turn(titles, assistant_text(sdir), work)
        except RuntimeError as e:
            print(str(e), file=sys.stderr)
            print(f"tool_call titles during ask turn: {titles}", file=sys.stderr)
            return 1
        if (persisted := json.loads((sdir / "session.json").read_text(encoding="utf-8")).get("mode")) != "ask":
            print(f"session.json mode is {persisted!r} after the ask turn, want ask", file=sys.stderr)
            return 1

        set_mode(proc, sid, "agent", nid)
        r_agent, agent_backlog = rpc_call(
            proc,
            "session/prompt",
            {"sessionId": sid, "prompt": [{"type": "text", "text": ask_prompt_text(work)}]},
            nid,
            timeout_s=420,
        )
        if "error" in r_agent:
            print("agent session/prompt error", jd(r_agent), file=sys.stderr)
            return 1
        try:
            wait_for_artifact(work)
        except RuntimeError as e:
            print(str(e), file=sys.stderr)
            print(f"tool_call titles during agent turn: {extract_tool_titles(agent_backlog)}", file=sys.stderr)
            return 1

        print(f"ok acp ask mode e2e (ask tools: {sorted(set(titles))})")
        return 0
    except RuntimeError as e:
        print(str(e), file=sys.stderr)
        return 1
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=2)
        except subprocess.TimeoutExpired:
            proc.kill()
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
