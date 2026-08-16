#!/usr/bin/env python3
"""HTTP e2e: staged self-configuration (ACP twin: acp_e2e_config.py, CLI twin: cli_e2e_config.py).

Runs the uci-like config tool family against the live `coddy http` server:

1. ask the agent to change agent.max_turns - it must only STAGE the edit
   (active config untouched) and ask whether to save;
2. confirm in Russian - the agent commits, the file changes, and the
   pre-commit snapshot (config.yaml.prev) appears next to it;
3. ask to roll back - the agent restores the previous configuration, so the
   shared server config ends the script unchanged.

Environment: BASE_URL, MODEL, CODDY_CONFIG (the server's resolved config path).
"""

from __future__ import annotations

import json
import os
import re
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any, Tuple

ORIGINAL_MAX_TURNS = 35
TARGET_MAX_TURNS = 19


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
        with urllib.request.urlopen(req, timeout=300) as resp:
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


def max_turns_in(config_path: Path) -> int:
    m = re.search(r"max_turns:\s*(\d+)", config_path.read_text(encoding="utf-8"))
    if not m:
        raise AssertionError(f"max_turns not found in {config_path}")
    return int(m.group(1))


def main() -> int:
    v1 = os.environ.get("BASE_URL", "http://127.0.0.1:19876/v1").rstrip("/")
    yaml_model = os.environ.get("MODEL", "rpa/gpt-oss:120b").strip()
    profile = os.environ.get("CODDY_CHAT_PROFILE", "agent").strip()
    cfg_env = os.environ.get("CODDY_CONFIG", "").strip()
    if not cfg_env:
        print("CODDY_CONFIG required (the server's active config path)", file=sys.stderr)
        return 2
    cfg = Path(cfg_env)
    snapshot = cfg.parent / "config.yaml.prev"

    def ask(text: str, sid: str) -> str:
        body = {"model": profile, "metadata": {"model": yaml_model}, "input": text}
        headers = {"X-Coddy-Session-ID": sid} if sid else {}
        code, resp, hdr = http_json("POST", f"{v1}/responses", body, headers)
        if code != 200:
            raise AssertionError(f"responses failed: {code} {resp}")
        return (hdr.get("x-coddy-session-id") or sid or "").strip()

    # 1. Stage only: the active file must not change yet.
    sid = ask(
        f"Change your own Coddy configuration: set agent.max_turns to {TARGET_MAX_TURNS}. "
        "Use your config tools to STAGE the edit, show me the staged commands, and ask me "
        "whether to save. Do NOT commit or apply anything until I answer.",
        "",
    )
    if max_turns_in(cfg) != ORIGINAL_MAX_TURNS:
        print("FAIL: staging already changed the active config", file=sys.stderr)
        return 11

    # 2. Confirm in Russian: the agent must commit and hot-reload.
    ask("Да, сохраняй.", sid)
    if max_turns_in(cfg) != TARGET_MAX_TURNS:
        print("FAIL: confirmation did not commit the staged change", file=sys.stderr)
        return 12
    if not snapshot.is_file() or f"max_turns: {ORIGINAL_MAX_TURNS}" not in snapshot.read_text(encoding="utf-8"):
        print("FAIL: pre-commit snapshot missing or wrong", file=sys.stderr)
        return 13

    # 3. Roll back so the shared server config ends the script unchanged.
    ask(
        "Now roll back your configuration to the previous version from the snapshot backup. "
        "I understand the warning about losing the change - proceed.",
        sid,
    )
    if max_turns_in(cfg) != ORIGINAL_MAX_TURNS:
        print("FAIL: rollback did not restore the previous configuration", file=sys.stderr)
        return 14

    print("ok http e2e config", flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
