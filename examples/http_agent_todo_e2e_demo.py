#!/usr/bin/env python3

from __future__ import annotations

import json
import os
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


def main() -> int:
    base = os.environ.get("BASE_URL", "http://127.0.0.1:19876/v1").rstrip("/")
    model = os.environ.get("MODEL", "rpa/gpt-oss:120b").strip()
    work = Path(os.environ.get("WORK_DIR", "")).resolve()
    if not work.is_dir():
        print("WORK_DIR must point to an existing directory", file=sys.stderr)
        return 2

    out_path = work / "http_todo_report.md"
    if out_path.exists():
        out_path.unlink()

    prompt = f"""All work MUST stay inside this directory tree: {work}

Create a todo checklist with EXACTLY 4 items using the builtin todo tools.
Then execute them and mark every item done.

Finally, write a markdown file at: {out_path}
The file must include the final checklist and one sentence recap.
"""

    code, cc, headers = http_json(
        "POST",
        f"{base}/chat/completions",
        {"model": model, "stream": False, "messages": [{"role": "user", "content": prompt}]},
        {},
    )
    if code != 200:
        print("bad chat completion code", code, file=sys.stderr)
        return 1
    _ = cc
    _sid = (headers.get("X-Coddy-Session-Id") or headers.get("X-Coddy-Session-ID") or "").strip()
    if not _sid:
        print("missing X-Coddy-Session-ID header", file=sys.stderr)
        return 1

    deadline = time.time() + 10
    while time.time() < deadline:
        if out_path.is_file() and out_path.stat().st_size > 20:
            break
        time.sleep(0.2)

    if not out_path.is_file():
        print("todo report was not created", file=sys.stderr)
        return 1
    text = out_path.read_text(encoding="utf-8", errors="replace")
    if text.count("[x]") < 4:
        print("expected at least 4 done checklist items in report", file=sys.stderr)
        print(text[:800], file=sys.stderr)
        return 1

    print("ok http todo e2e")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
