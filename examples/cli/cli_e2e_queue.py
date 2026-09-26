#!/usr/bin/env python3
"""The console's message queue in a real pty (issue #364): both modes.

No model and no key: a scripted OpenAI-compatible server in this process keeps
its first answer open until the script releases it, so the console is
genuinely mid-turn while the operator types, and answers every other prompt at
once. The config names no queue mode, so:

- the first message written during the turn asks which mode Enter uses; 1
  saves steer to config.yaml and queues the message to steer;
- Tab queues the next one for after the turn;
- /queue drop puts a queued message back into the input, and Tab queues it
  again;
- once the held answer is released, the model reads the steer message in the
  same turn and then the deferred one as a prompt of its own, and the console
  shows the deferred message above the answer to it.

Usage: CODDY_BIN=build/coddy python3 examples/cli/cli_e2e_queue.py
Needs a cli-tagged build (make build TAGS=cli), pexpect and pyte
(examples/cli/requirements.txt).
"""

from __future__ import annotations

import json
import os
import shutil
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pexpect
import pyte

from cli_tui_driver import CR, COLS, ROWS, coddy_bin, require_cli_build

MODEL = "stub/coddy-demo"
PROMPT = "Review the implementation"
STEER = "Check the Windows path too"
DEFERRED = "Update the changelog after"


def typed_of(messages: list[dict]) -> str:
    """The last message the operator typed; Coddy follows it with its own context."""
    for m in reversed(messages):
        if m.get("role") != "user":
            continue
        content = m.get("content")
        if isinstance(content, list):
            content = "".join(p.get("text", "") for p in content if isinstance(p, dict))
        text = (content or "").strip()
        if text and not text.startswith("<turn_context>"):
            return text
    return ""


class Model:
    """The scripted model: the first prompt waits for release(), the rest answer at once."""

    def __init__(self) -> None:
        self.typed: list[str] = []
        self.lock = threading.Lock()
        self.released = threading.Event()
        model = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_args) -> None:  # noqa: D401 - silence
                pass

            def do_GET(self) -> None:  # noqa: N802 - http.server API
                if not self.path.endswith("/models"):
                    self.send_error(404)
                    return
                self._json({"object": "list", "data": [{"id": "coddy-demo", "object": "model", "owned_by": "e2e"}]})

            def do_POST(self) -> None:  # noqa: N802 - http.server API
                raw = self.rfile.read(int(self.headers.get("Content-Length") or 0))
                try:
                    body = json.loads(raw or b"{}")
                except ValueError:
                    body = {}
                if not body.get("stream"):
                    self._json({"id": "x", "object": "chat.completion", "model": "coddy-demo",
                                "choices": [{"index": 0, "finish_reason": "stop",
                                             "message": {"role": "assistant", "content": "Queue check"}}]})
                    return
                typed = typed_of(body.get("messages") or [])
                with model.lock:
                    model.typed.append(typed)
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Cache-Control", "no-cache")
                self.end_headers()
                try:
                    if typed == PROMPT:
                        self._chunk("Working on it. ")
                        while not model.released.wait(0.5):
                            self.wfile.write(b": keepalive\n\n")
                            self.wfile.flush()
                    self._chunk(f"Answer to: {typed}")
                    self.wfile.write(("data: " + json.dumps({"id": "x", "object": "chat.completion.chunk", "model": "coddy-demo",
                                                            "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]}) + "\n\n").encode())
                    self.wfile.write(b"data: [DONE]\n\n")
                    self.wfile.flush()
                except (BrokenPipeError, ConnectionResetError):
                    pass

            def _chunk(self, text: str) -> None:
                self.wfile.write(("data: " + json.dumps({"id": "x", "object": "chat.completion.chunk", "model": "coddy-demo",
                                                        "choices": [{"index": 0, "delta": {"content": text}, "finish_reason": None}]}) + "\n\n").encode())
                self.wfile.flush()

            def _json(self, payload: dict) -> None:
                out = json.dumps(payload).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(out)))
                self.end_headers()
                self.wfile.write(out)

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    @property
    def port(self) -> int:
        return self.server.server_address[1]


class Console:
    """The console on a pyte screen, in a home of its own (HOME included)."""

    def __init__(self, home: Path, work: Path) -> None:
        self.screen = pyte.Screen(COLS, ROWS)
        self.stream = pyte.ByteStream(self.screen)
        env = dict(os.environ)
        env.update({"TERM": "xterm-256color", "COLORTERM": "truecolor", "CODDY_HOME": str(home),
                    "HOME": str(home), "LANG": "en_US.UTF-8", "LC_ALL": "en_US.UTF-8"})
        for k in ("HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "ALL_PROXY"):
            env.pop(k, None)
        binary = coddy_bin()
        require_cli_build(binary)
        self.child = pexpect.spawn(binary, ["cli", "--plain", "--theme", "dark"], env=env, cwd=str(work),
                                   dimensions=(ROWS, COLS), encoding=None, timeout=5)

    def pump(self, seconds: float) -> None:
        deadline = time.time() + seconds
        while time.time() < deadline:
            try:
                data = self.child.read_nonblocking(size=65536, timeout=0.1)
                if data:
                    self.stream.feed(data)
            except pexpect.TIMEOUT:
                continue
            except pexpect.EOF:
                break

    def text(self) -> str:
        return "\n".join(self.screen.display)

    def wait_for(self, needle: str, timeout: float = 20.0) -> None:
        deadline = time.time() + timeout
        while time.time() < deadline:
            self.pump(0.2)
            if needle in self.text():
                return
        raise AssertionError(f"never saw {needle!r}:\n{self.text()}")

    def type(self, text: str) -> None:
        for ch in text:
            self.child.send(ch.encode())
            self.pump(0.01)

    def send(self, data: str) -> None:
        self.child.send(data.encode())

    def close(self) -> None:
        try:
            self.send("\x03")
            self.pump(0.2)
            self.send("\x03")
            self.child.expect(pexpect.EOF, timeout=10)
        except Exception:
            pass
        finally:
            self.child.terminate(force=True)


def check(label: str, ok: bool, detail: str = "") -> None:
    print(f"{'ok  ' if ok else 'FAIL'} {label}{(' ' + detail) if detail else ''}")
    if not ok:
        raise AssertionError(label)


def main() -> int:
    model = Model()
    home = Path(tempfile.mkdtemp(prefix="coddy-cli-queue-home-"))
    work = Path(tempfile.mkdtemp(prefix="coddy-cli-queue-work-"))
    (home / "config.yaml").write_text(f"""providers:
  - name: stub
    type: openai
    api_base: "http://127.0.0.1:{model.port}/v1"
    api_key: "sk-e2e-stub"
models:
  - model: {MODEL}
    max_context_tokens: 131072
agent:
  model: {MODEL}
tools:
  permission_mode: bypass
""")
    tui = Console(home, work)
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.pump(0.5)
        tui.type(PROMPT)
        tui.send(CR)
        tui.wait_for("Working on it.", timeout=30)

        tui.type(STEER)
        tui.send(CR)
        tui.wait_for("Choose the default queue mode once")
        check("the first message written during a turn asks which mode Enter uses", True)
        tui.send("1")
        tui.wait_for(f"[steer] {STEER}")
        saved = (home / "config.yaml").read_text()
        check("the answer is saved as agent.queue_mode", "queue_mode: steer" in saved)

        tui.type(DEFERRED)
        tui.send("\t")
        tui.wait_for(f"[after_turn] {DEFERRED}")
        check("Tab queues the other mode", True)

        tui.type("/queue drop 2")
        tui.send(CR)
        tui.wait_for("Taken back into the input")
        tui.pump(0.5)
        screen = tui.text()
        check("/queue drop takes the message off the queue", f"[after_turn] {DEFERRED}" not in screen)
        check("and puts its text into the input", screen.rstrip().count(DEFERRED) >= 2, "")
        tui.send("\t")
        tui.wait_for(f"[after_turn] {DEFERRED}")
        check("Tab queues it again", True)

        model.released.set()
        tui.wait_for(f"Answer to: {DEFERRED}", timeout=30)
        tui.pump(1.0)
        with model.lock:
            typed = list(model.typed)
        check("the model reads the steer message, then the deferred one as a prompt of its own",
              typed == [PROMPT, STEER, DEFERRED], " | ".join(typed))
        lines = tui.text().splitlines()

        def line_of(needle: str) -> int:
            return next((i for i, line in enumerate(lines) if needle in line), -1)

        steer_answer, deferred_msg, deferred_answer = line_of(f"Answer to: {STEER}"), -1, line_of(f"Answer to: {DEFERRED}")
        for i, line in enumerate(lines):
            if DEFERRED in line and "Answer to:" not in line and "[after_turn]" not in line:
                deferred_msg = i
        check("the console shows the deferred message between the two answers",
              0 <= steer_answer < deferred_msg < deferred_answer, f"{steer_answer} < {deferred_msg} < {deferred_answer}")
        check("the queue is empty once the turn is over", "queued messages" not in tui.text())
        print("message queue in the console: all checks passed")
        return 0
    except AssertionError as err:
        sys.stderr.write(f"FAIL: {err}\n--- screen ---\n{tui.text()}\n")
        return 1
    finally:
        tui.close()
        model.server.shutdown()
        if os.environ.get("CLI_E2E_KEEP", "") != "1":
            shutil.rmtree(home, ignore_errors=True)
            shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
