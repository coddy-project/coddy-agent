#!/usr/bin/env python3
"""HTTP e2e: websearch and webfetch hit the network and persist results.

Uses POST /v1/responses with stream=true, then checks under
CODDY_HOME/sessions/<sessionId>/tool_calls/*/meta.json for both tool names and
reads webfetch result.md for text from https://example.com/.

Environment: BASE_URL (ends with /v1), MODEL, WORK_DIR, CODDY_HOME.
"""

from __future__ import annotations

import json
import os
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any, Iterable


def _b(s: str) -> bytes:
    return s.encode("utf-8")


def sse_events(resp: Any) -> Iterable[tuple[str, str]]:
    event = ""
    data_lines: list[str] = []

    while True:
        raw = resp.readline()
        if not raw:
            break
        line = raw.decode("utf-8", errors="replace").rstrip("\r\n")
        if line == "":
            if data_lines:
                yield event or "message", "\n".join(data_lines)
            event = ""
            data_lines = []
            continue
        if line.startswith(":"):
            continue
        if line.startswith("event:"):
            event = line[len("event:") :].strip()
            continue
        if line.startswith("data:"):
            data_lines.append(line[len("data:") :].lstrip())
            continue


def _tool_names_from_disk(session_dir: Path) -> dict[str, str]:
    """Map toolCallId folder name -> tool name from meta.json."""
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
    base = os.environ.get("BASE_URL", "http://127.0.0.1:19876/v1").rstrip("/")
    yaml_model = os.environ.get("MODEL", "rpa/gpt-oss:120b").strip()
    profile = os.environ.get("CODDY_CHAT_PROFILE", "agent").strip()
    work = Path(os.environ.get("WORK_DIR", "")).resolve()
    home = Path(os.environ.get("CODDY_HOME", "")).resolve()
    if not work.is_dir() or not home.is_dir():
        print("WORK_DIR and CODDY_HOME must point to existing directories", file=sys.stderr)
        return 2

    prompt = f"""Working directory MUST be: {work}
Do not use filesystem write tools. Use only the built-in web tools.

You must run these two tools in order (wait for each result before the next):
1) Call websearch with query exactly: IANA example domain reserved
2) Call webfetch with url exactly: https://example.com/

When both complete, reply one line starting with WEB_E2E_OK and add a short quote from the page (under 15 words).
"""

    body = {
        "model": profile,
        "stream": True,
        "metadata": {"model": yaml_model},
        "input": prompt,
    }

    req = urllib.request.Request(f"{base}/responses", data=_b(json.dumps(body)), method="POST")
    req.add_header("Accept", "text/event-stream")
    req.add_header("Content-Type", "application/json")

    saw_tool_call = False
    saw_tool_call_update = False

    try:
        with urllib.request.urlopen(req, timeout=360) as resp:
            sid = (resp.headers.get("X-Coddy-Session-Id") or resp.headers.get("X-Coddy-Session-ID") or "").strip()
            if not sid:
                print("missing X-Coddy-Session-ID header", file=sys.stderr)
                return 1

            deadline = time.time() + 320
            for ev, data in sse_events(resp):
                if time.time() > deadline:
                    print("timed out waiting for streamed response", file=sys.stderr)
                    return 1
                if ev == "tool_call":
                    saw_tool_call = True
                elif ev == "tool_call_update":
                    saw_tool_call_update = True
                elif ev == "response.completed":
                    break

            if not saw_tool_call:
                print("expected tool_call SSE event", file=sys.stderr)
                return 1
            if not saw_tool_call_update:
                print("expected tool_call_update SSE event", file=sys.stderr)
                return 1

            session_dir = home / "sessions" / sid
            by_id = _tool_names_from_disk(session_dir)
            names = set(by_id.values())
            if "websearch" not in names:
                print(f"expected websearch in persisted tool metas, got {sorted(names)}", file=sys.stderr)
                return 1
            if "webfetch" not in names:
                print(f"expected webfetch in persisted tool metas, got {sorted(names)}", file=sys.stderr)
                return 1

            extract_body = ""
            for folder, name in by_id.items():
                if name != "webfetch":
                    continue
                rp = session_dir / "tool_calls" / folder / "result.md"
                if rp.is_file():
                    extract_body = rp.read_text(encoding="utf-8", errors="replace")
                    break

            low = extract_body.lower()
            if "example" not in low or "domain" not in low:
                print("webfetch result.md missing expected example.com phrases", file=sys.stderr)
                return 1

            print("ok http web tools e2e")
            return 0

    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        print(f"http error {e.code} {raw}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
