#!/usr/bin/env python3
"""ACP e2e for the background wake (issue #305), against a real `coddy acp`.

Self-boots the scripted model of cmd/tgfake (examples/shared/wake_e2e_common.py),
so no key and no network. The model starts a failing command in the background
with notify_on_finish and ends its turn; `coddy acp` wakes it when the command
ends, outside any session/prompt the client sent.

Verifies what an editor receives:

- a `background_wake` session update naming the task (failed, exit 2), then
  the same wake as a quoted agent text line for editors that render only the
  standard updates, then the woken turn's answer;
- no `user_message_chunk` for the instruction nobody typed;
- with a model that fixes what woke it: `session/request_permission` reaches
  the client inside the woken turn, and the allowed command runs;
- `session/load` in a fresh process replays the woken turn's first message as
  the wake, not as a user message.

Environment: CODDY_BIN (default build/coddy); KEEP=1 leaves the stand's files.
"""

from __future__ import annotations

import json
import os
import queue
import subprocess
import sys
import threading
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "shared"))

from wake_e2e_common import (  # noqa: E402
    ANSWER_FIXED,
    ANSWER_STARTED,
    ANSWER_WOKEN,
    FIX_COMMAND,
    START_PROMPT,
    WAKE_INSTRUCTION,
    WAKE_TITLE,
    WakeStand,
    coddy_bin,
)


def fail(msg: str) -> None:
    print("FAIL:", msg, file=sys.stderr)
    raise SystemExit(1)


class ACPClient:
    """Newline-delimited JSON-RPC over the pipes of one `coddy acp` process."""

    def __init__(self, stand: WakeStand) -> None:
        self.proc = subprocess.Popen(
            ["stdbuf", "-oL", "-eL", coddy_bin(), "acp", "--config", str(stand.config),
             "--home", str(stand.home), "--cwd", str(stand.work)],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        )
        self.lines: "queue.Queue[dict]" = queue.Queue()
        self.next_id = 0
        threading.Thread(target=self._read, daemon=True).start()

    def _read(self) -> None:
        for raw in self.proc.stdout:
            try:
                self.lines.put(json.loads(raw))
            except json.JSONDecodeError:
                continue

    def send(self, msg: dict) -> None:
        self.proc.stdin.write((json.dumps(msg) + "\n").encode())
        self.proc.stdin.flush()

    def request(self, method: str, params: dict) -> int:
        self.next_id += 1
        self.send({"jsonrpc": "2.0", "id": self.next_id, "method": method, "params": params})
        return self.next_id

    def next(self, timeout: float) -> dict | None:
        try:
            return self.lines.get(timeout=timeout)
        except queue.Empty:
            return None

    def close(self) -> None:
        if self.proc.poll() is None:
            self.proc.stdin.close()
            try:
                self.proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.proc.kill()


def same_id(a, b) -> bool:
    try:
        return float(a) == float(b)
    except (TypeError, ValueError):
        return a == b


def call(client: ACPClient, method: str, params: dict, updates: list, on_permission=None, timeout: float = 60) -> dict:
    """Send one request and read until its response, keeping every update."""
    rid = client.request(method, params)
    deadline = time.time() + timeout
    while time.time() < deadline:
        msg = client.next(0.5)
        if msg is None:
            continue
        if msg.get("method") == "session/update":
            updates.append(msg["params"]["update"])
        elif msg.get("method") == "session/request_permission" and "id" in msg:
            answer = on_permission(msg["params"]) if on_permission else "reject"
            client.send({"jsonrpc": "2.0", "id": msg["id"], "result": {"outcome": {"outcome": "selected", "optionId": answer}}})
        elif "id" in msg and "method" not in msg and same_id(msg["id"], rid):
            if msg.get("error"):
                fail(f"{method} answered {msg['error']}")
            return msg.get("result") or {}
    fail(f"{method} never answered")
    return {}


def text_of(updates: list) -> str:
    return "".join(
        (u.get("content") or {}).get("text") or ""
        for u in updates
        if u.get("sessionUpdate") == "agent_message_chunk" and (u.get("content") or {}).get("type") == "text"
    )


def open_session(client: ACPClient, stand: WakeStand) -> str:
    call(client, "initialize", {"protocolVersion": 1, "clientCapabilities": {}}, [])
    res = call(client, "session/new", {"cwd": str(stand.work), "mcpServers": []}, [])
    return res["sessionId"]


def wait_woken_turn(client: ACPClient, updates: list, until: str, on_permission=None, timeout: float = 30) -> None:
    """Read the notifications of the turn nobody prompted until its answer is in."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if until in text_of(updates):
            # A little longer, for the last chunks of the same answer.
            end = time.time() + 1.0
            while time.time() < end:
                msg = client.next(0.2)
                if msg and msg.get("method") == "session/update":
                    updates.append(msg["params"]["update"])
            return
        msg = client.next(0.5)
        if msg is None:
            continue
        if msg.get("method") == "session/update":
            updates.append(msg["params"]["update"])
        elif msg.get("method") == "session/request_permission" and "id" in msg:
            answer = on_permission(msg["params"]) if on_permission else "reject"
            client.send({"jsonrpc": "2.0", "id": msg["id"], "result": {"outcome": {"outcome": "selected", "optionId": answer}}})
    fail(f"the woken turn never said {until!r}; text so far: {text_of(updates)!r}")


def check_wake(updates: list) -> None:
    wakes = [u for u in updates if u.get("sessionUpdate") == "background_wake"]
    if len(wakes) != 1:
        fail(f"background_wake updates = {wakes}")
    task = (wakes[0].get("tasks") or [{}])[0]
    if task.get("status") != "failed" or task.get("exitCode") != 2:
        fail(f"background_wake = {wakes[0]}")
    if f"> {WAKE_TITLE}:" not in text_of(updates):
        fail(f"no quoted wake note for editors: {text_of(updates)!r}")
    for u in updates:
        if u.get("sessionUpdate") == "user_message_chunk" and WAKE_INSTRUCTION in ((u.get("content") or {}).get("text") or ""):
            fail("the instruction nobody typed reached the editor as a user message")


def phase_note() -> None:
    stand = WakeStand("acp-note")
    client = ACPClient(stand)
    try:
        sid = open_session(client, stand)
        updates: list = []
        call(client, "session/prompt", {"sessionId": sid, "prompt": [{"type": "text", "text": START_PROMPT}]}, updates)
        if ANSWER_STARTED not in text_of(updates):
            fail(f"the first turn answered {text_of(updates)!r}")
        woken: list = []
        wait_woken_turn(client, woken, ANSWER_WOKEN)
        check_wake(woken)
        client.close()

        # A fresh editor loads the session: the wake is replayed as the wake.
        replay_client = ACPClient(stand)
        try:
            call(replay_client, "initialize", {"protocolVersion": 1, "clientCapabilities": {}}, [])
            replay: list = []
            call(replay_client, "session/load", {"sessionId": sid, "cwd": str(stand.work), "mcpServers": []}, replay)
            check_wake(replay)
        finally:
            replay_client.close()
    finally:
        client.close()
        stand.close(keep=bool(os.environ.get("KEEP")))


def phase_permission() -> None:
    stand = WakeStand("acp-perm", fixes=True)
    client = ACPClient(stand)
    asked: list = []

    def allow(params: dict) -> str:
        asked.append(params)
        return "allow"

    try:
        sid = open_session(client, stand)
        call(client, "session/prompt", {"sessionId": sid, "prompt": [{"type": "text", "text": START_PROMPT}]}, [])
        woken: list = []
        wait_woken_turn(client, woken, ANSWER_FIXED, on_permission=allow)
        if len(asked) != 1 or FIX_COMMAND not in json.dumps(asked[0]):
            fail(f"permission requests = {asked}")
        if not (stand.work / "fixed.txt").exists():
            fail("the allowed command did not run")
    finally:
        client.close()
        stand.close(keep=bool(os.environ.get("KEEP")))


def main() -> int:
    phase_note()
    print("ok: the woken turn reaches the editor opening with the wake, and session/load replays it")
    phase_permission()
    print("ok: a permission prompt raised in the woken turn is asked of the editor")
    print("PASS acp_e2e_background_wake")
    return 0


if __name__ == "__main__":
    sys.exit(main())
