#!/usr/bin/env python3
"""HTTP e2e: rules glob + mention + bundled slash catalog (ACP twin: acp_e2e_rules.py)."""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any, Tuple


GLOB_TOKEN = "RULE_GLOB_TOKEN:e2e-glob"
MENTION_TOKEN = "RULE_MENTION_TOKEN:e2e-mention"
BUNDLED_SLASH = "coddy-generate-rules"


def http_json(
    method: str, url: str, body: dict[str, Any] | None, headers: dict[str, str]
) -> Tuple[int, dict[str, Any], dict[str, str]]:
    data = None if body is None else json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Accept", "application/json")
    if data is not None:
        req.add_header("Content-Type", "application/json")
    for k, v in headers.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=180) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            out = json.loads(raw) if raw.strip() else {}
            resp_headers = {k.lower(): v for k, v in resp.headers.items()}
            return resp.status, out, resp_headers
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        try:
            out = json.loads(raw) if raw.strip() else {}
        except json.JSONDecodeError:
            out = {"_raw": raw}
        rh = {k.lower(): v for k, v in e.headers.items()} if hasattr(e.headers, "items") else {}
        return e.code, out, rh


def openai_v1_base() -> str:
    return os.environ.get("BASE_URL", "http://127.0.0.1:19876/v1").rstrip("/")


def coddy_http_origin(v1: str) -> str:
    if v1.endswith("/v1"):
        return v1[:-3].rstrip("/") or v1
    return v1.rstrip("/")


def extract_output_text(resp: dict[str, Any]) -> str:
    parts: list[str] = []
    for item in resp.get("output") or []:
        if not isinstance(item, dict):
            continue
        if item.get("type") != "message":
            continue
        for c in item.get("content") or []:
            if isinstance(c, dict) and c.get("type") == "output_text":
                t = c.get("text")
                if isinstance(t, str):
                    parts.append(t)
    return "".join(parts)


def main() -> int:
    examples_dir = Path(__file__).resolve().parent.parent
    v1 = openai_v1_base()
    origin = coddy_http_origin(v1)
    yaml_model = os.environ.get("MODEL", "rpa/gpt-oss:120b").strip()
    profile = os.environ.get("CODDY_CHAT_PROFILE", "agent").strip()
    work = os.environ.get("WORK_DIR", "").strip()
    if not work:
        print("WORK_DIR required", file=sys.stderr)
        return 2
    work_p = Path(work)
    go_rel = "main.go"
    if not (work_p / go_rel).is_file():
        go_file = work_p / go_rel
        go_file.write_text("package main\n\nfunc main() {}\n", encoding="utf-8")

    code, page, _ = http_json(
        "GET",
        f"{origin}/coddy/slash-commands?page=1&page_size=80",
        None,
        {},
    )
    if code != 200:
        print("bad /coddy/slash-commands", code, page, file=sys.stderr)
        return 1
    names = [str((it or {}).get("name") or "") for it in (page.get("items") or [])]
    if BUNDLED_SLASH not in names:
        print("slash catalog missing", BUNDLED_SLASH, "got", names, file=sys.stderr)
        return 1

    headers: dict[str, str] = {}

    glob_body = {
        "model": profile,
        "metadata": {"model": yaml_model},
        "input": (
            "You are given main.go in context via attachment. Reply in one short sentence and include "
            f"{GLOB_TOKEN} verbatim."
        ),
        "attachments": [{"path": go_rel}],
    }
    code, resp, hdr = http_json("POST", f"{v1}/responses", glob_body, headers)
    sid = (hdr.get("x-coddy-session-id") or hdr.get("X-Coddy-Session-ID") or "").strip()
    if code != 200:
        print("glob responses failed", code, resp, file=sys.stderr)
        return 1
    glob_text = extract_output_text(resp)
    if GLOB_TOKEN not in glob_text:
        print("glob missing token", glob_text[:800], file=sys.stderr)
        return 1

    mention_body = {
        "model": profile,
        "metadata": {"model": yaml_model},
        "input": (
            "@mention_demo Apply the mention-only rule. "
            f"Reply in one short sentence with {MENTION_TOKEN} verbatim."
        ),
    }
    mention_headers = {"X-Coddy-Session-ID": sid} if sid else {}
    code, resp2, _ = http_json("POST", f"{v1}/responses", mention_body, mention_headers)
    if code != 200:
        print("mention responses failed", code, resp2, file=sys.stderr)
        return 1
    mention_text = extract_output_text(resp2)
    if MENTION_TOKEN not in mention_text:
        print("mention missing token", mention_text[:800], file=sys.stderr)
        return 1

    print("ok http e2e rules", flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
