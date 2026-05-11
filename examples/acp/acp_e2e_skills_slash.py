#!/usr/bin/env python3
"""ACP e2e: slash skills catalog and ``/coddy_slash_demo`` skill body on agent turns.

Mirrors ``examples/httpserver/http_e2e_skills_slash.py`` over stdio JSON-RPC:

1. After ``session/new``, an ``available_commands_update`` lists the fixture command ``coddy_slash_demo``.
2. A ``session/prompt`` starting with ``/coddy_slash_demo`` yields assistant text containing ``DEMO_SKILL_TOKEN:z7k9-demo-slash``.
3. A control turn must not repeat that token.

Uses a disposable ``CODDY_HOME`` with ``examples/skills_fixture/coddy_slash_demo`` copied under ``skills_fixture/`` (same layout as the HTTP harness).

Environment: ``CODDY_BIN``, ``SESSION_ROOT`` (optional).
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


FIXTURE_SLASH_NAME = "coddy_slash_demo"
VERIFICATION_TOKEN = "DEMO_SKILL_TOKEN:z7k9-demo-slash"


def jd(obj: dict[str, Any]) -> str:
    return json.dumps(obj, separators=(",", ":"), ensure_ascii=False)


def same_id(a: Any, b: Any) -> bool:
    if a == b:
        return True
    try:
        return float(a) == float(b)
    except (TypeError, ValueError):
        return False


def repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


def default_coddy_bin() -> str:
    p = repo_root() / "build" / "coddy"
    if p.is_file():
        return str(p)
    exe = shutil.which("coddy")
    return exe if exe else "coddy"


def rpc_call(
    proc: subprocess.Popen[str],
    method: str,
    params: dict[str, Any],
    next_id: list[int],
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    rid = next_id[0]
    next_id[0] += 1
    assert proc.stdin is not None
    assert proc.stdout is not None

    proc.stdin.write(jd({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}) + "\n")
    proc.stdin.flush()

    backlog: list[dict[str, Any]] = []
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
                        "result": {"outcome": "allow", "optionId": "allow"},
                    }
                )
                + "\n"
            )
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


def collect_assistant_text(backlog: list[dict[str, Any]]) -> str:
    parts: list[str] = []
    for m in backlog:
        if m.get("method") != "session/update":
            continue
        u = m.get("params", {}).get("update") or {}
        if u.get("sessionUpdate") != "agent_message_chunk":
            continue
        c = u.get("content") or {}
        if c.get("type") == "text" and isinstance(c.get("text"), str):
            parts.append(c["text"])
    return "".join(parts)


def slash_command_names(backlog: list[dict[str, Any]]) -> set[str]:
    out: set[str] = set()
    for m in backlog:
        if m.get("method") != "session/update":
            continue
        u = m.get("params", {}).get("update") or {}
        if u.get("sessionUpdate") != "available_commands_update":
            continue
        for cmd in u.get("availableCommands") or []:
            if isinstance(cmd, dict):
                n = cmd.get("name")
                if isinstance(n, str) and n.strip():
                    out.add(n.strip())
    return out


def main() -> int:
    examples_dir = Path(__file__).resolve().parent.parent
    src_cfg = examples_dir / "config.demo.yaml"
    if not src_cfg.is_file():
        print("missing", src_cfg, file=sys.stderr)
        return 2

    binary = os.environ.get("CODDY_BIN", default_coddy_bin())
    session_root = os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp")
    session_id = os.environ.get("SESSION_ID", f"acp-skills-slash-{os.getpid()}")

    home = tempfile.mkdtemp(prefix="coddy-acp-skills-home-")
    work = tempfile.mkdtemp(prefix="coddy-acp-skills-work-")
    log_f = Path(home) / "e2e.log"
    log_f.write_text("", encoding="utf-8")

    fixture_src = examples_dir / "skills_fixture" / "coddy_slash_demo"
    fixture_dst = Path(home) / "skills_fixture" / "coddy_slash_demo"
    fixture_dst.parent.mkdir(parents=True, exist_ok=True)
    shutil.copytree(fixture_src, fixture_dst, dirs_exist_ok=True)

    raw = src_cfg.read_text(encoding="utf-8")
    raw = raw.replace("__E2E_LOG_PATH__", str(log_f.resolve()))
    cfg_path = Path(home) / "config.resolved.yaml"
    cfg_path.write_text(raw, encoding="utf-8")

    os.makedirs(session_root, exist_ok=True)
    sdir = os.path.join(session_root, session_id)
    if os.path.isdir(sdir):
        shutil.rmtree(sdir)

    proc = subprocess.Popen(
        [
            "stdbuf",
            "-oL",
            "-eL",
            binary,
            "acp",
            "--home",
            home,
            "--config",
            str(cfg_path),
            "--sessions-dir",
            session_root,
            "--session-id",
            session_id,
            "--cwd",
            work,
            "--log-level",
            "warn",
        ],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=sys.stderr,
        text=True,
        bufsize=1,
        env={**os.environ, "CODDY_HOME": home},
    )
    assert proc.stdin is not None
    nid = [1]

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
                "clientInfo": {"name": "acp-skills-e2e", "title": "Skills", "version": "1.0.0"},
            },
            nid,
        )
        if "error" in r0:
            print("initialize error:", jd(r0), file=sys.stderr)
            return 1

        r1, bl1 = rpc_call(proc, "session/new", {"cwd": work, "mcpServers": []}, nid)
        if "error" in r1:
            print("session/new error:", jd(r1), file=sys.stderr)
            return 1
        sid = (r1.get("result") or {}).get("sessionId")
        if not sid:
            print("missing sessionId", jd(r1), file=sys.stderr)
            return 1

        names = slash_command_names(bl1)
        if FIXTURE_SLASH_NAME not in names:
            print(
                "slash catalog missing",
                FIXTURE_SLASH_NAME,
                "got",
                sorted(names),
                file=sys.stderr,
            )
            return 1

        r_mode, _ = rpc_call(proc, "session/set_mode", {"sessionId": sid, "modeId": "agent"}, nid)
        if "error" in r_mode:
            print("session/set_mode error:", jd(r_mode), file=sys.stderr)
            return 1

        prompt = (
            f"/{FIXTURE_SLASH_NAME}\n\n"
            "Follow the instructions from the user-invoked slash skill for this turn only. "
            "Reply in one short sentence and include the required verification token verbatim."
        )
        rp, blp = rpc_call(
            proc,
            "session/prompt",
            {"sessionId": sid, "prompt": [{"type": "text", "text": prompt}]},
            nid,
        )
        if "error" in rp:
            print("session/prompt error:", jd(rp), file=sys.stderr)
            return 1
        text = collect_assistant_text(blp)
        if VERIFICATION_TOKEN not in text:
            print("model output missing skill token", text[:800], file=sys.stderr)
            return 1

        rc, blc = rpc_call(
            proc,
            "session/prompt",
            {
                "sessionId": sid,
                "prompt": [{"type": "text", "text": "Say only: hello-demo-control"}],
            },
            nid,
        )
        if "error" in rc:
            print("control session/prompt error:", jd(rc), file=sys.stderr)
            return 1
        ctrl_text = collect_assistant_text(blc)
        if VERIFICATION_TOKEN in ctrl_text:
            print(
                "control turn unexpectedly contained skill token",
                ctrl_text[:400],
                file=sys.stderr,
            )
            return 1

        print("ok acp e2e skills slash", flush=True)
        return 0
    finally:
        proc.stdin.close()
        try:
            proc.wait(timeout=30)
        except subprocess.TimeoutExpired:
            proc.kill()
        shutil.rmtree(work, ignore_errors=True)
        shutil.rmtree(home, ignore_errors=True)
        if os.path.isdir(sdir):
            shutil.rmtree(sdir, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
