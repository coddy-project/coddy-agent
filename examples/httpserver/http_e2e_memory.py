#!/usr/bin/env python3

from __future__ import annotations

import json
import os
import secrets
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any, Tuple


def http_json(method: str, url: str, body: dict[str, Any] | None, headers: dict[str, str]) -> Tuple[int, dict[str, Any], dict[str, str]]:
    data = None if body is None else json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Accept", "application/json")
    if data is not None:
        req.add_header("Content-Type", "application/json")
    for k, v in headers.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=300) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            out = json.loads(raw) if raw.strip() else {}
            resp_headers = {k: v for k, v in resp.headers.items()}
            return resp.status, out, resp_headers
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"http {method} {url} failed: {e.code} {raw}") from e


def assistant_text(chat_completion: dict[str, Any]) -> str:
    return (((chat_completion.get("choices") or [{}])[0].get("message") or {}).get("content") or "").strip()


def main() -> int:
    base = os.environ.get("BASE_URL", "http://127.0.0.1:19876/v1").rstrip("/")
    yaml_model = os.environ.get("MODEL", "rpa/qwen3.6-35b-a3b").strip()
    profile = os.environ.get("CODDY_CHAT_PROFILE", "agent").strip()
    work = Path(os.environ.get("WORK_DIR", "")).resolve()
    home = Path(os.environ.get("CODDY_HOME", "")).resolve()
    if not work.is_dir() or not home.is_dir():
        print("WORK_DIR and CODDY_HOME must point to existing directories", file=sys.stderr)
        return 2

    global_mem = home / "memory"
    project_mem = work / "memory"
    global_mem.mkdir(parents=True, exist_ok=True)
    project_mem.mkdir(parents=True, exist_ok=True)

    fruit = "HTTP_MEM_FRUIT_" + secrets.token_hex(4).upper()

    seed_path = global_mem / "http-mem-seed.md"
    seed_path.write_text("# seed\nThis note exists only to ensure global memory is enabled for HTTP tests.\n", encoding="utf-8")

    code, cc1, headers = http_json(
        "POST",
        f"{base}/chat/completions",
        {
            "model": profile,
            "stream": False,
            "metadata": {"model": yaml_model},
            "messages": [{"role": "user", "content": "Say 'ready' only."}],
        },
        {},
    )
    if code != 200:
        print("chat 1 failed", file=sys.stderr)
        return 1
    sid = (headers.get("X-Coddy-Session-Id") or headers.get("X-Coddy-Session-ID") or "").strip()
    if not sid:
        print("missing X-Coddy-Session-ID", file=sys.stderr)
        return 1

    code, cc2, _ = http_json(
        "POST",
        f"{base}/chat/completions",
        {
            "model": profile,
            "stream": False,
            "metadata": {"model": yaml_model},
            "messages": [
                {
                    "role": "user",
                    "content": f"Remember this exact string for later and confirm by replying with it exactly: {fruit}",
                }
            ],
        },
        {"X-Coddy-Session-ID": sid},
    )
    if code != 200 or not assistant_text(cc2):
        print("chat 2 failed", file=sys.stderr)
        return 1

    deadline = time.time() + 120
    found = False
    while time.time() < deadline and not found:
        for root in (global_mem, project_mem):
            for p in root.rglob("*"):
                if not p.is_file():
                    continue
                if p.suffix.lower() not in (".md", ".txt"):
                    continue
                try:
                    if fruit in p.read_text(encoding="utf-8", errors="replace"):
                        found = True
                        break
                except OSError:
                    pass
            if found:
                break
        if not found:
            time.sleep(0.2)
    if not found:
        print("expected fruit to be persisted in memory markdown", file=sys.stderr)
        return 1

    # The run is a system agent task of the session, named memory, with the
    # child session that holds its transcript.
    coddy_base = base[: -len("/v1")] if base.endswith("/v1") else base
    code, tasks, _ = http_json("GET", f"{coddy_base}/coddy/sessions/{sid}/background-tasks", None, {})
    if code != 200:
        print("background-tasks failed", file=sys.stderr)
        return 1
    runs = [
        row
        for row in tasks.get("data") or []
        if row.get("kind") == "agent" and (row.get("agent") or {}).get("name") == "memory" and (row.get("agent") or {}).get("system") is True
    ]
    if not runs:
        print(f"expected a memory run among the background tasks, got {tasks}", file=sys.stderr)
        return 1
    child = (runs[-1].get("agent") or {}).get("session_id") or ""
    if not child:
        print(f"the memory task names no child session: {runs[-1]}", file=sys.stderr)
        return 1
    code, msgs, _ = http_json("GET", f"{coddy_base}/coddy/sessions/{child}/messages", None, {})
    if code != 200 or msgs.get("readOnly") is not True:
        print(f"expected the memory child transcript to be readable and read-only, got {code} {msgs}", file=sys.stderr)
        return 1
    print(f"memory runs: {len(runs)}, last child transcript: {child}", file=sys.stderr)

    print("ok http memory e2e")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
