#!/usr/bin/env python3
"""HTTP scheduler REST API plus on-disk job files (no LLM).

Checks ``GET/POST/PATCH/DELETE /coddy/scheduler/jobs`` and that ``$CODDY_HOME/scheduler/<job_id>.md``
appears with expected frontmatter or body, then disappears after delete.

Environment: ``BASE_URL`` (default ``http://127.0.0.1:19876/v1``), ``CODDY_HOME`` (required, absolute).
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


def job_md_path(home: Path, job_id: str) -> Path:
    return home / "scheduler" / f"{job_id}.md"


def assert_disk_job(
    home: Path,
    job_id: str,
    *,
    expect_description: str,
    expect_schedule: str,
    expect_body_substr: str,
) -> None:
    path = job_md_path(home, job_id)
    if not path.is_file():
        raise AssertionError(f"expected job file on disk: {path}")
    text = path.read_text(encoding="utf-8", errors="replace")
    if text.count("---") < 2:
        raise AssertionError(f"job {path} missing YAML frontmatter fences")
    if expect_schedule not in text:
        raise AssertionError(f"job file missing schedule {expect_schedule!r}: {path}")
    if expect_description not in text:
        raise AssertionError(f"job file missing description {expect_description!r}: {path}")
    if expect_body_substr not in text:
        raise AssertionError(f"job file missing body fragment {expect_body_substr!r}: {path}")


def main() -> int:
    home_raw = os.environ.get("CODDY_HOME", "").strip()
    if not home_raw:
        print("CODDY_HOME must be set to the same home the server uses", file=sys.stderr)
        return 10
    home = Path(home_raw).expanduser().resolve()
    if not home.is_dir():
        print("CODDY_HOME is not a directory", home, file=sys.stderr)
        return 11

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

    job_id = f"e2e_sched_api_{os.getpid()}"
    description = "e2e scheduler api job"
    schedule = "0 9 * * 1"
    body_text = "noop body for API plus disk test"
    create_body = {
        "job_id": job_id,
        "description": description,
        "schedule": schedule,
        "body": body_text,
    }
    code, created = http_json("POST", list_url, create_body, timeout=30.0)
    if code != 201:
        print("POST create job", code, created, file=sys.stderr)
        return 1

    assert_disk_job(home, job_id, expect_description=description, expect_schedule=schedule, expect_body_substr=body_text)

    one = f"{origin}/coddy/scheduler/jobs/{urllib.parse.quote(job_id, safe='')}"
    code, job = http_json("GET", one, None, timeout=30.0)
    if code != 200 or (job.get("job_id") or "").strip() != job_id:
        print("GET job", code, job, file=sys.stderr)
        return 1
    if (job.get("schedule") or "").strip() != schedule:
        print("GET job schedule mismatch", job.get("schedule"), file=sys.stderr)
        return 1

    runs_url = f"{one}/runs"
    code, runs = http_json("GET", runs_url, None, timeout=30.0)
    raw_runs = runs.get("runs")
    if raw_runs is None:
        raw_runs = []
    if code != 200 or not isinstance(raw_runs, list):
        print("GET runs", code, runs, file=sys.stderr)
        return 1

    code, _ = http_json("PATCH", one, {"paused": True}, timeout=30.0)
    if code != 200:
        print("PATCH pause", code, file=sys.stderr)
        return 1
    disk_after_pause = job_md_path(home, job_id).read_text(encoding="utf-8", errors="replace")
    if not re.search(r"(?mi)^paused\s*:\s*true\s*$", disk_after_pause):
        print("expected paused: true in on-disk job frontmatter", file=sys.stderr)
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

    if job_md_path(home, job_id).exists():
        print("job markdown file should be removed after DELETE", job_md_path(home, job_id), file=sys.stderr)
        return 1

    code, gone = http_json("GET", runs_url, None, timeout=30.0)
    if code != 404:
        print("GET runs after delete want 404 got", code, gone, file=sys.stderr)
        return 1

    print("ok http e2e scheduler api", flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
