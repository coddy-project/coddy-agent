#!/usr/bin/env python3
"""ACP e2e: line-range @mentions (HTTP twin: http_e2e_mentions.py, console twin: cli/cli_e2e_mentions.py).

1. A ``session/prompt`` whose text carries ``@file:3-4`` is hydrated server-side:
   the persisted user turn in ``messages.json`` holds a ``<coddy_attachment>``
   labelled ``lines="3-4"`` with only those lines, and the model quotes them.
2. A ``resource`` block whose URI carries the ``#L5-5`` fragment and no text is
   filled from disk the same way (the shape an editor client sends).
3. A resource asking for lines past the end of the file is refused as a
   ``session/prompt`` error, so the range never widens into the whole file.

Environment: CODDY_BIN, CODDY_CONFIG, SESSION_ROOT, SESSION_ID.
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

FILE_NAME = "mention_lines.txt"
LINE_TOKENS = ["MENTION_L1:alpha", "MENTION_L2:bravo", "MENTION_L3:charlie", "MENTION_L4:delta", "MENTION_L5:echo", "MENTION_L6:foxtrot"]


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
    proc: subprocess.Popen[str],
    method: str,
    params: dict[str, Any],
    next_id: list[int],
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    rid = next_id[0]
    next_id[0] += 1
    assert proc.stdin is not None and proc.stdout is not None
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
            proc.stdin.write(jd({"jsonrpc": "2.0", "id": msg.get("id"), "result": {"outcome": "allow"}}) + "\n")
            proc.stdin.flush()
            backlog.append({"_kind": "request_permission_sent", **msg})
            continue
        if m == "session/update":
            backlog.append(msg)
            continue
        if ("result" in msg or "error" in msg) and same_id(msg.get("id"), rid):
            return msg, backlog
        backlog.append({"_kind": "unexpected_response", **msg})


def assistant_text(backlog: list[dict[str, Any]]) -> str:
    parts: list[str] = []
    for m in backlog:
        if m.get("method") != "session/update":
            continue
        u = m.get("params", {}).get("update") or {}
        if u.get("sessionUpdate") == "agent_message_chunk":
            c = u.get("content") or {}
            if isinstance(c, dict) and isinstance(c.get("text"), str):
                parts.append(c["text"])
    return "".join(parts)


def persisted_user_turn(sdir: Path, lines: str) -> str:
    path = sdir / "messages.json"
    if not path.is_file():
        return ""
    data = json.loads(path.read_text(encoding="utf-8"))
    rows = data.get("messages", []) if isinstance(data, dict) else data
    for m in rows:
        if m.get("role") == "user" and f'lines="{lines}"' in str(m.get("content", "")):
            return str(m.get("content", ""))
    return ""


def check_attachment(content: str, lines: str, want: list[str], unwanted: list[str]) -> str | None:
    tag = re.search(r"<coddy_attachment [^>]*>", content)
    if not tag or f'path="{FILE_NAME}"' not in tag.group(0) or f'lines="{lines}"' not in tag.group(0):
        return f"attachment tag for {FILE_NAME} lines={lines} missing in: {content[:600]}"
    body = content[tag.end():]
    for tok in want:
        if tok not in body:
            return f"line {tok} missing from the hydrated body: {body[:600]}"
    for tok in unwanted:
        if tok in body:
            return f"line {tok} leaked into a {lines} attachment: {body[:600]}"
    return None


def main() -> int:
    binary = os.environ.get("CODDY_BIN", default_coddy_bin())
    cfg = os.environ.get("CODDY_CONFIG", default_config())
    session_root = Path(os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp-mentions")).resolve()
    session_id = os.environ.get("SESSION_ID", "example-acp-mentions")

    work = Path(tempfile.mkdtemp(prefix="coddy-acp-mentions-")).resolve()
    (work / FILE_NAME).write_text("\n".join(LINE_TOKENS) + "\n", encoding="utf-8")
    session_root.mkdir(parents=True, exist_ok=True)
    sdir = session_root / session_id
    if sdir.is_dir():
        shutil.rmtree(sdir)

    proc = subprocess.Popen(
        ["stdbuf", "-oL", "-eL", binary, "acp", "--config", cfg, "--sessions-dir", str(session_root),
         "--session-id", session_id, "--cwd", str(work), "--log-level", "warn"],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=sys.stderr, text=True, bufsize=1,
    )
    nid = [1]
    try:
        r0, _ = rpc_call(proc, "initialize", {
            "protocolVersion": 1,
            "clientCapabilities": {"terminal": True},
            "clientInfo": {"name": "acp-mentions-e2e", "title": "mentions", "version": "1.0.0"},
        }, nid)
        if "error" in r0:
            print("initialize error", jd(r0), file=sys.stderr)
            return 1
        r1, _ = rpc_call(proc, "session/new", {"cwd": str(work)}, nid)
        if "error" in r1:
            print("session/new error", jd(r1), file=sys.stderr)
            return 1
        sid = (r1.get("result") or {}).get("sessionId") or ""
        if not sid:
            print("missing sessionId", jd(r1), file=sys.stderr)
            return 1

        # 1. A ranged mention typed into the prompt text.
        r2, backlog = rpc_call(proc, "session/prompt", {"sessionId": sid, "prompt": [{
            "type": "text",
            "text": ("The attachment holds two lines of a file. Reply with exactly those two lines "
                     f"verbatim and nothing else. @{FILE_NAME}:3-4"),
        }]}, nid)
        if "error" in r2:
            print("session/prompt error", jd(r2), file=sys.stderr)
            return 1
        reply = assistant_text(backlog)
        if LINE_TOKENS[2] not in reply or LINE_TOKENS[3] not in reply:
            print("reply does not quote lines 3-4", reply[:800], file=sys.stderr)
            return 1
        content = persisted_user_turn(sdir, "3-4")
        if not content:
            print('messages.json lacks a user turn with lines="3-4"', file=sys.stderr)
            return 1
        err = check_attachment(content, "3-4", LINE_TOKENS[2:4], [LINE_TOKENS[0], LINE_TOKENS[4]])
        if err:
            print(err, file=sys.stderr)
            return 1

        # 2. An editor-style resource block carrying the range as a URI fragment.
        r3, backlog = rpc_call(proc, "session/prompt", {"sessionId": sid, "prompt": [
            {"type": "text", "text": "Now reply with just the one attached line, verbatim."},
            {"type": "resource", "resource": {"uri": f"{FILE_NAME}#L5-5", "mimeType": "text/plain"}},
        ]}, nid)
        if "error" in r3:
            print("session/prompt (resource) error", jd(r3), file=sys.stderr)
            return 1
        if LINE_TOKENS[4] not in assistant_text(backlog):
            print("reply does not quote line 5", assistant_text(backlog)[:800], file=sys.stderr)
            return 1
        content = persisted_user_turn(sdir, "5-5")
        if not content:
            print('messages.json lacks a user turn with lines="5-5"', file=sys.stderr)
            return 1
        err = check_attachment(content, "5-5", [LINE_TOKENS[4]], [LINE_TOKENS[3], LINE_TOKENS[5]])
        if err:
            print(err, file=sys.stderr)
            return 1

        # 3. Lines the file does not have are an error, not a whole-file attachment.
        r4, _ = rpc_call(proc, "session/prompt", {"sessionId": sid, "prompt": [
            {"type": "text", "text": "ignored"},
            {"type": "resource", "resource": {"uri": f"{FILE_NAME}#L40-41", "mimeType": "text/plain"}},
        ]}, nid)
        msg = str((r4.get("error") or {}).get("message", ""))
        if "error" not in r4 or "line range" not in msg:
            print("range past the end was not refused", jd(r4), file=sys.stderr)
            return 1

        print("ok acp e2e mentions", flush=True)
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
