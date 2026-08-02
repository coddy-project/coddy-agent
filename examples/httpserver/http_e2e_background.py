#!/usr/bin/env python3
"""End-to-end check of background tasks over the HTTP surface.

Asks a real model to start a detached command with ``run_command``
``background: true`` and an ``expected_seconds`` estimate, then drives the REST
surface the tasks drawer uses: list the session's tasks, read the captured
output while the task still runs, and confirm it reaches a terminal state.

Environment:

- ``BASE_URL`` - OpenAI-compatible base (default ``http://127.0.0.1:19876/v1``),
  same as the other HTTP harnesses.
- ``MODEL`` - YAML ``models[].model`` id (default ``rpa/gpt-oss:120b``).
- ``CODDY_CHAT_PROFILE`` - session profile (default ``agent``).

Exits non-zero on any HTTP error or unmet expectation.
"""

from __future__ import annotations

import json
import os
import sys
import time
import urllib.error
import urllib.request
from typing import Any, Tuple

SESSION_ID = "http-e2e-background"


def http_json(
    method: str,
    url: str,
    body: dict[str, Any] | None,
    headers: dict[str, str],
) -> Tuple[int, dict[str, Any]]:
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
            return resp.status, (json.loads(raw) if raw.strip() else {})
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"http {method} {url} failed: {e.code} {raw}") from e


def coddy_base(base: str) -> str:
    """Turn the /v1 base into the /coddy base the drawer endpoints live under."""
    return base[: -len("/v1")] if base.endswith("/v1") else base


def main() -> int:
    base = os.environ.get("BASE_URL", "http://127.0.0.1:19876/v1").rstrip("/")
    coddy = coddy_base(base)
    yaml_model = os.environ.get("MODEL", "rpa/gpt-oss:120b").strip()
    profile = os.environ.get("CODDY_CHAT_PROFILE", "agent").strip()
    tasks_url = f"{coddy}/coddy/sessions/{SESSION_ID}/background-tasks"

    marker = "coddy-background-e2e"
    prompt = (
        "Start ONE background task now and nothing else.\n"
        "Call run_command with background:true, expected_seconds:20, and this command:\n"
        f"  for i in 1 2 3 4 5 6 7 8; do echo {marker} $i; sleep 1; done\n"
        "As soon as the tool returns a task id, reply with that id and stop. "
        "Do NOT call background_wait and do NOT call background_stop."
    )

    code, _ = http_json(
        "POST",
        f"{base}/chat/completions",
        {
            "model": profile,
            "stream": False,
            "metadata": {"model": yaml_model},
            "messages": [{"role": "user", "content": prompt}],
        },
        {"X-Coddy-Session-ID": SESSION_ID},
    )
    if code != 200:
        print("bad chat completion code", code, file=sys.stderr)
        return 1

    # The pool is keyed by session, so the task is visible the moment the tool
    # call returns - there is no turn boundary to wait for.
    code, listing = http_json("GET", tasks_url, None, {"X-Coddy-Session-ID": SESSION_ID})
    if code != 200:
        print("bad background task list code", code, file=sys.stderr)
        return 1

    rows = listing.get("data") or []
    if not rows:
        print("model did not start a background task", file=sys.stderr)
        return 1

    task = rows[-1]
    task_id = str(task.get("id") or "")
    if not task_id:
        print("background task row has no id", file=sys.stderr)
        return 1
    if not task.get("expected_seconds"):
        print(f"task {task_id} carries no expected_seconds estimate", file=sys.stderr)
        return 1
    if not task.get("timeout_seconds"):
        print(f"task {task_id} carries no hard timeout", file=sys.stderr)
        return 1

    # Output must be readable while the task is still running: that is what the
    # drawer and background_output rely on.
    saw_output = False
    deadline = time.time() + 120
    while time.time() < deadline:
        code, single = http_json(
            "GET", f"{tasks_url}/{task_id}", None, {"X-Coddy-Session-ID": SESSION_ID}
        )
        if code != 200:
            print("bad background task get code", code, file=sys.stderr)
            return 1
        if marker in (single.get("output") or ""):
            saw_output = True
            break
        if (single.get("task") or {}).get("running") is False:
            break
        time.sleep(0.5)

    if not saw_output:
        print(f"never saw {marker!r} in the captured output of {task_id}", file=sys.stderr)
        return 1

    # And it must reach a terminal state on its own.
    final: dict[str, Any] = {}
    deadline = time.time() + 180
    while time.time() < deadline:
        code, single = http_json(
            "GET", f"{tasks_url}/{task_id}", None, {"X-Coddy-Session-ID": SESSION_ID}
        )
        final = single.get("task") or {}
        if final.get("running") is False:
            break
        time.sleep(0.5)

    status = str(final.get("status") or "")
    if status != "succeeded":
        print(f"task {task_id} ended as {status!r}, want succeeded", file=sys.stderr)
        return 1

    # A finished task is a no-op for stop, and stop must still answer 200.
    code, stopped = http_json(
        "POST", f"{tasks_url}/{task_id}/stop", None, {"X-Coddy-Session-ID": SESSION_ID}
    )
    if code != 200:
        print("stopping a finished task should still answer 200", file=sys.stderr)
        return 1
    if (stopped.get("task") or {}).get("status") != "succeeded":
        print("stopping a finished task changed its status", file=sys.stderr)
        return 1

    # Unknown ids are reported, not invented.
    try:
        http_json(
            "GET",
            f"{tasks_url}/bg_does_not_exist",
            None,
            {"X-Coddy-Session-ID": SESSION_ID},
        )
    except RuntimeError as e:
        if "404" not in str(e):
            print(f"unknown task id gave the wrong error: {e}", file=sys.stderr)
            return 1
    else:
        print("unknown task id should be a 404", file=sys.stderr)
        return 1

    print(f"ok http background e2e ({task_id})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
