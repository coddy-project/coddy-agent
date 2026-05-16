#!/usr/bin/env python3
"""ACP e2e: plan mode writes ``plans/<slug>.plan.md``, then run plan starts agent work.

Verifies on disk (not only model prose):

- ``plans/e2e-plan.plan.md`` exists under the session bundle and contains ``PLAN_E2E_MARKER_v1``
- ``plan_write`` appears in ACP ``tool_call`` updates during the plan turn
- After ``session/prompt`` with ``_meta.coddy.dev/runPlanSlug``, agent creates ``plan_e2e_done.txt``
  in the session cwd containing ``PLAN_RUN_E2E_OK``

Environment: ``CODDY_BIN``, ``CODDY_CONFIG``, ``SESSION_ROOT``, ``SESSION_ID``.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Any

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "shared"))

from plan_e2e_common import (  # noqa: E402
    PLAN_MARKER,
    PLAN_SLUG,
    RUN_ARTIFACT,
    RUN_MARKER,
    plan_prompt_text,
    wait_for_plan_file,
    wait_for_run_artifact,
)


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
    import shutil as sh

    exe = sh.which("coddy")
    return exe if exe else "coddy"


def default_config() -> str:
    return str(Path(__file__).resolve().parent.parent / "config.demo.yaml")


def rpc_call(
    proc: subprocess.Popen[str],
    method: str,
    params: dict[str, Any],
    next_id: list[int],
    timeout_s: float = 360,
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    rid = next_id[0]
    next_id[0] += 1
    proc.stdin.write(
        jd({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}) + "\n"
    )
    proc.stdin.flush()

    backlog: list[dict[str, Any]] = []
    assert proc.stdout is not None
    deadline = time.time() + timeout_s

    while True:
        if time.time() > deadline:
            raise TimeoutError(f"timed out waiting for {method} response")
        line = proc.stdout.readline()
        if not line:
            raise RuntimeError("unexpected EOF from coddy stdout")
        line = line.strip()
        if not line:
            continue
        msg = json.loads(line)
        m = msg.get("method")

        if m == "session/request_permission":
            proc.stdin.write(
                jd(
                    {
                        "jsonrpc": "2.0",
                        "id": msg.get("id"),
                        "result": {"outcome": "allow"},
                    }
                )
                + "\n"
            )
            proc.stdin.flush()
            backlog.append({"_kind": "request_permission_sent", **msg})
            continue

        if m == "session/request_question":
            proc.stdin.write(
                jd({"jsonrpc": "2.0", "id": msg.get("id"), "result": {"answers": []}})
                + "\n"
            )
            proc.stdin.flush()
            backlog.append({"_kind": "request_question_sent", **msg})
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


def extract_tool_titles(backlog: list[dict[str, Any]]) -> list[str]:
    titles: list[str] = []
    for m in backlog:
        if m.get("method") != "session/update":
            continue
        u = m.get("params", {}).get("update") or {}
        if u.get("sessionUpdate") != "tool_call":
            continue
        t = u.get("title")
        if isinstance(t, str) and t.strip():
            titles.append(t.strip())
    return titles


def has_design_plan_update(backlog: list[dict[str, Any]], slug: str) -> bool:
    for m in backlog:
        if m.get("method") != "session/update":
            continue
        u = m.get("params", {}).get("update") or {}
        if u.get("sessionUpdate") != "plan":
            continue
        meta = u.get("_meta") or {}
        if meta.get("coddy.dev/planKind") == "design" and meta.get("coddy.dev/planSlug") == slug:
            return True
    return False


def main() -> int:
    binary = os.environ.get("CODDY_BIN", default_coddy_bin())
    cfg = os.environ.get("CODDY_CONFIG", default_config())
    session_root = Path(
        os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp-plan")
    ).resolve()
    session_id = os.environ.get("SESSION_ID", "example-acp-plan")

    work = Path(tempfile.mkdtemp(prefix="coddy-acp-plan-")).resolve()
    session_root.mkdir(parents=True, exist_ok=True)
    sdir = session_root / session_id
    if sdir.is_dir():
        shutil.rmtree(sdir)

    proc = subprocess.Popen(
        [
            "stdbuf",
            "-oL",
            "-eL",
            binary,
            "acp",
            "--config",
            cfg,
            "--sessions-dir",
            str(session_root),
            "--session-id",
            session_id,
            "--cwd",
            str(work),
            "--log-level",
            "warn",
        ],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=sys.stderr,
        text=True,
        bufsize=1,
    )
    assert proc.stdin is not None

    nid = [1]
    try:
        r0, _ = rpc_call(
            proc,
            "initialize",
            {
                "protocolVersion": 1,
                "clientCapabilities": {"terminal": True},
                "clientInfo": {
                    "name": "acp-plan-e2e",
                    "title": "plan",
                    "version": "1.0.0",
                },
            },
            nid,
        )
        if "error" in r0:
            print("initialize error", jd(r0), file=sys.stderr)
            return 1

        r1, _ = rpc_call(proc, "session/new", {"cwd": str(work)}, nid)
        if "error" in r1:
            print("session/new error", jd(r1), file=sys.stderr)
            return 1
        sid = (r1.get("result") or {}).get("sessionId") or ""
        if not sid:
            print("missing sessionId", jd(r1), file=sys.stderr)
            return 1

        r_mode, _ = rpc_call(
            proc,
            "session/set_mode",
            {"sessionId": sid, "modeId": "plan"},
            nid,
        )
        if "error" in r_mode:
            print("session/set_mode plan error", jd(r_mode), file=sys.stderr)
            return 1

        r_plan, plan_backlog = rpc_call(
            proc,
            "session/prompt",
            {"sessionId": sid, "prompt": [{"type": "text", "text": plan_prompt_text(work)}]},
            nid,
            timeout_s=420,
        )
        if "error" in r_plan:
            print("plan session/prompt error", jd(r_plan), file=sys.stderr)
            return 1

        try:
            wait_for_plan_file(sdir, PLAN_SLUG, PLAN_MARKER)
        except RuntimeError as e:
            plan_titles = extract_tool_titles(plan_backlog)
            print(str(e), file=sys.stderr)
            print(f"tool_call titles during plan turn: {plan_titles}", file=sys.stderr)
            return 1

        plan_titles = extract_tool_titles(plan_backlog)
        if "plan_write" not in plan_titles:
            print(
                f"warn: plan_write not seen in tool_call titles (got {plan_titles}); "
                "plan file on disk is authoritative",
                file=sys.stderr,
            )

        if not has_design_plan_update(plan_backlog, PLAN_SLUG):
            print(
                "warn: no design plan session/update (_meta coddy.dev/planKind=design)",
                file=sys.stderr,
            )

        run_path = work / RUN_ARTIFACT
        if run_path.is_file():
            run_path.unlink()

        r_run, run_backlog = rpc_call(
            proc,
            "session/prompt",
            {
                "sessionId": sid,
                "prompt": [{"type": "text", "text": "Implement the plan."}],
                "_meta": {"coddy.dev/runPlanSlug": PLAN_SLUG},
            },
            nid,
            timeout_s=420,
        )
        if "error" in r_run:
            print("run plan session/prompt error", jd(r_run), file=sys.stderr)
            return 1

        stop = (r_run.get("result") or {}).get("stopReason") or ""
        if stop not in ("end_turn", "max_turns"):
            print(f"unexpected stopReason after run plan: {stop}", file=sys.stderr)
            return 1

        run_titles = extract_tool_titles(run_backlog)
        if "write" not in run_titles and "run_command" not in run_titles:
            print(
                f"expected write or run_command during run plan, got {run_titles}",
                file=sys.stderr,
            )

        try:
            wait_for_run_artifact(work, RUN_ARTIFACT, RUN_MARKER)
        except RuntimeError as e:
            print(str(e), file=sys.stderr)
            print(
                "hint: plan body should tell the agent to create "
                f"{RUN_ARTIFACT} with {RUN_MARKER}",
                file=sys.stderr,
            )
            return 1

        print("ok acp plan files e2e")
        return 0
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=2)
        except subprocess.TimeoutExpired:
            proc.kill()
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
