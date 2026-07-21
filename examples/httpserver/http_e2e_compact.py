#!/usr/bin/env python3
"""HTTP e2e: context compaction — /compact over /v1/responses and the REST endpoint.

ACP twin: ``examples/acp/acp_e2e_compact.py`` (which also covers the auto
threshold with a tiny-window config; here the shared server keeps the demo
config, so this harness exercises the manual surfaces).

Needs a running ``coddy http`` (see ``examples/httpserver/test_httpserver.sh``).
Calls a real configured LLM via ``POST /v1/responses`` (``model``: ``agent``).

Checks:

1. Three short exchanges seed a session; ``/compact`` sent as a normal prompt
   returns a confirmation text and inserts a ``compaction_summary`` row into
   ``GET /coddy/sessions/{id}/messages`` while keeping every original row and
   not persisting the command text.
2. A second session compacts via ``POST /coddy/sessions/{id}/compact``: 200
   with ``compacted: true``, non-empty ``summary``, positive counts.
3. ``POST .../compact`` on a too-short session answers 200 with
   ``compacted: false`` and reason ``nothing_to_compact``.
4. A follow-up prompt after compaction still produces an assistant reply.

Environment:

- ``BASE_URL`` - OpenAI-compatible base ending in ``/v1`` (default ``http://127.0.0.1:19876/v1``).
"""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.request
from typing import Any, Tuple


SEED_PROMPTS = [
    "Remember the codeword AZURE-7. Reply with one short sentence acknowledging it.",
    "Name any one planet of the Solar System. One short sentence.",
    "State any year in the 20th century. One short sentence.",
]


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
        with urllib.request.urlopen(req, timeout=600) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            out = json.loads(raw) if raw.strip() else {}
            return resp.status, out, {k.lower(): v for k, v in resp.headers.items()}
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


def agent_turn(v1: str, session_id: str | None, text: str) -> tuple[str, str]:
    """POST /v1/responses (non-stream). Returns (session_id, output_text)."""
    headers = {}
    if session_id:
        headers["X-Coddy-Session-ID"] = session_id
    code, body, rh = http_json(
        "POST",
        f"{v1}/responses",
        {"model": "agent", "input": text, "stream": False},
        headers,
    )
    if code != 200:
        raise RuntimeError(f"POST /v1/responses {code}: {body}")
    sid = session_id or rh.get("x-coddy-session-id") or str(body.get("id", ""))
    out = "".join(o.get("text", "") for o in body.get("output", []))
    return sid, out


def get_messages(origin: str, sid: str) -> list[dict[str, Any]]:
    code, body, _ = http_json("GET", f"{origin}/coddy/sessions/{sid}/messages", None, {})
    if code != 200:
        raise RuntimeError(f"GET messages {code}: {body}")
    return body.get("messages", [])


def main() -> int:
    v1 = openai_v1_base()
    origin = coddy_http_origin(v1)
    rc = 0

    # --- 1. /compact as a prompt ---------------------------------------------
    sid = None
    for p in SEED_PROMPTS:
        sid, _ = agent_turn(v1, sid, p)
    print("prompt-surface session:", sid, file=sys.stderr)

    _, out = agent_turn(v1, sid, "/compact")
    print("compact reply:", out, file=sys.stderr)
    if "compact" not in out.lower():
        print(f"FAIL: /compact reply has no confirmation: {out!r}", file=sys.stderr)
        rc = 11

    msgs = get_messages(origin, sid)
    if not any(m.get("compaction_summary") for m in msgs):
        print("FAIL: no compaction_summary row in messages", file=sys.stderr)
        rc = rc or 12
    joined = "\n".join(str(m.get("content", "")) for m in msgs)
    for p in SEED_PROMPTS:
        if p not in joined:
            print(f"FAIL: seeded prompt lost: {p!r}", file=sys.stderr)
            rc = rc or 13
    if any(str(m.get("content", "")).strip() == "/compact" for m in msgs):
        print("FAIL: /compact command persisted in transcript", file=sys.stderr)
        rc = rc or 14

    _, follow = agent_turn(v1, sid, "Reply with exactly: STILL-ALIVE")
    if not follow.strip():
        print("FAIL: follow-up after compaction returned empty output", file=sys.stderr)
        rc = rc or 15

    # --- 2. REST endpoint ----------------------------------------------------
    sid2 = None
    for p in SEED_PROMPTS:
        sid2, _ = agent_turn(v1, sid2, p)
    print("endpoint session:", sid2, file=sys.stderr)

    code, body, _ = http_json(
        "POST",
        f"{origin}/coddy/sessions/{sid2}/compact",
        {"instructions": "keep the codeword"},
        {},
    )
    if code != 200 or body.get("compacted") is not True:
        print(f"FAIL: compact endpoint {code}: {body}", file=sys.stderr)
        rc = rc or 21
    else:
        if not str(body.get("summary", "")).strip():
            print(f"FAIL: empty summary from endpoint: {body}", file=sys.stderr)
            rc = rc or 22
        if int(body.get("compacted_messages", 0)) <= 0 or int(body.get("kept_messages", 0)) <= 0:
            print(f"FAIL: endpoint counts missing: {body}", file=sys.stderr)
            rc = rc or 23
    msgs2 = get_messages(origin, sid2)
    if not any(m.get("compaction_summary") for m in msgs2):
        print("FAIL: endpoint compaction left no summary row", file=sys.stderr)
        rc = rc or 24

    # --- 3. nothing_to_compact on a short session ----------------------------
    sid3, _ = agent_turn(v1, None, "Reply with exactly: OK")
    code, body, _ = http_json("POST", f"{origin}/coddy/sessions/{sid3}/compact", {}, {})
    if code != 200 or body.get("compacted") is not False or body.get("reason") != "nothing_to_compact":
        print(f"FAIL: short session compact {code}: {body}", file=sys.stderr)
        rc = rc or 31

    if rc == 0:
        print("ok http compact e2e", file=sys.stderr)
    return rc


if __name__ == "__main__":
    sys.exit(main())
