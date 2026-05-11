#!/usr/bin/env python3
"""Scheduler HTTP REST smoke (no LLM).

Exercises /coddy/scheduler/jobs CRUD and pause or run conflict. Requires
``coddy http`` built with ``-tags http,scheduler``, scheduler enabled in
config, and the server started with ``--scheduler-enabled`` when the
effective config would otherwise leave it off.

Environment: ``BASE_URL`` (default ``http://127.0.0.1:19876/v1``) - scheduler
paths use the same host and port with ``/coddy/scheduler`` (not under ``/v1``).
"""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from typing import Any


def coddy_origin(base_v1: str) -> str:
    b = base_v1.rstrip("/")
    if b.endswith("/v1"):
        return b[:-3]
    return b


def http_json(
    method: str,
    url: str,
    body: dict[str, Any] | None,
    *,
    timeout: float = 60.0,
) -> tuple[int, dict[str, Any]]:
    data = None if body is None else json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Accept", "application/json")
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            out = json.loads(raw) if raw.strip() else {}
            return resp.status, out
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        try:
            out = json.loads(raw) if raw.strip() else {}
        except json.JSONDecodeError:
            out = {"_raw": raw}
        return e.code, out


def main() -> int:
    base_v1 = os.environ.get("BASE_URL", "http://127.0.0.1:19876/v1").rstrip("/")
    origin = coddy_origin(base_v1)
    list_url = f"{origin}/coddy/scheduler/jobs"

    code, env = http_json("GET", list_url, None, timeout=30.0)
    if code == 404:
        print(
            "GET /coddy/scheduler/jobs -> 404 (rebuild with: make build TAGS=\"http scheduler\")",
            file=sys.stderr,
        )
        return 2
    if code == 503:
        print(
            "scheduler disabled (503). Use scheduler.enabled in config and --scheduler-enabled if needed.",
            file=sys.stderr,
        )
        return 3
    if code != 200:
        print("GET /coddy/scheduler/jobs unexpected", code, env, file=sys.stderr)
        return 1
    if "scheduler" not in env or "jobs" not in env:
        print("GET /coddy/scheduler/jobs missing envelope keys", env, file=sys.stderr)
        return 1

    job_id = f"py_rest_smoke_{os.getpid()}"
    create_body = {
        "job_id": job_id,
        "description": "REST smoke job",
        "schedule": "0 9 * * 1",
        "body": "noop body for API test",
    }
    code, created = http_json("POST", list_url, create_body, timeout=30.0)
    if code != 201:
        print("POST create job", code, created, file=sys.stderr)
        return 1

    one = f"{origin}/coddy/scheduler/jobs/{urllib.parse.quote(job_id, safe='')}"
    code, job = http_json("GET", one, None, timeout=30.0)
    if code != 200 or (job.get("job_id") or "").strip() != job_id:
        print("GET job", code, job, file=sys.stderr)
        return 1

    runs_url = f"{one}/runs"
    code, runs = http_json("GET", runs_url, None, timeout=30.0)
    if code != 200 or not isinstance(runs.get("runs"), list):
        print("GET runs", code, runs, file=sys.stderr)
        return 1

    code, _ = http_json("PATCH", one, {"paused": True}, timeout=30.0)
    if code != 200:
        print("PATCH pause", code, file=sys.stderr)
        return 1

    code, run_body = http_json("POST", f"{one}/run", None, timeout=30.0)
    if code != 409:
        print("POST run while paused want 409 got", code, run_body, file=sys.stderr)
        return 1

    code, _ = http_json("PATCH", one, {"paused": False}, timeout=30.0)
    if code != 200:
        print("PATCH resume", code, file=sys.stderr)
        return 1

    code, _ = http_json("DELETE", one, None, timeout=30.0)
    if code != 204:
        print("DELETE job", code, file=sys.stderr)
        return 1

    code, gone = http_json("GET", runs_url, None, timeout=30.0)
    if code != 404:
        print("GET runs after delete want 404 got", code, gone, file=sys.stderr)
        return 1

    print("ok http scheduler rest smoke", flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
