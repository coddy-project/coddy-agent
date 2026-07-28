#!/usr/bin/env python3
"""ACP e2e: context compaction — manual /compact command plus auto threshold.

HTTP twin: ``examples/httpserver/http_e2e_compact.py`` (manual + REST endpoint).

Phase 1 (manual): seeds three short real-LLM exchanges, sends the built-in
``/compact`` command, then verifies against the persisted session bundle:

- ``messages.json`` gains a row with ``compaction_summary: true``;
- every seeded exchange stays in the transcript (nothing is deleted);
- the ``/compact`` command text itself is not persisted;
- an assistant confirmation row mentions the compaction;
- a follow-up prompt still works (the session continues from the summary).

Phase 2 (auto): restarts coddy with a derived config whose first model has a
tiny ``max_context_tokens``, so a regular prompt crosses the default 80%%
threshold and compacts automatically (summary row appears without any
``/compact``). The phase is skipped when the config file has no
``max_context_tokens`` line to shrink.

Environment: CODDY_BIN, CODDY_CONFIG, SESSION_ROOT, SESSION_ID.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any


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


def default_config() -> str:
    return str(Path(__file__).resolve().parent.parent / "config.demo.yaml")


def rpc_call(
    proc: subprocess.Popen[str],
    method: str,
    params: dict[str, Any],
    next_id: list[int],
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    rid = next_id[0]
    next_id[0] += 1
    proc.stdin.write(
        jd({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}) + "\n"
    )
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
            proc.stdin.write(
                jd({"jsonrpc": "2.0", "id": msg.get("id"), "result": {"outcome": "allow"}})
                + "\n"
            )
            proc.stdin.flush()
            continue

        if m == "session/update":
            backlog.append(msg)
            continue

        if ("result" in msg or "error" in msg) and same_id(msg.get("id"), rid):
            return msg, backlog

        backlog.append(msg)


def start_coddy(binary: str, cfg: str, session_root: str, session_id: str, work: str) -> subprocess.Popen[str]:
    return subprocess.Popen(
        [
            "stdbuf",
            "-oL",
            "-eL",
            binary,
            "acp",
            "--config",
            cfg,
            "--sessions-dir",
            session_root,
            "--session-id",
            session_id,
            "--cwd",
            work,
            "--log-level",
            "warn",
        ],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=sys.stderr,
        text=True,
        bufsize=1,
    )


def read_messages(session_root: str, session_id: str) -> list[dict[str, Any]]:
    path = Path(session_root) / session_id / "messages.json"
    if not path.is_file():
        return []
    data = json.loads(path.read_text(encoding="utf-8"))
    return data.get("messages", [])


def bootstrap(proc: subprocess.Popen[str], work: str, nid: list[int]) -> str:
    r0, _ = rpc_call(
        proc,
        "initialize",
        {
            "protocolVersion": 1,
            "clientCapabilities": {"fs": {"readTextFile": True, "writeTextFile": True}},
            "clientInfo": {"name": "acp-e2e-compact", "title": "E2E", "version": "1.0.0"},
        },
        nid,
    )
    if "error" in r0:
        raise RuntimeError(f"initialize error: {jd(r0)}")
    r1, _ = rpc_call(proc, "session/new", {"cwd": work, "mcpServers": []}, nid)
    if "error" in r1:
        raise RuntimeError(f"session/new error: {jd(r1)}")
    return r1["result"]["sessionId"]


def prompt(proc: subprocess.Popen[str], sid: str, text: str, nid: list[int]) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    rp, backlog = rpc_call(
        proc,
        "session/prompt",
        {"sessionId": sid, "prompt": [{"type": "text", "text": text}]},
        nid,
    )
    if "error" in rp:
        raise RuntimeError(f"session/prompt error: {jd(rp)}")
    return rp, backlog


SEED_PROMPTS = [
    "Remember the codeword AZURE-7. Reply with one short sentence acknowledging it.",
    "Name any one planet of the Solar System. One short sentence.",
    "State any year in the 20th century. One short sentence.",
]


def run_manual_phase(binary: str, cfg: str, session_root: str, session_id: str) -> int:
    work = tempfile.mkdtemp(prefix="coddy-acp-compact-")
    sdir = os.path.join(session_root, session_id)
    if os.path.isdir(sdir):
        shutil.rmtree(sdir)
    proc = start_coddy(binary, cfg, session_root, session_id, work)
    assert proc.stdin is not None
    nid = [1]
    rc = 0
    try:
        sid = bootstrap(proc, work, nid)
        print("manual phase sessionId=", sid, file=sys.stderr)

        for p in SEED_PROMPTS:
            prompt(proc, sid, p, nid)

        rp, backlog = prompt(proc, sid, "/compact", nid)
        print("compact stopReason=", rp.get("result"), file=sys.stderr)
        chunk_text = "".join(
            (u.get("params", {}).get("update") or {}).get("content", {}).get("text", "")
            for u in backlog
            if (u.get("params", {}).get("update") or {}).get("sessionUpdate") == "agent_message_chunk"
        )
        if "compact" not in chunk_text.lower():
            print(f"FAIL: no compaction confirmation streamed, got {chunk_text!r}", file=sys.stderr)
            rc = 11

        msgs = read_messages(session_root, session_id)
        summaries = [m for m in msgs if m.get("compaction_summary")]
        if not summaries:
            print("FAIL: messages.json has no compaction_summary row", file=sys.stderr)
            rc = rc or 12
        joined = "\n".join(str(m.get("content", "")) for m in msgs)
        for p in SEED_PROMPTS:
            if p not in joined:
                print(f"FAIL: seeded prompt lost from transcript: {p!r}", file=sys.stderr)
                rc = rc or 13
        if "/compact" in joined:
            print("FAIL: /compact command leaked into transcript", file=sys.stderr)
            rc = rc or 14
        if not any(
            m.get("role") == "assistant" and "compact" in str(m.get("content", "")).lower()
            for m in msgs
        ):
            print("FAIL: no assistant confirmation row", file=sys.stderr)
            rc = rc or 15

        before = len(read_messages(session_root, session_id))
        prompt(proc, sid, "Reply with exactly: STILL-ALIVE", nid)
        after_msgs = read_messages(session_root, session_id)
        if len(after_msgs) <= before:
            print("FAIL: follow-up prompt after compaction produced no new rows", file=sys.stderr)
            rc = rc or 16
        print(f"manual phase: rows={len(after_msgs)} summaries={len(summaries)}", file=sys.stderr)
    finally:
        proc.stdin.close()
        proc.wait(timeout=600)
        shutil.rmtree(work, ignore_errors=True)
    return rc


def derive_tiny_window_config(cfg: str) -> str | None:
    """Copy the config, shrinking the first max_context_tokens to 64 tokens."""
    text = Path(cfg).read_text(encoding="utf-8")
    marker = "max_context_tokens:"
    idx = text.find(marker)
    if idx < 0:
        return None
    eol = text.find("\n", idx)
    derived = text[:idx] + "max_context_tokens: 64" + text[eol:]
    fd, path = tempfile.mkstemp(prefix="coddy-compact-tiny-", suffix=".yaml")
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        f.write(derived)
    return path


def run_auto_phase(binary: str, cfg: str, session_root: str, session_id: str) -> int:
    tiny_cfg = derive_tiny_window_config(cfg)
    if tiny_cfg is None:
        print("SKIP auto phase: config has no max_context_tokens line", file=sys.stderr)
        return 0
    work = tempfile.mkdtemp(prefix="coddy-acp-compact-auto-")
    auto_session = session_id + "-auto"
    sdir = os.path.join(session_root, auto_session)
    if os.path.isdir(sdir):
        shutil.rmtree(sdir)
    proc = start_coddy(binary, tiny_cfg, session_root, auto_session, work)
    assert proc.stdin is not None
    nid = [1]
    rc = 0
    try:
        sid = bootstrap(proc, work, nid)
        print("auto phase sessionId=", sid, file=sys.stderr)
        # Three user turns: with keep_recent_turns=2 the third turn crosses the
        # tiny window threshold and compacts before the model call.
        for p in SEED_PROMPTS:
            prompt(proc, sid, p, nid)
        msgs = read_messages(session_root, auto_session)
        summaries = [m for m in msgs if m.get("compaction_summary")]
        if not summaries:
            print("FAIL: auto phase produced no compaction_summary row", file=sys.stderr)
            rc = 21
        joined = "\n".join(str(m.get("content", "")) for m in msgs)
        for p in SEED_PROMPTS:
            if p not in joined:
                print(f"FAIL: auto phase lost seeded prompt: {p!r}", file=sys.stderr)
                rc = rc or 22
        print(f"auto phase: rows={len(msgs)} summaries={len(summaries)}", file=sys.stderr)
    finally:
        proc.stdin.close()
        proc.wait(timeout=600)
        shutil.rmtree(work, ignore_errors=True)
        os.unlink(tiny_cfg)
    return rc


def main() -> None:
    binary = os.environ.get("CODDY_BIN", default_coddy_bin())
    cfg = os.environ.get("CODDY_CONFIG", default_config())
    session_root = os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp-e2e")
    session_id = os.environ.get("SESSION_ID", "example-acp") + "-compact-e2e"
    os.makedirs(session_root, exist_ok=True)

    rc = run_manual_phase(binary, cfg, session_root, session_id)
    rc = rc or run_auto_phase(binary, cfg, session_root, session_id)
    if rc == 0:
        print("ok acp compact e2e", file=sys.stderr)
    sys.exit(rc)


if __name__ == "__main__":
    main()
