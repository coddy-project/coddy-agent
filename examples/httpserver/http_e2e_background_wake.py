#!/usr/bin/env python3
"""HTTP e2e for the background wake (issue #305), against a real `coddy serve`.

Self-boots its own stand (examples/shared/wake_e2e_common.py): the scripted
model of cmd/tgfake and `coddy serve` with the HTTP API, no key and no
network. The model starts a failing command in the background with
notify_on_finish; when it ends the server wakes the agent.

Verifies what a browser reads:

- GET /coddy/events announces `background_wake` for the session, naming the
  task as failed;
- the woken turn's composer relay opens with the `background_wake` frame
  (exit code 2) before anything the turn says, and carries the answer;
- GET .../messages keeps the woken turn's first message as a background wake
  (after a reload the web UI reads it as the turn's opening, not as a user
  message, and shows nothing for it);
- the task row carries notify_on_finish and woke_agent, which keeps the bell on
  its card in the Tasks panel once it has ended;
- with a model that fixes what woke it: the woken turn's permission prompt
  arrives on the relay, POST .../permission answers it, the command runs.

Environment: CODDY_BIN (default build/coddy); KEEP=1 leaves the stand's files.
"""

from __future__ import annotations

import json
import os
import sys
import threading
import time
import urllib.request
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "shared"))

from wake_e2e_common import (  # noqa: E402
    ANSWER_FIXED,
    ANSWER_STARTED,
    ANSWER_WOKEN,
    FAIL_COMMAND,
    FIX_COMMAND,
    START_PROMPT,
    WAKE_INSTRUCTION,
    WakeStand,
)

SESSION_ID = "http-e2e-background-wake"


def fail(msg: str) -> None:
    print("FAIL:", msg, file=sys.stderr)
    raise SystemExit(1)


def sse_frames(resp):
    """Yield (event, data) for every frame of an SSE response as it arrives."""
    event, data = "", []
    for raw in resp:
        line = raw.decode("utf-8", errors="replace").rstrip("\r\n")
        if line == "":
            if event or data:
                yield event, "\n".join(data)
            event, data = "", []
        elif line.startswith(":"):
            continue
        elif line.startswith("event:"):
            event = line[len("event:"):].strip()
        elif line.startswith("data:"):
            data.append(line[len("data:"):].lstrip(" "))


class EventsFollower:
    """Holds GET /coddy/events open, as the web UI does, and keeps every frame."""

    def __init__(self, base: str) -> None:
        self.frames: list[tuple[str, str]] = []
        self.lock = threading.Lock()
        self.ready = threading.Event()
        self.resp = urllib.request.urlopen(f"{base}/coddy/events", timeout=120)
        threading.Thread(target=self._read, daemon=True).start()

    def _read(self) -> None:
        try:
            for event, data in sse_frames(self.resp):
                with self.lock:
                    self.frames.append((event, data))
                if event == "ready":
                    self.ready.set()
        except Exception:  # noqa: BLE001 - the stream ends with the server
            pass

    def wait(self, event: str, needle: str, timeout: float = 30.0) -> dict:
        deadline = time.time() + timeout
        while time.time() < deadline:
            with self.lock:
                for ev, data in self.frames:
                    if ev == event and needle in data:
                        return json.loads(data)
            time.sleep(0.02)
        fail(f"GET /coddy/events never carried {event} for {needle}; frames: {self.frames}")
        return {}


def post_turn(base: str, text: str) -> str:
    body = json.dumps({"model": "agent", "input": text, "stream": True}).encode()
    req = urllib.request.Request(f"{base}/v1/responses", data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("X-Coddy-Session-ID", SESSION_ID)
    answer = []
    with urllib.request.urlopen(req, timeout=120) as resp:
        for event, data in sse_frames(resp):
            if event or data == "[DONE]":
                continue
            try:
                chunk = json.loads(data)
            except json.JSONDecodeError:
                continue
            for choice in chunk.get("choices") or []:
                answer.append((choice.get("delta") or {}).get("content") or "")
    return "".join(answer)


def open_relay(base: str):
    req = urllib.request.Request(f"{base}/coddy/sessions/{SESSION_ID}/composer-stream")
    req.add_header("X-Coddy-Session-ID", SESSION_ID)
    return urllib.request.urlopen(req, timeout=120)


def http_json(method: str, url: str, body: dict | None = None) -> tuple[int, dict]:
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("X-Coddy-Session-ID", SESSION_ID)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=30) as resp:
        raw = resp.read().decode()
        return resp.status, (json.loads(raw) if raw.strip().startswith("{") else {})


CONTROL_FRAMES = {"message_queue", "turn_progress", "token_usage", "usage_update", "coddy_meta"}


def text_of(frames: list[tuple[str, str]]) -> str:
    out = []
    for event, data in frames:
        if event or data == "[DONE]":
            continue
        try:
            chunk = json.loads(data)
        except json.JSONDecodeError:
            continue
        for choice in chunk.get("choices") or []:
            out.append((choice.get("delta") or {}).get("content") or "")
    return "".join(out)


def phase_note() -> None:
    stand = WakeStand("http-note", http=True)
    try:
        base = stand.serve()
        events = EventsFollower(base)
        if not events.ready.wait(15):
            fail("the events stream never became ready")

        answer = post_turn(base, START_PROMPT)
        if ANSWER_STARTED not in answer:
            fail(f"the first turn answered {answer!r}")

        wake = events.wait("background_wake", SESSION_ID)
        tasks = wake.get("tasks") or []
        if wake.get("object") != "coddy.background_wake" or len(tasks) != 1 or tasks[0].get("status") != "failed":
            fail(f"background_wake event = {wake}")

        # Attach the way the web UI does once it hears of the turn.
        frames: list[tuple[str, str]] = []
        with open_relay(base) as resp:
            for event, data in sse_frames(resp):
                frames.append((event, data))
                if data == "[DONE]":
                    break
        transcript = [f for f in frames if f[0] not in CONTROL_FRAMES]
        if not transcript or transcript[0][0] != "background_wake":
            fail(f"the woken turn's stream does not open with background_wake: {frames[:6]}")
        first = json.loads(transcript[0][1])
        task = (first.get("tasks") or [{}])[0]
        if task.get("status") != "failed" or task.get("exitCode") != 2 or task.get("label") != FAIL_COMMAND:
            fail(f"background_wake frame = {first}")
        if ANSWER_WOKEN not in text_of(frames):
            fail(f"the woken turn answered {text_of(frames)!r}")

        # A reload reads the same wake from the transcript.
        _, messages = http_json("GET", f"{base}/coddy/sessions/{SESSION_ID}/messages")
        users = [m for m in messages.get("messages") or [] if m.get("role") == "user"]
        if len(users) != 2 or not users[1].get("background_wake"):
            fail(f"user messages = {users}")
        stored = users[1]["background_wake"]["tasks"][0]
        if stored.get("status") != "failed" or stored.get("exit_code") != 2:
            fail(f"stored wake = {users[1]['background_wake']}")
        if WAKE_INSTRUCTION not in (users[1].get("content") or ""):
            fail(f"the woken turn's first message is not the instruction: {users[1].get('content')!r}")
        if users[0].get("background_wake"):
            fail("the typed message came back marked as a wake")

        _, listing = http_json("GET", f"{base}/coddy/sessions/{SESSION_ID}/background-tasks")
        rows = listing.get("data") or []
        if len(rows) != 1 or not rows[0].get("notify_on_finish") or rows[0].get("status") != "failed":
            fail(f"task rows = {rows}")
        if not rows[0].get("woke_agent"):
            fail(f"the task that woke the agent is not marked: {rows[0]}")
    finally:
        stand.close(keep=bool(os.environ.get("KEEP")))


def phase_permission() -> None:
    stand = WakeStand("http-perm", fixes=True, http=True)
    try:
        base = stand.serve()
        events = EventsFollower(base)
        if not events.ready.wait(15):
            fail("the events stream never became ready")
        if ANSWER_STARTED not in post_turn(base, START_PROMPT):
            fail("the first turn did not start the tests")
        events.wait("background_wake", SESSION_ID)

        frames: list[tuple[str, str]] = []
        answered = False
        with open_relay(base) as resp:
            for event, data in sse_frames(resp):
                frames.append((event, data))
                if event == "permission" and not answered:
                    params = json.loads(data)
                    if FIX_COMMAND not in data:
                        fail(f"the woken turn asked about something else: {data}")
                    code, _ = http_json(
                        "POST",
                        f"{base}/coddy/sessions/{SESSION_ID}/permission",
                        {"toolCallId": params["toolCall"]["toolCallId"], "optionId": "allow"},
                    )
                    if code // 100 != 2:
                        fail(f"permission answer answered {code}")
                    answered = True
                if data == "[DONE]":
                    break
        if not answered:
            fail(f"the woken turn never asked permission: {frames}")
        if ANSWER_FIXED not in text_of(frames):
            fail(f"the woken turn answered {text_of(frames)!r}")
        if not (stand.work / "fixed.txt").exists():
            fail("the allowed command did not run")
    finally:
        stand.close(keep=bool(os.environ.get("KEEP")))


def main() -> int:
    phase_note()
    print("ok: the woken turn is announced, opens with the wake and keeps it after a reload")
    phase_permission()
    print("ok: a permission prompt raised in the woken turn is answered from the web API")
    print("PASS http_e2e_background_wake")
    return 0


if __name__ == "__main__":
    sys.exit(main())
