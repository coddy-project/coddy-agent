#!/usr/bin/env python3
"""HTTP e2e: the context usage falls after a compaction for every web client of a session.

Self-contained: boots its own ``coddy serve`` against a real OpenAI-compatible
provider (the NeuralDeep hub by default) and plays the clients a shared web
session has at once:

- **A, the sender**: ``POST /v1/responses`` with ``stream: true``, the way the
  composer sends a turn, reading every ``usage_update`` frame of its own stream;
- **B, a second browser tab**: subscribed to ``GET /coddy/events``; on
  ``turn_started`` for the session it attaches to
  ``GET /coddy/sessions/{id}/composer-stream`` and reads the same frames live, as
  the SPA's ``attachViewedComposer`` does;
- **C, an idle viewer**: reads ``GET /coddy/sessions/{id}/stats``, what a tab
  refreshes when a turn it watched ends.

Every phase first pastes a large text into the session (a log of numbered
lines, so the folded history is far larger than any summary of it) and then
compacts. Checks:

1. **``/compact`` from the composer.** The last ``usage_update`` A reads, the
   value B reads on the relay, and C's stats all agree and sit below the
   estimate before the compaction by at least half of the pasted text.
2. **``POST /coddy/sessions/{id}/compact``** (a script, another client). C's stats
   fall the same way, and B is told about it on ``/coddy/events``
   (``turn_started`` and ``turn_ended``), which is what makes an idle tab reload
   the numbers.
3. **Automatic compaction.** A second server runs with a
   ``compaction.threshold_percent`` computed so the pasted text crosses it and
   the compacted session does not. The turn after the paste compacts before its
   first model call: A's and B's streams both show the high estimate and then the
   low one, C's stats end low, and the log names the trigger.

Requires a ``coddy`` binary built with ``-tags http`` and a provider key, read by
coddy from ``<PROVIDER>_API_KEY`` (``NEURALDEEP_API_KEY``). When the variable is
not set, the line is taken from ``$CODDY_HOME/.env`` (what ``test_httpserver.sh``
seeds). Without a key the harness prints SKIP and exits 0.

Environment:

- ``CODDY_BIN`` - coddy binary (default ``<repo>/build/coddy``).
- ``COMPACT_MODEL`` - ``provider/api-model-id`` (default ``neuraldeep/qwen3.8-27b``).
- ``COMPACT_API_BASE`` - the provider's OpenAI-compatible base (default ``https://api.neuraldeep.ru/v1``).
- ``COMPACT_CLIENTS_PORT`` - loopback port (default 19914).
"""

from __future__ import annotations

import json
import math
import os
import re
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any, Iterator

DEFAULT_WINDOW = 128000


def repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


def fail(msg: str) -> None:
    print("FAIL:", msg, file=sys.stderr)
    raise SystemExit(1)


def log(msg: str) -> None:
    print(msg, file=sys.stderr, flush=True)


def http_json(method: str, url: str, body: dict[str, Any] | None = None,
              headers: dict[str, str] | None = None, timeout: float = 600) -> tuple[int, Any]:
    data = None if body is None else json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Accept", "application/json")
    if data is not None:
        req.add_header("Content-Type", "application/json")
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            return resp.status, (json.loads(raw) if raw.strip() else {})
    except urllib.error.HTTPError as e:
        return e.code, {"_raw": e.read().decode("utf-8", errors="replace")}


def env_var_name(provider: str) -> str:
    return re.sub(r"[^A-Z0-9]", "_", provider.upper()) + "_API_KEY"


def resolve_key(var: str) -> tuple[str, str]:
    if os.environ.get(var, "").strip():
        return os.environ[var].strip(), "env"
    suite_home = os.environ.get("CODDY_HOME", "").strip()
    if suite_home:
        env_file = Path(suite_home) / ".env"
        if env_file.is_file():
            for line in env_file.read_text(encoding="utf-8").splitlines():
                name, _, value = line.partition("=")
                if name.strip() == var and value.strip():
                    return value.strip().strip('"').strip("'"), "dotenv"
    return "", ""


def iter_sse(resp) -> Iterator[tuple[str, str]]:
    """(event, data) per SSE frame; the default event is ''. id/retry lines are skipped."""
    event, data = "", []
    for raw in resp:
        line = raw.decode("utf-8", errors="replace").rstrip("\r\n")
        if not line:
            if data:
                yield event, "\n".join(data)
            event, data = "", []
        elif line.startswith("event:"):
            event = line[6:].strip()
        elif line.startswith("data:"):
            data.append(line[5:].strip())


def usage_of(data: str) -> tuple[int, int] | None:
    try:
        frame = json.loads(data)
    except json.JSONDecodeError:
        return None
    return int(frame.get("used") or 0), int(frame.get("size") or 0)


def filler(tokens: int) -> str:
    """Deterministic pasted text of about `tokens` estimated tokens (coddy estimates runes/4)."""
    lines, size, i = [], 0, 0
    while size < tokens * 4:
        i += 1
        line = f"log {i:05d}: service node-{i % 7} answered request {i * 37 % 1000:03d} in {i % 90 + 5} ms, status ok"
        lines.append(line)
        size += len(line) + 1
    return "\n".join(lines)


class Server:
    def __init__(self, binary: Path, root: Path, name: str, port: int, provider: str, model: str,
                 api_base: str, threshold: int, env: dict[str, str], dotenv_line: str) -> None:
        self.home = root / name / "home"
        self.work = root / name / "work"
        self.home.mkdir(parents=True)
        self.work.mkdir(parents=True)
        self.log_path = root / name / "coddy.log"
        (self.home / "config.yaml").write_text(
            f"""providers:
  - name: {provider}
    type: openai
    api_base: "{api_base}"
models:
  # No max_context_tokens: the window comes from the provider's listing.
  - model: "{model}"
    max_tokens: 1024
    temperature: 0.2
agent:
  model: "{model}"
  max_turns: 4
compaction:
  enable: true
  threshold_percent: {threshold}
tools:
  permission_mode: bypass
memory:
  enable: false
logger:
  level: "info"
  outputs: ["file"]
  file: "{self.log_path.as_posix()}"
  format: "text"
""",
            encoding="utf-8",
        )
        if dotenv_line:
            (self.home / ".env").write_text(dotenv_line + "\n", encoding="utf-8")
        self.base = f"http://127.0.0.1:{port}"
        self.proc = subprocess.Popen(
            [str(binary), "serve", "--config", str(self.home / "config.yaml"), "--home", str(self.home),
             "--cwd", str(self.work), "-H", "127.0.0.1", "-P", str(port)],
            env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        for _ in range(200):
            if self.proc.poll() is not None:
                fail(f"coddy serve ({name}) exited early with {self.proc.returncode}")
            try:
                if http_json("GET", self.base + "/v1/models", timeout=2)[0] == 200:
                    return
            except OSError:
                pass
            time.sleep(0.25)
        fail(f"coddy serve ({name}) did not become ready")

    def stop(self) -> None:
        self.proc.terminate()
        try:
            self.proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            self.proc.kill()

    def window(self, model: str) -> int:
        code, body = http_json("GET", self.base + "/v1/models", timeout=30)
        for row in body.get("data", []) if code == 200 else []:
            if row.get("id") == model:
                return int(row.get("max_context_tokens") or 0)
        fail(f"GET /v1/models has no {model} row")
        return 0

    def stats(self, sid: str) -> dict[str, Any]:
        """Client C: what a tab reads from GET /coddy/sessions/{id}/stats."""
        code, body = http_json("GET", f"{self.base}/coddy/sessions/{sid}/stats", timeout=30)
        if code != 200:
            fail(f"GET stats {code}: {body}")
        breakdown = ((body.get("stats") or {}).get("contextBreakdown")) or {}
        if not breakdown.get("estimatedTotal"):
            fail(f"stats of {sid} carry no context estimate: {body}")
        return breakdown

    def turn(self, sid: str | None, text: str, model: str) -> tuple[str, list[tuple[int, int]], str]:
        """Client A: one composer turn over POST /v1/responses (stream)."""
        body = {"model": "agent", "input": text, "stream": True, "metadata": {"model": model}}
        req = urllib.request.Request(self.base + "/v1/responses", data=json.dumps(body).encode("utf-8"), method="POST")
        req.add_header("Content-Type", "application/json")
        if sid:
            req.add_header("X-Coddy-Session-ID", sid)
        usage: list[tuple[int, int]] = []
        answer: list[str] = []
        with urllib.request.urlopen(req, timeout=600) as resp:
            sid = resp.headers.get("X-Coddy-Session-ID") or sid
            for event, data in iter_sse(resp):
                if event == "usage_update":
                    u = usage_of(data)
                    if u:
                        usage.append(u)
                elif not event and data != "[DONE]":
                    try:
                        frame = json.loads(data)
                    except json.JSONDecodeError:
                        continue
                    if isinstance(frame, dict) and frame.get("error"):
                        fail(f"turn failed: {frame['error']}")
                    for choice in frame.get("choices", []) if isinstance(frame, dict) else []:
                        answer.append((choice.get("delta") or {}).get("content") or "")
        if not sid:
            fail("the turn did not name its session")
        return sid, usage, "".join(answer)


class Watcher:
    """Client B: a second tab on the same session, driven by GET /coddy/events."""

    def __init__(self, base: str, sid: str) -> None:
        self.base, self.sid = base, sid
        self.events: list[str] = []
        self.relay_usage: list[tuple[int, int]] = []
        self.relay_done = threading.Event()
        self.ready = threading.Event()
        self.ended = threading.Event()
        self._resp = None
        self._relays: list[threading.Thread] = []
        self._thread = threading.Thread(target=self._run_events, daemon=True)
        self._thread.start()
        if not self.ready.wait(15):
            fail("GET /coddy/events never said ready")

    def _run_events(self) -> None:
        try:
            self._resp = urllib.request.urlopen(self.base + "/coddy/events", timeout=900)
            for event, data in iter_sse(self._resp):
                if event == "ready":
                    self.ready.set()
                    continue
                if event not in ("turn_started", "turn_ended"):
                    continue
                try:
                    if json.loads(data).get("sessionId") != self.sid:
                        continue
                except json.JSONDecodeError:
                    continue
                self.events.append(event)
                if event == "turn_started":
                    t = threading.Thread(target=self._run_relay, daemon=True)
                    self._relays.append(t)
                    t.start()
                else:
                    self.ended.set()
        except Exception:  # closed by stop(), or the server went away
            pass

    def _run_relay(self) -> None:
        req = urllib.request.Request(f"{self.base}/coddy/sessions/{self.sid}/composer-stream")
        try:
            with urllib.request.urlopen(req, timeout=600) as resp:
                for event, data in iter_sse(resp):
                    if event == "usage_update":
                        u = usage_of(data)
                        if u:
                            self.relay_usage.append(u)
        except Exception:
            pass
        finally:
            self.relay_done.set()

    def wait_turn_end(self, expect_relay: bool) -> None:
        if not self.ended.wait(120):
            fail(f"client B never saw turn_ended for {self.sid} on /coddy/events (saw {self.events})")
        if expect_relay and not self.relay_done.wait(60):
            fail("client B's composer-stream never finished")

    def stop(self) -> None:
        try:
            if self._resp is not None:
                self._resp.close()
        except Exception:
            pass


def check_drop(phase: str, before: int, after: int, pasted: int) -> None:
    # The pasted text is folded into a summary: the estimate has to lose at
    # least half of it, whatever the summary itself costs.
    if not after < before - pasted // 2:
        fail(f"{phase}: context estimate {before} -> {after}, want below {before - pasted // 2} (pasted ~{pasted})")


def main() -> int:
    binary = Path(os.environ.get("CODDY_BIN", str(repo_root() / "build" / "coddy")))
    model = os.environ.get("COMPACT_MODEL", "neuraldeep/qwen3.8-27b").strip()
    api_base = os.environ.get("COMPACT_API_BASE", "https://api.neuraldeep.ru/v1").strip()
    port = int(os.environ.get("COMPACT_CLIENTS_PORT", "19914"))
    provider, _, api_model = model.partition("/")
    if not provider or not api_model:
        fail(f"COMPACT_MODEL {model!r} is not provider/api-model-id")
    var = env_var_name(provider)
    key, key_source = resolve_key(var)
    if not key:
        log(f"SKIP http compact clients e2e: no {var} in the environment or $CODDY_HOME/.env")
        return 0
    env = dict(os.environ)
    dotenv_line = f"{var}={key}" if key_source == "dotenv" else ""

    root = Path(tempfile.mkdtemp(prefix="coddy-compact-clients-"))
    servers: list[Server] = []
    try:
        # --- manual compactions: /compact from the composer, then the REST route -------
        srv = Server(binary, root, "manual", port, provider, model, api_base, 100, env, dotenv_line)
        servers.append(srv)
        window = srv.window(model)
        pasted = max(6000, min(16000, window // 20))
        big = filler(pasted)
        log(f"window {window}, pasted text ~{pasted} tokens")

        sid0, _, _ = srv.turn(None, "Reply with exactly: READY", model)
        fixed = srv.stats(sid0)["estimatedTotal"]
        # The pasted text goes into an older turn: keep_recent_turns (2) keeps the
        # two newest turns verbatim, so what a compaction can fold is what came
        # before them.
        sid, _, _ = srv.turn(None, "Keep this log in mind; reply with exactly: NOTED\n\n" + big, model)
        srv.turn(sid, "Reply with exactly: ONE", model)
        srv.turn(sid, "Reply with exactly: TWO", model)
        before = srv.stats(sid)["estimatedTotal"]
        if before < fixed + pasted * 3 // 4:
            fail(f"the pasted text did not land in the context: {fixed} -> {before}")

        b = Watcher(srv.base, sid)
        _, a_usage, answer = srv.turn(sid, "/compact", model)
        b.wait_turn_end(expect_relay=True)
        b.stop()
        after = srv.stats(sid)["estimatedTotal"]
        log(f"/compact: stats {before} -> {after}; A usage {a_usage}; B relay usage {b.relay_usage}; B events {b.events}; reply {answer.strip()[:60]!r}")
        check_drop("/compact", before, after, pasted)
        if not a_usage or a_usage[-1][0] != after:
            fail(f"/compact: A's last usage_update {a_usage[-1:] or None} != stats {after}")
        if after not in [u for u, _ in b.relay_usage]:
            fail(f"/compact: client B never read the compacted {after} on the relay: {b.relay_usage}")
        for who, frames in (("A", a_usage), ("B", b.relay_usage)):
            if any(size != window for _, size in frames):
                fail(f"/compact: client {who} got a usage_update sized other than the window {window}: {frames}")

        sid2, _, _ = srv.turn(None, "Keep this log in mind; reply with exactly: NOTED\n\n" + big, model)
        before2 = srv.stats(sid2)["estimatedTotal"]
        b2 = Watcher(srv.base, sid2)
        code, body = http_json("POST", f"{srv.base}/coddy/sessions/{sid2}/compact", {})
        if code != 200 or body.get("compacted") is not True:
            fail(f"REST compact {code}: {body}")
        after2 = srv.stats(sid2)["estimatedTotal"]
        told = b2.ended.wait(15)
        b2.stop()
        log(f"REST compact: stats {before2} -> {after2}; B events {b2.events}")
        check_drop("REST compact", before2, after2, pasted)
        if not told or "turn_started" not in b2.events:
            fail(f"REST compact: client B was never told the session changed on /coddy/events: {b2.events}")
        srv.stop()
        servers.pop()

        # --- automatic compaction ---------------------------------------------------------
        # A threshold between the compacted session (fixed part plus a summary) and the
        # session holding the pasted text.
        threshold = math.ceil(100 * (fixed + pasted * 0.5) / window)
        if not (fixed + pasted) * 100 > threshold * window > (fixed + 1500) * 100:
            log(f"SKIP auto phase: window {window} leaves no threshold between {fixed} and {fixed + pasted}")
        else:
            auto = Server(binary, root, "auto", port, provider, model, api_base, threshold, env, dotenv_line)
            servers.append(auto)
            sid3, _, _ = auto.turn(None, "Keep this log in mind; reply with exactly: NOTED\n\n" + big, model)
            before3 = auto.stats(sid3)["estimatedTotal"]
            b3 = Watcher(auto.base, sid3)
            _, a3_usage, _ = auto.turn(sid3, "Reply with exactly: DONE", model)
            b3.wait_turn_end(expect_relay=True)
            b3.stop()
            after3 = auto.stats(sid3)["estimatedTotal"]
            log(f"auto (threshold {threshold}%): stats {before3} -> {after3}; A usage {a3_usage}; B relay usage {b3.relay_usage}")
            check_drop("auto", before3, after3, pasted)
            for who, frames in (("A", a3_usage), ("B", b3.relay_usage)):
                used = [u for u, _ in frames]
                if not used or max(used) < before3 or min(used) >= before3 - pasted // 2:
                    fail(f"auto: client {who} did not see the estimate fall during the turn: {frames}")
                if used.index(max(used)) > used.index(min(used)):
                    fail(f"auto: client {who} saw the low estimate before the high one: {frames}")
            text = auto.log_path.read_text(encoding="utf-8", errors="replace") if auto.log_path.exists() else ""
            if "auto-compacted session context" not in text:
                fail("auto: the server log has no 'auto-compacted session context' line")
    finally:
        for s in servers:
            s.stop()
        shutil.rmtree(root, ignore_errors=True)

    log("ok http compact clients e2e")
    return 0


if __name__ == "__main__":
    sys.exit(main())
