#!/usr/bin/env python3
"""ACP E2E for subagents.

Copies ``examples/agents_fixture/.coddy/agents`` (the project-scope
``marker-reporter`` definition) into the session work dir, approves it for
that workspace with ``coddy agents trust`` (``subagents.project_trust``
defaults to ``ask``), writes a marker file, and asks a real model to delegate
reading that file to the subagent with ``spawn_agent`` and to repeat what the
child reported.

Verifies:

- ``spawn_agent`` was called in the parent turn
- the run was persisted as a task of kind ``agent`` under
  ``<parent session>/background/<task_id>/`` whose ``agent.session_id`` names
  a ``sub_*`` child session, and the task ended ``succeeded``
- the child session bundle exists under the sessions root with
  ``subagentRun: true`` and ``parentSessionId`` equal to the parent id
- the child's ``messages.json`` and the task's ``output.log`` report block
  carry the marker
- the parent's final assistant text repeats the marker

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

AGENT = "marker-reporter"
MARKER = "MARKER: coddy-subagent-e2e-acp"
REPORT_HEADER = "=== subagent report ==="


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


def default_config() -> str:
    return str(repo_root() / "examples" / "config.demo.yaml")


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


def messages_text(session_dir: Path) -> str:
    """Flatten a bundle's messages.json into one searchable string."""
    path = session_dir / "messages.json"
    if not path.is_file():
        return ""
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return ""
    msgs = data.get("messages", []) if isinstance(data, dict) else data
    parts: list[str] = []
    for m in msgs:
        if not isinstance(m, dict):
            continue
        c = m.get("content")
        if isinstance(c, str):
            parts.append(c)
    return "\n".join(parts)


def install_agents_fixture(work: str) -> None:
    src = repo_root() / "examples" / "agents_fixture" / ".coddy" / "agents"
    dst = Path(work) / ".coddy" / "agents"
    dst.parent.mkdir(parents=True, exist_ok=True)
    shutil.copytree(src, dst, dirs_exist_ok=True)


def trust_agent(binary: str, home: str, cfg: str, work: str) -> bool:
    """Record the workspace receipt the ask policy demands before a spawn."""
    res = subprocess.run(
        [binary, "agents", "trust", AGENT, "--cwd", work],
        cwd=work,
        env={**os.environ, "CODDY_HOME": home, "CODDY_CONFIG": cfg},
        capture_output=True,
        text=True,
        timeout=60,
    )
    if res.returncode != 0:
        print(
            f"FAIL: coddy agents trust {AGENT} exited {res.returncode}: {res.stderr.strip()}",
            file=sys.stderr,
        )
        return False
    if res.stdout.strip():
        print(res.stdout.strip(), file=sys.stderr)
    return True


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--keep-session", action="store_true")
    ap.add_argument("--keep-work-dir", action="store_true")
    ap.add_argument("--work-dir", default="")
    args = ap.parse_args()

    binary = os.environ.get("CODDY_BIN", default_coddy_bin())
    src_cfg = Path(os.environ.get("CODDY_CONFIG", default_config()))
    session_root = os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp-e2e")
    session_id = os.environ.get("SESSION_ID", "example-acp-subagents-e2e")
    if not src_cfg.is_file():
        print("missing config", src_cfg, file=sys.stderr)
        sys.exit(2)

    if args.work_dir:
        work = os.path.abspath(args.work_dir)
        os.makedirs(work, exist_ok=True)
        cleanup_work = False
    else:
        work = tempfile.mkdtemp(prefix="coddy-acp-subagents-e2e-")
        cleanup_work = not args.keep_work_dir

    # The trust receipt lives under the coddy home, so the script gets its own
    # home and a config copy with the log placeholder resolved into it.
    home = tempfile.mkdtemp(prefix="coddy-acp-subagents-home-")
    log_f = Path(home) / "e2e.log"
    log_f.write_text("", encoding="utf-8")
    raw = src_cfg.read_text(encoding="utf-8").replace("__E2E_LOG_PATH__", str(log_f.resolve()))
    cfg = str(Path(home) / "config.resolved.yaml")
    Path(cfg).write_text(raw, encoding="utf-8")

    install_agents_fixture(work)
    (Path(work) / "notes.txt").write_text(MARKER + "\n", encoding="utf-8")

    os.makedirs(session_root, exist_ok=True)
    sdir = os.path.join(session_root, session_id)
    if not args.keep_session and os.path.isdir(sdir):
        shutil.rmtree(sdir)

    if not trust_agent(binary, home, cfg, work):
        shutil.rmtree(home, ignore_errors=True)
        if cleanup_work:
            shutil.rmtree(work, ignore_errors=True)
        sys.exit(11)

    proc = subprocess.Popen(
        [
            "stdbuf", "-oL", "-eL",
            binary, "acp",
            "--home", home,
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
        env={**os.environ, "CODDY_HOME": home},
    )
    nid = [1]

    prompt = f"""All work MUST stay inside this directory tree: {work}

Do exactly this, autonomously and without asking questions:

1. Call spawn_agent ONCE with agent "{AGENT}", description "read the marker", background false, and this prompt:
   Read the file {work}/notes.txt and reply with exactly the line that starts with MARKER:.
   Wait for the tool to return the child's report.
2. Reply with one short sentence that repeats the marker line the subagent reported, verbatim, then stop.

Do not read notes.txt yourself. Do not call any other tool. Do not call spawn_agent a second time."""

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

        if "spawn_agent" not in seen_tools:
            print("FAIL: spawn_agent was never called", file=sys.stderr)
            exit_code = 12

        parent_text = collect_assistant_text(backlog)
        if MARKER not in parent_text:
            print(
                f"FAIL: parent reply does not repeat {MARKER!r} (got {parent_text[:300]!r})",
                file=sys.stderr,
            )
            exit_code = 13

        tasks = wait_for_terminal(session_root, session_id, timeout_s=240)
        print("--- persisted background tasks ---", file=sys.stderr)
        for t in tasks:
            agent = t.get("agent") or {}
            print(
                f"  {t.get('id')} kind={t.get('kind')} {t.get('status')} "
                f"agent={agent.get('name')} child={agent.get('session_id')}",
                file=sys.stderr,
            )

        agent_tasks = [t for t in tasks if t.get("kind") == "agent"]
        if not agent_tasks:
            print("FAIL: no task of kind agent was persisted", file=sys.stderr)
            sys.exit(14)

        task = agent_tasks[-1]
        agent = task.get("agent") or {}
        child_id = str(agent.get("session_id") or "")
        if not child_id.startswith("sub_") or agent.get("name") != AGENT:
            print(
                f"FAIL: agent task does not name a sub_ child session for {AGENT} (got {agent!r})",
                file=sys.stderr,
            )
            sys.exit(15)
        if task.get("status") != "succeeded":
            print(
                f"FAIL: task ended as {task.get('status')!r}, want succeeded",
                file=sys.stderr,
            )
            exit_code = 16

        log = Path(task["_dir"]) / "output.log"
        text = log.read_text(encoding="utf-8", errors="replace") if log.is_file() else ""
        if REPORT_HEADER not in text or MARKER not in text.split(REPORT_HEADER)[-1]:
            print(
                f"FAIL: task output has no report block carrying {MARKER!r} (got {text[-400:]!r})",
                file=sys.stderr,
            )
            exit_code = 17

        child_dir = Path(session_root) / child_id
        meta_path = child_dir / "session.json"
        if not meta_path.is_file():
            print(f"FAIL: child session bundle missing at {child_dir}", file=sys.stderr)
            sys.exit(18)
        meta = json.loads(meta_path.read_text(encoding="utf-8"))
        if meta.get("subagentRun") is not True or meta.get("parentSessionId") != session_id:
            print(
                "FAIL: child session.json lacks the parent link "
                f"(subagentRun={meta.get('subagentRun')!r}, parentSessionId={meta.get('parentSessionId')!r})",
                file=sys.stderr,
            )
            exit_code = 19
        print(
            f"child session {child_id}: name={meta.get('subagentName')!r} task={meta.get('subagentTaskId')!r}",
            file=sys.stderr,
        )

        if MARKER not in messages_text(child_dir):
            print(f"FAIL: child messages.json does not contain {MARKER!r}", file=sys.stderr)
            exit_code = 20

        if exit_code == 0:
            print(f"ok acp subagents e2e ({task.get('id')} -> {child_id})")
    finally:
        try:
            if proc.stdin:
                proc.stdin.close()
            proc.wait(timeout=10)
        except Exception:
            proc.kill()
        if cleanup_work:
            shutil.rmtree(work, ignore_errors=True)
        shutil.rmtree(home, ignore_errors=True)

    sys.exit(exit_code)


if __name__ == "__main__":
    main()
