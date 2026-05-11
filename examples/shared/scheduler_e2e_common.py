#!/usr/bin/env python3
"""Shared helpers for scheduler E2E demos (acp and http).

Config template: examples/config.demo.yaml (placeholder __E2E_LOG_PATH__ for global log).
"""

from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import threading
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any, Callable

EXAMPLES_DIR = Path(__file__).resolve().parent.parent
CONFIG_DEMO = EXAMPLES_DIR / "config.demo.yaml"

# Keep in sync with httpserver.port in config.demo.yaml when running HTTP e2e.
DEFAULT_HTTPSERVER_PORT = 19876

PHASE1_MARKER = "PHASE1.marker"
PHASE1_NEEDLE = "PHASE1_SCHEDULER_E2E_OK"
JOB_BASENAME = "e2e_minute_tick.md"
JOB_ID = Path(JOB_BASENAME).stem
JOB_DESCRIPTION = "E2E minute scheduler tick"
JOB_SCHEDULE = "* * * * *"
RESULT_FILE = "SCHEDULER_RUN_RESULT.txt"
RESULT_NEEDLE = "SCHEDULER_RUN_RESULT_OK"

# Must match slog msg strings in external/scheduler/daemon/run.go (text or JSON log lines).
SCHEDULER_LOG_RUN_SPAWN = "scheduler_run_spawn"
SCHEDULER_LOG_RUN_FINISH = "scheduler_run_finish"


def jd(obj: dict[str, Any]) -> str:
    return json.dumps(obj, separators=(",", ":"), ensure_ascii=False)


def same_id(a: Any, b: Any) -> bool:
    if a == b:
        return True
    try:
        return float(a) == float(b)
    except (TypeError, ValueError):
        return False


def default_coddy_bin() -> str:
    exe = shutil.which("coddy")
    return exe if exe else "coddy"


def load_e2e_config(work: Path) -> str:
    if not CONFIG_DEMO.is_file():
        raise FileNotFoundError(f"missing shared config: {CONFIG_DEMO}")
    raw = CONFIG_DEMO.read_text(encoding="utf-8")
    log_path = (work / "coddy-e2e-global.log").resolve()
    if "__E2E_LOG_PATH__" not in raw:
        raise ValueError(f"{CONFIG_DEMO} must set logger.file to __E2E_LOG_PATH__ for e2e")
    return raw.replace("__E2E_LOG_PATH__", str(log_path))


def httpserver_port_from_demo() -> int:
    raw = CONFIG_DEMO.read_text(encoding="utf-8")
    m = re.search(
        r"(?ms)^httpserver:\s*(?:^\s+.*\n)+",
        raw,
    )
    if not m:
        return DEFAULT_HTTPSERVER_PORT
    block = m.group(0)
    pm = re.search(r"^\s*port:\s*(\d+)\s*$", block, re.M)
    if pm:
        return int(pm.group(1))
    return DEFAULT_HTTPSERVER_PORT


def rpc_call(
    proc: subprocess.Popen[str],
    method: str,
    params: dict[str, Any],
    next_id: list[int],
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    rid = next_id[0]
    next_id[0] += 1
    assert proc.stdin is not None
    proc.stdin.write(jd({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}) + "\n")
    proc.stdin.flush()

    backlog: list[dict[str, Any]] = []
    assert proc.stdout is not None

    while True:
        line = proc.stdout.readline()
        if not line:
            raise RuntimeError("unexpected EOF from coddy stdout")
        line = line.strip()
        if not line:
            continue
        msg = json.loads(line)
        m = msg.get("method")

        if m == "session/request_permission":
            proc.stdin.write(jd({"jsonrpc": "2.0", "id": msg.get("id"), "result": {"outcome": "allow"}}) + "\n")
            proc.stdin.flush()
            backlog.append({"_kind": "request_permission_sent", **msg})
            continue

        if m == "session/update":
            backlog.append(msg)
            continue

        if "id" in msg and "method" not in msg:
            if same_id(msg.get("id"), rid):
                return msg, backlog
            backlog.append({"_kind": "unexpected_response", **msg})
            continue

        if "result" in msg or "error" in msg:
            if same_id(msg.get("id"), rid):
                return msg, backlog
            backlog.append({"_kind": "unexpected_response", **msg})
            continue

        backlog.append({"_kind": "unknown_line", **msg})


def http_json(
    method: str, url: str, body: dict[str, Any] | None,
) -> tuple[int, dict[str, Any], dict[str, str]]:
    data = None if body is None else json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Accept", "application/json")
    if data is not None:
        req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=600) as resp:
        raw = resp.read().decode("utf-8", errors="replace")
        out = json.loads(raw) if raw.strip() else {}
        hdrs = {k: v for k, v in resp.headers.items()}
        return resp.status, out, hdrs


def job_instruction_body() -> str:
    return (
        "In the session working directory, run shell command:\n"
        f"bash -lc 'printf %s \"{RESULT_NEEDLE}\" > {RESULT_FILE}'\n\n"
        "Then stop.\n"
    )


def job_create_args() -> dict[str, Any]:
    return {
        "job_id": JOB_ID,
        "description": JOB_DESCRIPTION,
        "schedule": JOB_SCHEDULE,
        "cwd": "",
        "mode": "agent",
        "body": job_instruction_body(),
    }


def job_markdown_exact() -> str:
    return (
        f"---\n"
        f'description: "{JOB_DESCRIPTION}"\n'
        f'schedule: "{JOB_SCHEDULE}"\n'
        "cwd: \"\"\n"
        "mode: agent\n"
        "---\n\n"
        + job_instruction_body()
    )


def verify_job_md(path: Path) -> None:
    text = path.read_text(encoding="utf-8", errors="replace")
    if text.count("---") < 2:
        raise AssertionError(f"job {path} missing YAML frontmatter fences")
    if JOB_SCHEDULE.strip() not in text:
        raise AssertionError(f'job missing schedule line containing "{JOB_SCHEDULE}"')
    if JOB_DESCRIPTION not in text and JOB_DESCRIPTION.split()[0].lower() not in text.lower():
        raise AssertionError("job missing expected description")
    if RESULT_FILE not in text or RESULT_NEEDLE not in text:
        raise AssertionError("job missing RESULT_FILE body or NEEDLE literal")


def wait_until(
    pred: Callable[[], bool],
    timeout_sec: float,
    *,
    poll: float = 0.4,
    what: str = "",
) -> None:
    deadline = time.time() + timeout_sec
    while time.time() < deadline:
        if pred():
            return
        time.sleep(poll)
    raise TimeoutError(what or "condition not reached in time")


def wait_log_patterns(log_path: Path, patterns: list[str], timeout_sec: float) -> str:
    def ok() -> bool:
        if not log_path.is_file():
            return False
        blob = log_path.read_text(encoding="utf-8", errors="replace")
        return all(p in blob for p in patterns)

    wait_until(ok, timeout_sec, what=f"log patterns {patterns} -> {log_path}")
    return log_path.read_text(encoding="utf-8", errors="replace")


def assert_no_stale_lock(job_md: Path) -> None:
    lock_path = job_md.parent / f"{job_md.stem}.lock"
    wait_until(lambda: not lock_path.exists(), 30.0, what="stale scheduler lock cleared")
    if lock_path.exists():
        raise AssertionError(f"scheduler lock still present: {lock_path}")


def ensure_job_file_written(home_scheduler: Path, work: Path) -> Path:
    path = home_scheduler / JOB_BASENAME
    if path.is_file():
        try:
            verify_job_md(path)
            return path
        except AssertionError:
            pass
    home_scheduler.mkdir(parents=True, exist_ok=True)
    path.write_text(job_markdown_exact(), encoding="utf-8")
    verify_job_md(path)
    print("harness wrote scheduler job fallback", path, flush=True)
    return path


def drain_stderr(proc: subprocess.Popen[str]) -> None:
    try:
        assert proc.stderr is not None
        for _ in iter(proc.stderr.readline, ""):
            continue
    except Exception:
        return


def run_acp(binary: Path, cfg_path: Path, home: Path, work: Path) -> int:
    proc = subprocess.Popen(
        [
            str(binary),
            "acp",
            "--config",
            str(cfg_path),
            "--home",
            str(home),
            "--cwd",
            str(work),
            "--scheduler-enabled",
            "--sessions-dir",
            str(home / "sessions"),
        ],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        bufsize=1,
    )

    threading.Thread(target=drain_stderr, args=(proc,), daemon=True).start()
    nid = [1]

    scheduler_dir = home / "scheduler"
    scheduler_dir.mkdir(parents=True, exist_ok=True)

    create_json = jd(job_create_args())

    compound_prompt = f"""Working directory MUST be: {work}
Do not browse outside this cwd.

STEP 1 - Run exactly this shell via run_command tool (choose the project's run_command wrapper name if different):
bash -lc 'echo {PHASE1_NEEDLE} > {PHASE1_MARKER}'
Verify file {PHASE1_MARKER} exists in cwd before continuing.

STEP 2 - Call tool coddy_scheduler_job_create exactly once. Use these JSON arguments verbatim (one tool call): {create_json}

STEP 3 - Reply single line OK when both steps succeeded.
"""

    glo = (work / "coddy-e2e-global.log").resolve()

    try:
        r0, _ = rpc_call(
            proc,
            "initialize",
            {
                "protocolVersion": 1,
                "clientCapabilities": {"fs": {"readTextFile": True, "writeTextFile": True}, "terminal": True},
                "clientInfo": {"name": "sched-e2e", "title": "E2E", "version": "1.0.0"},
            },
            nid,
        )
        if "error" in r0:
            print("initialize error:", r0, flush=True)
            return 3

        r1, _ = rpc_call(proc, "session/new", {"cwd": str(work), "mcpServers": []}, nid)
        if "error" in r1:
            print("session/new error:", r1, flush=True)
            return 4

        rp, _ = rpc_call(
            proc,
            "session/prompt",
            {"sessionId": r1["result"]["sessionId"], "prompt": [{"type": "text", "text": compound_prompt}]},
            nid,
        )
        if "error" in rp:
            print("session/prompt error:", rp, flush=True)
            return 5

        p1 = work / PHASE1_MARKER
        if not p1.is_file():
            print("WAIT phase1 marker", flush=True)
            wait_until(lambda: p1.is_file(), 240.0, what="phase1 marker")
        blob = p1.read_text(encoding="utf-8", errors="replace")
        if PHASE1_NEEDLE not in blob:
            raise AssertionError(f"phase1 marker missing needle: got {blob!r}")

        job_md = ensure_job_file_written(scheduler_dir, work)
        verify_job_md(job_md)

        result_path = work / RESULT_FILE

        patterns = [SCHEDULER_LOG_RUN_SPAWN, SCHEDULER_LOG_RUN_FINISH, JOB_ID]

        wait_until(lambda: result_path.is_file(), 210.0, what="scheduled run_result file")
        data = result_path.read_text(encoding="utf-8", errors="replace")
        if RESULT_NEEDLE not in data:
            raise AssertionError(f"missing RESULT_NEEDLE in {data!r}")

        wait_log_patterns(glo, patterns, timeout_sec=90.0)
        body = glo.read_text(encoding="utf-8", errors="replace")
        if SCHEDULER_LOG_RUN_SPAWN not in body or SCHEDULER_LOG_RUN_FINISH not in body:
            raise AssertionError("scheduler lifecycle lines missing")

        state_p = job_md.parent / (job_md.stem + ".state")
        wait_until(lambda: state_p.is_file(), 120.0, what="basename.state")
        jst = json.loads(state_p.read_text(encoding="utf-8"))
        if not jst.get("last_scheduled_utc"):
            raise AssertionError(".state missing last_scheduled_utc")

        assert_no_stale_lock(job_md)
        print("ok acp e2e scheduler agent", flush=True)
        return 0
    finally:
        proc.stdin.close()
        try:
            proc.wait(timeout=30)
        except subprocess.TimeoutExpired:
            proc.kill()


def model_from_demo() -> str:
    raw = CONFIG_DEMO.read_text(encoding="utf-8")
    am = re.search(r"^\s*model:\s*\"([^\"]+)\"\s*$", raw, re.M)
    return am.group(1).strip() if am else os.environ.get("MODEL", "rpa/gpt-oss:120b").strip()


def resolve_scheduler_global_log(home: Path, work: Path) -> Path:
    """Prefer CODDY_HOME/e2e.log (examples harness); else workdir global log."""
    for p in (home / "e2e.log", work / "coddy-e2e-global.log"):
        if p.is_file():
            return p
    return home / "e2e.log"


def run_http_scheduler_agent_e2e_existing(base_v1: str, home: Path, work: Path) -> int:
    """Drive scheduler via HTTP chat against an already running coddy http (same home or cwd as process).

    Expects BASE_URL-style ``base_v1`` ending in ``/v1``, scheduler dir under ``home/scheduler``,
    and job side effects under ``work``.
    """
    base = base_v1.rstrip("/")
    if not base.endswith("/v1"):
        print("BASE_URL must end with /v1", flush=True)
        return 12

    model = os.environ.get("MODEL", "").strip() or model_from_demo()
    create_json = jd(job_create_args())
    compound_prompt = f"""Working directory MUST be: {work}
STEP 1: run_command bash -lc 'echo {PHASE1_NEEDLE} > {PHASE1_MARKER}'
STEP 2: coddy_scheduler_job_create JSON arguments exactly (one call): {create_json}
STEP 3: reply OK
"""

    code, cc, _hdr = http_json(
        "POST",
        f"{base}/chat/completions",
        {
            "model": model,
            "stream": False,
            "messages": [{"role": "user", "content": compound_prompt}],
        },
    )
    if code != 200:
        print("chat failed", code, cc, flush=True)
        return 14

    scheduler_dir = home / "scheduler"

    p1 = work / PHASE1_MARKER
    if not p1.is_file():
        wait_until(lambda: p1.is_file(), 240.0, what="phase1 marker http")

    job_md = ensure_job_file_written(scheduler_dir, work)
    verify_job_md(job_md)

    result_path = work / RESULT_FILE
    glo = resolve_scheduler_global_log(home, work)

    wait_until(lambda: result_path.is_file(), 210.0, what="http scheduled result")

    txt = result_path.read_text(encoding="utf-8", errors="replace")
    if RESULT_NEEDLE not in txt:
        raise AssertionError(f"missing RESULT_NEEDLE HTTP {txt!r}")

    wait_log_patterns(
        glo.resolve(),
        [SCHEDULER_LOG_RUN_SPAWN, SCHEDULER_LOG_RUN_FINISH, JOB_ID],
        90.0,
    )
    state_p = job_md.parent / (job_md.stem + ".state")
    wait_until(lambda: state_p.is_file(), 120.0, what="basename.state http")
    assert_no_stale_lock(job_md)
    print("ok http scheduler agent e2e", flush=True)
    return 0


def run_http(binary: Path, cfg_path: Path, home: Path, work: Path, port: int) -> int:
    url = f"http://127.0.0.1:{port}/v1/models"
    base = f"http://127.0.0.1:{port}/v1"

    proc = subprocess.Popen(
        [
            str(binary),
            "http",
            "--config",
            str(cfg_path),
            "--home",
            str(home),
            "--cwd",
            str(work),
            "--sessions-dir",
            str(home / "sessions_http"),
            "--scheduler-enabled",
            "-H",
            "127.0.0.1",
            "-P",
            str(port),
        ],
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        text=True,
    )

    try:
        for _ in range(100):
            try:
                code, _, _ = http_json("GET", url, None)
                if code == 200:
                    break
            except urllib.error.URLError:
                time.sleep(0.15)
        else:
            print("HTTP server did not respond", flush=True)
            return 13

        return run_http_scheduler_agent_e2e_existing(base, home, work)
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=20)
        except subprocess.TimeoutExpired:
            proc.kill()


def validate_coddy_scheduler_bin(bin_path: Path, *, need_http_help: bool) -> int | None:
    if not bin_path.is_file() and shutil.which(str(bin_path)) is None:
        print(f"CODDY_BIN not executable: {bin_path}", flush=True)
        return 99

    proc_help = subprocess.run([str(bin_path), "acp", "--help"], capture_output=True, text=True)
    help_txt = (proc_help.stdout or "") + (proc_help.stderr or "")
    if "scheduler-enabled" not in help_txt:
        print(
            "Binary missing -scheduler-enabled (rebuild with: go build -tags=scheduler)",
            flush=True,
        )
        return 97

    if need_http_help:
        hp = subprocess.run([str(bin_path), "http", "--help"], capture_output=True, text=True)
        hh = (hp.stdout or "") + (hp.stderr or "")
        if "scheduler-enabled" not in hh:
            print("Binary missing scheduler on http command (need -tags http,scheduler)", flush=True)
            return 96
    return None
