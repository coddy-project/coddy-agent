#!/usr/bin/env python3
"""ACP e2e: websearch and webfetch hit the network and persist results.

Runs one prompt that requires both tools, then verifies session/update includes
tool_call and tool_call_update, and disk has both tool names plus example.com
text in webfetch result.md.

Environment: CODDY_BIN, CODDY_CONFIG, SESSION_ROOT, SESSION_ID.
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
    proc.stdin.write(jd({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}) + "\n")
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
            proc.stdin.write(jd({"jsonrpc": "2.0", "id": msg.get("id"), "result": {"outcome": "allow"}}) + "\n")
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


def extract_tool_events(backlog: list[dict[str, Any]]) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    calls: list[dict[str, Any]] = []
    updates: list[dict[str, Any]] = []
    for m in backlog:
        if m.get("method") != "session/update":
            continue
        u = m.get("params", {}).get("update") or {}
        if u.get("sessionUpdate") == "tool_call":
            calls.append(u)
        elif u.get("sessionUpdate") == "tool_call_update":
            updates.append(u)
    return calls, updates


def _titles(calls: list[dict[str, Any]]) -> list[str]:
    out: list[str] = []
    for u in calls:
        t = u.get("title")
        if isinstance(t, str) and t.strip():
            out.append(t.strip())
    return out


def _tool_names_from_disk(session_dir: Path) -> dict[str, str]:
    root = session_dir / "tool_calls"
    out: dict[str, str] = {}
    if not root.is_dir():
        return out
    for sub in root.iterdir():
        if not sub.is_dir():
            continue
        mp = sub / "meta.json"
        if not mp.is_file():
            continue
        try:
            meta = json.loads(mp.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            continue
        name = (meta.get("name") or "").strip()
        if name:
            out[sub.name] = name
    return out


def main() -> int:
    binary = os.environ.get("CODDY_BIN", default_coddy_bin())
    cfg = os.environ.get("CODDY_CONFIG", default_config())
    session_root = Path(os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp-web")).resolve()
    session_id = os.environ.get("SESSION_ID", "example-acp-web")

    work = Path(tempfile.mkdtemp(prefix="coddy-acp-web-")).resolve()
    session_root.mkdir(parents=True, exist_ok=True)
    sdir = session_root / session_id
    if sdir.is_dir():
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
            str(session_root),
            "--session-id",
            session_id,
            "--cwd",
            str(work),
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
        r0, _ = rpc_call(
            proc,
            "initialize",
            {
                "protocolVersion": 1,
                "clientCapabilities": {"terminal": True},
                "clientInfo": {"name": "acp-web-e2e", "title": "web", "version": "1.0.0"},
            },
            nid,
        )
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

        prompt = f"""Working directory MUST be: {work}
Do not use filesystem write tools. Use only the built-in web tools.

You must run these two tools in order (wait for each result before the next):
1) Call websearch with query exactly: IANA example domain reserved
2) Call webfetch with url exactly: https://example.com/

When both complete, reply one line starting with WEB_E2E_OK and add a short quote from the page (under 15 words).
"""

        r2, backlog = rpc_call(
            proc,
            "session/prompt",
            {"sessionId": sid, "prompt": [{"type": "text", "text": prompt}]},
            nid,
        )
        if "error" in r2:
            print("session/prompt error", jd(r2), file=sys.stderr)
            return 1

        calls, updates = extract_tool_events(backlog)
        if not calls:
            print("expected tool_call updates in ACP backlog", file=sys.stderr)
            return 1
        if not updates:
            print("expected tool_call_update updates in ACP backlog", file=sys.stderr)
            return 1

        titles = _titles(calls)
        if "websearch" not in titles:
            print(f"expected websearch in tool_call titles, got {titles}", file=sys.stderr)
            return 1
        if "webfetch" not in titles:
            print(f"expected webfetch in tool_call titles, got {titles}", file=sys.stderr)
            return 1

        by_id = _tool_names_from_disk(sdir)
        names = set(by_id.values())
        if "websearch" not in names:
            print(f"expected websearch on disk, got {sorted(names)}", file=sys.stderr)
            return 1
        if "webfetch" not in names:
            print(f"expected webfetch on disk, got {sorted(names)}", file=sys.stderr)
            return 1

        extract_body = ""
        for folder, name in by_id.items():
            if name != "webfetch":
                continue
            rp = sdir / "tool_calls" / folder / "result.md"
            if rp.is_file():
                extract_body = rp.read_text(encoding="utf-8", errors="replace")
                break

        low = extract_body.lower()
        if "example" not in low or "domain" not in low:
            print("webfetch result.md missing expected example.com phrases", file=sys.stderr)
            return 1

        print("ok acp web tools e2e")
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
