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
    model = os.environ.get("MODEL", "rpa/gpt-oss:120b").strip()
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
        {"model": model, "stream": False, "messages": [{"role": "user", "content": "Say 'ready' only."}]},
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
            "model": model,
            "stream": False,
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

    deadline = time.time() + 10
    found = False
    while time.time() < deadline and not found:
        for root in (global_mem, project_mem):
            for p in root.rglob("*.md"):
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

    print("ok http memory e2e")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
