#!/usr/bin/env python3
"""Measure the coddy console's startup time in a real pty.

For every scenario (skill set on disk, configured sources) and every binary
the script spawns `coddy cli --plain --theme dark --model <demo model>` the
way examples/cli/cli_e2e_startup.py does, feeds the pty through a pyte
terminal and records, from the spawn:

  first_bytes  the first read of the pty returned data (a read may hold
               more than one frame; this is when output was first observed)
  header       the version header ("coddy v") is on screen - the first frame
  ready        the "escape interrupt" hint is on screen
  echo         a probe typed after the hint is echoed by the editor - the
               console has taken and rendered input (an input round trip;
               the three points above only say what was on screen)

Scenarios:
  empty          the demo config with an empty skills directory (what CI runs)
  real           a copy of ~/.coddy/skills, no remote sources
  real+sources   the same copy plus the sources of ~/.coddy/config.yaml
  synth300       300 generated SKILL.md files
  synth300+dead  the same 300 plus one source that accepts TCP and never answers
  synth1000      1000 generated SKILL.md files

Nothing under ~/.coddy is touched: every run gets its own CODDY_HOME.
Requires pexpect and pyte (examples/cli/requirements.txt) and a cli-tagged
binary (CODDY_BIN, or the installed coddy). The numbers behind
docs/plans/console-mcp-startup.md came from this script.

Usage: bench_tui_startup.py --bin release=/usr/bin/coddy --bin main=build/coddy --out results.json
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import signal
import socket
import statistics
import sys
import tempfile
import threading
import time
from pathlib import Path

import pexpect
import pyte

REPO = Path(__file__).resolve().parents[2]
DEMO_CONFIG = REPO / "examples" / "config.demo.yaml"
MODEL = "rpa/qwen3.6-35b-a3b"  # the demo provider row that reports no account usage
# A tall screen: a long [Skills] section of the header would otherwise
# scroll the version line off the emulated screen before it is checked.
COLS, ROWS = 100, 200
HEADER_NEEDLE = "coddy v"
READY_NEEDLE = "escape interrupt"
ECHO_PROBE = "probe9q"  # a token no chrome of the console contains
CTRL_C = b"\x03"

# The console's own account of its MCP servers connecting after the first
# frame: the footer segment while any is still connecting, and the rows for
# the ones that failed or wait for approval (external/cli/mcp_status.go).
MCP_COUNT = re.compile(r"• MCP (\d+)/(\d+)")
MCP_FAILED = re.compile(r"MCP server (\S+) did not connect")
MCP_HELD = re.compile(r"MCP server (\S+) waits for approval")

# Escape sequences of the console's byte stream: CSI, OSC (hyperlinks), APC
# (the cursor marker), two-character ESC sequences.
ANSI = re.compile(r"\x1b\[[0-9;?<>=!]*[A-Za-z@`]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b_[^\x1b]*\x1b\\|\x1b[()][A-Za-z0-9]|\x1b[A-Za-z=>]")


def plain_text(raw: bytes) -> str:
    """The console's output with its escape sequences removed: what it wrote,
    whether or not the emulated screen still shows it."""
    return ANSI.sub("", raw.decode("utf-8", "replace")).replace("\r", "")


# --- fixtures -----------------------------------------------------------------


def render_config(home: Path, skills_dirs: list[Path], sources: list[str]) -> None:
    """Write the demo config into `home` with the skills block replaced."""
    text = DEMO_CONFIG.read_text().replace("__E2E_LOG_PATH__", str(home / "e2e.log"))
    start = text.index("skills:\n")
    end = text.index("mcp_servers:")
    block = "skills:\n  dirs:\n" + "".join(f'    - "{d}"\n' for d in skills_dirs)
    if sources:
        block += "  sources:\n" + "".join(f'    - "{s}"\n' for s in sources)
    block += "\n"
    (home / "config.yaml").write_text(text[:start] + block + text[end:])
    (home / "sessions").mkdir(exist_ok=True)


def make_synthetic_skills(root: Path, count: int) -> None:
    """Generate `count` skills with a realistic frontmatter and a ~2 KiB body."""
    body_paragraph = (
        "This paragraph stands in for the instructions a real skill carries. "
        "It is long enough that the loader has to read and parse a file of "
        "ordinary size rather than an empty stub.\n\n"
    )
    for i in range(1, count + 1):
        d = root / f"synthetic-skill-{i:04d}"
        d.mkdir(parents=True, exist_ok=True)
        (d / "SKILL.md").write_text(
            "---\n"
            f"name: synthetic-skill-{i:04d}\n"
            "metadata:\n  version: 1.0.0\n"
            "description: >\n"
            f"  Synthetic skill number {i} for the startup benchmark. Run when the\n"
            f"  user asks for synthetic task {i}.\n"
            "---\n\n"
            f"# Synthetic skill {i}\n\n" + body_paragraph * 8
        )


def copy_real_skills(dst: Path) -> int:
    src = Path.home() / ".coddy" / "skills"
    shutil.copytree(src, dst, dirs_exist_ok=True)
    return sum(1 for p in dst.iterdir() if p.is_dir() and (p / "SKILL.md").exists())


def real_sources() -> list[str]:
    """The skills.sources list of ~/.coddy/config.yaml, read without a YAML library."""
    lines = (Path.home() / ".coddy" / "config.yaml").read_text().splitlines()
    out: list[str] = []
    in_skills = in_sources = False
    for line in lines:
        if re.match(r"^skills:\s*$", line):
            in_skills = True
            continue
        if in_skills and re.match(r"^[A-Za-z_]+:", line):
            break
        if in_skills and re.match(r"^\s{2}sources:\s*$", line):
            in_sources = True
            continue
        if in_sources:
            m = re.match(r"^\s{4}-\s*(.+?)\s*$", line)
            if m:
                out.append(m.group(1).strip("\"'"))
            elif line.strip() and not line.lstrip().startswith("#"):
                in_sources = False
    return out


class DeadSource:
    """A TCP listener on 127.0.0.1 that accepts every connection and never answers."""

    def __init__(self) -> None:
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.sock.bind(("127.0.0.1", 0))
        self.sock.listen(64)
        self.port = self.sock.getsockname()[1]
        self.conns: list[socket.socket] = []
        self.accepted = 0
        self._stop = False
        self.thread = threading.Thread(target=self._loop, daemon=True)
        self.thread.start()

    def _loop(self) -> None:
        self.sock.settimeout(0.2)
        while not self._stop:
            try:
                conn, _ = self.sock.accept()
            except socket.timeout:
                continue
            except OSError:
                break
            self.accepted += 1
            self.conns.append(conn)  # hold it open, say nothing

    @property
    def url(self) -> str:
        return f"http://127.0.0.1:{self.port}/marketplace.json"

    def close(self) -> None:
        self._stop = True
        for c in self.conns:
            try:
                c.close()
            except OSError:
                pass
        self.sock.close()


# --- one console start --------------------------------------------------------


def start_once(
    binary: str, home: Path, work: Path, timeout: float,
    model: str | None = MODEL, env_extra: dict | None = None,
    mcp_timeout: float = 0.0,
) -> dict:
    """One console start, timed. With mcp_timeout > 0 the run goes on after
    the first frame until the footer's `MCP n/m` segment has gone, which is
    when every configured server has settled, and records how long that took
    and which servers failed (result["mcp"])."""
    env = dict(os.environ)
    env.update(
        {
            "TERM": "xterm-256color",
            "COLORTERM": "truecolor",
            "CODDY_HOME": str(home),
            "LANG": "en_US.UTF-8",
            "LC_ALL": "en_US.UTF-8",
        }
    )
    for k, v in (env_extra or {}).items():
        if v is None:
            env.pop(k, None)
        else:
            env[k] = v
    screen = pyte.Screen(COLS, ROWS)
    stream = pyte.ByteStream(screen)
    args = ["cli", "--plain", "--theme", "dark"]
    if model:
        args += ["--model", model]

    t0 = time.perf_counter()
    child = pexpect.spawn(
        binary, args, env=env, cwd=str(work), dimensions=(ROWS, COLS), encoding=None, timeout=5
    )
    result: dict = {"first_bytes": None, "header": None, "ready": None, "echo": None, "exited": False, "screen": ""}
    deadline = t0 + timeout
    # The needles are searched on the emulated screen and in everything the
    # console wrote so far: a frame that scrolled off the screen between two
    # reads still counts, and it is the writing that is timed.
    raw = bytearray()
    while time.perf_counter() < deadline:
        try:
            data = child.read_nonblocking(size=65536, timeout=0.05)
        except pexpect.TIMEOUT:
            data = b""
        except pexpect.EOF:
            result["exited"] = True
            break
        if not data:
            if not child.isalive():
                result["exited"] = True
                break
            continue
        now = time.perf_counter() - t0
        if result["first_bytes"] is None:
            result["first_bytes"] = now
        stream.feed(data)
        raw += data
        text = "\n".join(screen.display) + "\n" + plain_text(bytes(raw))
        if result["header"] is None and HEADER_NEEDLE in text:
            result["header"] = now
        if READY_NEEDLE in text:
            result["ready"] = now
            break
    if result["ready"] is None:
        result["screen"] = "\n".join(line.rstrip() for line in screen.display if line.strip())[:1500]
    else:
        # The input round trip: a probe the editor has to echo. Typed as one
        # write, timed until the whole token is on screen.
        child.send(ECHO_PROBE.encode())
        sent = time.perf_counter()
        while time.perf_counter() < deadline:
            try:
                data = child.read_nonblocking(size=65536, timeout=0.05)
            except pexpect.TIMEOUT:
                continue
            except pexpect.EOF:
                break
            if data:
                stream.feed(data)
                raw += data
                if ECHO_PROBE in "\n".join(screen.display) or ECHO_PROBE in plain_text(bytes(raw)):
                    result["echo"] = time.perf_counter() - t0
                    result["echo_after_ready"] = time.perf_counter() - sent
                    break
    if result["ready"] is not None and mcp_timeout > 0:
        mcp: dict = {"first_seen": None, "settled": None, "connected": None, "total": None, "failed": [], "held": []}
        seen = False
        absent_since = None
        mcp_deadline = t0 + mcp_timeout
        while time.perf_counter() < mcp_deadline:
            try:
                data = child.read_nonblocking(size=65536, timeout=0.05)
            except pexpect.TIMEOUT:
                data = b""
            except pexpect.EOF:
                break
            if data:
                stream.feed(data)
                raw += data
            now = time.perf_counter() - t0
            m = MCP_COUNT.search("\n".join(screen.display))
            if m:
                if not seen:
                    seen, mcp["first_seen"] = True, now
                mcp["connected"], mcp["total"] = int(m.group(1)), int(m.group(2))
                absent_since = None
            elif seen:
                # The segment leaves the footer once every server settled. A
                # redraw can leave the footer off the emulated screen for a
                # read or two, so the absence has to last a second.
                if absent_since is None:
                    absent_since = now
                elif now - absent_since >= 1.0:
                    mcp["settled"] = absent_since
                    break
            elif now - result["ready"] > 3:
                # No segment within seconds of the first frame: this binary
                # does not defer its servers, or has none to connect.
                break
        plain = plain_text(bytes(raw))
        mcp["failed"] = sorted(set(MCP_FAILED.findall(plain)))
        mcp["held"] = sorted(set(MCP_HELD.findall(plain)))
        result["mcp"] = mcp

    # Leave the way the CI script does: ctrl+c asks for a second press, the second exits.
    try:
        if child.isalive():
            child.send(CTRL_C)
            time.sleep(0.3)
            child.send(CTRL_C)
            time.sleep(0.3)
            child.send(CTRL_C)
            child.expect(pexpect.EOF, timeout=10)
    except Exception:
        pass
    finally:
        try:
            os.killpg(child.pid, signal.SIGKILL)
        except Exception:
            pass
        try:
            child.terminate(force=True)
        except Exception:
            pass
    return result


def version_of(binary: str) -> str:
    """The binary's version line, run as a process, never through a shell."""
    import subprocess

    out = subprocess.run([binary, "-v"], capture_output=True, text=True, check=False, timeout=30)
    return (out.stdout + out.stderr).strip()


def bare_version_time(binary: str, runs: int) -> float:
    """Median wall time of `coddy -v`: the process cost with no console at all."""
    import subprocess

    times = []
    for _ in range(runs):
        t0 = time.perf_counter()
        subprocess.run([binary, "-v"], capture_output=True, timeout=30)
        times.append(time.perf_counter() - t0)
    return statistics.median(times)


# --- driver -------------------------------------------------------------------


def fmt(v: float | None) -> str:
    return "timeout" if v is None else f"{v * 1000:.0f} ms"


def summarize(values: list[float | None]) -> str:
    ok = [v for v in values if v is not None]
    misses = len(values) - len(ok)
    if not ok:
        return f"timeout x{misses}"
    s = f"{statistics.median(ok) * 1000:.0f} ms (min {min(ok) * 1000:.0f}, max {max(ok) * 1000:.0f})"
    if misses:
        s += f", timeout x{misses}"
    return s


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--bin", action="append", required=True, help="label=path, repeatable")
    ap.add_argument("--runs", type=int, default=5)
    ap.add_argument("--timeout", type=float, default=30.0, help="seconds to wait for the ready hint")
    ap.add_argument("--out", type=Path, required=True, help="JSON results file")
    ap.add_argument("--scenarios", default="empty,real,real+sources,synth300,synth300+dead,synth1000")
    args = ap.parse_args()

    binaries = []
    for spec in args.bin:
        label, _, path = spec.partition("=")
        binaries.append((label, path))

    base = Path(tempfile.mkdtemp(prefix="coddy-tui-bench-"))
    fixtures: dict[str, tuple[list[Path], list[str]]] = {}
    dead: DeadSource | None = None
    wanted = args.scenarios.split(",")

    empty_dir = base / "skills-empty"
    empty_dir.mkdir()
    fixtures["empty"] = ([empty_dir], [])
    if "real" in wanted or "real+sources" in wanted:
        real_dir = base / "skills-real"
        n_real = copy_real_skills(real_dir)
        fixtures["real"] = ([real_dir], [])
        fixtures["real+sources"] = ([real_dir], real_sources())
        print(f"real skills copied: {n_real}, sources: {len(fixtures['real+sources'][1])}", file=sys.stderr)
    if "synth300" in wanted or "synth300+dead" in wanted:
        s300 = base / "skills-synth300"
        make_synthetic_skills(s300, 300)
        fixtures["synth300"] = ([s300], [])
        dead = DeadSource()
        fixtures["synth300+dead"] = ([s300], [dead.url])
    if "synth1000" in wanted:
        s1000 = base / "skills-synth1000"
        make_synthetic_skills(s1000, 1000)
        fixtures["synth1000"] = ([s1000], [])

    results: dict = {"runs": args.runs, "timeout_s": args.timeout, "binaries": {}, "scenarios": {}}
    try:
        for label, path in binaries:
            ver = version_of(path)
            results["binaries"][label] = {"path": path, "version": ver, "bare_version_ms": bare_version_time(path, args.runs) * 1000}
            print(f"[{label}] {ver}: `coddy -v` median {results['binaries'][label]['bare_version_ms']:.0f} ms", file=sys.stderr)

        for name in wanted:
            dirs, sources = fixtures[name]
            results["scenarios"][name] = {}
            for label, path in binaries:
                runs: list[dict] = []
                for i in range(args.runs):
                    home = Path(tempfile.mkdtemp(prefix=f"home-{name}-", dir=base))
                    work = Path(tempfile.mkdtemp(prefix=f"work-{name}-", dir=base))
                    render_config(home, dirs, sources)
                    if dead is not None and name.endswith("+dead"):
                        before = dead.accepted
                    r = start_once(path, home, work, args.timeout)
                    if dead is not None and name.endswith("+dead"):
                        r["dead_source_connections"] = dead.accepted - before
                    runs.append(r)
                    print(
                        f"  {name:14s} {label:8s} run {i + 1}: bytes {fmt(r['first_bytes'])}, header {fmt(r['header'])}, ready {fmt(r['ready'])}, echo {fmt(r.get('echo'))}"
                        + (" (exited)" if r["exited"] else ""),
                        file=sys.stderr,
                    )
                    shutil.rmtree(home, ignore_errors=True)
                    shutil.rmtree(work, ignore_errors=True)
                    if r["ready"] is None and i >= 1:
                        break  # two timeouts are enough evidence for one scenario
                results["scenarios"][name][label] = {
                    "first_bytes": summarize([r["first_bytes"] for r in runs]),
                    "header": summarize([r["header"] for r in runs]),
                    "ready": summarize([r["ready"] for r in runs]),
                    "echo": summarize([r.get("echo") for r in runs]),
                    "runs": runs,
                }
    finally:
        if dead is not None:
            dead.close()
        shutil.rmtree(base, ignore_errors=True)

    args.out.write_text(json.dumps(results, indent=2))

    print("\n| scenario | binary | first output | header (first frame) | ready (hint) | echo (input round trip) |")
    print("|---|---|---|---|---|---|")
    for name, per_bin in results["scenarios"].items():
        for label, s in per_bin.items():
            print(f"| {name} | {label} | {s['first_bytes']} | {s['header']} | {s['ready']} | {s['echo']} |")
    for name, per_bin in results["scenarios"].items():
        for label, s in per_bin.items():
            for r in s["runs"]:
                if r["ready"] is None and r["screen"]:
                    print(f"\n--- screen at timeout: {name} / {label} ---\n{r['screen']}")
                    break
    return 0


if __name__ == "__main__":
    sys.exit(main())
