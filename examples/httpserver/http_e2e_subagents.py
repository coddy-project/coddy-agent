#!/usr/bin/env python3
"""End-to-end check of subagents over the HTTP surface.

Copies ``examples/agents_fixture/.coddy/agents`` (the project-scope
``marker-reporter`` definition) into the server workspace, approves it through
``POST /coddy/subagents/{name}/trust`` (``subagents.project_trust`` defaults to
``ask``), writes a marker file, and asks a real model to delegate reading that
file to the subagent with ``spawn_agent``. Then it drives the REST surface the
tasks drawer and the transcript view use: the agent task row and its child
session id, the read-only child transcript with its parent link, the sessions
list with and without ``include_subagents``, and the subagent catalog with the
bounds each definition declares.

A second phase covers a detached child's permission prompt: the fixture
``echo-runner`` (``permission_mode: ask``, ``background: true``) is approved
and asked to run a command in the background, the parent's own stream is read
to its end without answering anything, and the prompt is then found on the
agent task row as ``pending_permission``, replayed by ``GET /coddy/events`` as
``subagent_permission``, answered with ``POST /coddy/sessions/{child}/permission``
against the child session, and announced settled; the run ends ``succeeded``
with the command's output in its report.

Environment:

- ``BASE_URL`` - OpenAI-compatible base (default ``http://127.0.0.1:19876/v1``),
  same as the other HTTP harnesses.
- ``MODEL`` - YAML ``models[].model`` id (default ``rpa/qwen3.6-35b-a3b``).
- ``CODDY_CHAT_PROFILE`` - session profile (default ``agent``).
- ``WORK_DIR`` - the workspace the server was started with (``--cwd``); the
  definition and the marker file are placed there.
- ``CODDY_HOME`` - optional; when set, the persisted bundles under
  ``$CODDY_HOME/sessions`` are checked on disk as well.

Exits non-zero on any HTTP error or unmet expectation.
"""

from __future__ import annotations

import json
import os
import shutil
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any, Iterable, Tuple

SESSION_ID = "http-e2e-subagents"
AGENT = "marker-reporter"
MARKER = "MARKER: coddy-subagent-e2e-http"
REPORT_HEADER = "=== subagent report ==="
# The detached-prompt phase runs in a session of its own, so its task rows are
# not mixed with the marker run's.
DETACHED_SESSION_ID = "http-e2e-subagents-detached"
DETACHED_AGENT = "echo-runner"


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


def sse_events(resp: Any) -> Iterable[Tuple[str, str]]:
    """Yield (event, data) pairs from a text/event-stream response."""
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


def coddy_base(base: str) -> str:
    """Turn the /v1 base into the /coddy base the drawer endpoints live under."""
    return base[: -len("/v1")] if base.endswith("/v1") else base


def catalog_rows(payload: Any) -> list[dict[str, Any]]:
    """Accept the catalog as a bare list or under the usual list keys."""
    if isinstance(payload, list):
        return [r for r in payload if isinstance(r, dict)]
    if isinstance(payload, dict):
        for key in ("data", "items", "subagents", "agents"):
            rows = payload.get(key)
            if isinstance(rows, list):
                return [r for r in rows if isinstance(r, dict)]
    return []


def session_ids(coddy: str, query: str) -> list[str]:
    ids: list[str] = []
    cursor = ""
    while True:
        url = f"{coddy}/coddy/sessions?limit=100{query}"
        if cursor:
            url += f"&cursor={urllib.parse.quote(cursor)}"
        code, page = http_json("GET", url, None, {})
        if code != 200:
            raise RuntimeError(f"bad sessions list code {code}")
        ids.extend(str(s.get("id") or "") for s in (page.get("sessions") or []))
        nxt = page.get("nextCursor")
        if not page.get("hasMore") or not nxt:
            return ids
        cursor = str(nxt)


def transcript_text(msgs: list[dict[str, Any]]) -> str:
    return "\n".join(str(m.get("content") or "") for m in msgs if isinstance(m, dict))


def has_tool_call(msgs: list[dict[str, Any]], name: str) -> bool:
    for m in msgs:
        if not isinstance(m, dict):
            continue
        for tc in m.get("tool_calls") or []:
            fn = (tc or {}).get("function") or {}
            if fn.get("name") == name:
                return True
    return False


def last_assistant_text(msgs: list[dict[str, Any]]) -> str:
    for m in reversed(msgs):
        if isinstance(m, dict) and m.get("role") == "assistant" and str(m.get("content") or "").strip():
            return str(m.get("content"))
    return ""


def bundle_messages_text(session_dir: Path) -> str:
    path = session_dir / "messages.json"
    if not path.is_file():
        return ""
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return ""
    msgs = data.get("messages", []) if isinstance(data, dict) else data
    return transcript_text(msgs)


def check_on_disk(home: str, task_id: str, child_id: str) -> bool:
    """Persisted bundle checks; only when the server home is known."""
    root = Path(home) / "sessions"
    if not root.is_dir():
        print(f"skipping on-disk checks: {root} is not a directory", file=sys.stderr)
        return True

    meta_path = root / SESSION_ID / "background" / task_id / "meta.json"
    if not meta_path.is_file():
        print(f"persisted task record missing at {meta_path}", file=sys.stderr)
        return False
    snap = json.loads(meta_path.read_text(encoding="utf-8"))
    if snap.get("kind") != "agent" or (snap.get("agent") or {}).get("session_id") != child_id:
        print(f"persisted task record is not the agent run: {snap}", file=sys.stderr)
        return False

    # A session spawned by another one is stored inside it, so the bundle is
    # under the parent's own folder rather than beside it in the sessions root.
    child_dir = root / SESSION_ID / "subagents" / child_id
    session_json = child_dir / "session.json"
    if not session_json.is_file():
        print(f"child session bundle missing at {child_dir}", file=sys.stderr)
        return False
    if (root / child_id).exists():
        print(f"the sessions root holds a folder of its own for the child: {root / child_id}", file=sys.stderr)
        return False
    meta = json.loads(session_json.read_text(encoding="utf-8"))
    if meta.get("subagentRun") is not True or meta.get("parentSessionId") != SESSION_ID:
        print(f"child session.json lacks the parent link: {meta}", file=sys.stderr)
        return False
    if MARKER not in bundle_messages_text(child_dir):
        print(f"child messages.json does not contain {MARKER!r}", file=sys.stderr)
        return False
    return True


def main() -> int:
    base = os.environ.get("BASE_URL", "http://127.0.0.1:19876/v1").rstrip("/")
    coddy = coddy_base(base)
    yaml_model = os.environ.get("MODEL", "rpa/qwen3.6-35b-a3b").strip()
    profile = os.environ.get("CODDY_CHAT_PROFILE", "agent").strip()
    work = os.environ.get("WORK_DIR", "").strip()
    home = os.environ.get("CODDY_HOME", "").strip()
    if not work:
        print("WORK_DIR required (the workspace the server was started with)", file=sys.stderr)
        return 2
    work_p = Path(work)
    if not work_p.is_dir():
        print("WORK_DIR must be an existing directory", file=sys.stderr)
        return 2
    tasks_url = f"{coddy}/coddy/sessions/{SESSION_ID}/background-tasks"
    headers = {"X-Coddy-Session-ID": SESSION_ID}

    examples_dir = Path(__file__).resolve().parent.parent
    agents_src = examples_dir / "agents_fixture" / ".coddy" / "agents"
    agents_dst = work_p / ".coddy" / "agents"
    agents_dst.parent.mkdir(parents=True, exist_ok=True)
    shutil.copytree(agents_src, agents_dst, dirs_exist_ok=True)
    (work_p / "notes.txt").write_text(MARKER + "\n", encoding="utf-8")

    # The project definition arrived with the checkout, so the default ask
    # policy refuses to spawn it until a receipt exists for this workspace.
    code, trusted = http_json(
        "POST", f"{coddy}/coddy/subagents/{AGENT}/trust", {"cwd": work}, {}
    )
    if code != 200:
        print("bad subagent trust code", code, file=sys.stderr)
        return 1
    # The route answers with the catalog entry (under "item") as it stands
    # after the approval.
    receipt = trusted.get("item") if isinstance(trusted.get("item"), dict) else trusted
    if receipt.get("trusted") is not True:
        print(f"trust route did not report trusted: true ({trusted})", file=sys.stderr)
        return 1

    code, catalog = http_json(
        "GET", f"{coddy}/coddy/subagents?cwd={urllib.parse.quote(work)}", None, {}
    )
    if code != 200:
        print("bad subagent catalog code", code, file=sys.stderr)
        return 1
    rows = catalog_rows(catalog)
    entry = next((r for r in rows if r.get("name") == AGENT), None)
    if entry is None:
        print(f"catalog does not list {AGENT}: {[r.get('name') for r in rows]}", file=sys.stderr)
        return 1
    if entry.get("trust") != "trusted":
        print(f"catalog lists {AGENT} with trust {entry.get('trust')!r}, want trusted", file=sys.stderr)
        return 1
    # The catalog carries what the definition declares, so a surface can show
    # what it is approving: the fixture narrows to three read-only tools and
    # leaves the rest to inherit, which the row reports by leaving it out.
    if entry.get("tools") != ["read", "glob", "grep"]:
        print(f"catalog row for {AGENT} does not carry its tools: {entry.get('tools')!r}", file=sys.stderr)
        return 1
    for absent in ("permission_mode", "disallowed_tools", "background"):
        if entry.get(absent):
            print(f"catalog row for {AGENT} declares {absent}={entry.get(absent)!r}, but the file does not", file=sys.stderr)
            return 1
    if not int(entry.get("role_bytes") or 0) > 0:
        print(f"catalog row for {AGENT} reports no role_bytes: {entry}", file=sys.stderr)
        return 1

    prompt = (
        "Do exactly this, autonomously and without asking questions.\n"
        f"1. Call spawn_agent ONCE with agent \"{AGENT}\", description \"read the marker\", "
        "background false, and this prompt:\n"
        f"   Read the file {work}/notes.txt and reply with exactly the line that starts with MARKER:.\n"
        "   Wait for the tool to return the child's report.\n"
        "2. Reply with one short sentence that repeats the marker line the subagent reported, verbatim, then stop.\n"
        "Do not read notes.txt yourself. Do not call any other tool. Do not call spawn_agent a second time."
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
    choices = completion.get("choices") or []
    reply = str(((choices[0] if choices else {}).get("message") or {}).get("content") or "")
    if MARKER not in reply:
        print(f"parent reply does not repeat {MARKER!r}: {reply[:300]!r}", file=sys.stderr)
        return 1

    # The run is a task of the parent session, so the drawer endpoint lists it
    # as an agent task naming the child session that holds the transcript.
    code, listing = http_json("GET", tasks_url, None, headers)
    if code != 200:
        print("bad background task list code", code, file=sys.stderr)
        return 1
    agent_rows = [r for r in (listing.get("data") or []) if r.get("kind") == "agent"]
    if not agent_rows:
        print("model did not start a subagent task", file=sys.stderr)
        return 1
    task = agent_rows[-1]
    task_id = str(task.get("id") or "")
    agent = task.get("agent") or {}
    child_id = str(agent.get("session_id") or "")
    if not task_id or not child_id or agent.get("name") != AGENT:
        print(f"agent task row does not name a child session for {AGENT}: {task}", file=sys.stderr)
        return 1

    final: dict[str, Any] = {}
    output = ""
    deadline = time.time() + 240
    while time.time() < deadline:
        code, single = http_json("GET", f"{tasks_url}/{task_id}", None, headers)
        if code != 200:
            print("bad background task get code", code, file=sys.stderr)
            return 1
        final = single.get("task") or {}
        output = str(single.get("output") or "")
        if final.get("running") is False:
            break
        time.sleep(0.5)
    status = str(final.get("status") or "")
    if status != "succeeded":
        print(f"task {task_id} ended as {status!r}, want succeeded", file=sys.stderr)
        return 1
    if REPORT_HEADER not in output or MARKER not in output.split(REPORT_HEADER)[-1]:
        print(f"task output has no report block carrying {MARKER!r}: {output[-400:]!r}", file=sys.stderr)
        return 1

    # Parent transcript: the spawn is an ordinary tool call row and the final
    # answer repeats what the child reported.
    code, parent = http_json("GET", f"{coddy}/coddy/sessions/{SESSION_ID}/messages", None, headers)
    if code != 200:
        print("bad parent messages code", code, file=sys.stderr)
        return 1
    parent_msgs = parent.get("messages") or []
    if not has_tool_call(parent_msgs, "spawn_agent"):
        print("parent transcript has no spawn_agent tool call", file=sys.stderr)
        return 1
    if MARKER not in last_assistant_text(parent_msgs):
        print("parent transcript's final assistant text lacks the marker", file=sys.stderr)
        return 1

    # Child transcript: readable, read-only, linked to the parent.
    code, child = http_json("GET", f"{coddy}/coddy/sessions/{child_id}/messages", None, {})
    if code != 200:
        print("bad child messages code", code, file=sys.stderr)
        return 1
    if child.get("readOnly") is not True:
        print(f"child transcript is not flagged readOnly: {child.get('readOnly')!r}", file=sys.stderr)
        return 1
    link = child.get("subagent") or {}
    if link.get("parentSessionId") != SESSION_ID:
        print(f"child transcript does not link to the parent: {link}", file=sys.stderr)
        return 1
    if MARKER not in transcript_text(child.get("messages") or []):
        print("child transcript does not contain the marker", file=sys.stderr)
        return 1

    # History hides child sessions unless asked for them.
    if child_id in session_ids(coddy, ""):
        print(f"default sessions list must not include the child {child_id}", file=sys.stderr)
        return 1
    if child_id not in session_ids(coddy, "&include_subagents=true"):
        print(f"sessions list with include_subagents=true must include {child_id}", file=sys.stderr)
        return 1

    if home and not check_on_disk(home, task_id, child_id):
        return 1

    detached = detached_permission_phase(base, coddy, yaml_model, profile, work)
    if detached is None:
        return 1

    print(f"ok http subagents e2e ({task_id} -> {child_id}; detached prompt {detached})")
    return 0


def detached_permission_phase(base: str, coddy: str, yaml_model: str, profile: str, work: str) -> str | None:
    """A background child narrowed to ``ask`` needs an answer after the parent's
    reply. Returns ``"<task> -> <child>"`` or None on any unmet expectation."""
    nonce = f"detached-e2e-{os.getpid()}-{int(time.time())}"
    headers = {"X-Coddy-Session-ID": DETACHED_SESSION_ID}
    tasks_url = f"{coddy}/coddy/sessions/{DETACHED_SESSION_ID}/background-tasks"

    code, trusted = http_json(
        "POST", f"{coddy}/coddy/subagents/{DETACHED_AGENT}/trust", {"cwd": work}, {}
    )
    if code != 200:
        print("bad subagent trust code for the detached fixture", code, file=sys.stderr)
        return None
    receipt = trusted.get("item") if isinstance(trusted.get("item"), dict) else trusted
    if receipt.get("trusted") is not True:
        print(f"trust route did not report the detached fixture trusted: {trusted}", file=sys.stderr)
        return None
    if receipt.get("permission_mode") != "ask" or receipt.get("background") is not True:
        print(f"catalog row for {DETACHED_AGENT} does not carry its declared bounds: {receipt}", file=sys.stderr)
        return None

    prompt = (
        "Do exactly this, autonomously and without asking questions.\n"
        f"1. Call spawn_agent ONCE with agent \"{DETACHED_AGENT}\", description \"run the command\", "
        "background true, and this prompt:\n"
        f"   Run the shell command `echo {nonce}` with the run_command tool, then reply with its output.\n"
        "2. Do not wait for the task: do not call background_wait, background_output or any other tool. "
        "Reply with the single word STARTED, then stop."
    )
    # The parent's own stream is read like a browser reads it, to its end and
    # without answering anything. A child that asks while the parent is still
    # replying arrives here as a permission event of the finished turn; ending
    # the turn on it is the order the detached prompt has to survive.
    body = {"model": profile, "stream": True, "input": prompt, "metadata": {"model": yaml_model}}
    req = urllib.request.Request(f"{base}/responses", data=json.dumps(body).encode("utf-8"), method="POST")
    req.add_header("Accept", "text/event-stream")
    req.add_header("Content-Type", "application/json")
    req.add_header("X-Coddy-Session-ID", DETACHED_SESSION_ID)
    completed = False
    failed = ""
    inline_prompts = 0
    try:
        with urllib.request.urlopen(req, timeout=600) as resp:
            for event, data in sse_events(resp):
                if event == "permission":
                    inline_prompts += 1
                elif event == "error":
                    failed = data[:300]
                elif event == "response.completed" or data.strip() == "[DONE]":
                    completed = True
                    break
    except urllib.error.HTTPError as e:
        print(f"parent stream failed: {e.code} {e.read().decode('utf-8', errors='replace')[:300]}", file=sys.stderr)
        return None
    if failed:
        print(f"the parent turn failed: {failed}", file=sys.stderr)
        return None
    if not completed:
        print("the parent stream ended without [DONE]", file=sys.stderr)
        return None

    # The prompt waits on the agent task row of the parent session, addressed
    # to the child session, with the ids a client needs to place and answer it.
    task: dict[str, Any] = {}
    pending: dict[str, Any] = {}
    deadline = time.time() + 240
    while time.time() < deadline:
        code, listing = http_json("GET", tasks_url, None, headers)
        if code != 200:
            print("bad background task list code", code, file=sys.stderr)
            return None
        rows = [
            r
            for r in (listing.get("data") or [])
            if r.get("kind") == "agent" and (r.get("agent") or {}).get("name") == DETACHED_AGENT
        ]
        for row in rows:
            p = row.get("pending_permission")
            if isinstance(p, dict) and str(p.get("sessionId") or "").strip() and str(((p.get("toolCall") or {}).get("toolCallId")) or "").strip():
                task, pending = row, p
                break
        if pending:
            break
        if rows and all(r.get("running") is False for r in rows):
            print(f"the detached run ended without asking: {rows[-1].get('status')!r}", file=sys.stderr)
            return None
        time.sleep(0.5)
    if not pending:
        print(f"no pending_permission appeared on the {DETACHED_AGENT} task row", file=sys.stderr)
        return None
    task_id = str(task.get("id") or "")
    child_id = str((task.get("agent") or {}).get("session_id") or "")
    tool_call_id = str((pending.get("toolCall") or {}).get("toolCallId") or "")
    if pending.get("sessionId") != child_id:
        print(f"pending_permission is addressed to {pending.get('sessionId')!r}, want the child {child_id!r}", file=sys.stderr)
        return None
    if pending.get("parent_session_id") != DETACHED_SESSION_ID or pending.get("task_id") != task_id or pending.get("agent_name") != DETACHED_AGENT:
        print(f"pending_permission does not name its parent, task and agent: {pending}", file=sys.stderr)
        return None
    title = str((pending.get("toolCall") or {}).get("title") or "")
    if not title.startswith(f"[subagent {DETACHED_AGENT}]"):
        print(f"pending_permission title does not name the subagent: {title!r}", file=sys.stderr)
        return None
    option_ids = [str(o.get("optionId") or "") for o in (pending.get("options") or [])]
    if "allow" not in option_ids or "reject" not in option_ids or any(o.startswith("allow_always") for o in option_ids):
        print(f"pending_permission offers {option_ids}, want allow and reject and no standing grant", file=sys.stderr)
        return None
    if not str(pending.get("asked_at") or "").strip():
        print(f"pending_permission carries no asked_at: {pending}", file=sys.stderr)
        return None

    # A client connecting now hears about the waiting prompt before ready, and
    # once it is answered - against the child session - hears it settled.
    ev = urllib.request.Request(f"{coddy}/coddy/events", method="GET")
    ev.add_header("Accept", "text/event-stream")
    with urllib.request.urlopen(ev, timeout=60) as resp:
        replayed = False
        for event, data in sse_events(resp):
            if event == "subagent_permission":
                frame = json.loads(data)
                if frame.get("phase") == "asked" and frame.get("childSessionId") == child_id and frame.get("toolCallId") == tool_call_id:
                    replayed = True
                    if frame.get("parentSessionId") != DETACHED_SESSION_ID or not isinstance(frame.get("request"), dict):
                        print(f"asked frame lacks the parent or the request: {frame}", file=sys.stderr)
                        return None
            elif event == "ready":
                break
        if not replayed:
            print("GET /coddy/events did not replay the waiting prompt before ready", file=sys.stderr)
            return None
        code, _ = http_json(
            "POST",
            f"{coddy}/coddy/sessions/{child_id}/permission",
            {"toolCallId": tool_call_id, "optionId": "allow"},
            {"X-Coddy-Session-ID": child_id},
        )
        if code != 204:
            print("bad permission answer code against the child session", code, file=sys.stderr)
            return None
        settled = False
        for event, data in sse_events(resp):
            if event != "subagent_permission":
                continue
            frame = json.loads(data)
            if frame.get("phase") == "settled" and frame.get("childSessionId") == child_id and frame.get("toolCallId") == tool_call_id:
                settled = True
                break
        if not settled:
            print("GET /coddy/events did not announce the prompt settled", file=sys.stderr)
            return None

    # Allowed, the child runs its command and the run ends with the output in
    # its report; the row no longer carries a prompt.
    final: dict[str, Any] = {}
    output = ""
    deadline = time.time() + 240
    while time.time() < deadline:
        code, single = http_json("GET", f"{tasks_url}/{task_id}", None, headers)
        if code != 200:
            print("bad background task get code", code, file=sys.stderr)
            return None
        final = single.get("task") or {}
        output = str(single.get("output") or "")
        if final.get("running") is False:
            break
        time.sleep(0.5)
    if final.get("status") != "succeeded":
        print(f"detached task {task_id} ended as {final.get('status')!r}, want succeeded: {output[-400:]!r}", file=sys.stderr)
        return None
    if final.get("pending_permission"):
        print(f"the answered prompt is still on the task row: {final.get('pending_permission')}", file=sys.stderr)
        return None
    if nonce not in output:
        print(f"task output does not carry the command output {nonce!r}: {output[-400:]!r}", file=sys.stderr)
        return None
    code, child = http_json("GET", f"{coddy}/coddy/sessions/{child_id}/messages", None, {})
    if code != 200:
        print("bad child messages code", code, file=sys.stderr)
        return None
    child_text = transcript_text(child.get("messages") or [])
    if "permission not granted" in child_text or "permission denied by user" in child_text:
        print("the child's command was refused although the prompt was allowed", file=sys.stderr)
        return None
    if nonce not in child_text:
        print(f"child transcript does not contain the command output {nonce!r}", file=sys.stderr)
        return None
    print(f"detached prompt: {inline_prompts} inline prompt(s) on the parent stream, answered on the task row", file=sys.stderr)
    return f"{task_id} -> {child_id}"


if __name__ == "__main__":
    raise SystemExit(main())
