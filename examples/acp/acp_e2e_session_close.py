#!/usr/bin/env python3
"""ACP e2e: session/close and the not-found answer of a closed session.

Verifies the ACP v1 contract Coddy answers as an agent:
- initialize advertises sessionCapabilities.close
- session/close answers the empty object, and answers it again for a session
  that is already closed (closing is idempotent)
- session/prompt against the closed session is refused with -32002 and
  data.sessionId naming the session the client asked about
- session/load opens the same session again, since close leaves the bundle

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


def default_config() -> str:
    return str(Path(__file__).resolve().parent.parent / "config.demo.yaml")


def default_binary() -> str:
    exe = shutil.which("coddy")
    return exe if exe else "coddy"


def rpc_call(
    proc: subprocess.Popen[str],
    method: str,
    params: dict[str, Any],
    next_id: list[int],
) -> dict[str, Any]:
    """Sends one request and returns its response, answering update notifications."""
    rid = next_id[0]
    next_id[0] += 1
    proc.stdin.write(jd({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}) + "\n")
    proc.stdin.flush()
    assert proc.stdout is not None
    while True:
        line = proc.stdout.readline()
        if not line:
            raise RuntimeError("unexpected EOF from coddy stdout")
        line = line.strip()
        if not line:
            continue
        msg = json.loads(line)
        if msg.get("method") == "session/request_permission":
            # Nothing in this scenario runs a tool, but a stray prompt must not
            # hang the harness.
            proc.stdin.write(jd({"jsonrpc": "2.0", "id": msg.get("id"), "result": {"outcome": "allow"}}) + "\n")
            proc.stdin.flush()
            continue
        if msg.get("method"):
            continue
        if same_id(msg.get("id"), rid):
            return msg
        raise RuntimeError(f"unexpected response {jd(msg)} while waiting for {method}")


def main() -> int:
    binary = os.environ.get("CODDY_BIN", default_binary())
    cfg = os.environ.get("CODDY_CONFIG", default_config())
    session_root = Path(os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp-close")).resolve()
    session_id = os.environ.get("SESSION_ID", "example-acp-close")

    work = Path(tempfile.mkdtemp(prefix="coddy-acp-close-")).resolve()
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
        init = rpc_call(
            proc,
            "initialize",
            {
                "protocolVersion": 1,
                "clientCapabilities": {},
                "clientInfo": {"name": "acp-close-e2e", "title": "close", "version": "1.0.0"},
            },
            nid,
        )
        if "error" in init:
            print("initialize error", jd(init), file=sys.stderr)
            return 1
        caps = ((init.get("result") or {}).get("agentCapabilities") or {}).get("sessionCapabilities") or {}
        if "close" not in caps:
            print(f"sessionCapabilities.close is not advertised: {jd(init)}", file=sys.stderr)
            return 1

        new = rpc_call(proc, "session/new", {"cwd": str(work)}, nid)
        if "error" in new:
            print("session/new error", jd(new), file=sys.stderr)
            return 1
        sid = (new.get("result") or {}).get("sessionId") or ""
        if not sid:
            print("missing sessionId", jd(new), file=sys.stderr)
            return 1

        closed = rpc_call(proc, "session/close", {"sessionId": sid}, nid)
        if "error" in closed:
            print("session/close answered an error", jd(closed), file=sys.stderr)
            return 1
        if closed.get("result") != {}:
            print(f"session/close result = {jd(closed)}, want the empty object", file=sys.stderr)
            return 1

        again = rpc_call(proc, "session/close", {"sessionId": sid}, nid)
        if "error" in again or again.get("result") != {}:
            print(f"a second session/close is not idempotent: {jd(again)}", file=sys.stderr)
            return 1

        refused = rpc_call(
            proc,
            "session/prompt",
            {"sessionId": sid, "prompt": [{"type": "text", "text": "anyone there?"}]},
            nid,
        )
        err = refused.get("error") or {}
        if err.get("code") != -32002:
            print(f"a prompt to a closed session answered {jd(refused)}, want -32002", file=sys.stderr)
            return 1
        if (err.get("data") or {}).get("sessionId") != sid:
            print(f"the not-found error names {(err.get('data') or {}).get('sessionId')}, want {sid}", file=sys.stderr)
            return 1

        reload = rpc_call(proc, "session/load", {"sessionId": sid, "cwd": str(work), "mcpServers": []}, nid)
        if "error" in reload:
            print(f"session/load after a close failed: {jd(reload)}", file=sys.stderr)
            return 1

        print("ok acp session close e2e")
        return 0
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=2)
        except subprocess.TimeoutExpired:
            proc.kill()


if __name__ == "__main__":
    sys.exit(main())
