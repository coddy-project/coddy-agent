#!/usr/bin/env python3
"""HTTP e2e: merged model list mirrors config, profiles can steer LLM via metadata.model.

Prerequisites: ``coddy http`` is already listening (same host/port as BASE_URL).

Uses ``examples/config.demo.yaml`` (or ``CODDY_CONFIG``). One ``models[].model`` row is enough; when multiple exist, picks a different selector than ``agent.model`` when possible for the metadata echo check.

Environment:

- ``BASE_URL`` - base for OpenAI-compatible routes (default ``http://127.0.0.1:19876/v1``).
- ``CODDY_CONFIG`` - same YAML the server was started with (default repo ``examples/config.demo.yaml``).

The script verifies:

1. ``GET /v1/models`` lists ``agent`` and ``plan`` with ``owned_by`` ``coddy`` and every YAML
   model row with ``owned_by`` equal to the provider prefix.
2. Direct completion rejects a body that includes ``metadata.model`` (HTTP 400).
3. ``POST /v1/responses`` with ``model=agent`` and ``metadata.model`` set to a configured selector (not
   necessarily different from ``agent.model`` when only one model exists) returns HTTP 200 and echoes it in
   response ``metadata.model`` (needs a working LLM).
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


def http_json(method: str, url: str, body: dict[str, Any] | None, headers: dict[str, str]) -> Tuple[int, dict[str, Any], dict[str, str]]:
    data = None if body is None else json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Accept", "application/json")
    if data is not None:
        req.add_header("Content-Type", "application/json")
    for k, v in headers.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
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
        return e.code, out, dict(e.headers.items()) if hasattr(e.headers, "items") else {}


def default_config_path() -> Path:
    return Path(__file__).resolve().parent.parent / "config.demo.yaml"


def parse_models_from_yaml(raw: str) -> list[str]:
    return [m.strip() for m in re.findall(r"(?m)^\s*-\s+model:\s*\"([^\"]+)\"\s*$", raw)]


def parse_agent_default_model(raw: str) -> str:
    blk = re.search(r"(?ms)^agent:\s*\n(.*?)(?=^[a-z_]+:\s*$)", raw)
    if not blk:
        return ""
    m = re.search(r"(?m)^\s+model:\s*\"([^\"]+)\"\s*$", blk.group(1))
    return m.group(1).strip() if m else ""


def provider_prefix(model_id: str) -> str:
    i = model_id.find("/")
    return model_id[:i] if i > 0 else model_id


def main() -> int:
    cfg_path = Path(os.environ.get("CODDY_CONFIG", str(default_config_path()))).expanduser()
    if not cfg_path.is_file():
        print("CODDY_CONFIG not found:", cfg_path, file=sys.stderr)
        return 1
    raw = cfg_path.read_text(encoding="utf-8")
    yaml_models = parse_models_from_yaml(raw)
    agent_default = parse_agent_default_model(raw)
    if len(yaml_models) < 1:
        print("need at least one models[] entry for this demo, got:", yaml_models, file=sys.stderr)
        return 1
    if not agent_default or agent_default not in yaml_models:
        print("agent.model must match a models[].model row", agent_default, yaml_models, file=sys.stderr)
        return 1

    base = os.environ.get("BASE_URL", "http://127.0.0.1:19876/v1").rstrip("/")

    alts = [m for m in yaml_models if m != agent_default]
    alt = alts[0] if alts else agent_default
    primary = yaml_models[0]

    code, blob, _ = http_json("GET", f"{base}/models", None, {})
    if code != 200 or blob.get("object") != "list":
        print("bad GET /models", code, blob, file=sys.stderr)
        return 1
    rows = blob.get("data") or []
    by_id = {str(r.get("id", "")): r for r in rows}

    for need in ["agent", "plan"]:
        row = by_id.get(need)
        if row is None:
            print("missing profile", need, blob, file=sys.stderr)
            return 1
        if row.get("owned_by") != "coddy":
            print("bad owned_by for", need, row, file=sys.stderr)
            return 1

    for mid in yaml_models:
        row = by_id.get(mid)
        if row is None:
            print("YAML model missing from GET /models", mid, list(by_id), file=sys.stderr)
            return 1
        if row.get("owned_by") != provider_prefix(mid):
            print("bad owned_by for", mid, row, file=sys.stderr)
            return 1

    bad_code, _, _ = http_json(
        "POST",
        f"{base}/responses",
        {"model": primary, "input": "x", "stream": False, "metadata": {"model": primary}},
        {},
    )
    if bad_code != 400:
        print("want 400 when metadata.model is set on direct completion, got", bad_code, file=sys.stderr)
        return 1

    code, deny, _ = http_json(
        "POST",
        f"{base}/responses",
        {"model": "agent", "input": "x", "stream": False, "metadata": {"model": ""}},
        {},
    )
    if code != 400:
        print("want 400 empty metadata.model on profile, got", code, deny, file=sys.stderr)
        return 1

    code, live, hdr = http_json(
        "POST",
        f"{base}/responses",
        {"model": "agent", "input": "Reply with exactly: OK.", "stream": False, "metadata": {"model": alt}},
        {},
    )
    if code != 200:
        print("bad profile completion", code, live, file=sys.stderr)
        return 1

    sid = (hdr.get("X-Coddy-Session-Id") or hdr.get("X-Coddy-Session-ID") or "").strip()
    md = live.get("metadata") or {}
    if md.get("model") != alt:
        print("metadata.model mismatch", md, "want", alt, file=sys.stderr)
        return 1

    # Switch metadata.model back to agent default (e.g. 120b after testing 20b).
    if alt != agent_default:
        extra: dict[str, str] = {}
        if sid:
            extra["X-Coddy-Session-ID"] = sid
        code2, sw, _ = http_json(
            "POST",
            f"{base}/responses",
            {
                "model": "agent",
                "input": "Reply with exactly: HI.",
                "stream": False,
                "metadata": {"model": agent_default},
            },
            extra,
        )
        if code2 != 200:
            print("restore completion want 200, got", code2, sw, file=sys.stderr)
            return 1
        md2 = sw.get("metadata") or {}
        if md2.get("model") != agent_default:
            print("restore metadata.model mismatch", md2, "want", agent_default, file=sys.stderr)
            return 1

    print("ok http models e2e")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
