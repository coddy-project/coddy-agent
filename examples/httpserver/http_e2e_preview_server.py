#!/usr/bin/env python3
"""End-to-end check of the preview server over the HTTP surface.

Asks a real model, in the words an operator would use, to put the project on a
web server so they can try it themselves. The model is expected to call
``preview_server``, and the script then checks what the operator gets:

- the answer names a ``http://127.0.0.1:<port>/`` address;
- the session has a running ``server`` task carrying that address;
- the address serves the page and its ES module with a script content type,
  reads an edited file fresh, and hides dot-files;
- the request log is the task's output;
- stopping the task through the REST surface takes the address down.

Environment:

- ``BASE_URL`` - OpenAI-compatible base (default ``http://127.0.0.1:19876/v1``),
  same as the other HTTP harnesses.
- ``MODEL`` - YAML ``models[].model`` id (default ``rpa/qwen3.6-35b-a3b``).
- ``CODDY_CHAT_PROFILE`` - session profile (default ``agent``).
- ``WORK_DIR`` - the server's session cwd; the page is written there.

Exits non-zero on any HTTP error or unmet expectation.
"""

from __future__ import annotations

import json
import os
import re
import socket
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any, Tuple

SESSION_ID = "http-e2e-preview-server"
SITE = "preview_site"


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


def fetch(url: str) -> Tuple[int, str, str]:
    """GET a page of the preview server: status, content type, body."""
    try:
        with urllib.request.urlopen(url, timeout=15) as resp:
            body = resp.read().decode("utf-8", errors="replace")
            return resp.status, resp.headers.get("Content-Type", ""), body
    except urllib.error.HTTPError as e:
        return e.code, e.headers.get("Content-Type", ""), ""


def coddy_base(base: str) -> str:
    """Turn the /v1 base into the /coddy base the drawer endpoints live under."""
    return base[: -len("/v1")] if base.endswith("/v1") else base


def answer_text(completion: dict[str, Any]) -> str:
    choices = completion.get("choices") or []
    if not choices:
        return ""
    content = (choices[0].get("message") or {}).get("content")
    return content if isinstance(content, str) else json.dumps(content)


def write_site(root: Path, marker: str) -> None:
    (root / "js").mkdir(parents=True, exist_ok=True)
    (root / "index.html").write_text(
        f'<!doctype html><title>preview</title><p>{marker}</p>'
        '<script type="module" src="js/app.js"></script>\n',
        encoding="utf-8",
    )
    (root / "js" / "app.js").write_text(
        'import { n } from "./n.js";\ndocument.title = String(n);\n', encoding="utf-8"
    )
    (root / "js" / "n.js").write_text("export const n = 42;\n", encoding="utf-8")
    (root / ".env").write_text("SECRET=do-not-serve\n", encoding="utf-8")


def main() -> int:
    base = os.environ.get("BASE_URL", "http://127.0.0.1:19876/v1").rstrip("/")
    coddy = coddy_base(base)
    yaml_model = os.environ.get("MODEL", "rpa/qwen3.6-35b-a3b").strip()
    profile = os.environ.get("CODDY_CHAT_PROFILE", "agent").strip()
    work_dir = os.environ.get("WORK_DIR", "").strip()
    if not work_dir:
        print("WORK_DIR (the server's session cwd) is not set", file=sys.stderr)
        return 1
    tasks_url = f"{coddy}/coddy/sessions/{SESSION_ID}/background-tasks"
    headers = {"X-Coddy-Session-ID": SESSION_ID}

    marker = "coddy-preview-e2e"
    site = Path(work_dir) / SITE
    write_site(site, marker)

    # The words of an operator, not a tool name: choosing preview_server for
    # them is the model's half of the feature.
    prompt = (
        f"The folder {SITE} holds a small web page I just made. "
        "Run it on a local web server for me, I want to open it in my browser and click around myself. "
        "Give me the address when it is up."
    )
    code, completion = http_json(
        "POST",
        f"{base}/chat/completions",
        {
            "model": profile,
            "stream": False,
            "metadata": {"model": yaml_model},
            "messages": [{"role": "user", "content": prompt}],
        },
        headers,
    )
    if code != 200:
        print("bad chat completion code", code, file=sys.stderr)
        return 1

    code, listing = http_json("GET", tasks_url, None, headers)
    if code != 200:
        print("bad background task list code", code, file=sys.stderr)
        return 1
    servers = [r for r in (listing.get("data") or []) if r.get("kind") == "server"]
    if not servers:
        kinds = [r.get("kind") for r in (listing.get("data") or [])]
        print(f"model did not start a preview server (task kinds: {kinds})", file=sys.stderr)
        print("answer was:", answer_text(completion)[:400], file=sys.stderr)
        return 1
    task = servers[-1]
    task_id = str(task.get("id") or "")
    url = str(task.get("url") or "")
    if not task.get("running"):
        print(f"server task {task_id} is not running: {task.get('status')}", file=sys.stderr)
        return 1
    if not re.fullmatch(r"http://127\.0\.0\.1:\d+/", url):
        print(f"server task {task_id} carries url {url!r}, want http://127.0.0.1:<port>/", file=sys.stderr)
        return 1
    if task.get("timeout_seconds"):
        print(f"server task {task_id} has a hard timeout of {task.get('timeout_seconds')}s, want none", file=sys.stderr)
        return 1

    # The operator only sees the answer: the address has to be in it.
    answer = answer_text(completion)
    port = urllib.parse.urlparse(url).port
    if f":{port}" not in answer:
        print(f"the answer does not give the user the address {url}: {answer[:400]!r}", file=sys.stderr)
        return 1

    status, ctype, body = fetch(url)
    if status != 200 or marker not in body:
        print(f"GET {url}: {status}, marker missing from {body[:200]!r}", file=sys.stderr)
        return 1
    status, ctype, body = fetch(url + "js/app.js")
    if status != 200 or "javascript" not in ctype:
        print(f"GET js/app.js: {status} {ctype!r}, want 200 with a script type", file=sys.stderr)
        return 1
    status, _, body = fetch(url + ".env")
    if status != 404 or "do-not-serve" in body:
        print(f"GET .env: {status}, want 404", file=sys.stderr)
        return 1

    # An edit shows on the next request.
    write_site(site, marker + "-edited")
    status, _, body = fetch(url)
    if marker + "-edited" not in body:
        print("an edited file was not served fresh", file=sys.stderr)
        return 1

    # The request log is the task's output.
    # A line is written once its handler returns, a moment after the client
    # has the answer, so give the last one that moment.
    log = ""
    deadline = time.time() + 5
    while time.time() < deadline:
        code, single = http_json("GET", f"{tasks_url}/{task_id}", None, headers)
        if code != 200:
            print("bad background task get code", code, file=sys.stderr)
            return 1
        log = single.get("output") or ""
        if "GET /js/app.js 200" in log and "GET /.env 404" in log:
            break
        time.sleep(0.2)
    if "GET /js/app.js 200" not in log or "GET /.env 404" not in log:
        print(f"request log is missing the requests just made: {log!r}", file=sys.stderr)
        return 1

    code, stopped = http_json("POST", f"{tasks_url}/{task_id}/stop", None, headers)
    if code != 200 or (stopped.get("task") or {}).get("status") != "stopped":
        print(f"stop answered {code} {stopped}", file=sys.stderr)
        return 1
    deadline = time.time() + 10
    while time.time() < deadline:
        try:
            socket.create_connection(("127.0.0.1", port), timeout=1).close()
        except OSError:
            break
        time.sleep(0.25)
    else:
        print(f"{url} still accepts connections after the task was stopped", file=sys.stderr)
        return 1

    print(f"ok http preview server e2e ({task_id} at {url})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
