#!/usr/bin/env python3
"""HTTP e2e for published previews, against a real `coddy serve`.

Self-boots its own stand: the scripted model of tgfake
(github.com/EvilFreelancer/tgfake, `--llm`) and `coddy serve` with the HTTP API
and `tools.preview_publish` turned on, so the published-preview origin runs as
a subsystem of that process. No key, no provider, no network.

What it checks, end to end and the way an operator would meet it:

- publishing asks for permission first (the session runs in `ask`), and the
  answer over `POST /coddy/sessions/{id}/permission` lets the turn finish;
- the address the tool reports is under the configured `public_url`, ends in a
  slash, and the origin on the fixed port answers it with the published page;
- a dot-file of the project is neither copied nor served, and the origin never
  lists its own root;
- rewriting the project does not change what the published address answers:
  the artifact is a copy;
- publishing again under the same name replaces the artifact at the same
  address;
- `preview_unpublish` takes it down and the address answers 404.

Environment: CODDY_BIN (default build/coddy), CODDY_TGFAKE_BIN (else tgfake is
built from the version go.mod pins); KEEP=1 leaves the stand's files.
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

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "shared"))

from wake_e2e_common import coddy_bin, free_port, tgfake_binary  # noqa: E402

SESSION_ID = "http-e2e-preview-publish"
SITE = "site"
MARKER = "coddy-publish-e2e"
PUBLISH_PROMPT = "publish the site"
REPUBLISH_PROMPT = "publish it again"
UNPUBLISH_PROMPT = "take it down"
ANSWER_PUBLISHED = "Published."
ANSWER_REPUBLISHED = "Republished."
ANSWER_UNPUBLISHED = "Taken down."
ADDRESS = re.compile(r"http://127\.0\.0\.1:\d+/\S*")


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


def http_json(base: str, method: str, path: str, body: dict | None = None) -> tuple[int, dict]:
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(f"{base}{path}", data=data, method=method)
    req.add_header("X-Coddy-Session-ID", SESSION_ID)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=60) as resp:
        raw = resp.read().decode()
        return resp.status, (json.loads(raw) if raw.strip().startswith("{") else {})


def fetch(address: str) -> tuple[int, str]:
    try:
        with urllib.request.urlopen(address, timeout=15) as resp:
            return resp.status, resp.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", errors="replace")


def turn(base: str, text: str) -> tuple[str, bool]:
    """Run one turn, allowing whatever it asks about; returns the answer and
    whether a permission prompt came up."""
    body = json.dumps({"model": "agent", "input": text, "stream": True}).encode()
    req = urllib.request.Request(f"{base}/v1/responses", data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("X-Coddy-Session-ID", SESSION_ID)
    answer, asked = [], False
    with urllib.request.urlopen(req, timeout=180) as resp:
        for event, data in sse_frames(resp):
            if event == "permission":
                params = json.loads(data)
                code, _ = http_json(base, "POST", f"/coddy/sessions/{SESSION_ID}/permission",
                                    {"toolCallId": params["toolCall"]["toolCallId"], "optionId": "allow"})
                if code // 100 != 2:
                    fail(f"the permission answer returned {code}")
                asked = True
                continue
            if event or data == "[DONE]":
                continue
            try:
                chunk = json.loads(data)
            except json.JSONDecodeError:
                continue
            for choice in chunk.get("choices") or []:
                answer.append((choice.get("delta") or {}).get("content") or "")
    return "".join(answer), asked


def published_address(home: Path, after: int = 0) -> str:
    """The address of the most recent preview_publish result, read where the
    model read it: the tool result kept in the session bundle."""
    calls = home / "sessions" / SESSION_ID / "tool_calls"
    results = sorted(calls.glob("*/result.md"), key=lambda p: p.stat().st_mtime)
    for path in reversed(results):
        if path.stat().st_mtime_ns <= after:
            continue
        text = path.read_text(encoding="utf-8", errors="replace")
        if "Published preview:" in text:
            found = ADDRESS.search(text)
            if found:
                return found.group(0)
    fail(f"no published address in the tool results under {calls}")
    return ""


def config_yaml(model_port: int, publish_port: int, root: Path, log_file: Path) -> str:
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
  preview_publish:
    enable: true
    host: 127.0.0.1
    port: {publish_port}
    root: "{root.as_posix()}"
    public_url: "http://127.0.0.1:{publish_port}/"
    ttl_seconds: 3600
httpserver:
  enable: true
logger:
  level: debug
  format: text
  outputs: [file]
  file: "{log_file.as_posix()}"
"""


def script_rules() -> list[dict]:
    publish = {"name": "preview_publish", "arguments": {"path": SITE, "name": "demo"}}
    return [
        {"match": PUBLISH_PROMPT, "tool": publish, "answer": ANSWER_PUBLISHED},
        {"match": REPUBLISH_PROMPT, "tool": publish, "answer": ANSWER_REPUBLISHED},
        {"match": UNPUBLISH_PROMPT,
         "tool": {"name": "preview_unpublish", "arguments": {"name": "demo"}},
         "answer": ANSWER_UNPUBLISHED},
    ]


class Stand:
    """The scripted model plus a coddy serve that runs the published-preview origin."""

    def __init__(self) -> None:
        self.tmp = Path(tempfile.mkdtemp(prefix="coddy-publish-e2e-"))
        self.home = self.tmp / "home"
        self.work = self.tmp / "work"
        self.root = self.tmp / "previews"
        for d in (self.home, self.work, self.home / "sessions"):
            d.mkdir(parents=True, exist_ok=True)
        self.model_port = free_port()
        self.publish_port = free_port()
        self.log_file = self.home / "coddy.log"
        self.config = self.home / "config.yaml"
        self.config.write_text(
            config_yaml(self.model_port, self.publish_port, self.root, self.log_file), encoding="utf-8")
        # Publishing asks, which is the point: the prompt is answered over REST.
        (self.home / "permission-mode.json").write_text(
            json.dumps({"permissionMode": "ask"}), encoding="utf-8")
        script = self.tmp / "rules.json"
        script.write_text(json.dumps(script_rules()), encoding="utf-8")
        tgfake = tgfake_binary(self.tmp)
        self.model = subprocess.Popen(
            [str(tgfake), "--addr", f"127.0.0.1:{self.model_port}", "--llm",
             "--llm-model", "coddy-demo", "--llm-strip-tag", "turn_context",
             "--llm-script", str(script)],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self._wait(f"http://127.0.0.1:{self.model_port}/v1/models", "the scripted model")
        self.procs: list[subprocess.Popen] = []

    def _wait(self, url: str, what: str, tries: int = 120) -> None:
        for _ in range(tries):
            try:
                with urllib.request.urlopen(url, timeout=2):
                    return
            except urllib.error.HTTPError:
                return
            except (urllib.error.URLError, OSError):
                time.sleep(0.25)
        raise RuntimeError(f"{what} never came up at {url}; log: {self.log_file}")

    def serve(self) -> str:
        port = free_port()
        proc = subprocess.Popen(
            [coddy_bin(), "serve", "--config", str(self.config), "--home", str(self.home),
             "--cwd", str(self.work), "-H", "127.0.0.1", "-P", str(port)],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
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

    def write_site(self, marker: str) -> None:
        site = self.work / SITE
        site.mkdir(parents=True, exist_ok=True)
        (site / "index.html").write_text(
            f"<!doctype html><title>{marker}</title><p>{marker}</p>\n", encoding="utf-8")
        (site / ".env").write_text("SECRET=do-not-publish\n", encoding="utf-8")

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


def main() -> int:
    stand = Stand()
    try:
        stand.write_site(MARKER)
        base = stand.serve()
        origin = f"http://127.0.0.1:{stand.publish_port}/"
        # The origin is up with the process, before anything was published.
        stand._wait(origin, "the published-preview origin")

        answer, asked = turn(base, PUBLISH_PROMPT)
        if ANSWER_PUBLISHED not in answer:
            fail(f"the publishing turn answered {answer!r}; log: {stand.log_file}")
        if not asked:
            fail("publishing a preview did not ask for permission")

        address = published_address(stand.home)
        if not address.startswith(origin) or not address.endswith("/"):
            fail(f"the reported address {address!r} is not under {origin} or does not end in a slash")

        status, body = fetch(address)
        if status != 200 or MARKER not in body:
            fail(f"GET {address}: {status}, marker missing from {body[:200]!r}")
        status, body = fetch(address + ".env")
        if status != 404 or "do-not-publish" in body:
            fail(f"GET .env of the artifact: {status}, want 404")
        if list((stand.root).glob("*/.env")):
            fail("a dot-file of the project was copied into the artifact root")
        status, _ = fetch(origin)
        if status != 404:
            fail(f"the origin listed its own root: {status}")

        # The artifact is a copy: the project moving on does not move it.
        stand.write_site(MARKER + "-edited")
        status, body = fetch(address)
        if status != 200 or (MARKER + "-edited") in body:
            fail(f"the published address followed the project: {body[:200]!r}")

        published_at = max(p.stat().st_mtime_ns for p in
                           (stand.home / "sessions" / SESSION_ID / "tool_calls").glob("*/result.md"))
        answer, _ = turn(base, REPUBLISH_PROMPT)
        if ANSWER_REPUBLISHED not in answer:
            fail(f"the republishing turn answered {answer!r}")
        again = published_address(stand.home, after=published_at)
        if again != address:
            fail(f"republishing under the same name moved the address: {address} -> {again}")
        status, body = fetch(address)
        if status != 200 or (MARKER + "-edited") not in body:
            fail(f"the republished address still answers with the old build: {body[:200]!r}")

        answer, _ = turn(base, UNPUBLISH_PROMPT)
        if ANSWER_UNPUBLISHED not in answer:
            fail(f"the unpublishing turn answered {answer!r}")
        status, _ = fetch(address)
        if status != 404:
            fail(f"the unpublished address still answers {status}")

        print("ok: published behind permission, served from the fixed origin, "
              "copied without dot-files, republished at the same address and taken down")
        print("PASS http_e2e_preview_publish")
        return 0
    finally:
        stand.close(keep=bool(os.environ.get("KEEP")))


if __name__ == "__main__":
    sys.exit(main())
