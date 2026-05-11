#!/usr/bin/env python3
"""ACP e2e: verify tool calls visible and persisted.

Runs one prompt that forces at least one tool call, then verifies
- ACP session/update stream includes tool_call and tool_call_update
- session folder contains tool_calls/<toolCallId> with args.json, result.md, meta.json

Environment: CODDY_BIN, CODDY_CONFIG, SESSION_ROOT, SESSION_ID.
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


def jd(obj: dict[str, Any]) -> str:
    return json.dumps(obj, separators=(",", ":"), ensure_ascii=False)


def same_id(a: Any, b: Any) -> bool:
    if a == b:
        return True
    try:
        return float(a) == float(b)
    except (TypeError, ValueError):
        return False


def default_coddy_bin() -> str:
    exe = shutil.which("coddy")
    return exe if exe else "coddy"


def default_config() -> str:
    return str(Path(__file__).resolve().parent.parent / "config.demo.yaml")


def rpc_call(
    proc: subprocess.Popen[str],
    method: str,
    params: dict[str, Any],
    next_id: list[int],
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    rid = next_id[0]
    next_id[0] += 1
    proc.stdin.write(jd({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}) + "\n")
    proc.stdin.flush()

    backlog: list[dict[str, Any]] = []
    assert proc.stdout is not None

    while True:
        line = proc.stdout.readline()
        if not line:
            raise RuntimeError("unexpected EOF from coddy stdout")
        line = line.strip()
        if not line:
            continue
        msg = json.loads(line)
        m = msg.get("method")

        if m == "session/request_permission":
            proc.stdin.write(jd({"jsonrpc": "2.0", "id": msg.get("id"), "result": {"outcome": "allow"}}) + "\n")
            proc.stdin.flush()
            backlog.append({"_kind": "request_permission_sent", **msg})
            continue

        if m == "session/update":
            backlog.append(msg)
            continue

        if "id" in msg and "method" not in msg:
            if same_id(msg.get("id"), rid):
                return msg, backlog
            backlog.append({"_kind": "unexpected_response", **msg})
            continue

        if "result" in msg or "error" in msg:
            if same_id(msg.get("id"), rid):
                return msg, backlog
            backlog.append({"_kind": "unexpected_response", **msg})
            continue

        backlog.append({"_kind": "unknown_line", **msg})


def extract_tool_events(backlog: list[dict[str, Any]]) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    calls: list[dict[str, Any]] = []
    updates: list[dict[str, Any]] = []
    for m in backlog:
        if m.get("method") != "session/update":
            continue
        u = m.get("params", {}).get("update") or {}
        if u.get("sessionUpdate") == "tool_call":
            calls.append(u)
        elif u.get("sessionUpdate") == "tool_call_update":
            updates.append(u)
    return calls, updates


def main() -> int:
    binary = os.environ.get("CODDY_BIN", default_coddy_bin())
    cfg = os.environ.get("CODDY_CONFIG", default_config())
    session_root = Path(os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp-toolcalls")).resolve()
    session_id = os.environ.get("SESSION_ID", "example-acp-toolcalls-persist")

    work = Path(tempfile.mkdtemp(prefix="coddy-acp-toolcalls-")).resolve()
    session_root.mkdir(parents=True, exist_ok=True)
    sdir = session_root / session_id
    if sdir.is_dir():
        shutil.rmtree(sdir)

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
                "clientInfo": {"name": "acp-toolcalls-e2e", "title": "toolcalls", "version": "1.0.0"},
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

        marker = work / "TOOLCALLS_E2E.marker"
        prompt = f"""Working directory MUST be: {work}
Do not browse outside this cwd.

STEP 1 - Run exactly this shell via run_command tool:
bash -lc 'echo TOOLCALLS_E2E_OK > {marker.name}'

STEP 2 - Reply single line OK when done.
"""

        r2, backlog = rpc_call(
            proc,
            "session/prompt",
            {"sessionId": sid, "prompt": [{"type": "text", "text": prompt}]},
            nid,
        )
        if "error" in r2:
            print("session/prompt error", jd(r2), file=sys.stderr)
            return 1

        calls, updates = extract_tool_events(backlog)
        if not calls:
            print("expected tool_call updates in ACP backlog", file=sys.stderr)
            return 1
        if not updates:
            print("expected tool_call_update updates in ACP backlog", file=sys.stderr)
            return 1

        tcid = (calls[0].get("toolCallId") or "").strip()
        if not tcid:
            print("missing toolCallId in tool_call", calls[0], file=sys.stderr)
            return 1

        tool_dir = sdir / "tool_calls" / tcid
        for name in ("args.json", "result.md", "meta.json"):
            p = tool_dir / name
            if not p.is_file() or p.stat().st_size < 2:
                print(f"missing persisted tool call file {p}", file=sys.stderr)
                return 1

        if not marker.is_file():
            print("expected marker file from run_command tool", file=sys.stderr)
            return 1

        print("ok acp toolcalls persist e2e")
        return 0
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=2)
        except subprocess.TimeoutExpired:
            proc.kill()
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
