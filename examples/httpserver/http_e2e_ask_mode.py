#!/usr/bin/env python3
"""HTTP e2e: the ``ask`` profile reads but never writes; ``agent`` on the same session still writes.

One session, two turns over ``POST /v1/responses``:

1. ``model: ask`` with a prompt that asks to read ``ask_e2e_note.txt`` and to create
   ``ask_e2e_written.txt``. The SSE ``tool_call`` titles must all be read-only tools, the
   answer must quote the note marker, the artifact must not exist, and
   ``GET /coddy/sessions/{id}/messages`` must report ``mode: ask``.
2. ``model: agent`` with the same prompt. The artifact must appear with the marker and the
   transcript must report ``mode: agent`` again (regression: the old modes keep working).

Mirrors ``examples/acp/acp_e2e_ask_mode.py`` and ``examples/cli/cli_e2e_ask_mode.py``.

Environment: ``BASE_URL`` (ends with ``/v1``), ``MODEL``, ``WORK_DIR``, ``CODDY_HOME``.
"""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "shared"))
sys.path.insert(0, os.path.dirname(__file__))

from ask_e2e_common import (  # noqa: E402
    ASK_ARTIFACT_MARKER,
    check_ask_turn,
    seed_note,
    ask_prompt_text,
    wait_for_artifact,
)
from http_e2e_plan_files import stream_responses, tool_titles_from_sse  # noqa: E402


def answer_from_sse(events: list[tuple[str, str]]) -> str:
    parts: list[str] = []
    for ev, data in events:
        if ev != "message":
            continue
        if not data.startswith("{"):
            continue
        try:
            payload = json.loads(data)
        except json.JSONDecodeError:
            continue
        for choice in payload.get("choices") or []:
            delta = choice.get("delta") or {}
            content = delta.get("content")
            if isinstance(content, str):
                parts.append(content)
    return "".join(parts)


def session_mode(base: str, sid: str) -> str:
    url = base[: -len("/v1")] if base.endswith("/v1") else base
    with urllib.request.urlopen(f"{url}/coddy/sessions/{sid}/messages", timeout=30) as resp:
        payload: dict[str, Any] = json.loads(resp.read().decode("utf-8"))
    return str(payload.get("mode") or "")


def main() -> int:
    base = os.environ.get("BASE_URL", "http://127.0.0.1:19876/v1").rstrip("/")
    yaml_model = os.environ.get("MODEL", "rpa/gpt-oss:120b").strip()
    work = Path(os.environ.get("WORK_DIR", "")).resolve()
    home = Path(os.environ.get("CODDY_HOME", "")).resolve()
    if not work.is_dir() or not home.is_dir():
        print("WORK_DIR and CODDY_HOME must point to existing directories", file=sys.stderr)
        return 2

    seed_note(work)
    try:
        sid, ask_events = stream_responses(
            base,
            {
                "model": "ask",
                "stream": True,
                "metadata": {"model": yaml_model},
                "input": ask_prompt_text(work),
            },
        )
        titles = tool_titles_from_sse(ask_events)
        answer = answer_from_sse(ask_events)
        try:
            check_ask_turn(titles, answer, work)
        except RuntimeError as e:
            print(str(e), file=sys.stderr)
            print(f"SSE tool_call titles during ask turn: {titles}", file=sys.stderr)
            return 1
        if (mode := session_mode(base, sid)) != "ask":
            print(f"transcript reports mode {mode!r} after the ask turn, want ask", file=sys.stderr)
            return 1

        # Same session, same prompt, agent profile: the write must now happen.
        _, agent_events = stream_responses(
            base,
            {
                "model": "agent",
                "stream": True,
                "metadata": {"model": yaml_model},
                "input": ask_prompt_text(work),
            },
            headers={"X-Coddy-Session-ID": sid},
        )
        agent_titles = tool_titles_from_sse(agent_events)
        try:
            wait_for_artifact(work)
        except RuntimeError as e:
            print(str(e), file=sys.stderr)
            print(f"SSE tool_call titles during agent turn: {agent_titles}", file=sys.stderr)
            return 1
        if (mode := session_mode(base, sid)) != "agent":
            print(f"transcript reports mode {mode!r} after the agent turn, want agent", file=sys.stderr)
            return 1

        print(f"ok http ask mode e2e (ask tools: {sorted(set(titles))}, artifact: {ASK_ARTIFACT_MARKER})")
        return 0
    except (urllib.error.HTTPError, TimeoutError, RuntimeError) as e:
        print(str(e), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
