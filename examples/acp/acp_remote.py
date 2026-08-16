#!/usr/bin/env python3
"""ACP e2e: ``coddy acp --remote`` proxies the protocol to a remote coddy http server.

Boots a bearer-protected ``coddy http`` from the same binary, then drives
``coddy acp --remote <url> --remote-token <token>`` over stdio JSON-RPC:
initialize, session/new, session/prompt. The streamed agent_message_chunk
updates must carry the marker answer, and the session must persist on the
server side only.
"""

from __future__ import annotations

import json
import os
import secrets
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
DEFAULT_MODEL = os.environ.get("CODDY_E2E_MODEL", "neuraldeep/qwen3.8-27b")


def jd(obj: dict[str, Any]) -> str:
    return json.dumps(obj, separators=(",", ":"), ensure_ascii=False)


def coddy_bin() -> str:
    override = os.environ.get("CODDY_BIN")
    if override:
        return override
    candidate = REPO_ROOT / "build" / "coddy"
    if candidate.exists():
        return str(candidate)
    found = shutil.which("coddy")
    if found:
        return found
    raise SystemExit("no coddy binary: build it or set CODDY_BIN")


def prepare_home(name: str) -> Path:
    home = Path(tempfile.mkdtemp(prefix=f"coddy-acp-{name}-home-"))
    template = (REPO_ROOT / "examples" / "config.demo.yaml").read_text()
    resolved = template.replace("__E2E_LOG_PATH__", str(home / "e2e.log"))
    resolved = resolved.replace(
        'model: "rpa/gpt-oss:120b"\n  max_turns', f'model: "{DEFAULT_MODEL}"\n  max_turns'
    )
    resolved = resolved.replace(
        'memory:\n  enabled: true\n  model: "rpa/gpt-oss:120b"',
        f'memory:\n  enabled: true\n  model: "{DEFAULT_MODEL}"',
    )
    (home / "config.yaml").write_text(resolved)
    (home / "sessions").mkdir(exist_ok=True)
    (home / "skills_fixture").mkdir(exist_ok=True)
    if os.environ.get("NEURALDEEP_API_KEY"):
        (home / ".env").write_text(f"NEURALDEEP_API_KEY={os.environ['NEURALDEEP_API_KEY']}\n")
    else:
        global_env = Path.home() / ".coddy" / ".env"
        if global_env.exists():
            for line in global_env.read_text().splitlines():
                if line.startswith("NEURALDEEP_API_KEY="):
                    (home / ".env").write_text(line + "\n")
                    break
    return home


def free_port() -> int:
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def wait_models(base: str, token: str, timeout: float = 45.0) -> None:
    deadline = time.time() + timeout
    last = "no attempt"
    while time.time() < deadline:
        try:
            req = urllib.request.Request(base + "/v1/models", headers={"Authorization": f"Bearer {token}"})
            with urllib.request.urlopen(req, timeout=3) as res:
                if res.status == 200:
                    return
                last = f"status {res.status}"
        except urllib.error.HTTPError as exc:
            raise SystemExit(f"remote auth probe failed hard: {exc}")
        except Exception as exc:  # noqa: BLE001 - connection refused while booting
            last = str(exc)
        time.sleep(0.3)
    raise SystemExit(f"coddy http did not come up: {last}")


def session_dirs(home: Path) -> set[str]:
    root = home / "sessions"
    if not root.exists():
        return set()
    return {p.name for p in root.iterdir() if p.is_dir()}


def rpc(proc: subprocess.Popen, method: str, params: dict[str, Any], next_id: list[int], chunks: list[str]) -> dict[str, Any]:
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
            if upd.get("sessionUpdate") == "agent_message_chunk":
                content = upd.get("content") or {}
                if content.get("type") == "text":
                    chunks.append(content.get("text", ""))
            continue
        if msg.get("id") == rid:
            return msg


def main() -> int:
    token = secrets.token_urlsafe(24)
    port = free_port()
    base = f"http://127.0.0.1:{port}"
    server_home = prepare_home("remote-srv")
    client_home = prepare_home("remote-cli")

    env_srv = dict(os.environ)
    env_srv["CODDY_HOME"] = str(server_home)
    server = subprocess.Popen(
        [coddy_bin(), "http", "-H", "127.0.0.1", "-P", str(port), "--auth-token", token],
        env=env_srv,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    acp = None
    try:
        wait_models(base, token)
        print(f"[acp-remote] server up at {base}")

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

        init = rpc(acp, "initialize", {"protocolVersion": 1, "clientInfo": {"name": "acp-remote-e2e"}}, next_id, chunks)
        title = ((init.get("result") or {}).get("agentInfo") or {}).get("title", "")
        if "remote" not in title.lower():
            raise SystemExit(f"initialize must flag remote mode in the agent title, got {title!r}")

        new = rpc(acp, "session/new", {"cwd": ""}, next_id, chunks)
        sid = (new.get("result") or {}).get("sessionId", "")
        if not sid:
            raise SystemExit(f"session/new failed: {new}")
        opts = (new.get("result") or {}).get("configOptions") or []
        model_opt = next((o for o in opts if o.get("id") == "model"), None)
        if not model_opt or not model_opt.get("options"):
            raise SystemExit(f"session/new must list the remote model catalog, got {opts}")
        print(f"[acp-remote] session {sid} with {len(model_opt['options'])} remote models")

        res = rpc(
            acp,
            "session/prompt",
            {"sessionId": sid, "prompt": [{"type": "text", "text": "Reply with exactly: ACP_REMOTE_OK"}]},
            next_id,
            chunks,
        )
        stop = (res.get("result") or {}).get("stopReason", "")
        if stop != "end_turn":
            raise SystemExit(f"stopReason {stop!r}, chunks={chunks[-3:]}")
        if "ACP_REMOTE_OK" not in "".join(chunks):
            raise SystemExit(f"answer marker missing in streamed chunks: {chunks[-5:]}")
        if session_dirs(client_home):
            raise SystemExit(f"client home grew sessions: {session_dirs(client_home)}")
        if sid not in session_dirs(server_home):
            raise SystemExit(f"session {sid} not persisted on the server: {sorted(session_dirs(server_home))}")
        print("[acp-remote] prompt streamed remotely, persisted server-side only")
        print("acp_remote: OK")
        return 0
    finally:
        if acp is not None:
            acp.terminate()
            try:
                acp.wait(timeout=5)
            except subprocess.TimeoutExpired:
                acp.kill()
        server.terminate()
        try:
            server.wait(timeout=10)
        except subprocess.TimeoutExpired:
            server.kill()


if __name__ == "__main__":
    sys.exit(main())
