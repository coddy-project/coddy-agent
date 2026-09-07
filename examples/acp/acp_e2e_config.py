#!/usr/bin/env python3
"""ACP e2e: staged self-configuration (HTTP twin: http_e2e_config.py, CLI twin: cli_e2e_config.py).

Drives the uci-like config tool family end to end in one ACP session:

1. ask the agent to change agent.max_turns - it must only STAGE the edit
   (active config untouched) and ask whether to save;
2. confirm in Russian ("da, sokhranyay") - the agent commits, the file changes,
   and a pre-commit snapshot (config.yaml.prev) appears next to it;
3. ask to roll back to the previous configuration - the agent restores the
   snapshot after warning about it.

The script copies examples/config.demo.yaml into a temp home so the shared demo
config is never mutated. Environment: CODDY_BIN, SESSION_ROOT, SESSION_ID.
"""

from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

ORIGINAL_MAX_TURNS = 35
TARGET_MAX_TURNS = 19


def jd(obj: dict[str, Any]) -> str:
    return json.dumps(obj, separators=(",", ":"), ensure_ascii=False)


def same_id(a: Any, b: Any) -> bool:
    if a == b:
        return True
    try:
        return float(a) == float(b)
    except (TypeError, ValueError):
        return False


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
                jd({"jsonrpc": "2.0", "id": msg.get("id"), "result": {"outcome": "allow"}}) + "\n"
            )
            proc.stdin.flush()
            continue
        if m == "session/update":
            backlog.append(msg)
            continue
        if ("result" in msg or "error" in msg) and same_id(msg.get("id"), rid):
            return msg, backlog
        backlog.append(msg)


def max_turns_in(config_path: Path) -> int:
    m = re.search(r"max_turns:\s*(\d+)", config_path.read_text(encoding="utf-8"))
    if not m:
        raise AssertionError(f"max_turns not found in {config_path}")
    return int(m.group(1))


def main() -> int:
    binary = os.environ.get("CODDY_BIN", "coddy")
    session_root = os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp-e2e")
    session_id = os.environ.get("SESSION_ID", "example-acp") + "-config-e2e"

    home = Path(tempfile.mkdtemp(prefix="coddy-acp-config-home-"))
    work = Path(tempfile.mkdtemp(prefix="coddy-acp-config-work-"))
    template = Path(__file__).resolve().parent.parent / "config.demo.yaml"
    cfg = home / "config.yaml"
    cfg.write_text(
        template.read_text(encoding="utf-8").replace("__E2E_LOG_PATH__", str(home / "e2e.log")),
        encoding="utf-8",
    )
    snapshot = cfg.parent / "config.yaml.prev"

    os.makedirs(session_root, exist_ok=True)
    sdir = Path(session_root) / session_id
    if sdir.is_dir():
        shutil.rmtree(sdir)

    proc = subprocess.Popen(
        [
            "stdbuf", "-oL", "-eL",
            binary, "acp",
            "--config", str(cfg),
            "--sessions-dir", session_root,
            "--session-id", session_id,
            "--cwd", str(work),
            "--log-level", "warn",
        ],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=sys.stderr,
        text=True,
        bufsize=1,
    )
    assert proc.stdin is not None
    nid = [1]
    exit_code = 0
    try:
        r0, _ = rpc_call(
            proc,
            "initialize",
            {
                "protocolVersion": 1,
                "clientCapabilities": {"fs": {"readTextFile": True, "writeTextFile": True}},
                "clientInfo": {"name": "acp-e2e", "title": "E2E", "version": "1.0.0"},
            },
            nid,
        )
        if "error" in r0:
            print("initialize error:", jd(r0), file=sys.stderr)
            return 1
        r1, _ = rpc_call(proc, "session/new", {"cwd": str(work), "mcpServers": []}, nid)
        if "error" in r1:
            print("session/new error:", jd(r1), file=sys.stderr)
            return 1
        sid = r1["result"]["sessionId"]

        def prompt(text: str) -> None:
            rp, _ = rpc_call(
                proc,
                "session/prompt",
                {"sessionId": sid, "prompt": [{"type": "text", "text": text}]},
                nid,
            )
            if "error" in rp:
                raise AssertionError(f"session/prompt error: {jd(rp)}")

        # 1. Stage only: the active file must not change yet.
        prompt(
            f"Change your own Coddy configuration: set agent.max_turns to {TARGET_MAX_TURNS}. "
            "Use your config tools to STAGE the edit, show me the staged commands, and ask me "
            "whether to save. Do NOT commit or apply anything until I answer."
        )
        if max_turns_in(cfg) != ORIGINAL_MAX_TURNS:
            print("FAIL: staging already changed the active config", file=sys.stderr)
            return 11
        staging = sdir / "config_staging.json"
        if not staging.is_file():
            print("FAIL: no staged commands were recorded in the session bundle", file=sys.stderr)
            return 12

        # 2. Confirm in Russian: the agent must commit and hot-reload.
        prompt("Да, сохраняй.")
        if max_turns_in(cfg) != TARGET_MAX_TURNS:
            print("FAIL: confirmation did not commit the staged change", file=sys.stderr)
            return 13
        if not snapshot.is_file() or f"max_turns: {ORIGINAL_MAX_TURNS}" not in snapshot.read_text(encoding="utf-8"):
            print("FAIL: pre-commit snapshot missing or wrong", file=sys.stderr)
            return 14

        # 3. Roll back to the previous configuration from the snapshot.
        prompt(
            "Now roll back your configuration to the previous version from the snapshot backup. "
            "I understand the warning about losing the change - proceed."
        )
        if max_turns_in(cfg) != ORIGINAL_MAX_TURNS:
            print("FAIL: rollback did not restore the previous configuration", file=sys.stderr)
            return 15

        print("ok acp e2e config", flush=True)
    finally:
        proc.stdin.close()
        proc.wait(timeout=600)
        shutil.rmtree(work, ignore_errors=True)
        shutil.rmtree(home, ignore_errors=True)
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
