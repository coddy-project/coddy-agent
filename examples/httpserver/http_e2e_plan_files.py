#!/usr/bin/env python3
"""HTTP e2e: plan mode persists ``plans/e2e-plan.plan.md``, then ``metadata.runPlanSlug`` runs it.

Mirrors ``examples/acp/acp_e2e_plan_files.py`` over ``POST /v1/responses``.

Environment: ``BASE_URL`` (ends with ``/v1``), ``MODEL``, ``WORK_DIR``, ``CODDY_HOME``.
"""

from __future__ import annotations

import json
import os
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any, Iterable

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "shared"))

from plan_e2e_common import (  # noqa: E402
    PLAN_MARKER,
    PLAN_SLUG,
    RUN_ARTIFACT,
    RUN_MARKER,
    plan_prompt_text,
    wait_for_plan_file,
    wait_for_run_artifact,
)


def _b(s: str) -> bytes:
    return s.encode("utf-8")


def sse_events(resp: Any) -> Iterable[tuple[str, str]]:
    event = ""
    data_lines: list[str] = []
    while True:
        raw = resp.readline()
        if not raw:
            break
        line = raw.decode("utf-8", errors="replace").rstrip("\r\n")
        if line == "":
            if data_lines:
                yield event or "message", "\n".join(data_lines)
            event = ""
            data_lines = []
            continue
        if line.startswith(":"):
            continue
        if line.startswith("event:"):
            event = line[len("event:") :].strip()
            continue
        if line.startswith("data:"):
            data_lines.append(line[len("data:") :].lstrip())
            continue


def stream_responses(
    base: str,
    body: dict[str, Any],
    deadline_s: float = 420,
) -> tuple[str, list[tuple[str, str]]]:
    req = urllib.request.Request(f"{base}/responses", data=_b(json.dumps(body)), method="POST")
    req.add_header("Accept", "text/event-stream")
    req.add_header("Content-Type", "application/json")

    events: list[tuple[str, str]] = []
    with urllib.request.urlopen(req, timeout=deadline_s + 30) as resp:
        sid = (resp.headers.get("X-Coddy-Session-Id") or resp.headers.get("X-Coddy-Session-ID") or "").strip()
        if not sid:
            raise RuntimeError("missing X-Coddy-Session-ID header")

        deadline = time.time() + deadline_s
        for ev, data in sse_events(resp):
            events.append((ev, data))
            if time.time() > deadline:
                raise TimeoutError("timed out waiting for response.completed")
            if ev == "response.completed":
                break
            if ev == "error" or (data.strip().startswith("{") and '"error"' in data):
                raise RuntimeError(f"SSE error: {data[:500]}")
        return sid, events


def tool_titles_from_sse(events: list[tuple[str, str]]) -> list[str]:
    titles: list[str] = []
    for ev, data in events:
        if ev != "tool_call":
            continue
        try:
            payload = json.loads(data)
        except json.JSONDecodeError:
            continue
        t = payload.get("title")
        if isinstance(t, str) and t.strip():
            titles.append(t.strip())
    return titles


def main() -> int:
    base = os.environ.get("BASE_URL", "http://127.0.0.1:19876/v1").rstrip("/")
    yaml_model = os.environ.get("MODEL", "rpa/gpt-oss:120b").strip()
    work = Path(os.environ.get("WORK_DIR", "")).resolve()
    home = Path(os.environ.get("CODDY_HOME", "")).resolve()
    if not work.is_dir() or not home.is_dir():
        print("WORK_DIR and CODDY_HOME must point to existing directories", file=sys.stderr)
        return 2

    try:
        sid, plan_events = stream_responses(
            base,
            {
                "model": "plan",
                "stream": True,
                "metadata": {"model": yaml_model},
                "input": plan_prompt_text(work),
            },
        )
        session_dir = home / "sessions" / sid

        try:
            wait_for_plan_file(session_dir, PLAN_SLUG, PLAN_MARKER)
        except RuntimeError as e:
            plan_titles = tool_titles_from_sse(plan_events)
            print(str(e), file=sys.stderr)
            print(f"SSE tool_call titles during plan turn: {plan_titles}", file=sys.stderr)
            return 1

        plan_titles = tool_titles_from_sse(plan_events)
        if "plan_write" not in plan_titles:
            print(
                f"warn: plan_write not in SSE tool_call (got {plan_titles}); "
                "plan file on disk is authoritative",
                file=sys.stderr,
            )

        run_path = work / RUN_ARTIFACT
        if run_path.is_file():
            run_path.unlink()

        _, run_events = stream_responses(
            base,
            {
                "model": "agent",
                "stream": True,
                "metadata": {"model": yaml_model, "runPlanSlug": PLAN_SLUG},
                "input": "Implement the plan.",
            },
        )
        run_titles = tool_titles_from_sse(run_events)
        if "write" not in run_titles and "run_command" not in run_titles:
            print(
                f"expected write or run_command during run plan, got {run_titles}",
                file=sys.stderr,
            )

        try:
            wait_for_run_artifact(work, RUN_ARTIFACT, RUN_MARKER)
        except RuntimeError as e:
            print(str(e), file=sys.stderr)
            return 1

        print("ok http plan files e2e")
        return 0

    except (urllib.error.HTTPError, TimeoutError, RuntimeError) as e:
        print(str(e), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
