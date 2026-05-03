#!/usr/bin/env python3
"""Reopen an existing persisted session (--session-id + snapshot) and print todos/active.md.

Run acp_todo_persistence_demo.py first, or set SESSION_ROOT / SESSION_ID to match.

Environment: same as acp_todo_persistence_demo.py (CODDY_BIN, CODDY_CONFIG, SESSION_ROOT, SESSION_ID, REPO_CWD).
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
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
    return str(Path.home() / ".config" / "coddy-agent" / "config.yaml")


def rpc_call(
    proc: subprocess.Popen[str],
    method: str,
    params: dict[str, Any],
    next_id: list[int],
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    rid = next_id[0]
    next_id[0] += 1
    proc.stdin.write(
        jd({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}) + "\n"
    )
    proc.stdin.flush()

    backlog: list[dict[str, Any]] = []
    assert proc.stdout is not None

    while True:
        raw = proc.stdout.readline()
        if not raw:
            raise RuntimeError("EOF from coddy")
        line = raw.strip()
        if not line:
            continue
        msg = json.loads(line)
        m = msg.get("method")

        if m == "session/request_permission":
            proc.stdin.write(
                jd(
                    {
                        "jsonrpc": "2.0",
                        "id": msg.get("id"),
                        "result": {"outcome": "allow"},
                    }
                )
                + "\n"
            )
            proc.stdin.flush()
            backlog.append(msg)
            continue

        if m == "session/update":
            backlog.append(msg)
            continue

        if "id" in msg and "method" not in msg and same_id(msg.get("id"), rid):
            return msg, backlog
        if ("result" in msg or "error" in msg) and same_id(msg.get("id"), rid):
            return msg, backlog


def main() -> None:
    binary = os.environ.get("CODDY_BIN", default_coddy_bin())
    cfg = os.environ.get("CODDY_CONFIG", default_config())
    session_root = os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp-sessions")
    session_id = os.environ.get("SESSION_ID", "example-acp-plan-todos")
    repo_cwd = os.environ.get("REPO_CWD", os.getcwd())

    todo_path = Path(session_root) / session_id / "todos" / "active.md"
    if not todo_path.is_file():
        print(f"missing {todo_path}; run acp_todo_persistence_demo.py first", file=sys.stderr)
        sys.exit(2)
    on_disk_before = todo_path.read_text(encoding="utf-8")

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
            repo_cwd,
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
        rpc_call(
            proc,
            "initialize",
            {
                "protocolVersion": 1,
                "clientCapabilities": {},
                "clientInfo": {"name": "acp-reload", "version": "1"},
            },
            nid,
        )

        _, backlog = rpc_call(
            proc,
            "session/new",
            {"cwd": repo_cwd, "mcpServers": []},
            nid,
        )

        plans_snip = [
            json.dumps(x["params"]["update"], ensure_ascii=False)
            for x in backlog
            if x.get("method") == "session/update"
            and x.get("params", {}).get("update", {}).get("sessionUpdate") == "plan"
        ]
        sys.stderr.write("--- tail of last plan notification on reopen (if any) ---\n")
        if plans_snip:
            sys.stderr.write(plans_snip[-1][-1500:] + "\n")
        else:
            sys.stderr.write("(none)\n")

        sys.stderr.write("--- todos/active.md ---\n")
        sys.stderr.write(todo_path.read_text(encoding="utf-8"))
    finally:
        proc.stdin.close()
        proc.wait(timeout=120)

    on_disk_after = todo_path.read_text(encoding="utf-8")
    print("reload_ok=", on_disk_before == on_disk_after)


if __name__ == "__main__":
    main()
