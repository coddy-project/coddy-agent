#!/usr/bin/env python3
"""End-to-end check that a fresh coddy cleans up after one that was killed.

Self-contained: this harness boots its own ``build/coddy http`` instances, so it
does not depend on the server ``test_httpserver.sh`` starts.

The story it drives is the one the hard timeout cannot cover. A coddy that is
drained stops its background tasks; a coddy that is *killed* leaves the shell
trees it spawned running with nobody supervising them. The task record persists
the process group leader pid, so a fresh coddy over the same session bundle can
find those survivors and reap them.

  1. boot coddy, ask the model to start one long background task;
  2. read the task's pid from the REST surface and confirm the OS agrees;
  3. kill coddy outright - no drain, no cleanup;
  4. confirm the background process outlived it;
  5. boot coddy again on the same home and session, and ask the model to clean up;
  6. confirm the process is gone and the record says so.

Step 6 is what regressed on Windows: probing a pid there answers "can I open a
process object with this number", which is true for a corpse whose handle is
still held and true for any recycled pid, so the model was told about leftovers
that were not there and reaping ran taskkill on whatever now owned the number.

Requires ``build/coddy`` built with ``-tags http``.

Environment:

- ``CODDY_BIN`` - path to the coddy binary (default ``<repo>/build/coddy``).
- ``MODEL`` - YAML ``models[].model`` id (default ``neuraldeep/gpt-oss-120b``).
- ``NEURALDEEP_API_KEY`` - provider key, read by coddy itself. Never written to
  the generated config: a provider named ``neuraldeep`` with no ``api_key``
  falls back to this variable.
- ``REAP_PORT`` - loopback port (default 19912).

Exits non-zero on any HTTP error or unmet expectation.
"""

from __future__ import annotations

import json
import os
import platform
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any, Tuple

SESSION_ID = "http-e2e-background-reap"

CONFIG_TEMPLATE = """\
providers:
  - name: neuraldeep
    type: neuraldeep

models:
  - model: "__MODEL__"
    max_tokens: 8192
    temperature: 0.2
    max_context_tokens: 131072

agent:
  model: "__MODEL__"
  max_turns: 12

sessions:
  dir: ""

mcp_servers: []

tools:
  permission_mode: bypass

logger:
  level: "info"
  outputs: ["stderr"]
  format: "text"
"""


def repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


def fail(msg: str) -> None:
    print("FAIL:", msg, file=sys.stderr)
    raise SystemExit(1)


def http_json(
    method: str,
    url: str,
    body: dict[str, Any] | None,
    headers: dict[str, str],
    timeout: int = 300,
) -> Tuple[int, dict[str, Any]]:
    data = None if body is None else json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Accept", "application/json")
    if data is not None:
        req.add_header("Content-Type", "application/json")
    for k, v in headers.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            return resp.status, (json.loads(raw) if raw.strip() else {})
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"http {method} {url} failed: {e.code} {raw}") from e


def pid_alive(pid: int) -> bool:
    """Ask the OS directly, from outside coddy, whether the pid is a live process.

    Deliberately not coddy's own probe: this harness exists to check that probe,
    so it must not borrow it. tasklist only lists running processes, and signal 0
    only reaches one.
    """
    if pid <= 0:
        return False
    if platform.system() == "Windows":
        out = subprocess.run(
            ["tasklist", "/FI", f"PID eq {pid}", "/NH"],
            capture_output=True,
            text=True,
            check=False,
        ).stdout
        return str(pid) in out
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def kill_pid(pid: int) -> None:
    """Best-effort cleanup so a failed run does not leave a sleep behind."""
    if pid <= 0:
        return
    if platform.system() == "Windows":
        subprocess.run(["taskkill", "/T", "/F", "/PID", str(pid)], capture_output=True, check=False)
        return
    try:
        os.kill(pid, 9)
    except OSError:
        pass


def boot_server(binary: Path, home: Path, work: Path, port: int) -> Tuple[subprocess.Popen, str]:
    args = [
        str(binary), "http",
        "--config", str(home / "config.yaml"),
        "--home", str(home),
        "--cwd", str(work),
        "-H", "127.0.0.1", "-P", str(port),
    ]
    env = dict(os.environ)
    # The flags above are the whole point; an ambient home or config from the
    # suite that invoked this script must not steer the child somewhere else.
    env.pop("CODDY_CONFIG", None)
    env.pop("CODDY_HOME", None)
    proc = subprocess.Popen(args, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    base = f"http://127.0.0.1:{port}"
    for _ in range(240):
        if proc.poll() is not None:
            raise RuntimeError(f"coddy exited early with {proc.returncode}")
        try:
            code, _ = http_json("GET", f"{base}/v1/models", None, {}, timeout=10)
            if code == 200:
                return proc, base
        except (OSError, RuntimeError):
            pass
        time.sleep(0.25)
    proc.kill()
    raise RuntimeError(f"coddy did not become ready on port {port}")


def prompt(base: str, model: str, text: str) -> str:
    """Run one agent turn and return the assistant's reply."""
    code, body = http_json(
        "POST",
        f"{base}/v1/chat/completions",
        {
            "model": os.environ.get("CODDY_CHAT_PROFILE", "agent"),
            "stream": False,
            "metadata": {"model": model},
            "messages": [{"role": "user", "content": text}],
        },
        {"X-Coddy-Session-ID": SESSION_ID},
    )
    if code != 200:
        fail(f"chat completion answered {code}")
    choices = body.get("choices") or []
    if not choices:
        fail(f"chat completion returned no choices: {body}")
    return str((choices[0].get("message") or {}).get("content") or "")


def task_rows(base: str) -> list[dict[str, Any]]:
    code, listing = http_json(
        "GET",
        f"{base}/coddy/sessions/{SESSION_ID}/background-tasks",
        None,
        {"X-Coddy-Session-ID": SESSION_ID},
        timeout=30,
    )
    if code != 200:
        fail(f"background task list answered {code}")
    return listing.get("data") or []


def main() -> int:
    binary = Path(os.environ.get("CODDY_BIN", str(repo_root() / "build" / "coddy")))
    if not binary.is_file():
        candidate = binary.with_suffix(".exe")
        if candidate.is_file():
            binary = candidate
        else:
            print(f"coddy binary not found: {binary} (build with -tags http)", file=sys.stderr)
            return 1

    model = os.environ.get("MODEL", "neuraldeep/gpt-oss-120b").strip()
    port = int(os.environ.get("REAP_PORT", "19912"))
    home = Path(tempfile.mkdtemp(prefix="coddy-reap-home-"))
    work = Path(tempfile.mkdtemp(prefix="coddy-reap-work-"))

    # Inside test_httpserver.sh the suite already resolved a config and a model;
    # reuse them rather than second-guessing which provider is in play. Run on its
    # own and the harness generates a NeuralDeep config, whose key comes from the
    # environment so no secret is ever written to disk.
    supplied = os.environ.get("CODDY_CONFIG", "").strip()
    if supplied:
        (home / "config.yaml").write_text(Path(supplied).read_text(encoding="utf-8"), encoding="utf-8")
    else:
        provider = model.split("/", 1)[0]
        key_var = provider.upper().replace("-", "_") + "_API_KEY"
        if not os.environ.get(key_var, "").strip():
            print(f"{key_var} is not set and CODDY_CONFIG is unset; this harness needs a real model", file=sys.stderr)
            return 1
        (home / "config.yaml").write_text(CONFIG_TEMPLATE.replace("__MODEL__", model), encoding="utf-8")

    leftover_pid = 0
    first = second = None
    try:
        # 1. The run that is about to be killed.
        first, base = boot_server(binary, home, work, port)
        reply = prompt(
            base,
            model,
            "Start ONE background task and nothing else.\n"
            "Call run_command with background:true, expected_seconds:600, and a command "
            "that simply sleeps for 600 seconds using this machine's shell "
            "(PowerShell: 'Start-Sleep -Seconds 600'; POSIX shell: 'sleep 600').\n"
            "As soon as the tool returns a task id, reply with that id and stop. "
            "Do NOT call background_wait and do NOT call background_stop.",
        )

        rows = task_rows(base)
        if not rows:
            fail(f"the model started no background task; it replied: {reply!r}")
        task = rows[-1]
        task_id = str(task.get("id") or "")
        leftover_pid = int(task.get("pid") or 0)
        if leftover_pid <= 0:
            fail(f"task {task_id} carries no pid, so no later run could ever reach it")

        # 2. The OS has to agree the process exists before anything else means much.
        if not pid_alive(leftover_pid):
            fail(f"the OS does not know pid {leftover_pid} that task {task_id} claims")
        print(f"  started {task_id} as pid {leftover_pid}")

        # 3. Kill coddy outright. No drain, so nothing stops the child.
        first.kill()
        first.wait(timeout=30)
        first = None
        time.sleep(1.0)

        # 4. That is the whole premise: the work outlived the run that started it.
        if not pid_alive(leftover_pid):
            fail(f"pid {leftover_pid} died with coddy, so there is no leftover to reap")
        print("  coddy was killed and the background process outlived it")

        # 5. A fresh coddy over the same bundle, told to clean up.
        second, base = boot_server(binary, home, work, port)

        listed = task_rows(base)
        if not any(str(r.get("id")) == task_id for r in listed):
            fail(f"the fresh run does not see task {task_id} from the bundle: {listed}")

        reply = prompt(
            base,
            model,
            "An earlier coddy run on this session was killed and may have left background "
            "processes behind. Check the background tasks, and if anything is still alive "
            "from that earlier run, reap it. Then report what you killed.",
        )
        print(f"  model replied: {reply.strip()[:400]}")

        # 6. The only assertion that matters: the survivor is actually gone.
        deadline = time.time() + 60
        while time.time() < deadline and pid_alive(leftover_pid):
            time.sleep(0.5)
        if pid_alive(leftover_pid):
            fail(f"pid {leftover_pid} is still running after the model was asked to reap it")

        # And the record says so, so a later run does not offer the same kill again.
        final = next((r for r in task_rows(base) if str(r.get("id")) == task_id), {})
        status = str(final.get("status") or "")
        if status not in ("stopped", "orphaned"):
            fail(f"task {task_id} ended as {status!r}, want stopped")
        if status != "stopped":
            fail(f"task {task_id} is still {status!r}: the reap was never recorded")

        print(f"ok http background reap e2e ({task_id}, pid {leftover_pid}, model {model})")
        return 0
    finally:
        for proc in (first, second):
            if proc is None:
                continue
            proc.terminate()
            try:
                proc.wait(timeout=15)
            except subprocess.TimeoutExpired:
                proc.kill()
        kill_pid(leftover_pid)


if __name__ == "__main__":
    raise SystemExit(main())
