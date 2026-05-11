#!/usr/bin/env python3
"""ACP e2e: session configOptions list every configured model and set_config_option can switch.

Expects ``CODDY_CONFIG`` (default ``examples/config.demo.yaml``) to define at least one ``models[].model`` entry.
When several exist, sets ``model`` to a different selector than ``agent.model`` where possible.
"""

from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any


def jd(obj: dict[str, Any]) -> str:
    return json.dumps(obj, separators=(",", ":"), ensure_ascii=False)


def default_coddy_bin() -> str:
    exe = shutil.which("coddy")
    return exe if exe else "coddy"


def rpc_call(proc: subprocess.Popen[str], method: str, params: dict[str, Any], next_id: list[int]) -> dict[str, Any]:
    rid = next_id[0]
    next_id[0] += 1
    assert proc.stdin is not None
    assert proc.stdout is not None

    proc.stdin.write(jd({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}) + "\n")
    proc.stdin.flush()

    while True:
        line = proc.stdout.readline()
        if not line:
            raise RuntimeError("unexpected EOF from coddy stdout")
        line = line.strip()
        if not line:
            continue
        msg = json.loads(line)

        if msg.get("method") == "session/request_permission":
            proc.stdin.write(
                jd({"jsonrpc": "2.0", "id": msg.get("id"), "result": {"outcome": "allow"}}) + "\n"
            )
            proc.stdin.flush()
            continue

        if msg.get("method") == "session/update":
            continue

        if msg.get("id") == rid:
            return msg


def default_config_path() -> Path:
    return Path(__file__).resolve().parent.parent / "config.demo.yaml"


def parse_models_from_yaml(raw: str) -> list[str]:
    return [m.strip() for m in re.findall(r"(?m)^\s*-\s+model:\s*\"([^\"]+)\"\s*$", raw)]


def parse_agent_default_model(raw: str) -> str:
    blk = re.search(r"(?ms)^agent:\s*\n(.*?)(?=^[a-z_]+:\s*$)", raw)
    if not blk:
        return ""
    m = re.search(r"(?m)^\s+model:\s*\"([^\"]+)\"\s*$", blk.group(1))
    return m.group(1).strip() if m else ""


def norm_opt_values(opt: dict[str, Any]) -> list[str]:
    out: list[str] = []
    for row in opt.get("options") or []:
        v = row.get("value")
        if isinstance(v, str) and v.strip():
            out.append(v.strip())
    return out


def main() -> int:
    binary = os.environ.get("CODDY_BIN", default_coddy_bin())
    cfg_path = Path(os.environ.get("CODDY_CONFIG", str(default_config_path()))).expanduser()
    if not cfg_path.is_file():
        print("CODDY_CONFIG not found:", cfg_path, file=sys.stderr)
        return 1
    raw = cfg_path.read_text(encoding="utf-8")
    yaml_models = parse_models_from_yaml(raw)
    agent_default = parse_agent_default_model(raw)
    if len(yaml_models) < 1:
        print("need at least one models[] entry", yaml_models, file=sys.stderr)
        return 1
    if agent_default not in yaml_models:
        print("agent.model must appear in models list", agent_default, file=sys.stderr)
        return 1

    alts = [m for m in yaml_models if m != agent_default]
    alt = alts[0] if alts else agent_default

    session_root = os.environ.get("SESSION_ROOT", "/tmp/coddy-examples-acp-models")
    session_id = os.environ.get("SESSION_ID", "example-acp-models")

    work = tempfile.mkdtemp(prefix="coddy-acp-models-")
    os.makedirs(session_root, exist_ok=True)

    proc = subprocess.Popen(
        [
            "stdbuf",
            "-oL",
            "-eL",
            binary,
            "acp",
            "--config",
            str(cfg_path),
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

    nid = [1]
    try:
        r0 = rpc_call(
            proc,
            "initialize",
            {
                "protocolVersion": 1,
                "clientCapabilities": {"terminal": True},
                "clientInfo": {"name": "acp-models-e2e", "title": "models", "version": "1.0.0"},
            },
            nid,
        )
        if "error" in r0:
            print("initialize error", jd(r0), file=sys.stderr)
            return 1

        r1 = rpc_call(proc, "session/new", {"cwd": work}, nid)
        if "error" in r1:
            print("session/new error", jd(r1), file=sys.stderr)
            return 1
        res = r1.get("result") or {}
        sid = res.get("sessionId")
        if not sid:
            print("missing sessionId", jd(r1), file=sys.stderr)
            return 1

        opts_list = res.get("configOptions") or []
        model_opt = None
        for row in opts_list:
            if row.get("id") == "model":
                model_opt = row
                break
        if model_opt is None:
            print("no model configOption", jd(r1), file=sys.stderr)
            return 1

        advertised = norm_opt_values(model_opt)
        sort_a = sorted(advertised)
        sort_y = sorted(yaml_models)
        if sort_a != sort_y:
            print("model options mismatch", sort_a, "!=", sort_y, file=sys.stderr)
            return 1
        cv = str(model_opt.get("currentValue", "")).strip()
        if cv != agent_default:
            print("currentValue want", agent_default, "got", cv, file=sys.stderr)
            return 1

        r_set = rpc_call(
            proc,
            "session/set_config_option",
            {"sessionId": sid, "configId": "model", "value": alt},
            nid,
        )
        if "error" in r_set:
            print("session/set_config_option error", jd(r_set), file=sys.stderr)
            return 1
        rs = r_set.get("result") or {}
        refreshed = rs.get("configOptions") or []
        model_now = None
        for row in refreshed:
            if row.get("id") == "model":
                model_now = row
                break
        if model_now is None:
            print("missing model after set", jd(r_set), file=sys.stderr)
            return 1
        if str(model_now.get("currentValue", "")).strip() != alt:
            print("currentValue after set want", alt, "got", model_now.get("currentValue"), file=sys.stderr)
            return 1

        r_rest = rpc_call(
            proc,
            "session/set_config_option",
            {"sessionId": sid, "configId": "model", "value": agent_default},
            nid,
        )
        if "error" in r_rest:
            print("session/set_config_option restore error", jd(r_rest), file=sys.stderr)
            return 1
        rs2 = (r_rest.get("result") or {}).get("configOptions") or []
        model_restore = None
        for row in rs2:
            if row.get("id") == "model":
                model_restore = row
                break
        if model_restore is None or str(model_restore.get("currentValue", "")).strip() != agent_default:
            print("restore failed", jd(r_rest), file=sys.stderr)
            return 1

        print("ok acp models e2e")
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
