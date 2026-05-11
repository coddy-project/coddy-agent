#!/usr/bin/env python3
"""HTTP e2e: verify tool calls visible in SSE stream and persisted on disk.

Uses /v1/responses with stream=true and asserts that events include:
- event tool_call
- event tool_call_update

Then checks CODDY_HOME/sessions/<sessionId>/tool_calls/<toolCallId> contains:
- args.json
- result.md
- meta.json

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


def main() -> int:
    base = os.environ.get("BASE_URL", "http://127.0.0.1:19876/v1").rstrip("/")
    yaml_model = os.environ.get("MODEL", "rpa/gpt-oss:120b").strip()
    profile = os.environ.get("CODDY_CHAT_PROFILE", "agent").strip()
    work = Path(os.environ.get("WORK_DIR", "")).resolve()
    home = Path(os.environ.get("CODDY_HOME", "")).resolve()
    if not work.is_dir() or not home.is_dir():
        print("WORK_DIR and CODDY_HOME must point to existing directories", file=sys.stderr)
        return 2

    marker = work / "HTTP_TOOLCALLS_E2E.marker"
    if marker.exists():
        marker.unlink()

    prompt = f"""Working directory MUST be: {work}
Do not browse outside this cwd.

STEP 1 - Run exactly this shell via run_command tool:
bash -lc 'echo HTTP_TOOLCALLS_E2E_OK > {marker.name}'

STEP 2 - Reply single line OK when done.
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

    tool_call_ids: list[str] = []
    saw_tool_call = False
    saw_tool_call_update = False
    saw_completed_update = False

    try:
        with urllib.request.urlopen(req, timeout=300) as resp:
            sid = (resp.headers.get("X-Coddy-Session-Id") or resp.headers.get("X-Coddy-Session-ID") or "").strip()
            if not sid:
                print("missing X-Coddy-Session-ID header", file=sys.stderr)
                return 1

            deadline = time.time() + 240
            for ev, data in sse_events(resp):
                if time.time() > deadline:
                    print("timed out waiting for streamed response", file=sys.stderr)
                    return 1
                if ev == "tool_call":
                    saw_tool_call = True
                    try:
                        obj = json.loads(data)
                    except json.JSONDecodeError:
                        continue
                    tcid = (obj.get("toolCallId") or "").strip()
                    if tcid:
                        tool_call_ids.append(tcid)
                elif ev == "tool_call_update":
                    saw_tool_call_update = True
                    try:
                        obj = json.loads(data)
                    except json.JSONDecodeError:
                        continue
                    tcid = (obj.get("toolCallId") or "").strip()
                    if tcid:
                        tool_call_ids.append(tcid)
                    status = (obj.get("status") or "").strip().lower()
                    if status in ("completed", "done", "succeeded", "success"):
                        saw_completed_update = True
                elif ev == "response.completed":
                    break

            if not saw_tool_call:
                print("expected tool_call SSE event", file=sys.stderr)
                return 1
            if not saw_tool_call_update:
                print("expected tool_call_update SSE event", file=sys.stderr)
                return 1
            if not tool_call_ids:
                print("expected at least one toolCallId from SSE", file=sys.stderr)
                return 1

            tcid = tool_call_ids[0]
            tool_dir = home / "sessions" / sid / "tool_calls" / tcid
            for name in ("args.json", "result.md", "meta.json"):
                p = tool_dir / name
                if not p.is_file() or p.stat().st_size < 2:
                    print(f"missing persisted tool call file {p}", file=sys.stderr)
                    return 1

            if not marker.is_file():
                print("expected marker file from run_command tool", file=sys.stderr)
                return 1
            if not saw_completed_update:
                print("warning: did not observe a completed tool_call_update status", file=sys.stderr)

            print("ok http toolcalls persist e2e")
            return 0

    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        print(f"http error {e.code} {raw}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
