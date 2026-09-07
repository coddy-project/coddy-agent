#!/usr/bin/env python3
"""ACP E2E for lifecycle hooks.

Gives the session its own coddy home with a ``hooks.json`` (user scope) whose
``PreToolUse`` and ``PostToolUse`` hooks append every payload they receive to a
JSONL file, and a workspace with a project-scope ``.coddy/hooks.json`` whose
hook writes a marker file. A real model is asked to run one shell command.

Verifies:

- the user-scope hooks ran for ``run_command``: the JSONL carries a
  ``PreToolUse`` payload naming the tool, the session id and the workspace, and
  a ``PostToolUse`` payload with the tool's response
- the project-scope hook did not run (``hooks.project_trust`` defaults to
  ``ask``): no marker, and ``coddy hooks list`` reports the file as
  ``needs_approval``
- after ``coddy hooks trust .coddy/hooks.json`` a second turn in the same
  session runs the project hook: the marker exists and the catalog says
  ``trusted``

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
from pathlib import Path
from typing import Any

MARKER_ONE = "hooks-e2e-first"
MARKER_TWO = "hooks-e2e-second"


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
                jd({"jsonrpc": "2.0", "id": msg.get("id"), "result": {"outcome": "allow"}})
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


def read_events(path: Path) -> list[dict[str, Any]]:
    """Read the JSONL the recording hook appended to."""
    if not path.is_file():
        return []
    out: list[dict[str, Any]] = []
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            out.append(json.loads(line))
        except json.JSONDecodeError:
            continue
    return out


def install_hooks(home: str, work: str, events: Path, marker: Path) -> None:
    """The user-scope recorder and the project-scope marker hook."""
    hooks_dir = Path(home) / "hooks"
    hooks_dir.mkdir(parents=True, exist_ok=True)
    recorder = hooks_dir / "record.sh"
    recorder.write_text(
        "#!/bin/sh\n"
        "# Append the payload as one JSON line; the hook decides nothing.\n"
        f'cat >> "{events}"\n'
        f'printf "\\n" >> "{events}"\n'
        "exit 0\n",
        encoding="utf-8",
    )
    recorder.chmod(0o755)
    (Path(home) / "hooks.json").write_text(
        json.dumps(
            {
                "hooks": {
                    "PreToolUse": [
                        {"matcher": "run_command", "hooks": [{"type": "command", "command": str(recorder)}]}
                    ],
                    "PostToolUse": [{"hooks": [{"type": "command", "command": str(recorder)}]}],
                }
            },
            indent=2,
        ),
        encoding="utf-8",
    )
    project = Path(work) / ".coddy"
    project.mkdir(parents=True, exist_ok=True)
    (project / "hooks.json").write_text(
        json.dumps(
            {
                "hooks": {
                    "PreToolUse": [
                        {
                            "matcher": "run_command",
                            "hooks": [{"type": "command", "command": f'printf "ran\\n" >> "{marker}"'}],
                        }
                    ]
                }
            },
            indent=2,
        ),
        encoding="utf-8",
    )


def hooks_cli(binary: str, home: str, cfg: str, work: str, *args: str) -> str:
    res = subprocess.run(
        [binary, "hooks", *args, "--cwd", work],
        cwd=work,
        env={**os.environ, "CODDY_HOME": home, "CODDY_CONFIG": cfg},
        capture_output=True,
        text=True,
        timeout=60,
    )
    if res.returncode != 0:
        raise RuntimeError(f"coddy hooks {' '.join(args)} exited {res.returncode}: {res.stderr.strip()}")
    return res.stdout


def prompt_for(marker: str, work: str) -> str:
    return f"""All work MUST stay inside this directory tree: {work}

Do exactly this, autonomously and without asking questions:

1. Call run_command ONCE with the command: echo {marker}
2. Reply with one short sentence that repeats what the command printed, then stop.

Do not call any other tool. Do not run the command a second time."""


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--keep-session", action="store_true")
    ap.add_argument("--keep-work-dir", action="store_true")
    ap.add_argument("--work-dir", default="")
    args = ap.parse_args()

    binary = os.environ.get("CODDY_BIN", default_coddy_bin())
    src_cfg = Path(os.environ.get("CODDY_CONFIG", default_config()))
    session_root = os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp-e2e")
    session_id = os.environ.get("SESSION_ID", "example-acp-hooks-e2e")
    if not src_cfg.is_file():
        print("missing config", src_cfg, file=sys.stderr)
        sys.exit(2)

    if args.work_dir:
        work = os.path.abspath(args.work_dir)
        os.makedirs(work, exist_ok=True)
        cleanup_work = False
    else:
        work = tempfile.mkdtemp(prefix="coddy-acp-hooks-e2e-")
        cleanup_work = not args.keep_work_dir

    # The user-scope hooks file and the trust receipt live under the coddy
    # home, so the script gets its own home and a config copy with the log
    # placeholder resolved into it.
    home = tempfile.mkdtemp(prefix="coddy-acp-hooks-home-")
    log_f = Path(home) / "e2e.log"
    log_f.write_text("", encoding="utf-8")
    raw = src_cfg.read_text(encoding="utf-8").replace("__E2E_LOG_PATH__", str(log_f.resolve()))
    cfg = str(Path(home) / "config.resolved.yaml")
    Path(cfg).write_text(raw, encoding="utf-8")

    events = Path(home) / "hook-events.jsonl"
    marker = Path(work) / "project-hook.marker"
    install_hooks(home, work, events, marker)

    os.makedirs(session_root, exist_ok=True)
    sdir = os.path.join(session_root, session_id)
    if not args.keep_session and os.path.isdir(sdir):
        shutil.rmtree(sdir)

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
    exit_code = 0
    try:
        r0, _ = rpc_call(
            proc,
            "initialize",
            {
                "protocolVersion": 1,
                "clientCapabilities": {"fs": {"readTextFile": True, "writeTextFile": True}, "terminal": True},
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

        # Turn 1: the user-scope recorder runs, the project hook is held.
        rp, backlog = rpc_call(
            proc,
            "session/prompt",
            {"sessionId": sid, "prompt": [{"type": "text", "text": prompt_for(MARKER_ONE, work)}]},
            nid,
        )
        if "error" in rp:
            print("session/prompt error:", jd(rp), file=sys.stderr)
            sys.exit(1)
        seen_tools = set(collect_tool_call_titles(backlog))
        print("stopReason=", rp.get("result"), "distinct_tool_calls=", sorted(seen_tools), file=sys.stderr)
        if "run_command" not in seen_tools:
            print("FAIL: run_command was never called", file=sys.stderr)
            exit_code = 12

        recorded = read_events(events)
        pre = [e for e in recorded if e.get("hook_event_name") == "PreToolUse" and e.get("tool_name") == "run_command"]
        post = [e for e in recorded if e.get("hook_event_name") == "PostToolUse" and e.get("tool_name") == "run_command"]
        print(f"recorded_events= {len(recorded)} pre={len(pre)} post={len(post)}", file=sys.stderr)
        if not pre:
            print("FAIL: no PreToolUse payload for run_command was recorded", file=sys.stderr)
            exit_code = 13
        else:
            p0 = pre[0]
            if p0.get("session_id") != sid or p0.get("cwd") != work:
                print(f"FAIL: PreToolUse payload lacks the session and workspace (got {jd(p0)[:300]!r})", file=sys.stderr)
                exit_code = 14
            if MARKER_ONE not in str((p0.get("tool_input") or {}).get("command", "")):
                print(f"FAIL: PreToolUse tool_input does not carry the command (got {p0.get('tool_input')!r})", file=sys.stderr)
                exit_code = 15
        if not post:
            print("FAIL: no PostToolUse payload for run_command was recorded", file=sys.stderr)
            exit_code = 16
        elif MARKER_ONE not in str(post[0].get("tool_response", "")):
            print(f"FAIL: PostToolUse tool_response lacks the command output (got {post[0].get('tool_response')!r})", file=sys.stderr)
            exit_code = 17

        if marker.exists():
            print("FAIL: the unapproved project hook ran (marker exists)", file=sys.stderr)
            exit_code = 18
        listing = hooks_cli(binary, home, cfg, work, "list")
        if ".coddy/hooks.json" not in listing or "needs_approval" not in listing:
            print(f"FAIL: coddy hooks list does not report the held project file:\n{listing}", file=sys.stderr)
            exit_code = 19

        # Approve the project file, then a second turn runs its hook.
        approval = hooks_cli(binary, home, cfg, work, "trust", ".coddy/hooks.json")
        print(approval.strip(), file=sys.stderr)
        listing = hooks_cli(binary, home, cfg, work, "list")
        if "needs_approval" in listing:
            print(f"FAIL: coddy hooks list still reports needs_approval after trust:\n{listing}", file=sys.stderr)
            exit_code = 20

        rp2, backlog2 = rpc_call(
            proc,
            "session/prompt",
            {"sessionId": sid, "prompt": [{"type": "text", "text": prompt_for(MARKER_TWO, work)}]},
            nid,
        )
        if "error" in rp2:
            print("second session/prompt error:", jd(rp2), file=sys.stderr)
            sys.exit(1)
        if "run_command" not in set(collect_tool_call_titles(backlog2)):
            print("FAIL: run_command was never called in the second turn", file=sys.stderr)
            exit_code = 21
        if not marker.exists():
            print("FAIL: the approved project hook did not run (no marker)", file=sys.stderr)
            exit_code = 22

        if exit_code == 0:
            print("PASS: hooks e2e", file=sys.stderr)
    finally:
        try:
            if proc.stdin:
                proc.stdin.close()
        except OSError:
            pass
        try:
            proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            proc.kill()
        shutil.rmtree(home, ignore_errors=True)
        if cleanup_work:
            shutil.rmtree(work, ignore_errors=True)
        if not args.keep_session and os.path.isdir(sdir):
            shutil.rmtree(sdir, ignore_errors=True)
    sys.exit(exit_code)


if __name__ == "__main__":
    main()
