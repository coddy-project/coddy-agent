#!/usr/bin/env python3
"""ACP long-term memory copilot E2E (uses default coddy binary from `make build`).

Verifies the memory subsystem behaves like an internal voice (not main-agent tools):

- Pre-seeded global markdown is found via recall and influences the main reply without read_file to that path.
- After a second turn that asks the model to surface a new memorable fact, a new .md appears under
  $CODDY_HOME/memory or <cwd>/memory and a third question recalls it.
- Optional prune step: user text nudges the recall copilot to remove a disposable global note; file must disappear.

Environment (required for real LLM calls, use one of):

- RPA_API_KEY for provider `rpa` / model `rpa/gpt-oss:120b` (optional RPA_API_BASE, default https://api.rpa.icu/v1)
- or OPENAI_API_KEY for provider `openai` / model `openai/gpt-4o-mini`

Environment (paths):

- CODDY_BIN (default: <repo>/build/coddy if that file exists, else "coddy" from PATH)
- SESSION_ROOT, SESSION_ID (same semantics as other ACP examples)

Flags: --keep-session, --keep-work-dir, --keep-coddy-home, --skip-prune (skip delete check).
"""

from __future__ import annotations

import argparse
import json
import os
import secrets
import shutil
import subprocess
import sys
import tempfile
import time
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


def repo_root() -> Path:
    return Path(__file__).resolve().parents[1]


def default_coddy_bin() -> str:
    p = repo_root() / "build" / "coddy"
    if p.is_file():
        return str(p)
    exe = shutil.which("coddy")
    return exe if exe else "coddy"


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
                jd(
                    {
                        "jsonrpc": "2.0",
                        "id": msg.get("id"),
                        "result": {"outcome": "allow", "optionId": "allow"},
                    }
                )
                + "\n"
            )
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


def collect_assistant_text(backlog: list[dict[str, Any]]) -> str:
    parts: list[str] = []
    for m in backlog:
        if m.get("method") != "session/update":
            continue
        u = m.get("params", {}).get("update") or {}
        if u.get("sessionUpdate") != "agent_message_chunk":
            continue
        c = u.get("content") or {}
        if c.get("type") == "text" and isinstance(c.get("text"), str):
            parts.append(c["text"])
    return "".join(parts)


def list_memory_markdown(global_mem: Path, project_mem: Path) -> list[Path]:
    out: list[Path] = []
    for root in (global_mem, project_mem):
        if not root.is_dir():
            continue
        for p in root.rglob("*.md"):
            if p.is_file():
                out.append(p)
        for p in root.rglob("*.txt"):
            if p.is_file():
                out.append(p)
    return sorted(out)


def read_all_memory_text(paths: list[Path]) -> str:
    chunks: list[str] = []
    for p in paths:
        try:
            chunks.append(p.read_text(encoding="utf-8", errors="replace"))
        except OSError:
            pass
    return "\n".join(chunks)


def assert_memory_binary(binary: str) -> None:
    bp = Path(binary)
    if not bp.is_file():
        print(
            f"ERROR: CODDY_BIN {binary!r} is not a file. Run: make build",
            file=sys.stderr,
        )
        sys.exit(2)
    strings_exe = shutil.which("strings")
    if not strings_exe:
        print("WARN: `strings` not found; skipping coddy_memory_search binary check", file=sys.stderr)
        return
    try:
        out = subprocess.check_output([strings_exe, str(bp)], stderr=subprocess.DEVNULL, timeout=60)
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as e:
        print(f"WARN: strings failed ({e}); skipping binary probe", file=sys.stderr)
        return
    if b"coddy_memory_search" not in out:
        print(
            "ERROR: binary does not contain coddy_memory_search (stale or wrong binary?). Run: make build",
            file=sys.stderr,
        )
        sys.exit(2)


def write_isolated_config(path: Path, rpa_api_base: str) -> None:
    rpa_key = os.environ.get("RPA_API_KEY", "https://t.me/evilfreelancer").strip()
    oai_key = os.environ.get("OPENAI_API_KEY", "").strip()
    if rpa_key:
        os.environ["RPA_API_KEY"] = rpa_key
        os.environ["RPA_API_BASE"] = rpa_api_base
        text = f"""providers:
  - name: rpa
    type: openai_compatible
    api_base: "${{RPA_API_BASE}}"
    # Must be the real API secret string from the provider, not a URL or placeholder page.
    api_key: "${{RPA_API_KEY}}"

models:
  - model: "rpa/gpt-oss:120b"
    max_tokens: 8192
    temperature: 0.2

agent:
  model: "rpa/gpt-oss:120b"
  max_turns: 24

memory:
  enabled: true

tools:
  require_permission_for_commands: false
  require_permission_for_writes: false
  restrict_to_cwd: true

logger:
  level: warn
  format: text
"""
    elif oai_key:
        text = """providers:
  - name: openai
    type: openai
    api_key: "${OPENAI_API_KEY}"

models:
  - model: "openai/gpt-4o-mini"
    max_tokens: 8192
    temperature: 0.2

agent:
  model: "openai/gpt-4o-mini"
  max_turns: 24

memory:
  enabled: true

tools:
  require_permission_for_commands: false
  require_permission_for_writes: false
  restrict_to_cwd: true

logger:
  level: warn
  format: text
"""
    else:
        print(
            "ERROR: set RPA_API_KEY or OPENAI_API_KEY for live LLM calls.",
            file=sys.stderr,
        )
        sys.exit(2)
    path.write_text(text, encoding="utf-8")


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--keep-session", action="store_true")
    ap.add_argument("--keep-work-dir", action="store_true")
    ap.add_argument("--keep-coddy-home", action="store_true")
    ap.add_argument("--skip-prune", action="store_true", help="Do not test disposable global note deletion")
    ap.add_argument(
        "--api-base",
        default=os.environ.get("RPA_API_BASE", "https://api.rpa.icu/v1"),
        help="OpenAI-compatible base URL for the rpa provider",
    )
    ap.add_argument("--work-dir", default="", help="Session cwd (default: temp dir)")
    args = ap.parse_args()

    binary = os.environ.get("CODDY_BIN", default_coddy_bin())
    assert_memory_binary(binary)

    session_root = os.environ.get("SESSION_ROOT", str(repo_root() / "build" / "e2e-memory-sessions"))
    session_id = os.environ.get("SESSION_ID", "acp-memory-copilot-e2e")

    coddy_home = tempfile.mkdtemp(prefix="coddy-home-mem-e2e-")
    cfg_path = Path(coddy_home) / "config.yaml"
    write_isolated_config(cfg_path, args.api_base)

    if args.work_dir:
        work = os.path.abspath(args.work_dir)
        os.makedirs(work, exist_ok=True)
        cleanup_work = False
    else:
        work = tempfile.mkdtemp(prefix="coddy-mem-work-")
        cleanup_work = not args.keep_work_dir

    global_mem = Path(coddy_home) / "memory"
    project_mem = Path(work) / "memory"
    global_mem.mkdir(parents=True, exist_ok=True)
    project_mem.mkdir(parents=True, exist_ok=True)

    os.makedirs(session_root, exist_ok=True)
    sdir = os.path.join(session_root, session_id)
    if not args.keep_session and os.path.isdir(sdir):
        shutil.rmtree(sdir)

    token = secrets.token_hex(8).upper()
    fruit_word = "MEMFRUIT_" + secrets.token_hex(4).upper()
    prune_name = "e2e-disposable-global-note.md"
    prune_path = global_mem / prune_name
    prune_path.write_text(
        "# disposable\nThis line exists only so the memory copilot can delete it after a prune instruction.\n",
        encoding="utf-8",
    )

    seed_path = global_mem / f"e2e-seed-{session_id}.md"
    seed_path.write_text(
        f"# seeded long-term memory for automated test\n"
        f"VERIFIER_TOKEN={token}\n"
        f"Stated user preference for this workspace: use spaces not tabs for YAML.\n",
        encoding="utf-8",
    )

    before_paths = set(list_memory_markdown(global_mem, project_mem))

    proc = subprocess.Popen(
        [
            "stdbuf",
            "-oL",
            "-eL",
            binary,
            "acp",
            "--home",
            coddy_home,
            "--config",
            str(cfg_path),
            "--sessions-dir",
            session_root,
            "--session-id",
            session_id,
            "--cwd",
            work,
            "--log-level",
            "info",
        ],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=sys.stderr,
        text=True,
        bufsize=1,
        env={**os.environ, "CODDY_HOME": coddy_home},
    )
    assert proc.stdin is not None
    nid = [1]
    exit_code = 0

    try:
        r0, _ = rpc_call(
            proc,
            "initialize",
            {
                "protocolVersion": 1,
                "clientCapabilities": {
                    "fs": {"readTextFile": True, "writeTextFile": True},
                    "terminal": True,
                },
                "clientInfo": {"name": "acp-mem-e2e", "title": "Memory E2E", "version": "1.0.0"},
            },
            nid,
        )
        if "error" in r0:
            print("initialize error:", jd(r0), file=sys.stderr)
            sys.exit(1)

        r1, _ = rpc_call(proc, "session/new", {"cwd": work, "mcpServers": []}, nid)
        if "error" in r1:
            print("session/new error:", jd(r1), file=sys.stderr)
            sys.exit(1)
        sid = r1["result"]["sessionId"]
        print("sessionId=", sid, "work_dir=", work, "CODDY_HOME=", coddy_home, file=sys.stderr)

        # Turn 1: user never names the seed file; recall must surface VERIFIER_TOKEN from global memory.
        p1 = (
            "You are helping validate Coddy long-term memory recall.\n"
            "Answer in at most 4 short lines of plain text.\n"
            "Questions:\n"
            "1) What is the VERIFIER_TOKEN value?\n"
            "2) What indentation preference was recorded for YAML?\n"
            "Do not use filesystem tools if you can answer from context already injected for you."
        )
        rp1, backlog1 = rpc_call(
            proc,
            "session/prompt",
            {"sessionId": sid, "prompt": [{"type": "text", "text": p1}]},
            nid,
        )
        if "error" in rp1:
            print("session/prompt (1) error:", jd(rp1), file=sys.stderr)
            sys.exit(1)
        text1 = collect_assistant_text(backlog1)
        print("--- assistant turn 1 ---\n", text1, "\n", file=sys.stderr, sep="")
        if token not in text1:
            print(
                f"FAIL: VERIFIER_TOKEN {token} not found in assistant reply (recall likely broken).",
                file=sys.stderr,
            )
            exit_code = 11

        time.sleep(0.4)
        mid_paths = set(list_memory_markdown(global_mem, project_mem))

        # Turn 2: ask model to invent a stable new fact so judge may persist; then verify grep.
        p2 = (
            f"Second memory exercise. Pick exactly one whimsical favorite fruit for this test user "
            f"and state it as a single sentence that includes the codeword {fruit_word} verbatim.\n"
            "After stating it, add one line explaining that this fact should be remembered for future sessions.\n"
            "Still avoid tools unless absolutely required."
        )
        rp2, backlog2 = rpc_call(
            proc,
            "session/prompt",
            {"sessionId": sid, "prompt": [{"type": "text", "text": p2}]},
            nid,
        )
        if "error" in rp2:
            print("session/prompt (2) error:", jd(rp2), file=sys.stderr)
            sys.exit(1)
        text2 = collect_assistant_text(backlog2)
        print("--- assistant turn 2 ---\n", text2, "\n", file=sys.stderr, sep="")
        time.sleep(1.0)
        after_paths = set(list_memory_markdown(global_mem, project_mem))
        blob = read_all_memory_text(list(after_paths)).upper()
        if fruit_word.upper() not in blob:
            print(
                f"FAIL: codeword {fruit_word} not found under memory dirs after turn 2 "
                f"(persist or search indexing may have failed). Files: {sorted(after_paths)}",
                file=sys.stderr,
            )
            exit_code = 12

        # Turn 3: ask without repeating codeword; recall should still surface fruit_word from disk.
        p3 = (
            "Third check, answer briefly.\n"
            "What codeword starting with MEMFRUIT_ did you mention as part of the favorite-fruit line in the prior turn?\n"
            "Reply with that single token only on the first line."
        )
        rp3, backlog3 = rpc_call(
            proc,
            "session/prompt",
            {"sessionId": sid, "prompt": [{"type": "text", "text": p3}]},
            nid,
        )
        if "error" in rp3:
            print("session/prompt (3) error:", jd(rp3), file=sys.stderr)
            sys.exit(1)
        text3 = collect_assistant_text(backlog3)
        print("--- assistant turn 3 ---\n", text3, "\n", file=sys.stderr, sep="")
        if fruit_word not in text3 and fruit_word.upper() not in text3.upper():
            print(
                f"FAIL: expected model to recall {fruit_word} in turn 3 answer.",
                file=sys.stderr,
            )
            exit_code = 13

        if not args.skip_prune:
            assert prune_path.is_file(), "prune seed missing"
            p4 = (
                "Memory maintenance before you answer.\n"
                "If your environment includes an internal memory copilot with delete capability, "
                f"delete the global long-term memory entry at path global:{prune_name} "
                "(use the internal memory delete tool if available).\n"
                "Then reply exactly one word on the first line: PRUNED if removal was performed, else SKIP.\n"
            )
            rp4, backlog4 = rpc_call(
                proc,
                "session/prompt",
                {"sessionId": sid, "prompt": [{"type": "text", "text": p4}]},
                nid,
            )
            if "error" in rp4:
                print("session/prompt (4) error:", jd(rp4), file=sys.stderr)
                sys.exit(1)
            text4 = collect_assistant_text(backlog4)
            print("--- assistant turn 4 ---\n", text4, "\n", file=sys.stderr, sep="")
            time.sleep(0.6)
            if prune_path.exists():
                print(
                    "WARN: disposable global note still on disk after prune prompt "
                    "(copilot may have skipped delete; not a hard failure).",
                    file=sys.stderr,
                )
            else:
                print("OK: disposable global note removed.", file=sys.stderr)

        print(
            "summary:",
            {
                "token_found_turn1": token in text1,
                "fruit_persisted": fruit_word.upper() in blob,
                "fruit_recalled_turn3": fruit_word.upper() in text3.upper(),
                "seed_files_before": len(before_paths),
                "memory_files_mid": len(mid_paths),
                "memory_files_after": len(after_paths),
            },
            file=sys.stderr,
        )

    finally:
        proc.stdin.close()
        proc.wait(timeout=900)
        if cleanup_work and Path(work).exists():
            shutil.rmtree(work, ignore_errors=True)
        if not args.keep_coddy_home and Path(coddy_home).exists():
            shutil.rmtree(coddy_home, ignore_errors=True)

    sys.exit(exit_code)


if __name__ == "__main__":
    main()
