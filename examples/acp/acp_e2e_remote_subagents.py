#!/usr/bin/env python3
"""ACP e2e: a subagent spawned on a remote coddy http server, driven through ``coddy acp --remote``.

Boots a bearer-protected ``coddy http`` whose working directory carries the
project-scope ``marker-reporter`` definition (``examples/agents_fixture``),
approves that definition on the SERVER through ``POST /coddy/subagents/{name}/trust``
(the receipt lives in the server home: ``coddy agents trust`` on the client
machine would approve nothing), then drives ``coddy acp --remote`` and asks a
real model to delegate reading a marker file to the subagent.

Verifies:

- the ``spawn_agent`` tool call streams back to the remote ACP client
- the parent's answer repeats the marker the child reported
- the child session (``sub_*``) is persisted on the server, linked to the
  parent, and nothing is persisted in the client home

Environment: CODDY_BIN, CODDY_E2E_MODEL (default neuraldeep/qwen3.8-27b).
"""

from __future__ import annotations

import json
import os
import secrets
import shutil
import subprocess
import sys
import tempfile
import urllib.request
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))
from acp_remote import REPO_ROOT, coddy_bin, free_port, jd, prepare_home, session_dirs, wait_models  # noqa: E402

AGENT = "marker-reporter"
MARKER = "MARKER: coddy-subagent-e2e-acp-remote"


def rpc_collect(proc: subprocess.Popen, method: str, params: dict[str, Any], next_id: list[int], chunks: list[str], tools: list[str]) -> dict[str, Any]:
    """rpc() from acp_remote plus tool call names from session/update frames."""
    rid = next_id[0]
    next_id[0] += 1
    assert proc.stdin is not None and proc.stdout is not None
    proc.stdin.write(jd({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}) + "\n")
    proc.stdin.flush()
    while True:
        line = proc.stdout.readline()
        if not line:
            raise RuntimeError("unexpected EOF from coddy acp stdout")
        line = line.strip()
        if not line:
            continue
        msg = json.loads(line)
        if msg.get("method") == "session/request_permission":
            proc.stdin.write(jd({"jsonrpc": "2.0", "id": msg.get("id"), "result": {"outcome": "allow"}}) + "\n")
            proc.stdin.flush()
            continue
        if msg.get("method") == "session/update":
            upd = (msg.get("params") or {}).get("update") or {}
            kind = upd.get("sessionUpdate")
            if kind == "agent_message_chunk":
                content = upd.get("content") or {}
                if content.get("type") == "text":
                    chunks.append(content.get("text", ""))
            elif kind == "tool_call":
                tools.append(str(upd.get("title", "")))
            continue
        if msg.get("id") == rid:
            return msg


def post_json(base: str, token: str, path: str, body: dict[str, Any]) -> tuple[int, dict[str, Any]]:
    req = urllib.request.Request(
        base + path,
        data=json.dumps(body).encode(),
        headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=15) as res:
            return res.status, json.loads(res.read() or b"{}")
    except urllib.error.HTTPError as exc:  # type: ignore[attr-defined]
        return exc.code, json.loads(exc.read() or b"{}")


def main() -> int:
    token = secrets.token_urlsafe(24)
    port = free_port()
    base = f"http://127.0.0.1:{port}"
    server_home = prepare_home("remote-sub-srv")
    client_home = prepare_home("remote-sub-cli")
    server_work = Path(tempfile.mkdtemp(prefix="coddy-acp-remote-sub-work-"))
    shutil.copytree(REPO_ROOT / "examples" / "agents_fixture" / ".coddy" / "agents", server_work / ".coddy" / "agents")
    (server_work / "notes.txt").write_text(MARKER + "\n", encoding="utf-8")

    env_srv = dict(os.environ)
    env_srv["CODDY_HOME"] = str(server_home)
    server = subprocess.Popen(
        [coddy_bin(), "http", "-H", "127.0.0.1", "-P", str(port), "--auth-token", token, "--cwd", str(server_work)],
        env=env_srv,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    acp = None
    try:
        wait_models(base, token)
        print(f"[acp-remote-subagents] server up at {base}, workspace {server_work}")

        # The receipt is written on the server: that is where the manager runs.
        status, body = post_json(base, token, f"/coddy/subagents/{AGENT}/trust", {"cwd": str(server_work)})
        item = body.get("item") or {}
        if status != 200 or not item.get("trusted"):
            raise SystemExit(f"remote trust failed: {status} {body}")
        if not (server_home / "subagents-trust.json").exists():
            raise SystemExit("the trust receipt must live in the server home")
        print(f"[acp-remote-subagents] {AGENT} approved on the server (digest {item.get('digest', '')[:12]}…)")

        env_cli = dict(os.environ)
        env_cli["CODDY_HOME"] = str(client_home)
        acp = subprocess.Popen(
            [coddy_bin(), "acp", "--remote", base, "--remote-token", token],
            env=env_cli,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
        )
        next_id = [1]
        chunks: list[str] = []
        tools: list[str] = []
        rpc_collect(acp, "initialize", {"protocolVersion": 1, "clientInfo": {"name": "acp-remote-subagents-e2e"}}, next_id, chunks, tools)
        new = rpc_collect(acp, "session/new", {"cwd": ""}, next_id, chunks, tools)
        sid = (new.get("result") or {}).get("sessionId", "")
        if not sid:
            raise SystemExit(f"session/new failed: {new}")

        prompt = f"""You have a subagent named "{AGENT}". Do exactly this:

1. Call spawn_agent ONCE with agent "{AGENT}", description "read the marker", background false, and this prompt:
   Read the file {server_work}/notes.txt and reply with exactly the line that starts with MARKER:.

2. Reply with one short sentence that repeats the marker line the subagent reported, verbatim, then stop.

Do not read notes.txt yourself. Do not call any other tool. Do not call spawn_agent a second time."""
        res = rpc_collect(acp, "session/prompt", {"sessionId": sid, "prompt": [{"type": "text", "text": prompt}]}, next_id, chunks, tools)
        stop = (res.get("result") or {}).get("stopReason", "")
        text = "".join(chunks)
        if stop != "end_turn":
            raise SystemExit(f"stopReason {stop!r}, tools={tools}, text={text[-300:]!r}")
        if not any("spawn_agent" in t for t in tools):
            raise SystemExit(f"spawn_agent never streamed back to the remote client; tool calls seen: {tools}")
        if MARKER not in text:
            raise SystemExit(f"parent reply does not repeat {MARKER!r}: {text[-300:]!r}")

        if session_dirs(client_home):
            raise SystemExit(f"client home grew sessions: {session_dirs(client_home)}")
        children = [d for d in session_dirs(server_home) if d.startswith("sub_")]
        if not children:
            raise SystemExit(f"no child bundle on the server: {sorted(session_dirs(server_home))}")
        linked = []
        for child in children:
            meta = json.loads((server_home / "sessions" / child / "session.json").read_text())
            if meta.get("parentSessionId") == sid and meta.get("subagentRun"):
                linked.append(child)
        if not linked:
            raise SystemExit(f"no child bundle links back to {sid}: {children}")
        print(f"[acp-remote-subagents] child {linked[0]} persisted on the server under parent {sid}")
        print("ok acp remote subagents e2e")
        return 0
    finally:
        if acp is not None:
            try:
                acp.stdin.close()  # type: ignore[union-attr]
            except Exception:  # noqa: BLE001
                pass
            acp.terminate()
        server.terminate()
        try:
            server.wait(timeout=10)
        except subprocess.TimeoutExpired:
            server.kill()


if __name__ == "__main__":
    sys.exit(main())
