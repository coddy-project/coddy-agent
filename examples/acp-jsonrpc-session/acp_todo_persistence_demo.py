#!/usr/bin/env python3
"""Demo: drive coddy acp over stdio with sequential JSON-RPC and verify todos/active.md.

Why not plain printf/echo pipelines:
    - Responses may omit the JSON-RPC "result" key when Go encodes nil (omitempty), e.g. session/set_mode.
    - Child stdout is often fully buffered unless wrapped with stdbuf -oL.
    - Notifications (session/update) arrive before the matching response id.

Environment (optional):
    CODDY_BIN      path to coddy (default: "coddy" on PATH)
    CODDY_CONFIG   path to config.yaml (default: ~/.config/coddy-agent/config.yaml)
    SESSION_ROOT   persisted sessions directory (default: /tmp/coddy-examples-acp-sessions)
    SESSION_ID     folder name under SESSION_ROOT (default: example-acp-plan-todos)
    REPO_CWD       session cwd (default: current working directory)

Usage:
    ./acp_todo_persistence_demo.py
"""

from __future__ import annotations

import argparse
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
    if exe:
        return exe
    return "coddy"


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
        line = proc.stdout.readline()
        if not line:
            raise RuntimeError("unexpected EOF from coddy stdout")
        line = line.strip()
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
            backlog.append({"_kind": "request_permission_sent", **msg})
            continue

        if m == "session/update":
            backlog.append(msg)
            u = msg.get("params", {}).get("update", {})
            if u.get("sessionUpdate") == "plan":
                print("SESSION_UPDATE(plan):", jd(u)[:2000], file=sys.stderr)
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


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument(
        "--keep-session",
        action="store_true",
        help="Do not delete SESSION_ID under SESSION_ROOT before run",
    )
    args = ap.parse_args()

    binary = os.environ.get("CODDY_BIN", default_coddy_bin())
    cfg = os.environ.get("CODDY_CONFIG", default_config())
    session_root = os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp-sessions")
    session_id = os.environ.get("SESSION_ID", "example-acp-plan-todos")
    repo_cwd = os.environ.get("REPO_CWD", os.getcwd())

    os.makedirs(session_root, exist_ok=True)
    sdir = os.path.join(session_root, session_id)
    if not args.keep_session and os.path.isdir(sdir):
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

    active_path = os.path.join(session_root, session_id, "todos", "active.md")

    def dump_active(where: str) -> None:
        print(f"\n--- {where} ---", file=sys.stderr)
        if os.path.isfile(active_path):
            print(Path(active_path).read_text(encoding="utf-8"), end="", file=sys.stderr)
        else:
            print("(no active.md)", file=sys.stderr)

    try:
        r0, _ = rpc_call(
            proc,
            "initialize",
            {
                "protocolVersion": 1,
                "clientCapabilities": {
                    "fs": {"readTextFile": True, "writeTextFile": True},
                    "terminal": True,
                },
                "clientInfo": {"name": "acp-demo", "title": "ACP demo", "version": "1.0.0"},
            },
            nid,
        )
        if "error" in r0:
            print("initialize error:", jd(r0), file=sys.stderr)
            sys.exit(1)

        r1, _ = rpc_call(proc, "session/new", {"cwd": repo_cwd, "mcpServers": []}, nid)
        if "error" in r1:
            print("session/new error:", jd(r1), file=sys.stderr)
            sys.exit(1)
        sid = r1["result"]["sessionId"]
        print("sessionId=", sid, file=sys.stderr)

        r2, _ = rpc_call(
            proc,
            "session/set_mode",
            {"sessionId": sid, "modeId": "plan"},
            nid,
        )
        if "error" in r2:
            print("set_mode error:", jd(r2), file=sys.stderr)
            sys.exit(1)

        p1_text = (
            "Ответь кратко на русском. Режим плана без выполнения кода на машине пользователя.\n"
            "С помощью инструментов работы со списком дел (todo plan):\n"
            "1) Вызови create_todo_list и создай чеклист из трех пунктов на тему проверки ACP-сессии.\n"
            "2) Затем update_todo_items - отметь первый пункт выполненным [x].\n"
            "В конце перечисли итоговое состояние пунктов текстом.\n"
        )
        rp1, ex1 = rpc_call(
            proc,
            "session/prompt",
            {"sessionId": sid, "prompt": [{"type": "text", "text": p1_text}]},
            nid,
        )
        print(
            "prompt1 backlog updates:",
            sum(1 for x in ex1 if x.get("method") == "session/update"),
            file=sys.stderr,
        )
        if "error" in rp1:
            print("session/prompt error:", jd(rp1), file=sys.stderr)
            sys.exit(1)

        dump_active("after first prompt")

        p2_text = (
            "Добавь четвертый пункт в текущий план через update_todo_items "
            '("распечатать итог в отчёт"). После сохранения кратко подтверди.'
        )
        rp2, ex2 = rpc_call(
            proc,
            "session/prompt",
            {"sessionId": sid, "prompt": [{"type": "text", "text": p2_text}]},
            nid,
        )
        print(
            "prompt2 backlog updates:",
            sum(1 for x in ex2 if x.get("method") == "session/update"),
            file=sys.stderr,
        )
        if "error" in rp2:
            print("session/prompt2 error:", jd(rp2), file=sys.stderr)
            sys.exit(1)

        dump_active("after second prompt")
        print("\nSTOP_REASON_turn1=", rp1.get("result"), file=sys.stderr)
        print("STOP_REASON_turn2=", rp2.get("result"), file=sys.stderr)

    finally:
        proc.stdin.close()
        proc.wait(timeout=120)

    print(active_path)


if __name__ == "__main__":
    main()
