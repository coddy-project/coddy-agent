#!/usr/bin/env python3
"""ACP agent-mode E2E: agent invents plan, persists todos, uses tools plus generated artifacts.

Prompt only states high-level rules (bounded cwd, backlog, tooling, synthesized content).

Verifies:

- backlog shows todo list progressed to zero pending and min completed steps
- at least create_todo_list plus a reconcile operation (update_todo_item or done_todo_item)
- filesystem side effect from a write-class tool invocation
- non-trivial synthesized text persisted under cwd (combined size heuristic)

Environment: CODDY_BIN, CODDY_CONFIG, SESSION_ROOT, SESSION_ID.

Flags: WORK_DIR (--work-dir), --keep-session, --keep-work-dir, --min-completed-items (default 4).
"""

from __future__ import annotations

import argparse
import json
import os
import re
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
    return str(Path(__file__).resolve().parent / "config.demo.yaml")


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


def parse_checklist(md: str) -> tuple[int, int]:
    done = 0
    pending = 0
    for line in md.splitlines():
        line = line.strip()
        if re.match(r"^[-*]\s+\[x\]\s+", line, re.I):
            done += 1
        elif re.match(r"^[-*]\s+\[ \]\s+", line):
            pending += 1
    return done, pending


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


WRITE_CLASS_TOOLS = frozenset(
    {
        "write_file",
        "apply_diff",
        "mkdir",
        "touch",
        "mv",
        "run_command",
    }
)


def sum_utf8_regular_files(work: Path) -> int:
    if not work.is_dir():
        return 0
    total = 0
    for p in work.rglob("*"):
        if p.is_file():
            try:
                total += len(p.read_bytes())
            except OSError:
                pass
    return total


def count_words_in_files(work: Path) -> int:
    if not work.is_dir():
        return 0
    n = 0
    for p in work.rglob("*"):
        if not p.is_file():
            continue
        try:
            text = p.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError):
            continue
        n += len(re.findall(r"[A-Za-zА-Яа-я]{2,}", text))
    return n


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument(
        "--keep-session",
        action="store_true",
        help="Keep existing SESSION_ROOT/SESSION_ID before run",
    )
    ap.add_argument(
        "--keep-work-dir",
        action="store_true",
        help="Never delete WORK_DIR after run",
    )
    ap.add_argument(
        "--min-completed-items",
        type=int,
        default=4,
        metavar="N",
        help="Minimum checklist [x] count (default 4)",
    )
    ap.add_argument(
        "--min-artifact-bytes",
        type=int,
        default=110,
        metavar="BYTES",
        help="Minimum cumulative file bytes under cwd (default 110)",
    )
    ap.add_argument(
        "--min-content-words",
        type=int,
        default=26,
        metavar="WORDS",
        help="Minimum lexical words counted across readable utf8 files under cwd",
    )
    ap.add_argument(
        "--work-dir",
        default="",
        help="Session cwd (default: temp directory under system temp)",
    )
    args = ap.parse_args()

    binary = os.environ.get("CODDY_BIN", default_coddy_bin())
    cfg = os.environ.get("CODDY_CONFIG", default_config())
    session_root = os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp-e2e")
    session_id = os.environ.get("SESSION_ID", "example-acp-agent-todo-e2e")

    if args.work_dir:
        work = os.path.abspath(args.work_dir)
        os.makedirs(work, exist_ok=True)
        cleanup_work = False
    else:
        work = tempfile.mkdtemp(prefix="coddy-acp-e2e-")
        cleanup_work = not args.keep_work_dir

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
            work,
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

    active_path = Path(session_root) / session_id / "todos" / "active.md"

    # High-level briefing only: specifics of tools and numbering are inferred from prompts and tool definitions.
    prompt = f"""All work MUST stay inside this directory tree: {work}

Treat the directory as EMPTY except whatever you create. Do not crawl the host repository upstream of this cwd, no mega find/globs, keep total tool chatter modest so you remain inside agent max_turn turn budget.

Fully autonomously (no clarification questions):

1. Invent a miniature project sized for a single agent episode. The written checklist must contain EXACTLY {args.min_completed_items} items (not fewer, not more). Each checklist line must be plain short English only (no backticks, no embedded status words like in_progress).
2. Capture that checklist in Coddy session todos via the builtin todo tools (whatever names arrive in-context).
3. Execute the plan sequentially: synthesize BOTH narrative-style Markdown notes AND structured JSON authored by YOU, saved with filesystem tools (`write_file`, `mkdir`, optionally `touch`, etc.). Aim for substantive content describing your invented artifact (goals of the miniature project, timestamps, bogus metrics, whimsical names).
4. Incorporate at least one DIFFERENT class of tooling step from pure todo bookkeeping (listing a directory YOU created inside cwd, benign `run_command`, `read_file`, or `mv` between staged temp files—all paths stay rooted in this cwd).
5. After EACH substantive accomplishment, reconcile the persisted checklist (`update_todo_item`, `done_todo_item`, or equivalent) until every checklist row is `[x]`.
6. Close with one terse recap quoting relative paths."""

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

        backlog: list[dict[str, Any]] = []
        for attempt in range(3):
            turn_text = prompt if attempt == 0 else (
                "Finish the remaining todo checklist items from this session without adding new items. "
                "Execute the remaining steps and mark every checklist row as [x]. "
                "If anything is pending, complete it now."
            )
            rp, b = rpc_call(
                proc,
                "session/prompt",
                {"sessionId": sid, "prompt": [{"type": "text", "text": turn_text}]},
                nid,
            )
            backlog.extend(b)
            if "error" in rp:
                print("session/prompt error:", jd(rp), file=sys.stderr)
                sys.exit(1)
            if active_path.is_file():
                body_now = active_path.read_text(encoding="utf-8")
                done_now, pending_now = parse_checklist(body_now)
                if done_now >= args.min_completed_items and pending_now == 0:
                    break

        seen_tools = set(collect_tool_call_titles(backlog))
        print("stopReason=", rp.get("result"), file=sys.stderr)
        print("session_update_count=", sum(1 for x in backlog if x.get("method") == "session/update"), file=sys.stderr)
        print("distinct_tool_calls=", sorted(seen_tools), file=sys.stderr)

        if not active_path.is_file():
            print("FAIL: missing todos/active.md", file=sys.stderr)
            exit_code = 11
        else:
            body = active_path.read_text(encoding="utf-8")
            print("--- todos/active.md ---", file=sys.stderr)
            print(body, file=sys.stderr)
            done, pending = parse_checklist(body)
            print(f"checklist: completed={done} pending={pending}", file=sys.stderr)

            if done < args.min_completed_items:
                print(
                    f"FAIL: need >= {args.min_completed_items} completed items, got {done}",
                    file=sys.stderr,
                )
                exit_code = 12
            if pending != 0:
                print(f"FAIL: expected zero pending checklist rows, got {pending}", file=sys.stderr)
                exit_code = 13

        artifact_bytes = sum_utf8_regular_files(Path(work))
        word_count = count_words_in_files(Path(work))

        print("--- cwd artifacts ---", file=sys.stderr)
        print(f"bytes_total={artifact_bytes} wordish_tokens={word_count}", file=sys.stderr)
        wp = Path(work)
        if wp.exists():
            for p in sorted(wp.rglob("*")):
                rel = p.relative_to(wp)
                if p.is_file():
                    print(" ", rel, "(", p.stat().st_size, "bytes)", file=sys.stderr)
                elif p.is_dir():
                    print(" ", rel, "/", file=sys.stderr)

        if "create_todo_list" not in seen_tools:
            print("FAIL: never observed create_todo_list tool_call", file=sys.stderr)
            exit_code = max(exit_code, 21)

        if not (seen_tools & {"update_todo_item", "done_todo_item"}):
            print(
                "FAIL: expected update_todo_item or done_todo_item to reconcile checklist",
                file=sys.stderr,
            )
            exit_code = max(exit_code, 22)

        if not (seen_tools & WRITE_CLASS_TOOLS):
            print(
                f"FAIL: expected at least one write-class tool call from {sorted(WRITE_CLASS_TOOLS)}",
                file=sys.stderr,
            )
            exit_code = max(exit_code, 23)

        if artifact_bytes < args.min_artifact_bytes:
            print(
                f"FAIL: combined file bytes {artifact_bytes} < {args.min_artifact_bytes}",
                file=sys.stderr,
            )
            exit_code = max(exit_code, 31)

        if word_count < args.min_content_words:
            print(
                f"FAIL: readable word-like count {word_count} < {args.min_content_words}",
                file=sys.stderr,
            )
            exit_code = max(exit_code, 32)

    finally:
        proc.stdin.close()
        proc.wait(timeout=600)
        if cleanup_work and Path(work).exists():
            shutil.rmtree(work, ignore_errors=True)

    sys.exit(exit_code)


if __name__ == "__main__":
    main()
