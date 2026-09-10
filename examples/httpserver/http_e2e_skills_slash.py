#!/usr/bin/env python3
"""HTTP e2e: slash skills catalog and ephemeral /name skill body on agent turns.

ACP twin: ``examples/acp/acp_e2e_skills_slash.py``.

Needs a running ``coddy serve`` built with **http** (and **scheduler** if you use the default examples binary), using ``examples/config.demo.yaml`` with ``skills.dirs``
listing ``${CODDY_HOME}/skills_fixture`` and ``${CWD}/.coddy/skills``. The full HTTP harness copies ``examples/skills_fixture/coddy_slash_demo`` there (``examples/httpserver/test_httpserver.sh``).

Calls a real configured LLM via ``POST /v1/responses`` (``model``: ``agent`` or ``plan``).

Environment:

- ``BASE_URL`` - OpenAI-compatible base ending in ``/v1`` (default ``http://127.0.0.1:19876/v1``).
- ``MODEL`` - YAML ``models[].model`` id (default ``rpa/gpt-oss:120b``), same as other HTTP e2e harnesses.
- ``CODDY_CHAT_PROFILE`` - Coddy profile for ``POST /v1/responses`` (default ``agent``).

Checks:

1. ``GET /coddy/slash-commands`` lists the fixture command ``coddy_slash_demo``.
2. Agent reply includes ``DEMO_SKILL_TOKEN:z7k9-demo-slash`` after a prompt that starts with ``/coddy_slash_demo``.
3. Project-local skills follow the session workspace (coddy-project/coddy-agent#146): a session anchored on a
   fresh folder carrying ``.coddy/skills/coddy_project_demo`` lists ``coddy_project_demo`` in
   ``GET /coddy/slash-commands`` and ``GET /coddy/skills`` (with ``X-Coddy-Session-ID``), the header-less
   catalog of the server default workspace does not, and an agent turn on that session with
   ``/coddy_project_demo`` echoes ``PROJECT_SKILL_TOKEN:q4m2-project-slash``. The demo config lists
   ``${CWD}/.coddy/skills`` in ``skills.dirs`` and the server is started from another directory.
"""

from __future__ import annotations

import json
import os
import secrets
import shutil
import sys
import tempfile
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any, Tuple


FIXTURE_SLASH_NAME = "coddy_slash_demo"
VERIFICATION_TOKEN = "DEMO_SKILL_TOKEN:z7k9-demo-slash"
PROJECT_SLASH_NAME = "coddy_project_demo"
PROJECT_TOKEN = "PROJECT_SKILL_TOKEN:q4m2-project-slash"


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
    yaml_model = os.environ.get("MODEL", "rpa/gpt-oss:120b").strip()
    profile = os.environ.get("CODDY_CHAT_PROFILE", "agent").strip()

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
        {
            "model": profile,
            "input": prompt,
            "stream": False,
            "metadata": {"model": yaml_model},
        },
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
        {
            "model": profile,
            "input": "Say only: hello-demo-control",
            "stream": False,
            "metadata": {"model": yaml_model},
        },
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

    rc = check_project_local_skills(v1, origin, yaml_model, profile)
    if rc != 0:
        return rc

    print("ok http e2e skills slash", flush=True)
    return 0


def catalog_names(origin: str, headers: dict[str, str]) -> Tuple[int, list[str]]:
    code, page, _ = http_json(
        "GET",
        f"{origin}/coddy/slash-commands?page=1&page_size=200",
        None,
        headers,
    )
    return code, [str((it or {}).get("name") or "") for it in (page.get("items") or [])]


def check_project_local_skills(v1: str, origin: str, yaml_model: str, profile: str) -> int:
    """A session anchored on a project folder sees that folder's .coddy/skills."""
    fixture_src = Path(__file__).resolve().parents[1] / "skills_fixture" / PROJECT_SLASH_NAME
    if not fixture_src.is_dir():
        print("missing fixture", fixture_src, file=sys.stderr)
        return 2
    project = Path(tempfile.mkdtemp(prefix="coddy-http-project-"))
    try:
        shutil.copytree(fixture_src, project / ".coddy" / "skills" / PROJECT_SLASH_NAME)

        # The server default workspace (started elsewhere) must not carry the project skill.
        code, names = catalog_names(origin, {})
        if code != 200:
            print("bad /coddy/slash-commands (no session)", code, file=sys.stderr)
            return 1
        if PROJECT_SLASH_NAME in names:
            print("project skill leaked into the server default catalog", names, file=sys.stderr)
            return 1

        # Draft flow of the SPA: a fresh session id is anchored on the project before the first message.
        sid = "sess_" + secrets.token_hex(8)
        hdr = {"X-Coddy-Session-ID": sid}
        code, ws, _ = http_json(
            "POST",
            f"{origin}/coddy/sessions/{sid}/workspace",
            {"path": str(project)},
            {},
        )
        if code != 200:
            print("bad workspace switch", code, ws, file=sys.stderr)
            return 1

        code, names = catalog_names(origin, hdr)
        if code != 200 or PROJECT_SLASH_NAME not in names:
            print("session catalog missing project skill", code, names, file=sys.stderr)
            return 1

        code, skills, _ = http_json("GET", f"{origin}/coddy/skills", None, hdr)
        rows = {str((it or {}).get("name") or ""): it for it in (skills.get("items") or [])}
        row = rows.get(PROJECT_SLASH_NAME) or {}
        if code != 200 or not str(row.get("file_path") or "").startswith(str(project)):
            print("session skills list missing project skill", code, sorted(rows), file=sys.stderr)
            return 1

        prompt = (
            f"/{PROJECT_SLASH_NAME}\n\n"
            "Follow the instructions from the user-invoked slash skill for this turn only. "
            "Reply in one short sentence and include the required verification token verbatim."
        )
        code, resp, _ = http_json(
            "POST",
            f"{v1}/responses",
            {
                "model": profile,
                "input": prompt,
                "stream": False,
                "metadata": {"model": yaml_model},
            },
            hdr,
        )
        if code != 200:
            print("bad /v1/responses on project session", code, resp, file=sys.stderr)
            return 1
        out_blocks = resp.get("output") or []
        text = str(out_blocks[0].get("text") or "") if out_blocks and isinstance(out_blocks[0], dict) else ""
        if PROJECT_TOKEN not in text:
            print("model output missing project skill token", text[:800], file=sys.stderr)
            return 1
        return 0
    finally:
        shutil.rmtree(project, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
