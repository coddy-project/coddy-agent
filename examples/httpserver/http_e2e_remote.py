#!/usr/bin/env python3
"""HTTP e2e: connect to a remote, authenticated ``coddy http`` like a local one.

Self-contained: this harness boots two ``build/coddy http`` instances on loopback -
a REMOTE one protected by ``--auth-token`` and a LOCAL one with no auth - then acts
as the client the Coddy UI would be. It proves the bearer token gates the API, that
the token is never returned by ``GET /coddy/config``, that authenticated workspace /
session introspection works remotely, and that switching back to the local
(unauthenticated) server keeps the same client working.

Requires ``build/coddy`` built with ``-tags http`` (see ``examples/build_coddy.sh``).
No LLM/provider is needed: every checked route is metadata-only. The streamed
prompt + transcript round-trip is covered deterministically by the godog feature
``external/httpserver/features/remote_api.feature``.

Environment:

- ``CODDY_BIN`` - path to the coddy binary (default ``<repo>/build/coddy``).
- ``REMOTE_PORT`` / ``LOCAL_PORT`` - loopback ports (default 19910 / 19911).
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any, Tuple

TOKEN = "remote-secret-token"

MINIMAL_CONFIG = """\
providers:
  - name: openai
    type: openai
    api_key: "dummy-not-used"
models:
  - model: "openai/gpt-4o"
    max_tokens: 256
    temperature: 0.2
agent:
  model: "openai/gpt-4o"
  max_turns: 4
"""


def repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


def http_call(method: str, url: str, token: str | None, body: dict[str, Any] | None = None) -> Tuple[int, dict[str, Any], str]:
    data = None if body is None else json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Accept", "application/json")
    if data is not None:
        req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            parsed = json.loads(raw) if raw.strip().startswith(("{", "[")) else {}
            return resp.status, parsed, raw
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        try:
            parsed = json.loads(raw) if raw.strip().startswith(("{", "[")) else {}
        except json.JSONDecodeError:
            parsed = {}
        return e.code, parsed, raw


def boot_server(binary: Path, port: int, token: str | None) -> Tuple[subprocess.Popen, str]:
    home = Path(tempfile.mkdtemp(prefix=f"coddy-remote-{port}-"))
    (home / "config.yaml").write_text(MINIMAL_CONFIG, encoding="utf-8")
    work = Path(tempfile.mkdtemp(prefix=f"coddy-remote-work-{port}-"))
    args = [str(binary), "http", "--config", str(home / "config.yaml"),
            "--home", str(home), "--cwd", str(work), "-H", "127.0.0.1", "-P", str(port)]
    if token:
        args += ["--auth-token", token]
    env = dict(os.environ)
    env.pop("CODDY_HTTP_TOKEN", None)  # do not leak an ambient token into the child
    proc = subprocess.Popen(args, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    base = f"http://127.0.0.1:{port}"
    for _ in range(120):
        if proc.poll() is not None:
            raise RuntimeError(f"server on port {port} exited early with {proc.returncode}")
        try:
            # Ready when the port answers at all (200 without auth, 401 when gated).
            code, _, _ = http_call("GET", f"{base}/v1/models", token)
            if code in (200, 401):
                return proc, base
        except OSError:
            pass
        time.sleep(0.25)
    proc.terminate()
    raise RuntimeError(f"server on port {port} did not become ready")


def fail(msg: str) -> None:
    print("FAIL:", msg, file=sys.stderr)
    raise SystemExit(1)


def main() -> int:
    binary = Path(os.environ.get("CODDY_BIN", str(repo_root() / "build" / "coddy")))
    if not binary.is_file() or not os.access(binary, os.X_OK):
        print("coddy binary not found/executable:", binary, "(run ./examples/build_coddy.sh)", file=sys.stderr)
        return 1
    remote_port = int(os.environ.get("REMOTE_PORT", "19910"))
    local_port = int(os.environ.get("LOCAL_PORT", "19911"))

    remote_proc, remote = boot_server(binary, remote_port, TOKEN)
    local_proc = None
    try:
        local_proc, local = boot_server(binary, local_port, None)

        # 1. Remote rejects an unauthenticated client on protected routes.
        code, _, _ = http_call("GET", f"{remote}/v1/models", None)
        if code != 401:
            fail(f"remote /v1/models without token: got {code}, want 401")

        # 2. Remote rejects a wrong token.
        code, _, _ = http_call("GET", f"{remote}/v1/models", "wrong-token")
        if code != 401:
            fail(f"remote /v1/models with wrong token: got {code}, want 401")

        # 3. Remote with the token behaves like local: same model profiles.
        code, models, _ = http_call("GET", f"{remote}/v1/models", TOKEN)
        if code != 200:
            fail(f"remote /v1/models with token: got {code}, want 200")
        ids = {str(r.get("id")) for r in (models.get("data") or [])}
        if not {"agent", "plan"} <= ids:
            fail(f"remote model list missing agent/plan profiles: {sorted(ids)}")

        # 4. GET /coddy/config never returns the token; reports auth_configured.
        code, cfg, raw = http_call("GET", f"{remote}/coddy/config", TOKEN)
        if code != 200:
            fail(f"remote /coddy/config with token: got {code}, want 200")
        if TOKEN in raw:
            fail("remote /coddy/config leaked the auth token")
        if not (cfg.get("httpserver") or {}).get("auth_configured"):
            fail("remote /coddy/config did not report auth_configured")

        # 5. Authenticated remote workspace introspection (change/inspect working dir).
        probe = tempfile.mkdtemp(prefix="coddy-remote-probe-")
        code, ctx, _ = http_call("GET", f"{remote}/coddy/workspace/context?path={probe}", TOKEN)
        if code != 200:
            fail(f"remote workspace context with token: got {code}, want 200")
        if os.path.realpath(ctx.get("path", "")) != os.path.realpath(probe):
            fail(f"remote workspace context path mismatch: {ctx.get('path')} vs {probe}")
        # ...and it is protected without the token.
        code, _, _ = http_call("GET", f"{remote}/coddy/workspace/context?path={probe}", None)
        if code != 401:
            fail(f"remote workspace context without token: got {code}, want 401")

        # 6. Switch back to Local: the same client works with no token.
        code, lmodels, _ = http_call("GET", f"{local}/v1/models", None)
        if code != 200:
            fail(f"local /v1/models without token: got {code}, want 200")
        lids = {str(r.get("id")) for r in (lmodels.get("data") or [])}
        if not {"agent", "plan"} <= lids:
            fail(f"local model list missing agent/plan profiles: {sorted(lids)}")

        print("ok http remote e2e (auth gate, config redaction, workspace parity, local fallback)")
        return 0
    finally:
        for p in (remote_proc, local_proc):
            if p is not None:
                p.terminate()
                try:
                    p.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    p.kill()


if __name__ == "__main__":
    raise SystemExit(main())
