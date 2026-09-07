#!/usr/bin/env python3
"""HTTP e2e: line-range @mentions (ACP twin: acp_e2e_mentions.py, console twin: cli/cli_e2e_mentions.py).

1. ``GET /coddy/workspace/file`` returns the display lines behind the composer's
   line-range picker: ``max_lines`` caps ``lines`` while ``total_lines`` counts
   the whole file.
2. ``POST /v1/responses`` with ``attachments[].source.startLine`` / ``endLine``
   (what the SPA sends for ``@file:3-4``) persists a user turn whose
   ``<coddy_attachment>`` carries ``lines="3-4"`` and only those lines; the
   model quotes them back.
3. The same mention typed into ``input`` alone (no ``attachments``) hydrates the
   same way through the server-side text grammar.
4. A range that starts past the end of the file is refused with ``400``.

Environment: BASE_URL (ends with /v1), MODEL, WORK_DIR.
"""

from __future__ import annotations

import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any, Tuple

FILE_NAME = "mention_lines.txt"
LINE_TOKENS = ["MENTION_L1:alpha", "MENTION_L2:bravo", "MENTION_L3:charlie", "MENTION_L4:delta", "MENTION_L5:echo", "MENTION_L6:foxtrot"]


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
        with urllib.request.urlopen(req, timeout=240) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            out = json.loads(raw) if raw.strip() else {}
            return resp.status, out, {k.lower(): v for k, v in resp.headers.items()}
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        try:
            out = json.loads(raw) if raw.strip() else {}
        except json.JSONDecodeError:
            out = {"_raw": raw}
        return e.code, out, {k.lower(): v for k, v in e.headers.items()}


def openai_v1_base() -> str:
    return os.environ.get("BASE_URL", "http://127.0.0.1:19876/v1").rstrip("/")


def coddy_http_origin(v1: str) -> str:
    return v1[:-3].rstrip("/") if v1.endswith("/v1") else v1.rstrip("/")


def extract_output_text(resp: dict[str, Any]) -> str:
    parts: list[str] = []
    for item in resp.get("output") or []:
        if not isinstance(item, dict):
            continue
        if item.get("type") == "text" and isinstance(item.get("text"), str):
            parts.append(item["text"])
        elif item.get("type") == "message":
            for c in item.get("content") or []:
                if isinstance(c, dict) and c.get("type") == "output_text" and isinstance(c.get("text"), str):
                    parts.append(c["text"])
    return "".join(parts)


def user_turn_with_lines(origin: str, sid: str, lines: str) -> str:
    """Returns the persisted user message that carries the ``lines`` label, or ``""``."""
    code, body, _ = http_json("GET", f"{origin}/coddy/sessions/{sid}/messages", None, {})
    if code != 200:
        raise RuntimeError(f"GET messages {code}: {body}")
    for m in body.get("messages") or []:
        if m.get("role") == "user" and f'lines="{lines}"' in str(m.get("content", "")):
            return str(m.get("content", ""))
    return ""


def check_attachment(content: str, lines: str, want_tokens: list[str], unwanted_tokens: list[str]) -> str | None:
    tag = re.search(r'<coddy_attachment [^>]*>', content)
    if not tag or f'path="{FILE_NAME}"' not in tag.group(0) or f'lines="{lines}"' not in tag.group(0):
        return f"attachment tag for {FILE_NAME} lines={lines} missing in: {content[:600]}"
    body = content[tag.end():]
    for tok in want_tokens:
        if tok not in body:
            return f"line {tok} missing from the hydrated body: {body[:600]}"
    for tok in unwanted_tokens:
        if tok in body:
            return f"line {tok} leaked into a {lines} attachment: {body[:600]}"
    return None


def main() -> int:
    v1 = openai_v1_base()
    origin = coddy_http_origin(v1)
    yaml_model = os.environ.get("MODEL", "rpa/gpt-oss:120b").strip()
    profile = os.environ.get("CODDY_CHAT_PROFILE", "agent").strip()
    work = os.environ.get("WORK_DIR", "").strip()
    if not work:
        print("WORK_DIR required", file=sys.stderr)
        return 2
    (Path(work) / FILE_NAME).write_text("\n".join(LINE_TOKENS) + "\n", encoding="utf-8")

    # 1. The picker's read: capped lines, whole-file count.
    q = urllib.parse.urlencode({"path_rel": FILE_NAME, "max_lines": 2})
    code, page, _ = http_json("GET", f"{origin}/coddy/workspace/file?{q}", None, {})
    if code != 200 or page.get("lines") != LINE_TOKENS[:2] or page.get("total_lines") != len(LINE_TOKENS) or page.get("truncated") is not True:
        print("workspace/file capped read mismatch", code, page, file=sys.stderr)
        return 1
    q = urllib.parse.urlencode({"path_rel": FILE_NAME})
    code, page, _ = http_json("GET", f"{origin}/coddy/workspace/file?{q}", None, {})
    if code != 200 or page.get("lines") != LINE_TOKENS or page.get("truncated") is not False:
        print("workspace/file full read mismatch", code, page, file=sys.stderr)
        return 1

    # 2. The SPA shape: attachments[].source.startLine/endLine alongside the echo in input.
    body = {
        "model": profile,
        "metadata": {"model": yaml_model},
        "input": (
            "The attachment holds two lines of a file. Reply with exactly those two lines "
            f"verbatim and nothing else. @{FILE_NAME}:3-4"
        ),
        "attachments": [{"path": FILE_NAME, "source": {"startLine": 3, "endLine": 4}}],
    }
    code, resp, hdr = http_json("POST", f"{v1}/responses", body, {})
    if code != 200:
        print("ranged attachment responses failed", code, resp, file=sys.stderr)
        return 1
    sid = (hdr.get("x-coddy-session-id") or "").strip()
    if not sid:
        print("missing X-Coddy-Session-ID", file=sys.stderr)
        return 1
    reply = extract_output_text(resp)
    if LINE_TOKENS[2] not in reply or LINE_TOKENS[3] not in reply:
        print("reply does not quote lines 3-4", reply[:800], file=sys.stderr)
        return 1
    content = user_turn_with_lines(origin, sid, "3-4")
    if not content:
        print('persisted user turn lacks lines="3-4"', file=sys.stderr)
        return 1
    err = check_attachment(content, "3-4", LINE_TOKENS[2:4], [LINE_TOKENS[0], LINE_TOKENS[4]])
    if err:
        print(err, file=sys.stderr)
        return 1

    # 3. The same mention typed alone: the text grammar hydrates it server-side.
    headers = {"X-Coddy-Session-ID": sid}
    body = {
        "model": profile,
        "metadata": {"model": yaml_model},
        "input": f"Now reply with just the one attached line, verbatim. @{FILE_NAME}:5-5",
    }
    code, resp, _ = http_json("POST", f"{v1}/responses", body, headers)
    if code != 200:
        print("typed mention responses failed", code, resp, file=sys.stderr)
        return 1
    if LINE_TOKENS[4] not in extract_output_text(resp):
        print("reply does not quote line 5", extract_output_text(resp)[:800], file=sys.stderr)
        return 1
    content = user_turn_with_lines(origin, sid, "5-5")
    if not content:
        print('persisted user turn lacks lines="5-5"', file=sys.stderr)
        return 1
    err = check_attachment(content, "5-5", [LINE_TOKENS[4]], [LINE_TOKENS[3], LINE_TOKENS[5]])
    if err:
        print(err, file=sys.stderr)
        return 1

    # 4. A range the file cannot honour is refused before the model is called.
    body = {
        "model": profile,
        "metadata": {"model": yaml_model},
        "input": f"ignored @{FILE_NAME}:40-41",
        "attachments": [{"path": FILE_NAME, "source": {"startLine": 40, "endLine": 41}}],
    }
    code, resp, _ = http_json("POST", f"{v1}/responses", body, headers)
    msg = str(((resp or {}).get("error") or {}).get("message", "")) if isinstance(resp, dict) else ""
    if code != 400 or "line range" not in msg:
        print("range past the end was not refused with 400", code, resp, file=sys.stderr)
        return 1

    print("ok http e2e mentions", flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
