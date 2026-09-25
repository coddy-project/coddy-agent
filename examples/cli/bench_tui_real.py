#!/usr/bin/env python3
"""Console startup time with a copy of the operator's real ~/.coddy.

Every run gets a private CODDY_HOME built from ~/.coddy: config.yaml with every
absolute /home/<user>/.coddy path rewritten to the copy (sessions.dir,
logger.file, scheduler.dir, skills.dirs), plus .env, providers/, skills/,
agents/, memory/ and the trust files. Sessions, logs and backups are not
copied. The real home is never written to, which the script checks.

Variants:
  real/empty-cwd     the real config, an empty working directory
  real/repo-cwd      the real config, cwd = the coddy-agent checkout (a git workspace)
  real/no-mcp        the real config with mcp_servers: [] (isolates MCP connects)
  real/proxy-refused every provider unreachable at once (proxy port with no listener)
  real/proxy-dead    every provider unreachable and silent (proxy accepts, never answers)

Requires bench_tui_startup.py next to it, and pexpect + pyte. The config
copy holds the operator's keys for the length of a run and is deleted with
the run. The numbers behind docs/plans/console-mcp-startup.md came from
this script.

Usage: bench_tui_real.py --bin main=build/coddy --out results.json
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from bench_tui_startup import DeadSource, REPO, fmt, start_once, summarize  # noqa: E402

REAL_HOME = Path.home() / ".coddy"
COPY_DIRS = ["providers", "skills", "agents", "memory", "project-agents"]
COPY_FILES = [".env", "subagents-trust.json", "swarm-leases.json", "hooks.json", "AGENTS.md", "DESIGN.md"]
SKIP_TOP = {"sessions", "logs", "coddy.log", "config.yaml.bak", "config.yaml.prev"}


def real_state_snapshot() -> dict:
    """What a run must not change under the real home."""
    entries = sorted(p.name for p in REAL_HOME.iterdir())
    sessions = sorted(p.name for p in (REAL_HOME / "sessions").iterdir()) if (REAL_HOME / "sessions").exists() else []
    log = REAL_HOME / "coddy.log"
    return {"entries": entries, "sessions": sessions, "log_size": log.stat().st_size if log.exists() else -1}


def build_home(base: Path, no_mcp: bool) -> Path:
    home = Path(tempfile.mkdtemp(prefix="home-real-", dir=base))  # mode 0700
    text = (REAL_HOME / "config.yaml").read_text()
    text = text.replace(str(REAL_HOME), str(home)).replace("~/.coddy", str(home))
    if str(REAL_HOME) in text:
        raise SystemExit("a real-home path survived the rewrite")
    if no_mcp:
        text = re.sub(r"(?ms)^mcp_servers:.*?(?=^[a-z_]+:)", "mcp_servers: []\n\n", text)
    (home / "config.yaml").write_text(text)
    for d in COPY_DIRS:
        src = REAL_HOME / d
        if src.exists():
            shutil.copytree(src, home / d, dirs_exist_ok=True)
    for f in COPY_FILES:
        src = REAL_HOME / f
        if src.exists():
            shutil.copy2(src, home / f)
    (home / "sessions").mkdir(exist_ok=True)
    (home / "scheduler").mkdir(exist_ok=True)
    return home


def proxy_env(url: str | None) -> dict:
    env: dict = {"NO_PROXY": None, "no_proxy": None}
    for k in ("HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"):
        env[k] = url
    return env


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--bin", action="append", required=True, help="label=path, repeatable")
    ap.add_argument("--runs", type=int, default=5)
    ap.add_argument("--timeout", type=float, default=60.0)
    ap.add_argument("--out", type=Path, required=True)
    ap.add_argument("--variants", default="real/empty-cwd,real/repo-cwd,real/no-mcp,real/proxy-refused,real/proxy-dead")
    args = ap.parse_args()

    binaries = [tuple(spec.split("=", 1)) for spec in args.bin]
    before = real_state_snapshot()
    base = Path(tempfile.mkdtemp(prefix="coddy-tui-bench-real-"))
    dead = DeadSource()
    variants = {
        "real/empty-cwd": (False, None, {}),
        "real/repo-cwd": (False, REPO, {}),
        "real/no-mcp": (True, None, {}),
        "real/proxy-refused": (False, None, proxy_env("http://127.0.0.1:9")),
        "real/proxy-dead": (False, None, proxy_env(f"http://127.0.0.1:{dead.port}")),
        "real/no-mcp+proxy-refused": (True, None, proxy_env("http://127.0.0.1:9")),
        "real/no-mcp+proxy-dead": (True, None, proxy_env(f"http://127.0.0.1:{dead.port}")),
    }
    results: dict = {"runs": args.runs, "timeout_s": args.timeout, "binaries": {}, "scenarios": {}}
    try:
        for label, path in binaries:
            results["binaries"][label] = {"path": path, "version": os.popen(f"{path} -v 2>&1").read().strip()}
        for name in args.variants.split(","):
            no_mcp, cwd, env_extra = variants[name]
            results["scenarios"][name] = {}
            for label, path in binaries:
                runs = []
                for i in range(args.runs):
                    home = build_home(base, no_mcp)
                    work = Path(cwd) if cwd else Path(tempfile.mkdtemp(prefix="work-", dir=base))
                    r = start_once(path, home, work, args.timeout, model=None, env_extra=env_extra)
                    if name.endswith("proxy-dead"):
                        r["dead_proxy_connections"] = dead.accepted
                    r["sessions_written"] = len([p for p in (home / "sessions").iterdir()]) + len(
                        [p for p in home.iterdir() if p.name.startswith("sess_")]
                    )
                    log = home / "coddy.log"
                    if i == 0 and log.exists():
                        wanted = ("mcp", "MCP", "skill", "usage", "session/new", "ready", "connect", "git", "hook", "rules")
                        r["log_excerpt"] = [
                            line[:220] for line in log.read_text(errors="replace").splitlines()
                            if any(w in line for w in wanted)
                        ][:60]
                    runs.append(r)
                    print(
                        f"  {name:20s} {label:8s} run {i + 1}: bytes {fmt(r['first_bytes'])}, header {fmt(r['header'])}, ready {fmt(r['ready'])}"
                        + (" (exited)" if r["exited"] else "")
                        + f", sessions in temp home: {r['sessions_written']}",
                        file=sys.stderr,
                    )
                    shutil.rmtree(home, ignore_errors=True)
                    if not cwd:
                        shutil.rmtree(work, ignore_errors=True)
                    if r["ready"] is None and i >= 1:
                        break
                results["scenarios"][name][label] = {
                    "first_bytes": summarize([r["first_bytes"] for r in runs]),
                    "header": summarize([r["header"] for r in runs]),
                    "ready": summarize([r["ready"] for r in runs]),
                    "runs": runs,
                }
    finally:
        dead.close()
        shutil.rmtree(base, ignore_errors=True)

    after = real_state_snapshot()
    results["real_home_untouched"] = before == after
    args.out.write_text(json.dumps(results, indent=2))

    print(f"\nreal ~/.coddy untouched: {before == after}")
    if before != after:
        print(f"  before: {before}\n  after:  {after}")
    print("\n| variant | binary | first bytes | header (first frame) | ready (interactive) |")
    print("|---|---|---|---|---|")
    for name, per_bin in results["scenarios"].items():
        for label, s in per_bin.items():
            print(f"| {name} | {label} | {s['first_bytes']} | {s['header']} | {s['ready']} |")
    for name, per_bin in results["scenarios"].items():
        for label, s in per_bin.items():
            ex = s["runs"][0].get("log_excerpt")
            if ex and name in ("real/empty-cwd", "real/proxy-dead"):
                print(f"\n--- coddy.log excerpt, run 1: {name} / {label} ---")
                print("\n".join(ex))
    for name, per_bin in results["scenarios"].items():
        for label, s in per_bin.items():
            for r in s["runs"]:
                if r["ready"] is None and r["screen"]:
                    print(f"\n--- screen at timeout: {name} / {label} ---\n{r['screen']}")
                    break
    return 0


if __name__ == "__main__":
    sys.exit(main())
