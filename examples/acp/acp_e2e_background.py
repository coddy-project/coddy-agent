#!/usr/bin/env python3
"""ACP E2E for background tasks.

Asks a real model to start a detached command with ``run_command``
``background: true`` plus an ``expected_seconds`` estimate, then to observe it
with ``background_list`` / ``background_output`` and let it finish.

Verifies:

- ``run_command`` was called and answered with a task id instead of the output
- the task pool persisted the run under ``<session>/background/<task_id>/``
  with a ``meta.json`` recording the estimate and the hard timeout
- the captured ``output.log`` holds what the command printed
- the task reached a terminal state (``succeeded``), not an invented one
- the model reached for at least one of the background observation tools

Environment: CODDY_BIN, CODDY_CONFIG, SESSION_ROOT, SESSION_ID.

Flags: WORK_DIR (--work-dir), --keep-session, --keep-work-dir.
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Any

MARKER = "coddy-acp-background-e2e"
OBSERVATION_TOOLS = frozenset(
    {"background_list", "background_output", "background_wait"}
)


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
    proc: "subprocess.Popen[str]",
    method: str,
    params: dict[str, Any],
    next_id: list[int],
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    rid = next_id[0]
    next_id[0] += 1
    assert proc.stdin is not None
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
            continue

        if "result" in msg or "error" in msg:
            if same_id(msg.get("id"), rid):
                return msg, backlog
            backlog.append({"_kind": "unexpected_response", **msg})
            continue

        backlog.append({"_kind": "unknown_line", **msg})


def collect_tool_call_titles(backlog: list[dict[str, Any]]) -> list[str]:
    names: list[str] = []
    for m in backlog:
        if m.get("method") != "session/update":
            continue
        u = m.get("params", {}).get("update") or {}
        if u.get("sessionUpdate") != "tool_call":
            continue
        t = u.get("title")
        if isinstance(t, str) and t.strip():
            names.append(t.strip())
    return names


def load_tasks(session_root: str, session_id: str) -> list[dict[str, Any]]:
    """Read the persisted task records of the session bundle."""
    root = Path(session_root) / session_id / "background"
    if not root.is_dir():
        return []
    out: list[dict[str, Any]] = []
    for d in sorted(root.iterdir()):
        meta = d / "meta.json"
        if not meta.is_file():
            continue
        try:
            snap = json.loads(meta.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            continue
        snap["_dir"] = str(d)
        out.append(snap)
    return out


def wait_for_terminal(
    session_root: str, session_id: str, timeout_s: float
) -> list[dict[str, Any]]:
    """Poll the bundle until no recorded task is still in flight."""
    deadline = time.time() + timeout_s
    tasks = load_tasks(session_root, session_id)
    while time.time() < deadline:
        tasks = load_tasks(session_root, session_id)
        if tasks and all(
            t.get("status") not in ("queued", "running") for t in tasks
        ):
            return tasks
        time.sleep(0.5)
    return tasks


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--keep-session", action="store_true")
    ap.add_argument("--keep-work-dir", action="store_true")
    ap.add_argument("--work-dir", default="")
    args = ap.parse_args()

    binary = os.environ.get("CODDY_BIN", default_coddy_bin())
    cfg = os.environ.get("CODDY_CONFIG", default_config())
    session_root = os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp-e2e")
    session_id = os.environ.get("SESSION_ID", "example-acp-background-e2e")

    if args.work_dir:
        work = os.path.abspath(args.work_dir)
        os.makedirs(work, exist_ok=True)
        cleanup_work = False
    else:
        work = tempfile.mkdtemp(prefix="coddy-acp-bg-e2e-")
        cleanup_work = not args.keep_work_dir

    os.makedirs(session_root, exist_ok=True)
    sdir = os.path.join(session_root, session_id)
    if not args.keep_session and os.path.isdir(sdir):
        shutil.rmtree(sdir)

    proc = subprocess.Popen(
        [
            "stdbuf", "-oL", "-eL",
            binary, "acp",
            "--config", cfg,
            "--sessions-dir", session_root,
            "--session-id", session_id,
            "--cwd", work,
            "--log-level", "warn",
        ],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=sys.stderr,
        text=True,
        bufsize=1,
    )
    nid = [1]

    prompt = f"""All work MUST stay inside this directory tree: {work}

Do exactly this, autonomously and without asking questions:

1. Start ONE background task: call run_command with background:true, expected_seconds:20, and this command:
   for i in 1 2 3 4 5 6 7 8; do echo {MARKER} $i; sleep 1; done
2. Do not block on it immediately. Call background_list once to see its status.
3. Call background_output once to look at what it has printed so far.
4. Reply with the task id and its status in one short sentence, then stop.

Do not start any other command. Do not call background_stop."""

    exit_code = 0
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
                "clientInfo": {"name": "acp-e2e", "title": "E2E", "version": "1.0.0"},
            },
            nid,
        )
        if "error" in r0:
            print("initialize error:", jd(r0), file=sys.stderr)
            sys.exit(1)

        r1, _ = rpc_call(proc, "session/new", {"cwd": work, "mcpServers": []}, nid)
        if "error" in r1:
            print("session/new error:", jd(r1), file=sys.stderr)
            sys.exit(1)
        sid = r1["result"]["sessionId"]
        print("sessionId=", sid, "work_dir=", work, file=sys.stderr)

        rp, backlog = rpc_call(
            proc,
            "session/prompt",
            {"sessionId": sid, "prompt": [{"type": "text", "text": prompt}]},
            nid,
        )
        if "error" in rp:
            print("session/prompt error:", jd(rp), file=sys.stderr)
            sys.exit(1)

        seen_tools = set(collect_tool_call_titles(backlog))
        print("stopReason=", rp.get("result"), file=sys.stderr)
        print("distinct_tool_calls=", sorted(seen_tools), file=sys.stderr)

        if "run_command" not in seen_tools:
            print("FAIL: run_command was never called", file=sys.stderr)
            exit_code = 12

        if not (seen_tools & OBSERVATION_TOOLS):
            print(
                "FAIL: never observed a background task tool (expected one of "
                + ", ".join(sorted(OBSERVATION_TOOLS))
                + ")",
                file=sys.stderr,
            )
            exit_code = 13

        tasks = wait_for_terminal(session_root, session_id, timeout_s=180)
        print("--- persisted background tasks ---", file=sys.stderr)
        for t in tasks:
            print(
                f"  {t.get('id')} {t.get('status')} "
                f"expected={t.get('expected_seconds')} timeout={t.get('timeout_seconds')}",
                file=sys.stderr,
            )

        if not tasks:
            print("FAIL: no background task was persisted", file=sys.stderr)
            sys.exit(14)

        task = tasks[-1]
        if not task.get("expected_seconds"):
            print("FAIL: task carries no expected_seconds estimate", file=sys.stderr)
            exit_code = 15
        if not task.get("timeout_seconds"):
            print("FAIL: task carries no hard timeout", file=sys.stderr)
            exit_code = 16
        if task.get("status") != "succeeded":
            print(
                f"FAIL: task ended as {task.get('status')!r}, want succeeded",
                file=sys.stderr,
            )
            exit_code = 17

        log = Path(task["_dir"]) / "output.log"
        text = log.read_text(encoding="utf-8", errors="replace") if log.is_file() else ""
        if MARKER not in text:
            print(
                f"FAIL: captured log does not contain {MARKER!r} (got {text[:200]!r})",
                file=sys.stderr,
            )
            exit_code = 18

        if exit_code == 0:
            print(f"ok acp background e2e ({task.get('id')})")
    finally:
        try:
            if proc.stdin:
                proc.stdin.close()
            proc.wait(timeout=10)
        except Exception:
            proc.kill()
        if cleanup_work:
            shutil.rmtree(work, ignore_errors=True)

    sys.exit(exit_code)


if __name__ == "__main__":
    main()
