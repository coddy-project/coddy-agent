#!/usr/bin/env python3
"""HTTP e2e: slash skills catalog and ephemeral /name skill body on agent turns.

Needs a running ``coddy http`` built with TAGS=http, using ``examples/config.demo.yaml`` with ``skills.install_dir``
and ``skills.dirs`` set to ``${CODDY_HOME}/skills_fixture``. Populate that tree like ``test_httpserver_skills_slash.sh``
(copy the fixture skill into the home).

Calls a real configured LLM via ``POST /v1/responses`` (``model``: ``agent`` or ``plan``).

Environment:

- ``BASE_URL`` - OpenAI-compatible base ending in ``/v1`` (default ``http://127.0.0.1:19876/v1``).
- ``MODEL`` - Coddy profile (default ``agent``).

Checks:

1. ``GET /coddy/slash-commands`` lists the fixture command ``coddy_slash_demo``.
2. Agent reply includes ``DEMO_SKILL_TOKEN:z7k9-demo-slash`` after a prompt that starts with ``/coddy_slash_demo``.
"""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.request
from typing import Any, Tuple


FIXTURE_SLASH_NAME = "coddy_slash_demo"
VERIFICATION_TOKEN = "DEMO_SKILL_TOKEN:z7k9-demo-slash"


def http_json(method: str, url: str, body: dict[str, Any] | None, headers: dict[str, str]) -> Tuple[int, dict[str, Any], dict[str, str]]:
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


def main() -> int:
    v1 = openai_v1_base()
    origin = coddy_http_origin(v1)
    model = os.environ.get("MODEL", "agent").strip()

    code, page, _ = http_json(
        "GET",
        f"{origin}/coddy/slash-commands?page=1&page_size=50",
        None,
        {},
    )
    if code != 200:
        print("bad /coddy/slash-commands", code, page, file=sys.stderr)
        return 1
    names = [str((it or {}).get("name") or "") for it in (page.get("items") or [])]
    if FIXTURE_SLASH_NAME not in names:
        print("slash catalog missing", FIXTURE_SLASH_NAME, "got", names, file=sys.stderr)
        return 1

    prompt = (
        f"/{FIXTURE_SLASH_NAME}\n\n"
        "Follow the instructions from the user-invoked slash skill for this turn only. "
        "Reply in one short sentence and include the required verification token verbatim."
    )
    code, resp, headers = http_json(
        "POST",
        f"{v1}/responses",
        {"model": model, "input": prompt, "stream": False},
        {},
    )
    if code != 200:
        print("bad /v1/responses", code, resp, file=sys.stderr)
        return 1
    out_blocks = resp.get("output") or []
    text = ""
    if out_blocks and isinstance(out_blocks[0], dict):
        text = str(out_blocks[0].get("text") or "")
    if VERIFICATION_TOKEN not in text:
        print("model output missing skill token", text[:800], file=sys.stderr)
        return 1

    sid = (headers.get("x-coddy-session-id") or "").strip()
    code, ctrl, _ = http_json(
        "POST",
        f"{v1}/responses",
        {"model": model, "input": "Say only: hello-demo-control", "stream": False},
        {"X-Coddy-Session-ID": sid} if sid else {},
    )
    if code != 200:
        print("bad control /v1/responses", code, ctrl, file=sys.stderr)
        return 1
    cb = (ctrl.get("output") or [{}])[0] if ctrl.get("output") else {}
    ctrl_text = str((cb or {}).get("text") or "")
    if VERIFICATION_TOKEN in ctrl_text:
        print(
            "control turn unexpectedly contained skill token",
            ctrl_text[:400],
            file=sys.stderr,
        )
        return 1

    print("ok skills slash demo")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
