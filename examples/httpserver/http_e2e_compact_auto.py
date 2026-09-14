#!/usr/bin/env python3
"""HTTP e2e: automatic compaction for a model without max_context_tokens (issue #245).

Self-contained: boots its own ``coddy serve`` on loopback against a real
OpenAI-compatible provider (the NeuralDeep hub by default) with a model entry
that has **no** ``max_context_tokens`` - the configuration under which the web
UI drew its context ring against a made-up window while the automatic trigger
stayed off - and drives turns the way the web UI does (``POST /v1/responses``,
``stream: true``, ``metadata.model``).

Checks:

1. ``GET /v1/models`` reports, for the model, the context window the provider's
   own model listing reports (``limit.context``, ``context_length``,
   ``max_model_len``, ``max_context_length`` or ``context_window``), or 128000
   when it reports none. That is the number the web UI ring is drawn against.
2. Every ``usage_update`` frame on the turn streams carries that same window as
   ``size``: what the ring shows is what the trigger measures.
3. With ``compaction.threshold_percent: 1`` and the default
   ``keep_recent_turns`` (2), the session compacts by itself within
   ``COMPACT_TURNS`` turns: a ``compaction_summary`` row appears in
   ``GET /coddy/sessions/{id}/messages`` without any ``/compact``. The second
   turn is the first one with an earlier turn to fold, which is also the
   fallback that keeps fewer turns than ``keep_recent_turns``.
4. The server log names the window and where it came from
   (``auto-compacted session context ... contextWindow=<window>
   contextWindowSource=provider|default``).

Requires a ``coddy`` binary built with ``-tags http`` (``examples/build_coddy.sh``)
and a provider key. The key is read by coddy from ``<PROVIDER>_API_KEY`` (e.g.
``NEURALDEEP_API_KEY``); when that variable is not set, the line is taken from
``$CODDY_HOME/.env`` (what ``test_httpserver.sh`` seeds) into this harness's
own temporary home. Without a key the harness prints SKIP and exits 0.

Environment:

- ``CODDY_BIN`` - coddy binary (default ``<repo>/build/coddy``).
- ``COMPACT_MODEL`` - ``provider/api-model-id`` (default ``neuraldeep/qwen3.8-27b``).
- ``COMPACT_API_BASE`` - the provider's OpenAI-compatible base (default ``https://api.neuraldeep.ru/v1``).
- ``COMPACT_TURNS`` - most turns to wait for the compaction (default 4).
- ``COMPACT_AUTO_PORT`` - loopback port (default 19917).
"""

from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

DEFAULT_WINDOW = 128000

PROMPTS = [
    "Remember the codeword AZURE-7. Reply with one short sentence acknowledging it.",
    "Describe the planet Mars in about eighty words.",
    "Which codeword did I ask you to remember? Answer in one short sentence.",
    "Describe the Pacific Ocean in about eighty words.",
    "Name three programming languages and one strength of each, in about eighty words.",
    "Summarize what we talked about in two sentences.",
]


def repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


def fail(msg: str) -> None:
    print("FAIL:", msg, file=sys.stderr)
    raise SystemExit(1)


def http_json(method: str, url: str, body: dict[str, Any] | None = None,
              headers: dict[str, str] | None = None, timeout: float = 60) -> tuple[int, Any]:
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
    """The key and where it came from: the environment, the suite's .env, or nothing."""
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


def positive(value: Any) -> int:
    try:
        n = float(str(value).strip().strip('"'))
    except (TypeError, ValueError):
        return 0
    return int(n) if n >= 1 else 0


def provider_window(api_base: str, key: str, api_model: str) -> int:
    """The window the provider's listing reports for api_model, read the way coddy reads it."""
    code, body = http_json("GET", api_base.rstrip("/") + "/models", headers={"Authorization": "Bearer " + key}, timeout=30)
    if code != 200 or not isinstance(body, dict):
        fail(f"provider listing {api_base}/models answered {code}")
    for row in body.get("data", []):
        if str(row.get("id", "")).strip() != api_model:
            continue
        limit = row.get("limit")
        if isinstance(limit, dict) and positive(limit.get("context")):
            return positive(limit.get("context"))
        for field in ("context_length", "max_model_len", "max_context_length", "context_window"):
            if positive(row.get(field)):
                return positive(row.get(field))
        return 0
    fail(f"provider listing has no model {api_model!r}")
    return 0


def stream_turn(base: str, sid: str | None, text: str, model: str) -> tuple[str, list[dict[str, Any]], str]:
    """POST /v1/responses as the web UI does; returns (session id, usage_update frames, answer)."""
    body = {"model": "agent", "input": text, "stream": True, "metadata": {"model": model}}
    req = urllib.request.Request(base + "/v1/responses", data=json.dumps(body).encode("utf-8"), method="POST")
    req.add_header("Content-Type", "application/json")
    if sid:
        req.add_header("X-Coddy-Session-ID", sid)
    usage: list[dict[str, Any]] = []
    answer: list[str] = []
    with urllib.request.urlopen(req, timeout=600) as resp:
        sid = resp.headers.get("X-Coddy-Session-ID") or sid
        event = ""
        for raw in resp:
            line = raw.decode("utf-8", errors="replace").rstrip("\r\n")
            if not line:
                event = ""
            elif line.startswith("event:"):
                event = line[6:].strip()
            elif line.startswith("data:"):
                data = line[5:].strip()
                if data == "[DONE]":
                    continue
                try:
                    frame = json.loads(data)
                except json.JSONDecodeError:
                    continue
                if event == "usage_update":
                    usage.append(frame)
                elif not event and isinstance(frame, dict):
                    if frame.get("error"):
                        fail(f"turn failed: {frame['error']}")
                    for choice in frame.get("choices", []):
                        answer.append((choice.get("delta") or {}).get("content") or "")
    if not sid:
        fail("the first turn did not name its session")
    return sid, usage, "".join(answer)


def main() -> int:
    binary = Path(os.environ.get("CODDY_BIN", str(repo_root() / "build" / "coddy")))
    model = os.environ.get("COMPACT_MODEL", "neuraldeep/qwen3.8-27b").strip()
    api_base = os.environ.get("COMPACT_API_BASE", "https://api.neuraldeep.ru/v1").strip()
    turns = int(os.environ.get("COMPACT_TURNS", "4"))
    port = int(os.environ.get("COMPACT_AUTO_PORT", "19917"))
    provider, _, api_model = model.partition("/")
    if not provider or not api_model:
        fail(f"COMPACT_MODEL {model!r} is not provider/api-model-id")

    var = env_var_name(provider)
    key, key_source = resolve_key(var)
    if not key:
        print(f"SKIP http compact auto e2e: no {var} in the environment or $CODDY_HOME/.env", file=sys.stderr)
        return 0

    reported = provider_window(api_base, key, api_model)
    expected = reported or DEFAULT_WINDOW
    expected_source = "provider" if reported else "default"
    print(f"provider listing window for {api_model}: {expected} ({expected_source})", file=sys.stderr)

    root = Path(tempfile.mkdtemp(prefix="coddy-compact-auto-"))
    home, work = root / "home", root / "work"
    home.mkdir()
    work.mkdir()
    log_path = root / "coddy.log"
    (home / "config.yaml").write_text(
        f"""providers:
  - name: {provider}
    type: openai
    api_base: "{api_base}"
models:
  # No max_context_tokens: the window comes from the provider's listing.
  - model: "{model}"
    max_tokens: 2048
    temperature: 0.2
agent:
  model: "{model}"
  max_turns: 6
compaction:
  enable: true
  threshold_percent: 1
tools:
  permission_mode: bypass
memory:
  enable: false
logger:
  level: "info"
  outputs: ["file"]
  file: "{log_path.as_posix()}"
  format: "text"
""",
        encoding="utf-8",
    )
    env = dict(os.environ)
    if key_source == "dotenv":
        (home / ".env").write_text(f"{var}={key}\n", encoding="utf-8")
    proc = subprocess.Popen(
        [str(binary), "serve", "--config", str(home / "config.yaml"), "--home", str(home),
         "--cwd", str(work), "-H", "127.0.0.1", "-P", str(port)],
        env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    base = f"http://127.0.0.1:{port}"
    try:
        for _ in range(160):
            if proc.poll() is not None:
                fail(f"coddy serve exited early with {proc.returncode}")
            try:
                if http_json("GET", base + "/v1/models", timeout=2)[0] == 200:
                    break
            except OSError:
                pass
            time.sleep(0.25)
        else:
            fail("coddy serve did not become ready")

        # 1. The window the web UI draws its ring against.
        code, models = http_json("GET", base + "/v1/models")
        model_rows = {row.get("id"): row for row in models.get("data", [])} if code == 200 else {}
        if model not in model_rows:
            fail(f"GET /v1/models has no {model} row: {code} {models}")
        ui_window = int(model_rows[model].get("max_context_tokens") or 0)
        if ui_window != expected:
            fail(f"GET /v1/models reports {ui_window} for {model}, the provider reports {expected}")
        print(f"web UI window: {ui_window}", file=sys.stderr)

        sid = None
        compacted_after = 0
        frames_seen = 0
        for i, prompt in enumerate(PROMPTS[:turns], 1):
            sid, usage, answer = stream_turn(base, sid, prompt, model)
            # 2. Every usage_update sizes the ring by the same window.
            for frame in usage:
                frames_seen += 1
                if int(frame.get("size") or 0) != ui_window:
                    fail(f"turn {i}: usage_update size {frame.get('size')} != the web UI window {ui_window}")
            code, msgs = http_json("GET", f"{base}/coddy/sessions/{sid}/messages")
            if code != 200:
                fail(f"GET messages {code}: {msgs}")
            transcript = msgs.get("messages", [])
            if any(str(m.get("content", "")).strip().startswith("/compact") for m in transcript):
                fail("a /compact command reached the transcript; this harness never sends one")
            summaries = sum(1 for m in transcript if m.get("compaction_summary"))
            print(f"turn {i}: {len(usage)} usage_update frame(s), {summaries} summary row(s), answer {answer[:50]!r}", file=sys.stderr)
            if summaries:
                compacted_after = i
                break
        if frames_seen == 0:
            fail("no usage_update frame arrived: the stream never reported the context window")
        # 3. The session compacted by itself.
        if not compacted_after:
            fail(f"no compaction_summary row after {turns} turns over a 1% threshold")
        if compacted_after < 2:
            fail("compacted on the first turn: the prompt being answered must never be folded")

        # 4. The log names the window and its source.
        deadline = time.time() + 5
        line = ""
        while time.time() < deadline and not line:
            text = log_path.read_text(encoding="utf-8", errors="replace") if log_path.exists() else ""
            line = next((ln for ln in text.splitlines() if "auto-compacted session context" in ln), "")
            time.sleep(0.2)
        if not line:
            fail("the server log has no 'auto-compacted session context' line")
        if f"contextWindow={ui_window}" not in line or f"contextWindowSource={expected_source}" not in line:
            fail(f"the log line does not name window {ui_window} from {expected_source}: {line}")
        print("log:", line[line.find("auto-compacted"):], file=sys.stderr)
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
        shutil.rmtree(root, ignore_errors=True)

    print("ok http compact auto e2e", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
