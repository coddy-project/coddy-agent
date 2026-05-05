#!/usr/bin/env python3

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.request
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
        with urllib.request.urlopen(req, timeout=60) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            out = json.loads(raw) if raw.strip() else {}
            resp_headers = {k: v for k, v in resp.headers.items()}
            return resp.status, out, resp_headers
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        try:
            out = json.loads(raw) if raw.strip() else {}
        except json.JSONDecodeError:
            out = {"_raw": raw}
        return e.code, out, {}


def main() -> int:
    base = os.environ.get("BASE_URL", "http://127.0.0.1:19876/v1").rstrip("/")
    model = os.environ.get("MODEL", "rpa/gpt-oss:120b").strip()
    expect_live = os.environ.get("RUN_LIVE", "0").strip() == "1"

    code, models, _ = http_json("GET", f"{base}/models", None, {})
    if code != 200 or models.get("object") != "list":
        print("bad /models", models, file=sys.stderr)
        return 1

    code, cc, headers = http_json(
        "POST",
        f"{base}/chat/completions",
        {
            "model": model,
            "stream": False,
            "messages": [{"role": "user", "content": "Say 'ok' only."}],
        },
        {},
    )
    if code != 200:
        if not expect_live and code == 500 and "error" in cc:
            print("ok http smoke (llm skipped)")
            return 0
        print("bad /chat/completions code", code, cc, file=sys.stderr)
        return 1
    sid = (headers.get("X-Coddy-Session-Id") or headers.get("X-Coddy-Session-ID") or "").strip()
    content = (((cc.get("choices") or [{}])[0].get("message") or {}).get("content") or "").strip()
    if not content:
        print("empty chat completion content", cc, file=sys.stderr)
        return 1

    code, resp, _ = http_json(
        "POST",
        f"{base}/responses",
        {"model": model, "input": "Say 'pong' only."},
        {"X-Coddy-Session-ID": sid} if sid else {},
    )
    if code != 200 or resp.get("object") != "response":
        print("bad /responses", resp, file=sys.stderr)
        return 1
    rid = (resp.get("id") or "").strip()
    if not rid:
        print("missing responses id", resp, file=sys.stderr)
        return 1

    code, got, _ = http_json("GET", f"{base}/responses/{rid}", None, {})
    if code != 200 or got.get("id") != rid:
        print("bad /responses/{id}", got, file=sys.stderr)
        return 1

    print("ok http smoke")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
