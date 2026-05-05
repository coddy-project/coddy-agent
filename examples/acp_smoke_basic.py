#!/usr/bin/env python3

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


def default_coddy_bin() -> str:
    exe = shutil.which("coddy")
    return exe if exe else "coddy"


def rpc_call(
    proc: subprocess.Popen[str],
    method: str,
    params: dict[str, Any],
    next_id: list[int],
) -> dict[str, Any]:
    rid = next_id[0]
    next_id[0] += 1
    assert proc.stdin is not None
    assert proc.stdout is not None

    proc.stdin.write(jd({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}) + "\n")
    proc.stdin.flush()

    while True:
        line = proc.stdout.readline()
        if not line:
            raise RuntimeError("unexpected EOF from coddy stdout")
        line = line.strip()
        if not line:
            continue
        msg = json.loads(line)

        if msg.get("method") == "session/request_permission":
            proc.stdin.write(
                jd({"jsonrpc": "2.0", "id": msg.get("id"), "result": {"outcome": "allow"}}) + "\n"
            )
            proc.stdin.flush()
            continue

        if msg.get("method") == "session/update":
            continue

        if msg.get("id") == rid:
            return msg


def main() -> int:
    binary = os.environ.get("CODDY_BIN", default_coddy_bin())
    cfg = os.environ.get(
        "CODDY_CONFIG", str(Path(__file__).resolve().parent / "config.demo.yaml")
    )
    session_root = os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp-smoke")
    session_id = os.environ.get("SESSION_ID", "example-acp-smoke")

    work = tempfile.mkdtemp(prefix="coddy-acp-smoke-")
    os.makedirs(session_root, exist_ok=True)

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
            session_root,
            "--session-id",
            session_id,
            "--cwd",
            work,
            "--log-level",
            "warn",
            "--disable-session",
        ],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=sys.stderr,
        text=True,
        bufsize=1,
    )

    nid = [1]
    try:
        r0 = rpc_call(
            proc,
            "initialize",
            {
                "protocolVersion": 1,
                "clientCapabilities": {"terminal": True},
                "clientInfo": {"name": "acp-smoke", "title": "smoke", "version": "1.0.0"},
            },
            nid,
        )
        if "error" in r0:
            print("initialize error", jd(r0), file=sys.stderr)
            return 1

        r1 = rpc_call(proc, "session/new", {"cwd": work}, nid)
        if "error" in r1:
            print("session/new error", jd(r1), file=sys.stderr)
            return 1
        sid = (r1.get("result") or {}).get("sessionId")
        if not sid:
            print("missing sessionId", jd(r1), file=sys.stderr)
            return 1

        r2 = rpc_call(proc, "session/set_mode", {"sessionId": sid, "modeId": "plan"}, nid)
        if "error" in r2:
            print("session/set_mode error", jd(r2), file=sys.stderr)
            return 1

        print("ok acp smoke")
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
