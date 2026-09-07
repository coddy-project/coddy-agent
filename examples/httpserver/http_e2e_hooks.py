#!/usr/bin/env python3
"""End-to-end check of lifecycle hooks over the HTTP surface.

Writes a user-scope ``hooks.json`` into the server's coddy home whose
``PreToolUse`` and ``PostToolUse`` hooks append every payload they receive to
a JSONL file, and a project-scope ``.coddy/hooks.json`` into the server
workspace whose hook writes a marker file. Then it asks a real model to run
one shell command and drives the REST surface around hooks: the catalog, the
held project file and its notice row in the transcript, the approval route,
and a second turn that runs the approved hook.

Environment:

- ``BASE_URL`` - OpenAI-compatible base (default ``http://127.0.0.1:19876/v1``).
- ``MODEL`` - YAML ``models[].model`` id (default ``rpa/gpt-oss:120b``).
- ``CODDY_CHAT_PROFILE`` - session profile (default ``agent``).
- ``WORK_DIR`` - the workspace the server was started with (``--cwd``).
- ``CODDY_HOME`` - the server home (``--home``); the user-scope hooks file is
  written there, so it is required.

Exits non-zero on any HTTP error or unmet expectation.
"""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any, Tuple

SESSION_ID = "http-e2e-hooks"
PROJECT_FILE = ".coddy/hooks.json"
MARKER_ONE = "hooks-e2e-http-first"
MARKER_TWO = "hooks-e2e-http-second"


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
        with urllib.request.urlopen(req, timeout=600) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            return resp.status, (json.loads(raw) if raw.strip() else {})
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"http {method} {url} failed: {e.code} {raw}") from e


def coddy_base(base: str) -> str:
    return base[: -len("/v1")] if base.endswith("/v1") else base


def read_events(path: Path) -> list[dict[str, Any]]:
    if not path.is_file():
        return []
    out: list[dict[str, Any]] = []
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            out.append(json.loads(line))
        except json.JSONDecodeError:
            continue
    return out


def install_hooks(home: Path, work: Path, events: Path, marker: Path) -> None:
    hooks_dir = home / "hooks"
    hooks_dir.mkdir(parents=True, exist_ok=True)
    recorder = hooks_dir / "record.sh"
    recorder.write_text(
        "#!/bin/sh\n"
        f'cat >> "{events}"\n'
        f'printf "\\n" >> "{events}"\n'
        "exit 0\n",
        encoding="utf-8",
    )
    recorder.chmod(0o755)
    (home / "hooks.json").write_text(
        json.dumps(
            {
                "hooks": {
                    "PreToolUse": [
                        {"matcher": "run_command", "hooks": [{"type": "command", "command": str(recorder)}]}
                    ],
                    "PostToolUse": [{"hooks": [{"type": "command", "command": str(recorder)}]}],
                }
            },
            indent=2,
        ),
        encoding="utf-8",
    )
    project = work / ".coddy"
    project.mkdir(parents=True, exist_ok=True)
    (project / "hooks.json").write_text(
        json.dumps(
            {
                "hooks": {
                    "PreToolUse": [
                        {
                            "matcher": "run_command",
                            "hooks": [{"type": "command", "command": f'printf "ran\\n" >> "{marker}"'}],
                        }
                    ]
                }
            },
            indent=2,
        ),
        encoding="utf-8",
    )


def catalog_entry(coddy: str, work: str, file: str) -> dict[str, Any]:
    code, catalog = http_json("GET", f"{coddy}/coddy/hooks?cwd={urllib.parse.quote(work)}", None, {})
    if code != 200:
        raise RuntimeError(f"bad hooks catalog code {code}")
    for row in catalog.get("items") or []:
        if isinstance(row, dict) and row.get("file") == file:
            return row
    raise RuntimeError(f"hooks catalog does not list {file}: {[r.get('file') for r in catalog.get('items') or []]}")


def run_turn(base: str, profile: str, yaml_model: str, headers: dict[str, str], marker: str, work: str) -> str:
    prompt = (
        f"All work MUST stay inside this directory tree: {work}\n"
        "Do exactly this, autonomously and without asking questions.\n"
        f"1. Call run_command ONCE with the command: echo {marker}\n"
        "2. Reply with one short sentence that repeats what the command printed, then stop.\n"
        "Do not call any other tool. Do not run the command a second time."
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
        raise RuntimeError(f"bad chat completion code {code}")
    choices = completion.get("choices") or []
    return str(((choices[0] if choices else {}).get("message") or {}).get("content") or "")


def main() -> int:
    base = os.environ.get("BASE_URL", "http://127.0.0.1:19876/v1").rstrip("/")
    coddy = coddy_base(base)
    yaml_model = os.environ.get("MODEL", "rpa/gpt-oss:120b").strip()
    profile = os.environ.get("CODDY_CHAT_PROFILE", "agent").strip()
    work = os.environ.get("WORK_DIR", "").strip()
    home = os.environ.get("CODDY_HOME", "").strip()
    if not work or not Path(work).is_dir():
        print("WORK_DIR required (the workspace the server was started with)", file=sys.stderr)
        return 2
    if not home or not Path(home).is_dir():
        print("CODDY_HOME required (the server home the user-scope hooks file goes to)", file=sys.stderr)
        return 2
    headers = {"X-Coddy-Session-ID": SESSION_ID}
    events = Path(home) / "hook-events.jsonl"
    marker = Path(work) / "project-hook.marker"
    install_hooks(Path(home), Path(work), events, marker)

    # Before any turn: the operator's file is trusted, the project file is held.
    user_entry = catalog_entry(coddy, work, str(Path(home) / "hooks.json"))
    if user_entry.get("scope") != "user" or user_entry.get("trusted") is not True:
        print(f"user-scope file must be listed as trusted: {user_entry}", file=sys.stderr)
        return 1
    project_entry = catalog_entry(coddy, work, PROJECT_FILE)
    if project_entry.get("scope") != "project" or project_entry.get("needs_approval") is not True:
        print(f"project file must be listed as needs_approval: {project_entry}", file=sys.stderr)
        return 1
    if not any(h.get("event") == "PreToolUse" and h.get("matcher") == "run_command" for h in project_entry.get("hooks") or []):
        print(f"project entry does not list its PreToolUse hook: {project_entry}", file=sys.stderr)
        return 1

    # Turn 1: the recorder hooks run, the project hook does not.
    reply = run_turn(base, profile, yaml_model, headers, MARKER_ONE, work)
    if MARKER_ONE not in reply:
        print(f"reply does not repeat {MARKER_ONE!r}: {reply[:300]!r}", file=sys.stderr)
        return 1
    recorded = read_events(events)
    pre = [e for e in recorded if e.get("hook_event_name") == "PreToolUse" and e.get("tool_name") == "run_command"]
    post = [e for e in recorded if e.get("hook_event_name") == "PostToolUse" and e.get("tool_name") == "run_command"]
    if not pre or not post:
        print(f"recorder hooks did not see run_command (pre={len(pre)} post={len(post)}, events={len(recorded)})", file=sys.stderr)
        return 1
    if pre[0].get("session_id") != SESSION_ID or MARKER_ONE not in str((pre[0].get("tool_input") or {}).get("command", "")):
        print(f"PreToolUse payload lacks the session or the command: {json.dumps(pre[0])[:300]}", file=sys.stderr)
        return 1
    if MARKER_ONE not in str(post[0].get("tool_response", "")):
        print(f"PostToolUse payload lacks the command output: {post[0].get('tool_response')!r}", file=sys.stderr)
        return 1
    if marker.exists():
        print("the unapproved project hook ran (marker exists)", file=sys.stderr)
        return 1

    # The held file is reported once in the transcript as a notice row.
    code, transcript = http_json("GET", f"{coddy}/coddy/sessions/{SESSION_ID}/messages", None, headers)
    if code != 200:
        print("bad messages code", code, file=sys.stderr)
        return 1
    notices = [
        row for row in (transcript.get("uiLog") or [])
        if isinstance(row, dict) and row.get("level") == "notice" and PROJECT_FILE in str(row.get("message") or "")
    ]
    if len(notices) != 1:
        print(f"expected one notice row about {PROJECT_FILE}, got {len(notices)}: {transcript.get('uiLog')}", file=sys.stderr)
        return 1

    # Approve the project file through the route a remote client would use.
    code, approved = http_json("POST", f"{coddy}/coddy/hooks/trust", {"cwd": work, "file": PROJECT_FILE}, {})
    if code != 200 or (approved.get("item") or {}).get("trusted") is not True:
        print(f"trust route did not report trusted: {code} {approved}", file=sys.stderr)
        return 1
    if catalog_entry(coddy, work, PROJECT_FILE).get("trust") != "trusted":
        print("catalog still lists the project file as not trusted after the approval", file=sys.stderr)
        return 1

    # Turn 2 in the same session: the approved hook runs.
    reply = run_turn(base, profile, yaml_model, headers, MARKER_TWO, work)
    if MARKER_TWO not in reply:
        print(f"second reply does not repeat {MARKER_TWO!r}: {reply[:300]!r}", file=sys.stderr)
        return 1
    if not marker.exists():
        print("the approved project hook did not run (no marker)", file=sys.stderr)
        return 1

    # Withdrawing the receipt puts the file back on hold.
    code, revoked = http_json("POST", f"{coddy}/coddy/hooks/untrust", {"cwd": work, "file": PROJECT_FILE}, {})
    if code != 200 or (revoked.get("item") or {}).get("needs_approval") is not True:
        print(f"untrust route did not report needs_approval: {code} {revoked}", file=sys.stderr)
        return 1

    print(f"ok http hooks e2e ({len(recorded)} recorded payloads)")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except RuntimeError as e:
        print(str(e), file=sys.stderr)
        sys.exit(1)
