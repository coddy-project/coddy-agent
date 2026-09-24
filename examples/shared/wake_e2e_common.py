"""Shared stand for the background wake e2e harnesses (HTTP, ACP, console).

No model and no network: the scripted model of cmd/tgfake (`--llm`, rules
from a JSON script) answers every request. The first turn asks it to "start
the tests": it calls run_command with a failing command, background and
notify_on_finish, then answers once the tool result comes back. When the task
ends, the process wakes the agent, and the woken turn's prompt names the task:
the model answers it - or, in the `fixes` variant, first runs a command the
permission gate has to ask about, which is how a harness checks that a prompt
raised in a woken turn reaches a surface that can answer it.

Every harness boots its own stand with `WakeStand`, so the scripts run anywhere
a Go toolchain and a built coddy are: no key, no provider, no Telegram.
"""

from __future__ import annotations

import json
import os
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]

FAIL_COMMAND = "echo 'tests failed' >&2; exit 2"
FIX_COMMAND = "touch fixed.txt"
START_PROMPT = "start the tests"
ANSWER_STARTED = "Started the tests in the background."
ANSWER_WOKEN = "The tests failed with exit 2."
ANSWER_FIXED = "Fixed it."
# The one-line note the text-only surfaces (ACP, Telegram) put before the
# answer; the web UI and the console show nothing for a wake.
WAKE_TITLE = "Woken by a finished background task"
# The instruction a woken turn starts from (internal/agent/background_notify.go).
WAKE_INSTRUCTION = "background task you started has finished"


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def coddy_bin() -> str:
    env = os.environ.get("CODDY_BIN")
    if env:
        return env
    candidate = REPO_ROOT / "build" / "coddy"
    if candidate.exists():
        return str(candidate)
    found = shutil.which("coddy")
    if found:
        return found
    raise SystemExit("no coddy binary: ./examples/build_coddy.sh or set CODDY_BIN")


def rules(fixes: bool) -> list[dict]:
    """The model's script: start the failing command, then answer the wake."""
    start = {
        "match": START_PROMPT,
        "tool": {
            "name": "run_command",
            "arguments": {
                "command": FAIL_COMMAND,
                "background": True,
                "notify_on_finish": True,
                "expected_seconds": 1,
            },
        },
        "answer": ANSWER_STARTED,
    }
    if fixes:
        woken = {
            "match": WAKE_INSTRUCTION,
            "tool": {"name": "run_command", "arguments": {"command": FIX_COMMAND}},
            "answer": ANSWER_FIXED,
        }
    else:
        woken = {"match": WAKE_INSTRUCTION, "answer": ANSWER_WOKEN}
    return [start, woken]


def config_yaml(model_port: int, *, http: bool, log_file: Path) -> str:
    """A config pointing coddy at the scripted model.

    Commands are asked about - `ask` - except `echo`, the one the first turn
    starts, so the only prompt a harness meets is the woken turn's own.
    """
    return f"""providers:
  - name: stub
    type: openai
    api_base: "http://127.0.0.1:{model_port}/v1"
    api_key: "sk-stub"
models:
  - model: stub/coddy-demo
    max_context_tokens: 131072
agent:
  model: stub/coddy-demo
tools:
  permission_mode: ask
  command_allowlist: ["echo"]
httpserver:
  enable: {"true" if http else "false"}
logger:
  level: debug
  format: text
  outputs: [file]
  file: "{log_file}"
"""


class WakeStand:
    """The scripted model plus a home and a workspace for coddy."""

    def __init__(self, name: str, *, fixes: bool = False, http: bool = False) -> None:
        self.tmp = Path(tempfile.mkdtemp(prefix=f"coddy-wake-{name}-"))
        self.home = self.tmp / "home"
        self.work = self.tmp / "work"
        self.home.mkdir()
        self.work.mkdir()
        (self.home / "sessions").mkdir()
        self.model_port = free_port()
        self.log_file = self.home / "coddy.log"
        self.config = self.home / "config.yaml"
        self.config.write_text(config_yaml(self.model_port, http=http, log_file=self.log_file), encoding="utf-8")
        script = self.tmp / "rules.json"
        script.write_text(json.dumps(rules(fixes)), encoding="utf-8")
        tgfake = self.tmp / "tgfake"
        subprocess.run(["go", "build", "-o", str(tgfake), "./cmd/tgfake"], cwd=REPO_ROOT, check=True)
        # A pause between streamed words keeps a woken turn running long
        # enough for a client to attach to it, as a real model does.
        self.model = subprocess.Popen(
            [str(tgfake), "--addr", f"127.0.0.1:{self.model_port}", "--llm",
             "--llm-script", str(script), "--llm-delay", "80ms"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        self._wait_model()
        self.procs: list[subprocess.Popen] = []

    def _wait_model(self) -> None:
        url = f"http://127.0.0.1:{self.model_port}/v1/models"
        for _ in range(80):
            try:
                with urllib.request.urlopen(url, timeout=2):
                    return
            except (urllib.error.URLError, OSError):
                time.sleep(0.1)
        raise RuntimeError("the scripted model never came up")

    def serve(self) -> str:
        """Start `coddy serve` with the HTTP API on this stand; returns its origin."""
        port = free_port()
        proc = subprocess.Popen(
            [coddy_bin(), "serve", "--config", str(self.config), "--home", str(self.home),
             "--cwd", str(self.work), "-H", "127.0.0.1", "-P", str(port)],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        self.procs.append(proc)
        base = f"http://127.0.0.1:{port}"
        for _ in range(120):
            if proc.poll() is not None:
                raise RuntimeError(f"coddy serve exited early with {proc.returncode}; log: {self.log_file}")
            try:
                with urllib.request.urlopen(f"{base}/v1/models", timeout=2):
                    return base
            except (urllib.error.URLError, OSError):
                time.sleep(0.25)
        raise RuntimeError("coddy serve did not become ready")

    def close(self, keep: bool = False) -> None:
        for proc in [*self.procs, self.model]:
            if proc.poll() is None:
                proc.terminate()
                try:
                    proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    proc.kill()
        if not keep:
            shutil.rmtree(self.tmp, ignore_errors=True)
