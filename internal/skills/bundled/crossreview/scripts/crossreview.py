#!/usr/bin/env python3
"""crossreview helper: find the reviewer CLIs, keep the roster, write the
brief, run every reviewer in parallel and report on them.

Standard library only, Python 3.8 or newer, Linux, macOS and Windows. The
output is plain text meant for the agent that drives the skill.

  detect                 installed reviewer CLIs: agent, binary, models command, template
  models AGENT           model ids an agent offers, one per line
  roster                 where the roster is, whether it may be used, what it holds
  init SPEC...           write the roster from agent:model pairs (or --import a file)
  trust                  record the user's approval of a workspace roster
  brief                  write a review brief from a git scope, files or a document
  run                    start the reviewers in the background, print the run directory
  wait RUN               wait for a run (bounded by --max), print its status
  status RUN             print a run's status without waiting
  collect RUN            print every review, and the error of every reviewer that failed
  stop RUN [NAME...]     stop a whole run or some of its reviewers
  retry RUN NAME...      run reviewers of a finished run again
  probe                  smoke-test reviewers with a one-word brief

Exit codes: 0 done, 1 error, 2 no roster, 3 still running, 4 roster needs approval.
"""

import argparse
import datetime as _dt
import hashlib
import json
import os
import re
import secrets
import shlex
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from typing import Dict, List, Optional, Tuple

HERE = Path(__file__).resolve().parent
TABLE_PATH = HERE / "agents.tsv"
IS_WINDOWS = os.name == "nt"

EXIT_OK = 0
EXIT_ERROR = 1
EXIT_NO_ROSTER = 2
EXIT_RUNNING = 3
EXIT_NEEDS_APPROVAL = 4

DEFAULT_TIMEOUT = 2700
DEFAULT_MIN_REVIEWERS = 2
DEPTH_ENV = "CROSSREVIEW_DEPTH"
PONG_BRIEF = "Reply with exactly one word, PONG, and nothing else. Do not call any tools.\n"

# Statuses a reviewer can end in, and the ones that count as an answer.
FINAL = ("done", "empty", "failed", "timeout", "stopped", "missing", "lost")
ANSWERED = ("done",)


class CrossreviewError(Exception):
    """A problem the user or the agent has to fix; printed without a traceback."""


# ---------------------------------------------------------------- the table


class AgentSpec:
    def __init__(self, agent, binaries, marker, models, posix, powershell):
        self.agent = agent
        self.binaries = binaries
        self.marker = marker
        self.models = models
        self.posix = posix
        self.powershell = powershell

    def template(self, windows=IS_WINDOWS):
        return self.powershell if windows else self.posix


def load_table(path=TABLE_PATH) -> Dict[str, AgentSpec]:
    table = {}
    with open(path, encoding="utf-8") as fh:
        for raw in fh:
            line = raw.rstrip("\r\n")
            if not line.strip() or line.startswith("#"):
                continue
            cols = line.split("\t")
            if len(cols) != 6:
                raise CrossreviewError("%s: a row needs 6 tab-separated columns: %r" % (path, line))
            agent, bins, marker, models, posix, pwsh = cols
            table[agent] = AgentSpec(
                agent,
                [b.strip() for b in bins.split(",") if b.strip()],
                marker.lower(),
                None if models.strip() == "-" else models.strip(),
                posix,
                pwsh,
            )
    return table


def fill(template: str, **values) -> str:
    out = template
    for key, value in values.items():
        out = out.replace("{%s}" % key, value)
    return out


# ------------------------------------------------------------ processes


def _popen_group_kwargs():
    if IS_WINDOWS:
        return {"creationflags": subprocess.CREATE_NEW_PROCESS_GROUP | 0x08000000}  # CREATE_NO_WINDOW
    return {"start_new_session": True}


def kill_tree(pid: int, grace: float = 5.0, proc: Optional[subprocess.Popen] = None) -> None:
    """Stop a process and everything it started (its process group, its tree on Windows).

    The group is watched, not only its leader: an agent CLI whose child ignores
    SIGTERM keeps running after the leader exits, and gets SIGKILL with the rest."""
    if IS_WINDOWS:
        subprocess.run(["taskkill", "/T", "/F", "/PID", str(pid)],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        return
    for sig in (signal.SIGTERM, signal.SIGKILL):
        try:
            os.killpg(pid, sig)
        except (ProcessLookupError, PermissionError):
            # PermissionError: macOS answers EPERM for a group of zombies.
            return
        deadline = time.time() + grace
        while time.time() < deadline:
            if proc is not None:
                proc.poll()
            if not _group_alive(pid):
                return
            time.sleep(0.1)


def _group_alive(pgid: int) -> bool:
    try:
        os.killpg(pgid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def pid_alive(pid: int) -> bool:
    if pid <= 0:
        return False
    if IS_WINDOWS:
        import ctypes

        kernel32 = ctypes.windll.kernel32  # type: ignore[attr-defined]
        handle = kernel32.OpenProcess(0x00100000, False, pid)  # SYNCHRONIZE
        if not handle:
            return False
        try:
            return kernel32.WaitForSingleObject(handle, 0) == 0x00000102  # WAIT_TIMEOUT
        finally:
            kernel32.CloseHandle(handle)
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def run_capture(argv: List[str], timeout: float, cwd: Optional[str] = None) -> Tuple[Optional[int], str]:
    """Run a probe with a hard time bound; (None, text) when it timed out."""
    try:
        proc = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                stderr=subprocess.STDOUT, cwd=cwd, **_popen_group_kwargs())
    except OSError as exc:
        return 127, str(exc)
    try:
        data, _ = proc.communicate(timeout=timeout)
        return proc.returncode, data.decode("utf-8", "replace")
    except subprocess.TimeoutExpired:
        kill_tree(proc.pid, grace=1.0, proc=proc)
        try:
            data, _ = proc.communicate(timeout=2)
        except subprocess.TimeoutExpired:
            data = b""
        return None, data.decode("utf-8", "replace")


# ------------------------------------------------------------- detection


class Detected:
    def __init__(self, agent, path, binary, models_cmd, template, spec):
        self.agent = agent
        self.path = path
        self.binary = binary
        self.models_cmd = models_cmd
        self.template = template
        self.spec = spec

    def as_dict(self):
        return {"agent": self.agent, "path": self.path, "binary": self.binary,
                "models_cmd": self.models_cmd, "template": self.template}


def _verify(spec: AgentSpec, argv: List[str]) -> bool:
    for flag in ("--version", "--help"):
        rc, text = run_capture(argv + [flag], timeout=10)
        if rc == 0 and spec.marker in text.lower():
            return True
    return False


def detect_one(spec: AgentSpec, windows: bool = IS_WINDOWS) -> Optional[Detected]:
    for cand in spec.binaries:
        parts = cand.split()
        path = shutil.which(parts[0])
        if not path:
            continue
        argv = [path] + parts[1:]
        if not _verify(spec, argv):
            continue
        models_cmd = ""
        if spec.models:
            rc, _ = run_capture(argv + spec.models.split(), timeout=20)
            if rc == 0:
                models_cmd = "%s %s" % (cand, spec.models)
        return Detected(spec.agent, path, cand, models_cmd,
                        fill(spec.template(windows), bin=cand), spec)
    return None


def detect(table: Dict[str, AgentSpec], only: Optional[List[str]] = None) -> List[Detected]:
    specs = [s for s in table.values() if not only or s.agent in only]
    with ThreadPoolExecutor(max_workers=max(1, len(specs))) as pool:
        found = list(pool.map(detect_one, specs))
    return [d for d in found if d]


def cmd_detect(args) -> int:
    table = load_table()
    found = detect(table, args.agent or None)
    if args.json:
        print(json.dumps([d.as_dict() for d in found], indent=2))
        return EXIT_OK
    for d in found:
        print("\t".join([d.agent, d.path, d.models_cmd, d.template]))
    if not found:
        print("no reviewer CLI found on PATH (looked for: %s)" % ", ".join(table), file=sys.stderr)
    return EXIT_OK


# ---------------------------------------------------------------- models

CLAUDE_ALIASES = ["fable", "opus", "sonnet", "haiku"]
_ZERO_WIDTH = re.compile("[​‌‍⁠﻿]")


def parse_codex_models(text: str) -> List[str]:
    data = json.loads(text)
    models = data.get("models", data) if isinstance(data, dict) else data
    rows = []
    for m in models:
        if not isinstance(m, dict) or not m.get("slug"):
            continue
        if m.get("visibility") == "hide":
            continue
        rows.append((m.get("priority", 1000), m["slug"]))
    return [slug for _, slug in sorted(rows)]


def parse_cursor_models(text: str) -> List[str]:
    out = []
    for line in text.splitlines():
        line = _ZERO_WIDTH.sub("", line).strip()
        match = re.match(r"^([A-Za-z0-9][\w.\-\[\]=,:/]*)\s+-\s+\S", line)
        if match:
            out.append(match.group(1))
    return out


def parse_devin_models(text: str) -> List[str]:
    out = []
    for line in text.splitlines():
        if not line.startswith("  ") or line.strip().startswith("aliases:"):
            continue
        token = line.split()[0] if line.split() else ""
        if re.match(r"^[a-z0-9][\w.\-]*$", token):
            out.append(token)
    return out


def parse_opencode_models(text: str) -> List[str]:
    return [ln.strip() for ln in text.splitlines() if re.match(r"^\S+/\S+$", ln.strip())]


def coddy_config_path() -> Path:
    if os.environ.get("CODDY_CONFIG"):
        return Path(os.environ["CODDY_CONFIG"]).expanduser()
    home = Path(os.environ.get("CODDY_HOME") or Path.home() / ".coddy").expanduser()
    return home / "config.yaml"


def parse_coddy_models(text: str) -> List[str]:
    """models[].model from a Coddy config.yaml, without a YAML library."""
    out, inside = [], False
    for line in text.splitlines():
        if re.match(r"^models:\s*(#.*)?$", line):
            inside = True
            continue
        if inside and re.match(r"^\S", line):
            break
        if inside:
            match = re.match(r"^\s*(?:-\s+)?model:\s*['\"]?([^'\"#\s]+)", line)
            if match:
                out.append(match.group(1))
    return out


def list_models(agent: str, binary: Optional[str] = None) -> Tuple[List[str], str]:
    """(model ids, note). An empty list means the user has to type the id;
    a note starting with "failed" means the listing itself broke."""
    table = load_table()
    if agent not in table:
        raise CrossreviewError("unknown agent %r (known: %s)" % (agent, ", ".join(table)))
    spec = table[agent]
    if agent == "claude":
        return CLAUDE_ALIASES, "Claude Code lists no models; these aliases follow the newest model, a full id also works"
    if agent == "coddy":
        path = coddy_config_path()
        if not path.is_file():
            return [], "no Coddy config at %s; type the provider/model id" % path
        return parse_coddy_models(path.read_text(encoding="utf-8", errors="replace")), "models[].model of %s" % path
    if not spec.models:
        return [], "%s has no model listing; type the id the CLI accepts" % agent
    cand = binary or next((b for b in spec.binaries if shutil.which(b.split()[0])), None)
    if not cand:
        raise CrossreviewError("%s is not installed" % agent)
    parts = cand.split()
    argv = [shutil.which(parts[0]) or parts[0]] + parts[1:] + spec.models.split()
    rc, text = run_capture(argv, timeout=60)
    if rc != 0:
        return [], "failed: `%s %s` (%s): %s" % (cand, spec.models, "timeout" if rc is None else "rc=%s" % rc,
                                                text.strip()[-300:])
    parser = {"codex": parse_codex_models, "cursor": parse_cursor_models,
              "devin": parse_devin_models, "opencode": parse_opencode_models}.get(agent)
    if parser is None:
        return [ln.strip() for ln in text.splitlines() if ln.strip()], "raw output of %s %s" % (cand, spec.models)
    try:
        return parser(text), "from `%s %s`" % (cand, spec.models)
    except (ValueError, TypeError, AttributeError) as exc:
        return [], "failed: could not parse `%s %s`: %s" % (cand, spec.models, exc)


def cmd_models(args) -> int:
    ids, note = list_models(args.agent, args.binary)
    for model_id in ids:
        print(model_id)
    print("# %s" % note, file=sys.stderr)
    return EXIT_ERROR if note.startswith("failed") else EXIT_OK


# ---------------------------------------------------------------- roster

# Where each agent keeps its own settings, the way it keeps its skills and
# rules: the global roster is crossreview.json in the agent's home, the local
# one crossreview.json in the agent's folder of the project. An agent that is
# not listed uses the shared .agents folders.
HOSTS = {
    # host: (variable that moves its home, default home, folder in a project)
    "claude": ("CLAUDE_CONFIG_DIR", "~/.claude", ".claude"),
    "codex": ("CODEX_HOME", "~/.codex", ".codex"),
    "coddy": ("CODDY_HOME", "~/.coddy", ".coddy"),
    "cursor": (None, "~/.cursor", ".cursor"),
    "opencode": (None, "{config}/opencode", ".opencode"),
    "devin": (None, "{config}/devin", ".devin"),
    "gemini": (None, "~/.gemini", ".gemini"),
    "qwen": (None, "~/.qwen", ".qwen"),
    "kimi": (None, "~/.kimi", ".kimi"),
}
OTHER_HOST = (None, "~/.agents", ".agents")
HOST_ALIASES = {"claude-code": "claude", "claudecode": "claude", "cursor-agent": "cursor", "agent": "cursor",
                "codex-cli": "codex", "qwen-code": "qwen", "gemini-cli": "gemini", "kimi-cli": "kimi"}
ROSTER_FILE = "crossreview.json"
TRUST_FILE = "crossreview-trust.json"
SCOPES = ("local", "global")


def resolve_host(name: Optional[str] = None, required: bool = True) -> Optional[str]:
    """The agent that drives the run: --host, else $CROSSREVIEW_HOST, else what
    the environment gives away (Claude Code sets CLAUDECODE for its commands)."""
    name = (name or os.environ.get("CROSSREVIEW_HOST") or "").strip().lower()
    if not name and os.environ.get("CLAUDECODE"):
        name = "claude"
    if not name:
        if not required:
            return None
        raise CrossreviewError("say which agent you are with --host (%s, or your own name): the rosters live in "
                               "your folders" % ", ".join(HOSTS))
    if not re.match(r"^[a-z0-9][a-z0-9._-]*$", name):
        raise CrossreviewError("host %r is not a plain agent name" % name)
    return HOST_ALIASES.get(name, name)


def host_home(host: str) -> Path:
    """The agent's own settings folder; $CROSSREVIEW_HOME stands in for it, for
    every agent at once."""
    if os.environ.get("CROSSREVIEW_HOME"):
        return Path(os.environ["CROSSREVIEW_HOME"]).expanduser()
    env, default, _ = HOSTS.get(host, OTHER_HOST)
    if env and os.environ.get(env):
        return Path(os.environ[env]).expanduser()
    config = os.environ.get("XDG_CONFIG_HOME") or str(Path.home() / ".config")
    return Path(default.replace("{config}", config)).expanduser()


def global_roster_path(host: str) -> Path:
    return host_home(host) / ROSTER_FILE


def local_roster_path(host: str, workspace: Path) -> Path:
    return Path(workspace) / HOSTS.get(host, OTHER_HOST)[2] / ROSTER_FILE


def trust_path(host: str) -> Path:
    return host_home(host) / TRUST_FILE


def git_toplevel(path: Path) -> Optional[Path]:
    try:
        out = subprocess.run(["git", "rev-parse", "--show-toplevel"], cwd=str(path), stdin=subprocess.DEVNULL,
                             stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=20)
    except (OSError, subprocess.TimeoutExpired):
        return None
    if out.returncode != 0:
        return None
    return Path(out.stdout.decode().strip())


def canonical_workspace(path: Path) -> Path:
    """The project a directory belongs to: its git checkout, else the directory."""
    path = Path(path).expanduser().resolve()
    return git_toplevel(path) or path


def sha256_file(path: Path) -> str:
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


class RosterLocation:
    """A roster file read once: the digest, the approval check and the parsed
    roster all come from the same bytes, so a file swapped between them cannot
    run under an approval given to what it held before."""

    def __init__(self, path: Path, origin: str, host: Optional[str] = None, workspace: Optional[Path] = None):
        self.path = path
        self.origin = origin
        self.host = host
        self.workspace = workspace
        self.data = Path(path).read_bytes()
        self.digest = hashlib.sha256(self.data).hexdigest()

    def roster(self) -> dict:
        try:
            data = json.loads(self.data.decode("utf-8"))
        except (UnicodeDecodeError, ValueError) as exc:
            raise CrossreviewError("cannot read %s: %s" % (self.path, exc))
        if not isinstance(data, dict) or not isinstance(data.get("reviewers"), list):
            raise CrossreviewError("%s is not a roster: it needs a \"reviewers\" list" % self.path)
        return data

    @property
    def needs_approval(self) -> bool:
        # A project's roster can arrive with a clone, so it runs only once the
        # user approved these bytes; the one setup wrote is approved as it is written.
        return self.origin == "local" and not is_trusted(self.host or "", self.workspace, self.path, self.digest)


def _named_roster(path: Path, origin: str, host: Optional[str], workspace: Optional[str]) -> RosterLocation:
    """A roster named on the command line or in the environment. One that lives
    inside the project's checkout came with it, whatever named it, and needs the
    same approval as the project's own."""
    path = path.resolve()
    ws = git_toplevel(Path(workspace or os.getcwd()).expanduser().resolve())
    if ws is None:
        return RosterLocation(path, origin, host)
    try:
        path.relative_to(ws.resolve())
    except ValueError:
        return RosterLocation(path, origin, host)
    if host is None:
        raise CrossreviewError("%s lies inside the project: say which agent you are (--host), whose approvals apply"
                               % path)
    return RosterLocation(path, "local", host, ws)


def locate_roster(host: Optional[str], explicit: Optional[str] = None, workspace: Optional[str] = None,
                  scope: Optional[str] = None) -> Optional[RosterLocation]:
    """--roster, $CROSSREVIEW_ROSTER, then the host's local roster of this project,
    then its global one: a project's own set wins over the personal one."""
    if explicit:
        path = Path(explicit).expanduser()
        if not path.is_file():
            raise CrossreviewError("roster %s does not exist" % path)
        return _named_roster(path, "explicit", host, workspace)
    if os.environ.get("CROSSREVIEW_ROSTER"):
        path = Path(os.environ["CROSSREVIEW_ROSTER"]).expanduser()
        if not path.is_file():
            raise CrossreviewError("CROSSREVIEW_ROSTER names %s, which does not exist" % path)
        return _named_roster(path, "env", host, workspace)
    if host is None:
        host = resolve_host()
    ws = canonical_workspace(Path(workspace or os.getcwd()))
    if scope in (None, "local"):
        path = local_roster_path(host, ws)
        if path.is_file():
            return RosterLocation(path.resolve(), "local", host, ws)
    if scope in (None, "global"):
        path = global_roster_path(host)
        if path.is_file():
            return RosterLocation(path, "global", host)
    return None


def _read_json(path: Path, default):
    for attempt in range(50):
        try:
            return json.loads(Path(path).read_text(encoding="utf-8"))
        except FileNotFoundError:
            return default
        except PermissionError:
            # Windows: the writer is replacing the file this very moment.
            if attempt == 49:
                raise CrossreviewError("cannot read %s: permission denied" % path)
            time.sleep(0.05)
        except (OSError, ValueError) as exc:
            raise CrossreviewError("cannot read %s: %s" % (path, exc))
    return default


def _write_json(path: Path, data) -> None:
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + ".tmp-%d" % os.getpid())
    tmp.write_text(json.dumps(data, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    for attempt in range(50):
        try:
            os.replace(str(tmp), str(path))
            return
        except PermissionError:
            if attempt == 49:
                raise
            time.sleep(0.05)


def is_trusted(host: str, workspace: Optional[Path], roster: Path, digest: str) -> bool:
    data = _read_json(trust_path(host), {"version": 1, "approvals": []})
    for entry in data.get("approvals", []):
        if (entry.get("workspace") == str(workspace) and entry.get("roster") == str(roster)
                and entry.get("sha256") == digest):
            return True
    return False


def record_trust(host: str, workspace: Path, roster: Path, digest: str) -> None:
    data = _read_json(trust_path(host), {"version": 1, "approvals": []})
    approvals = [a for a in data.get("approvals", [])
                 if not (a.get("workspace") == str(workspace) and a.get("roster") == str(roster))]
    approvals.append({"workspace": str(workspace), "roster": str(roster), "sha256": digest,
                      "approved": _dt.datetime.now().isoformat(timespec="seconds")})
    data["approvals"] = approvals
    _write_json(trust_path(host), data)


def load_roster(path: Path) -> dict:
    data = _read_json(path, None)
    if not isinstance(data, dict) or not isinstance(data.get("reviewers"), list):
        raise CrossreviewError("%s is not a roster: it needs a \"reviewers\" list" % path)
    return data


WINDOWS_RESERVED = {"con", "prn", "aux", "nul"} | {"com%d" % i for i in range(1, 10)} | {"lpt%d" % i for i in range(1, 10)}


def slug(text: str) -> str:
    """A reviewer name that is safe as a single file name on every system."""
    name = re.sub(r"[^A-Za-z0-9._-]+", "-", text).strip("-.")[:80] or "reviewer"
    if name.split(".")[0].lower() in WINDOWS_RESERVED:
        name = "reviewer-" + name
    return name


def normalize_entries(roster: dict, origin: str = "user") -> List[dict]:
    """Reviewer entries with their defaults filled in and unique names."""
    out, seen = [], set()
    for i, raw in enumerate(roster.get("reviewers", [])):
        if not isinstance(raw, dict):
            raise CrossreviewError("reviewer #%d is not an object" % (i + 1))
        entry = dict(raw)
        entry.setdefault("kind", "cli")
        entry.setdefault("enabled", True)
        if entry["kind"] == "internal" and not entry.get("host"):
            # Coddy wrote the first rosters, and its internal reviewers are its own subagents.
            entry["host"] = "coddy"
        base = slug(entry.get("name") or "%s-%s" % (
            entry.get("agent") or entry.get("host") or entry["kind"], entry.get("model") or "default"))
        name, n = base, 2
        while name.lower() in seen:
            name, n = "%s-%d" % (base, n), n + 1
        seen.add(name.lower())
        entry["name"] = name
        out.append(entry)
    return out


KNOWN_ISSUES = (
    (re.compile(r"\"\$\((cat|Get-Content)\b"),
     "passes the brief as an argument: a brief over 128 KB fails with 'Argument list too long' "
     "(32 KB on Windows); the current template reads it from stdin or a file"),
    (re.compile(r"(^|\s)-p\s+-i\s"), "uses `-p -i`, which old Coddy builds read as the prompt '-i'"),
    (re.compile(r"Get-Content\s+-Raw\s+\{brief\}\s*\|"),
     "pipes the brief through PowerShell, which re-encodes it (Windows PowerShell 5.1 reads it in the "
     "system code page and writes the review as UTF-16); the current template hands redirection to cmd"),
)


def entry_warnings(entry: dict, table: Dict[str, AgentSpec]) -> List[str]:
    if entry.get("kind") != "cli":
        return []
    warnings = []
    command = entry.get("command") or ""
    for placeholder in ("{brief}", "{out}"):
        if placeholder not in command:
            warnings.append("command has no %s placeholder" % placeholder)
    if "{model}" in command:
        warnings.append("command still holds {model}: write the model into it")
    spec = table.get(entry.get("agent", ""))
    for pattern, text in KNOWN_ISSUES:
        if pattern.search(command) and not (spec and spec.agent == "koda" and "argument" in text):
            warnings.append(text)
    if spec and spec.agent == "koda":
        warnings.append("koda takes the brief only as a command-line argument: a brief over %s cannot reach it"
                        % human_bytes(ARG_LIMIT))
    if spec and entry.get("model") and entry.get("binary"):
        current = fill(spec.template(), bin=entry["binary"], model=entry["model"])
        if current != command and not any(t in w for w in warnings for t in ("argument", "-p -i", "re-encodes")):
            warnings.append("differs from the current template: %s" % current)
    if not spec:
        warnings.append("agent %r is not in agents.tsv; the command runs exactly as written" % entry.get("agent"))
    return warnings


def describe_roster(loc: RosterLocation, roster: dict, table: Dict[str, AgentSpec]) -> str:
    what = {"local": "local: this project, for %s" % loc.host, "global": "global: every project, for %s" % loc.host}
    lines = ["roster: %s (%s)" % (loc.path, what.get(loc.origin, loc.origin))]
    lines.append("min_reviewers: %s, timeout: %ss" % (roster.get("min_reviewers", DEFAULT_MIN_REVIEWERS),
                                                      roster.get("timeout", DEFAULT_TIMEOUT)))
    for i, e in enumerate(normalize_entries(roster, loc.origin), 1):
        flag = "" if e.get("enabled", True) else " (disabled)"
        if e["kind"] == "internal":
            lines.append("  %d. %s: internal reviewer of %s on %s%s" % (i, e["name"], e["host"], e.get("model", "?"), flag))
        else:
            lines.append("  %d. %s: %s%s" % (i, e["name"], e.get("command", "?"), flag))
        for w in entry_warnings(e, table):
            lines.append("       warning: %s" % w)
    return "\n".join(lines)


def cmd_roster(args) -> int:
    host = resolve_host(args.host, required=not (args.roster or os.environ.get("CROSSREVIEW_ROSTER")))
    loc = locate_roster(host, args.roster, args.workspace, args.scope)
    if loc is None:
        ws = canonical_workspace(Path(args.workspace or os.getcwd()))
        looked = [str(local_roster_path(host, ws)), str(global_roster_path(host))]
        if args.scope:
            looked = looked[:1] if args.scope == "local" else looked[1:]
        print("no roster for %s (looked at %s)" % (host, " and ".join(looked)))
        return EXIT_NO_ROSTER
    roster = loc.roster()
    table = load_table()
    if args.json:
        print(json.dumps({"path": str(loc.path), "origin": loc.origin, "host": loc.host, "sha256": loc.digest,
                          "needs_approval": loc.needs_approval,
                          "workspace": str(loc.workspace) if loc.workspace else None,
                          "min_reviewers": roster.get("min_reviewers", DEFAULT_MIN_REVIEWERS),
                          "reviewers": normalize_entries(roster, loc.origin)}, indent=2, ensure_ascii=False))
    else:
        print(describe_roster(loc, roster, table))
    if loc.needs_approval:
        print("\nThis roster belongs to the project, not to you: it may have come with the clone, and it runs the "
              "commands above. It is used only after the user approves this exact file (sha256 %s): show them the "
              "commands, and on a yes run `crossreview.py trust --host %s --workspace %s --roster %s --sha256 %s`. "
              "On a no, use the global roster: --scope global." % (
                  loc.digest, loc.host, loc.workspace, loc.path, loc.digest))
        return EXIT_NEEDS_APPROVAL
    return EXIT_OK


def cmd_trust(args) -> int:
    host = resolve_host(args.host)
    ws = canonical_workspace(Path(args.workspace or os.getcwd()))
    path = (Path(args.roster).expanduser() if args.roster else local_roster_path(host, ws)).resolve()
    try:
        path.relative_to(ws.resolve())
    except ValueError:
        raise CrossreviewError("%s is not inside the project %s; only a project's roster needs approval" % (path, ws))
    if not path.is_file():
        raise CrossreviewError("roster %s does not exist" % path)
    digest = sha256_file(path)
    if args.sha256 and args.sha256.lower() != digest:
        raise CrossreviewError("%s changed since it was shown (sha256 %s, now %s): show it to the user again"
                               % (path, args.sha256, digest))
    record_trust(host, ws, path, digest)
    print("approved %s for %s (sha256 %s); a change to the file needs a new approval" % (path, host, digest))
    return EXIT_OK


def parse_spec(spec: str) -> Tuple[str, Optional[str], Optional[str]]:
    """'agent:model' -> (agent, model, None); 'internal/host:model' -> ('internal', model, host)."""
    head, sep, model = spec.partition(":")
    model = model if sep else None
    if head == "internal" or head.startswith("internal/"):
        _, _, host = head.partition("/")
        return "internal", model, host or None
    return head, model, None


# What a model id may hold: provider/model, tags (gpt-oss:120b), variants
# (claude-opus-4-8[context=1m,effort=high]). Nothing a shell would read.
MODEL_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:/@+=,\[\]-]*$")


def check_model_id(model: str) -> None:
    if not MODEL_ID.match(model) or len(model) > 200:
        raise CrossreviewError("model id %r holds characters a command line would interpret; "
                               "write the command into the roster by hand if the CLI really needs them" % model)


def build_entries(specs: List[str], table: Dict[str, AgentSpec], windows: bool = IS_WINDOWS) -> List[dict]:
    wanted = sorted({parse_spec(s)[0] for s in specs} - {"internal"})
    unknown = [a for a in wanted if a not in table]
    if unknown:
        raise CrossreviewError("unknown agent(s): %s (known: %s)" % (", ".join(unknown), ", ".join(table)))
    found = {d.agent: d for d in detect(table, wanted)} if wanted else {}
    entries = []
    for spec in specs:
        agent, model, host = parse_spec(spec)
        if agent == "internal":
            if not host or not model:
                raise CrossreviewError("an internal reviewer is internal/<host>:<model>, e.g. internal/claude:sonnet")
            check_model_id(model)
            entries.append({"kind": "internal", "host": host, "model": model})
            continue
        if model:
            check_model_id(model)
        if agent not in found:
            raise CrossreviewError("%s is not installed or did not answer --version/--help" % agent)
        det = found[agent]
        needs_model = "{model}" in det.template
        if needs_model and not model:
            raise CrossreviewError("%s needs a model: %s:<model id>" % (agent, agent))
        entry = {"kind": "cli", "agent": agent, "binary": det.binary,
                 "command": fill(det.template, model=model or "")}
        if agent != "koda" and plan_command(entry["command"])["mode"] != "exec":
            raise CrossreviewError("the command built for %s is not a plain command line: %s" % (agent, entry["command"]))
        if model:
            entry["model"] = model
        entries.append(entry)
    return entries


def refresh_entries(entries: List[dict], table: Dict[str, AgentSpec]) -> List[dict]:
    """Rewrite each CLI entry of a known agent with the current template, keeping
    its binary when that is still one of the agent's candidates."""
    found = {d.agent: d for d in detect(table, sorted({e.get("agent") for e in entries
                                                       if e.get("kind", "cli") == "cli" and e.get("agent") in table}))}
    out = []
    for e in entries:
        e = dict(e)
        spec = table.get(e.get("agent", ""))
        if e.get("kind", "cli") == "cli" and spec and e.get("model"):
            # What detection verifies now wins over what the roster remembers: the
            # name `agent` may point at another program today.
            binary = found[spec.agent].binary if spec.agent in found else None
            if binary:
                e["binary"] = binary
                e["command"] = fill(spec.template(), bin=binary, model=e["model"])
                e.pop("prompt_via", None)
        out.append(e)
    return out


def cmd_init(args) -> int:
    host = resolve_host(args.host)
    ws = canonical_workspace(Path(args.workspace or os.getcwd()))
    if args.path:
        target = Path(args.path).expanduser()
    elif args.scope == "local":
        target = local_roster_path(host, ws)
    elif args.scope == "global":
        target = global_roster_path(host)
    else:
        raise CrossreviewError("say where the roster goes: --scope local (%s) or --scope global (%s)"
                               % (local_roster_path(host, ws), global_roster_path(host)))
    if args.import_path:
        roster = load_roster(Path(args.import_path).expanduser())
        for e in roster.get("reviewers", []):
            # Coddy wrote the first rosters, and its internal reviewers are its own subagents.
            if isinstance(e, dict) and e.get("kind") == "internal" and not e.get("host"):
                e["host"] = "coddy"
        if args.refresh:
            roster["reviewers"] = refresh_entries(roster.get("reviewers", []), load_table())
        entries = roster.get("reviewers", [])
    else:
        if not args.reviewers:
            raise CrossreviewError("name the reviewers, e.g. `init --scope global cursor:auto coddy:codex/gpt-5.6-sol`")
        entries = build_entries(args.reviewers, load_table())
        roster = {"version": 1, "min_reviewers": args.min_reviewers, "timeout": args.timeout, "reviewers": entries}
    if target.is_file() and not (args.force or args.add):
        raise CrossreviewError("%s already exists; pass --add to append or --force to replace it" % target)
    if args.add and target.is_file():
        old = load_roster(target)
        old["reviewers"] = old.get("reviewers", []) + entries
        roster = old
    _write_json(target, roster)
    resolved = target.resolve()
    try:
        resolved.relative_to(ws)
        inside = True
    except ValueError:
        inside = False
    if inside:
        # A roster in the project: the user asked for this file to be written,
        # so it is approved as it stands.
        loc = RosterLocation(resolved, "local", host, ws)
        record_trust(host, ws, resolved, loc.digest)
    else:
        loc = RosterLocation(resolved, "global" if resolved == global_roster_path(host).resolve() else "explicit", host)
    print(describe_roster(loc, roster, load_table()))
    print("\nwritten. Check that every reviewer answers: crossreview.py probe --host %s%s" % (
        host, "" if loc.origin in ("local", "global") else " --roster %s" % target))
    return EXIT_OK


# ----------------------------------------------------------------- brief


def git_out(args: List[str], cwd: Path, check: bool = True) -> str:
    proc = subprocess.run(["git"] + args, cwd=str(cwd), stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE)
    if check and proc.returncode not in (0, 1):
        raise CrossreviewError("git %s: %s" % (" ".join(args), proc.stderr.decode("utf-8", "replace").strip()))
    return proc.stdout.decode("utf-8", "replace")


def _is_text(data: bytes) -> bool:
    return b"\0" not in data[:8192]


def untracked_diff(ws: Path, pathspec: List[str], max_file: int) -> Tuple[str, List[str]]:
    files = [f for f in git_out(["ls-files", "--others", "--exclude-standard", "-z", "--"] + pathspec,
                                ws).split("\0") if f]
    chunks, skipped = [], []
    for rel in files:
        path = ws / rel
        try:
            data = path.read_bytes()
        except OSError:
            continue
        if len(data) > max_file or not _is_text(data):
            skipped.append(rel)
            continue
        text = data.decode("utf-8", "replace")
        lines = text.splitlines()
        body = "".join("+%s\n" % ln for ln in lines)
        if text and not text.endswith("\n"):
            body += "\\ No newline at end of file\n"
        chunks.append("diff --git a/%s b/%s\nnew file (untracked)\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n%s"
                      % (rel, rel, rel, len(lines), body))
    return "".join(chunks), skipped


def fence_for(text: str) -> str:
    longest = max((len(m) for m in re.findall(r"`{3,}", text)), default=0)
    return "`" * max(3, longest + 1)


TOOLS_LINE = {
    "none": "IMPORTANT: answer from this brief alone. Do not call tools, do not read or search files, "
            "do not run commands, do not edit anything. Reply with one message.",
    "read": "IMPORTANT: you may read and search files in the working directory to check a finding, but do not "
            "edit, create or delete anything and do not run commands that change state. Reply with one message.",
}


def read_input(path, what: str) -> str:
    path = Path(path).expanduser()
    if not path.is_file():
        raise CrossreviewError("%s %s does not exist" % (what, path))
    return path.read_text(encoding="utf-8", errors="replace")


def build_brief(args) -> Tuple[str, dict]:
    ws = Path(args.workspace or os.getcwd()).expanduser().resolve()
    pathspec = []
    if args.exclude:
        pathspec = ["."] + [":(exclude)%s" % p for p in args.exclude]
    unified = "-U%d" % args.unified
    stats = {"files": 0}
    sections = []
    if args.doc:
        doc = Path(args.doc).expanduser()
        text = read_input(doc, "document")
        scope = args.scope or "the document %s" % doc
        sections.append(("The document under review: %s" % doc.name, text, "markdown"))
        stats["files"] = 1
    elif args.files:
        scope = args.scope or "the files %s" % ", ".join(args.files)
        for name in args.files:
            path = (ws / name) if not Path(name).is_absolute() else Path(name)
            text = read_input(path, "file")
            sections.append(("File %s" % name, text, ""))
        stats["files"] = len(args.files)
    else:
        if git_toplevel(ws) is None:
            raise CrossreviewError("%s is not a git repository: pass --files or --doc" % ws)
        has_head = subprocess.run(["git", "rev-parse", "--verify", "-q", "HEAD"], cwd=str(ws),
                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
        untracked = False
        if args.range:
            diff = git_out(["diff", unified, args.range, "--"] + pathspec, ws)
            scope = args.scope or "the changes of git diff %s in %s" % (args.range, ws.name)
        elif args.staged:
            diff = git_out(["diff", unified, "--cached", "--"] + pathspec, ws)
            scope = args.scope or "the staged changes in %s" % ws.name
        elif args.base:
            base = git_out(["merge-base", args.base, "HEAD"], ws).strip()
            if not base:
                raise CrossreviewError("no merge base between %s and HEAD" % args.base)
            diff = git_out(["diff", unified, base, "--"] + pathspec, ws)
            untracked = True
            scope = args.scope or "everything on this branch since %s, committed or not, in %s" % (args.base, ws.name)
        else:
            if has_head:
                diff = git_out(["diff", unified, "HEAD", "--"] + pathspec, ws)
            else:
                diff = git_out(["diff", unified, "--cached", "--"] + pathspec, ws)
            untracked = True
            scope = args.scope or "the uncommitted changes in %s (git diff HEAD and untracked files)" % ws.name
        skipped = []
        if untracked and not args.no_untracked:
            extra, skipped = untracked_diff(ws, pathspec, args.max_file_bytes)
            diff += extra
        if not diff.strip():
            raise CrossreviewError("the scope is empty: nothing to review in %s" % scope)
        stats["files"] = len(re.findall(r"^diff --git ", diff, re.M))
        sections.append(("The change", diff, "diff"))
        if skipped:
            sections.append(("Untracked files left out (binary or larger than %d bytes)" % args.max_file_bytes,
                             "\n".join(skipped), ""))
    intent = args.intent
    if args.intent_file:
        intent = read_input(args.intent_file, "intent file").strip()
    notes = ""
    if args.notes_file:
        notes = read_input(args.notes_file, "notes file").strip()

    out = [TOOLS_LINE[args.tools], "", "# Code review brief", "",
           "You are one of several independent reviewers in a cross-review: other agents on other models review "
           "the same change without seeing each other's answers, and an orchestrator checks every finding against "
           "the code and decides what to act on. Look for real "
           "defects: wrong behaviour, crashes, data loss, races, security holes, broken contracts, missing or "
           "wrong tests, and documentation the change leaves stale. Skip style preferences unless they hide a "
           "bug. In a diff, lines starting with `-` are the old code: judge the new version. Review it yourself: "
           "do not start a cross-review of your own or hand the brief to other agents.", "",
           "## What the change is meant to do", "", intent or "Not stated: infer it from the change.", "",
           "## Scope", "", scope[:1].upper() + scope[1:] + ".", "",
           "## Answer format", "",
           "A numbered list of findings, most severe first. For each finding:", "",
           "- **severity**: critical, high, medium or low;",
           "- **where**: `path:line` in the new version (for a document, its section);",
           "- **problem**: what goes wrong, with a concrete scenario (the input or state, then the wrong result);",
           "- **fix**: the smallest change that fixes it.", "",
           "Questions and doubts go in a separate list after the findings. No praise and no summary of the change. "
           "If you find nothing, say so. End with one line: `VERDICT: approve`, `VERDICT: approve with changes` "
           "or `VERDICT: needs rework`. Write in %s." % args.language, ""]
    if notes:
        out += ["## Notes from the orchestrator", "",
                "Facts about the code and decisions the orchestrator of this review has already taken. Treat them as "
                "settled unless you have new evidence.", "", notes, ""]
    for title, body, lang in sections:
        fence = fence_for(body)
        out += ["## %s" % title, "", fence + lang, body.rstrip("\n"), fence, ""]
    text = "\n".join(out)
    stats["bytes"] = len(text.encode("utf-8"))
    stats["scope"] = scope
    return text, stats


def cmd_brief(args) -> int:
    if args.exclude and (args.files or args.doc):
        raise CrossreviewError("--exclude filters a git scope; with --files or --doc name only what you want")
    text, stats = build_brief(args)
    out = Path(args.out).expanduser()
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(text, encoding="utf-8")
    size = stats["bytes"]
    print("brief: %s (%s, about %d tokens, %d file(s))" % (out, human_bytes(size), size // 4, stats["files"]))
    print("scope: %s" % stats["scope"])
    if size > 400_000:
        print("warning: over 400 KB; most models will not take it whole. Split the review by layer or exclude "
              "generated files, lockfiles and tests with --exclude.")
    elif size > 150_000:
        print("warning: over 150 KB; smaller local models may stall or run out of context on it.")
    return EXIT_OK


# ---------------------------------------------------------------- running


def human_bytes(n: int) -> str:
    if n < 1024:
        return "%d B" % n
    if n < 1024 * 1024:
        return "%.1f KB" % (n / 1024)
    return "%.1f MB" % (n / 1024 / 1024)


def human_secs(s: Optional[float]) -> str:
    if s is None:
        return "-"
    s = int(s)
    if s < 60:
        return "%ds" % s
    if s < 3600:
        return "%dm%02ds" % (s // 60, s % 60)
    return "%dh%02dm" % (s // 3600, (s % 3600) // 60)


# One command-line argument: Linux caps it at 128 KB (MAX_ARG_STRLEN), Windows
# the whole command line at 32 767 characters.
ARG_LIMIT = 30_000 if IS_WINDOWS else 120_000
ARG_BRIEF = {'$(cat {brief})', '$(Get-Content -Raw {brief})', '$(Get-Content -Raw -Encoding UTF8 {brief})'}
PS_PREFIX = ["Get-Content", "-Raw"]
SHELL_ONLY = {";", "&", "&&", "||", "(", ")", "`"}
PUNCT = set("<>|;&()")


def plan_command(command: str) -> dict:
    """Read a roster command into argv plus redirections, so it runs without a shell.

    The templates are simple: `bin args [< src] [> {out}]`, the same behind
    `cmd /d /c --%` (the PowerShell column), the older PowerShell form
    `Get-Content -Raw {brief} | bin args [> {out}]`, or an argument
    "$(cat {brief})". Anything else is handed to a shell as written.
    """
    try:
        lexer = shlex.shlex(command, posix=True, punctuation_chars="<>|;&()")
        lexer.whitespace_split = True
        if IS_WINDOWS:
            # A Windows path is full of backslashes; quotes still group words.
            lexer.escape = ""
        tokens = list(lexer)
    except ValueError:
        return {"mode": "shell"}
    if not tokens or any(t in SHELL_ONLY for t in tokens) or re.search(r"\d>|>&|&>", command):
        return {"mode": "shell"}
    if any(set(t) <= PUNCT and t not in ("<", ">", "|") for t in tokens):
        return {"mode": "shell"}
    stdin = None
    if tokens[0].lower() in ("cmd", "cmd.exe") and "--%" in tokens[:5]:
        tokens = tokens[tokens.index("--%") + 1:]
        if not tokens:
            return {"mode": "shell"}
    if tokens[:2] == PS_PREFIX and len(tokens) > 3 and tokens[3] == "|":
        stdin = tokens[2]
        tokens = tokens[4:]
    if "|" in tokens:
        return {"mode": "shell"}
    argv, stdout, i = [], None, 0
    while i < len(tokens):
        tok = tokens[i]
        if tok in ("<", ">"):
            if i + 1 >= len(tokens):
                return {"mode": "shell"}
            if tok == "<":
                stdin = tokens[i + 1]
            else:
                stdout = tokens[i + 1]
            i += 2
            continue
        argv.append(tok)
        i += 1
    if not argv:
        return {"mode": "shell"}
    return {"mode": "exec", "argv": argv, "stdin": stdin, "stdout": stdout}


def ps_quote(s: str) -> str:
    return "'" + s.replace("'", "''") + "'"


def render_shell(command: str, brief: str, out: str) -> List[str]:
    if IS_WINDOWS:
        exe = shutil.which("pwsh") or shutil.which("powershell") or "powershell"
        # After --% the line belongs to cmd.exe, which knows only double quotes.
        quote = (lambda p: '"%s"' % p) if "--%" in command else ps_quote
        rendered = command.replace("{brief}", quote(brief)).replace("{out}", quote(out))
        return [exe, "-NoProfile", "-NonInteractive", "-Command", rendered]
    rendered = command.replace("{brief}", shlex.quote(brief)).replace("{out}", shlex.quote(out))
    return [shutil.which("sh") or "/bin/sh", "-c", rendered]


RUN_NAME = re.compile(r"^\d{8}-\d{6}-[A-Za-z0-9_]{4,}$")
KEEP_RUNS_DAYS = 7


def prune_old_runs(root: Path, days: int = KEEP_RUNS_DAYS) -> None:
    """Remove finished runs of the default root older than `days`; only our own
    directories (the run-name pattern, a status that says done) are touched."""
    cutoff = time.time() - days * 86400
    for path in root.iterdir() if root.is_dir() else []:
        try:
            if not (path.is_dir() and not path.is_symlink() and RUN_NAME.match(path.name)
                    and path.stat().st_mtime < cutoff):
                continue
            status = json.loads((path / "status.json").read_text(encoding="utf-8"))
            if status.get("done") and float(status.get("updated") or 0) < cutoff:
                shutil.rmtree(str(path), ignore_errors=True)
        except (OSError, ValueError):
            continue


def run_root() -> Path:
    """The per-user directory runs live in. Briefs hold whole diffs, so on a
    shared machine it must be ours and closed to everybody else: a directory
    another user created under the same name, or a symlink, is refused."""
    user = re.sub(r"\W", "", os.environ.get("USER") or os.environ.get("USERNAME") or "user") or "user"
    root = Path(tempfile.gettempdir()) / ("crossreview-%s" % user)
    try:
        root.mkdir(mode=0o700)
    except FileExistsError:
        pass
    if not IS_WINDOWS:
        st = os.lstat(str(root))
        if stat.S_ISLNK(st.st_mode) or not stat.S_ISDIR(st.st_mode) or st.st_uid != os.getuid():
            raise CrossreviewError("%s is not a directory of yours; pass --run-dir" % root)
        if st.st_mode & 0o077:
            os.chmod(str(root), 0o700)
    return root


def new_run_dir(base: Optional[str]) -> Path:
    if base:
        path = Path(base).expanduser()
        path.mkdir(parents=True, exist_ok=True)
        return path.resolve()
    root = run_root()
    prune_old_runs(root)
    stamp = _dt.datetime.now().strftime("%Y%m%d-%H%M%S")
    return Path(tempfile.mkdtemp(prefix=stamp + "-", dir=str(root)))


def select_entries(args) -> Tuple[List[dict], int, int, str]:
    """(entries, min_reviewers, timeout, where they came from)."""
    if args.reviewer:
        entries = normalize_entries({"reviewers": build_entries(args.reviewer, load_table())})
        return entries, min(DEFAULT_MIN_REVIEWERS, len(entries)), DEFAULT_TIMEOUT, "command line"
    host = resolve_host(args.host, required=not (args.roster or os.environ.get("CROSSREVIEW_ROSTER")))
    loc = locate_roster(host, args.roster, args.workspace, args.scope)
    if loc is None:
        raise CrossreviewError("no roster for %s: run /crossreview:setup (detect, then init) or name reviewers with "
                               "--reviewer" % host)
    if loc.needs_approval:
        raise CrossreviewError("the workspace roster %s is not approved; see `crossreview.py roster`" % loc.path)
    roster = loc.roster()
    entries = normalize_entries(roster, loc.origin)
    return (entries, int(roster.get("min_reviewers", DEFAULT_MIN_REVIEWERS)),
            int(roster.get("timeout", DEFAULT_TIMEOUT)), "%s (%s)" % (loc.path, loc.origin))


def prepare_run(entries: List[dict], brief_src: Path, run_dir: Path, cwd: Optional[str], timeout: int,
                min_reviewers: int, source: str, host: Optional[str], only: Optional[List[str]],
                parallel: int = 0) -> dict:
    if (run_dir / "run.json").exists() or (run_dir / "status.json").exists():
        raise CrossreviewError("%s already holds a run: pick a new directory, or `retry` that run" % run_dir)
    (run_dir / "reviews").mkdir(parents=True, exist_ok=True)
    work = Path(cwd).expanduser().resolve() if cwd else run_dir / "work"
    work.mkdir(parents=True, exist_ok=True)
    brief = run_dir / "brief.md"
    if brief_src.resolve() != brief.resolve():
        shutil.copyfile(str(brief_src), str(brief))
    names = [e["name"] for e in entries]
    unknown = [n for n in (only or []) if n not in names]
    if unknown:
        raise CrossreviewError("no reviewer named %s (the names are: %s)" % (", ".join(unknown), ", ".join(names)))
    reviewers = []
    for e in entries:
        if only and e["name"] not in only:
            continue
        if not e.get("enabled", True):
            continue
        out = run_dir / "reviews" / ("%s.md" % e["name"])
        item = {"name": e["name"], "kind": e["kind"], "agent": e.get("agent") or e.get("host"),
                "model": e.get("model"), "out": str(out), "err": str(out.with_suffix(".err")),
                "log": str(out.with_suffix(".log")), "timeout": int(e.get("timeout") or timeout)}
        if e["kind"] == "internal":
            item["status"] = "host" if host and e.get("host") == host else "skipped"
            for key in ("definition", "reasoning"):
                if e.get(key):
                    item[key] = e[key]
            extra = "".join(", %s %s" % (k, e[k]) for k in ("definition", "reasoning") if e.get(k))
            item["note"] = ("run it as a read-only subagent of %s on %s%s and save its answer to %s" % (
                e.get("host"), e.get("model"), extra, out)) if item["status"] == "host" else (
                "internal reviewer of %s; this host cannot start it" % e.get("host"))
            reviewers.append(item)
            continue
        command = e.get("command") or ""
        if "{model}" in command and e.get("model"):
            command = command.replace("{model}", e["model"])
        item["command"] = command
        plan = plan_command(command)
        if plan["mode"] == "exec":
            argv = [a.replace("{brief}", str(brief)).replace("{out}", str(out)) for a in plan["argv"]]
            if any(a in ARG_BRIEF for a in plan["argv"]):
                text = brief.read_text(encoding="utf-8")
                shim = shutil.which(plan["argv"][0]) or plan["argv"][0]
                if IS_WINDOWS and shim.lower().endswith((".cmd", ".bat")):
                    item.update({"status": "failed", "mode": "exec", "argv": [], "rc": None, "bytes": 0,
                                 "note": "%s is a batch file: cmd.exe would interpret the text of the brief "
                                         "passed as its argument, so it is not run" % Path(shim).name})
                    item["display"] = command
                    reviewers.append(item)
                    continue
                if len(text.encode("utf-8")) > ARG_LIMIT:
                    item.update({"status": "failed", "mode": "exec", "argv": [], "rc": None, "bytes": 0,
                                 "note": "this CLI takes the brief as a command-line argument, which holds at most "
                                         "%s here; the brief is %s" % (human_bytes(ARG_LIMIT),
                                                                        human_bytes(len(text.encode("utf-8"))))})
                    item["display"] = command
                    reviewers.append(item)
                    continue
                argv = [text if a in ARG_BRIEF else b for a, b in zip(plan["argv"], argv)]
            stdin = plan["stdin"]
            if stdin == "{brief}":
                stdin = str(brief)
            elif stdin in ("/dev/null", "NUL", "$null"):
                stdin = None
            stdout = plan["stdout"]
            if stdout in (None, "{out}"):
                stdout = str(out)
            item.update({"mode": "exec", "argv": argv, "stdin": stdin, "stdout": stdout})
            item["display"] = " ".join(shlex.quote(a) if len(a) < 300 else "<brief text>" for a in argv) + (
                " < %s" % shlex.quote(stdin) if stdin else "") + " > %s" % shlex.quote(stdout)
        else:
            item.update({"mode": "shell", "argv": render_shell(command, str(brief), str(out))})
            item["display"] = item["argv"][-1]
        # The listing is read by people: paths inside the run are shown relative to it.
        for prefix in {str(run_dir) + os.sep, str(run_dir.resolve()) + os.sep}:
            item["display"] = item["display"].replace(prefix, "")
        item["status"] = "queued"
        reviewers.append(item)
    if not reviewers:
        raise CrossreviewError("no reviewer to run (check the names, `enabled` and the roster)")
    plan = {"version": 1, "run_dir": str(run_dir), "brief": str(brief), "cwd": str(work),
            "created": _dt.datetime.now().isoformat(timespec="seconds"), "source": source,
            "min_reviewers": min_reviewers, "parallel": parallel, "reviewers": reviewers}
    _write_json(run_dir / "run.json", plan)
    return plan


HINTS = (
    (re.compile(r"usage.?limit|rate.?limit|quota|too many requests|\b429\b", re.I), "usage or rate limit"),
    (re.compile(r"\b401\b|unauthori[sz]ed|invalid.{0,20}(api.?key|token|credential)|not (logged|signed) in|"
                r"log ?in (first|required)|no auth type|authenticat", re.I), "not signed in or invalid credentials"),
    (re.compile(r"\b403\b|forbidden|not (available|included) (on|in) your (plan|tier)", re.I),
     "access denied for this model or plan"),
    (re.compile(r"argument list too long|E2BIG|command line is too long", re.I),
     "prompt passed as an argument is over the OS limit; use a stdin template"),
    (re.compile(r"(unknown|invalid|unsupported|unrecognized) model|model .{0,40}(not found|does not exist|"
                r"not supported|is not available)", re.I), "unknown model id"),
    (re.compile(r"context (length|window)|too many tokens|prompt is too long|maximum context", re.I),
     "brief does not fit the model's context; split it"),
    (re.compile(r"max_tokens|output limit|cut off", re.I), "reply hit the model's output limit"),
    (re.compile(r"workspace trust|trust (this|the) (folder|workspace|directory)", re.I),
     "the CLI asks to trust the directory; its template needs the trust flag"),
    (re.compile(r"not a git repo|git-repo-check", re.I), "the CLI insists on a git repository"),
)
VERDICT_RE = re.compile(r"(?im)^[\W_]*verdict[\W_]*[:\-]\s*[`*_]*\s*(approve with changes|approve|needs rework)")


def tail_text(path: str, limit: int = 4000) -> str:
    try:
        with open(path, "rb") as fh:
            fh.seek(0, os.SEEK_END)
            size = fh.tell()
            fh.seek(max(0, size - limit))
            return fh.read().decode("utf-8", "replace")
    except OSError:
        return ""


def failure_hint(item: dict, include_out: bool = True) -> str:
    text = tail_text(item["err"]) + "\n" + tail_text(item.get("log", ""))
    if include_out:
        text += "\n" + tail_text(item["out"], 2000)
    for pattern, hint in HINTS:
        if pattern.search(text):
            return hint
    if not include_out:
        return ""
    lines = [ln.strip() for ln in tail_text(item["err"]).splitlines() if ln.strip()]
    return lines[-1][:200] if lines else ""


def finish_item(item: dict, rc: Optional[int], status: Optional[str] = None, note: str = "") -> None:
    """Record how a reviewer ended. status forces the outcome (timeout, stopped, missing)."""
    item["rc"] = rc
    item["ended"] = time.time()
    item["seconds"] = round(item["ended"] - item.get("started", item["ended"]), 1)
    out = Path(item["out"])
    if item.get("mode") == "shell" and (not out.exists() or out.stat().st_size == 0):
        log = Path(item["log"])
        if log.exists() and log.stat().st_size > 0:
            shutil.copyfile(str(log), str(out))
    size = out.stat().st_size if out.exists() else 0
    item["bytes"] = size
    text = out.read_text(encoding="utf-8", errors="replace") if size else ""
    found = VERDICT_RE.findall(text)
    item["verdict"] = found[-1].lower() if found else None
    if status:
        item["status"] = status
        item["note"] = note or (failure_hint(item) if not text.strip() else "partial output kept")
        return
    hint = failure_hint(item)
    if rc == 0 and text.strip():
        # A CLI that prints an API error and exits 0 still left no review. Only
        # the CLI's own streams, or an answer that opens like an error, say so:
        # a short review may well talk about 401s and rate limits.
        side = failure_hint(item, include_out=False)
        first = next((ln for ln in text.splitlines() if ln.strip()), "")
        error_like = re.match(r"(?i)^\W*(error|fatal|exception|traceback)\b", first) is not None
        if not item["verdict"] and len(text.strip()) < 600 and (side or error_like):
            item["status"], item["note"] = "failed", side or hint or first.strip()[:200]
        else:
            item["status"], item["note"] = "done", side
    elif rc == 0:
        item["status"], item["note"] = "empty", hint or "exited 0 without a review"
    else:
        item["status"], item["note"] = "failed", hint


def write_status(run_dir: Path, plan: dict, done: bool) -> None:
    status = {"run_dir": str(run_dir), "brief": plan["brief"], "cwd": plan["cwd"],
              "min_reviewers": plan["min_reviewers"], "supervisor_pid": os.getpid(),
              "updated": time.time(), "done": done,
              "reviewers": [{k: v for k, v in r.items() if k not in ("argv",)} for r in plan["reviewers"]]}
    _write_json(run_dir / "status.json", status)


def supervise(run_dir: Path, parallel: int = 0) -> None:
    plan = _read_json(run_dir / "run.json", None)
    if plan is None:
        raise CrossreviewError("%s has no run.json" % run_dir)
    previous = _read_json(run_dir / "status.json", None)
    if previous:
        # A retry: reviewers that already ended keep their outcome; run.json
        # says which ones were queued again.
        ended = {r["name"]: r for r in previous.get("reviewers", [])}
        for item in plan["reviewers"]:
            old = ended.get(item["name"])
            if old and item["status"] != "queued":
                item.update({k: v for k, v in old.items() if k != "argv"})
    env = dict(os.environ)
    env[DEPTH_ENV] = str(int(env.get(DEPTH_ENV, "0") or 0) + 1)
    env.setdefault("NO_COLOR", "1")
    queue = [r for r in plan["reviewers"] if r["status"] == "queued"]
    running: Dict[str, Tuple[subprocess.Popen, list]] = {}
    write_status(run_dir, plan, False)
    while queue or running:
        while queue and (parallel <= 0 or len(running) < parallel):
            item = queue.pop(0)
            if (run_dir / "stop").exists() or (run_dir / ("stop.%s" % item["name"])).exists():
                finish_item(item, None, "stopped", "stopped before it started")
                continue
            handles = []
            try:
                stdin = open(item["stdin"], "rb") if item.get("stdin") else subprocess.DEVNULL
                if item.get("stdin"):
                    handles.append(stdin)
                stdout = open(item["stdout"] if item["mode"] == "exec" else item["log"], "wb")
                stderr = open(item["err"], "wb")
                handles += [stdout, stderr]
                argv = list(item["argv"])
                if item["mode"] == "exec":
                    resolved = shutil.which(argv[0])
                    if not resolved:
                        raise FileNotFoundError("%s is not on PATH" % argv[0])
                    argv[0] = resolved
                proc = subprocess.Popen(argv, cwd=plan["cwd"], stdin=stdin, stdout=stdout, stderr=stderr,
                                        env=env, **_popen_group_kwargs())
            except OSError as exc:
                for h in handles:
                    h.close()
                Path(item["err"]).write_text("%s\n" % exc, encoding="utf-8")
                item["started"] = time.time()
                finish_item(item, None, "missing" if isinstance(exc, FileNotFoundError) else "failed", str(exc))
                continue
            item["status"] = "running"
            item["started"] = time.time()
            item["pid"] = proc.pid
            running[item["name"]] = (proc, handles)
        write_status(run_dir, plan, False)
        time.sleep(0.5)
        stop_all = (run_dir / "stop").exists()
        for item in plan["reviewers"]:
            if item["name"] not in running:
                continue
            proc, handles = running[item["name"]]
            rc = proc.poll()
            stop = stop_all or (run_dir / ("stop.%s" % item["name"])).exists()
            overdue = time.time() - item["started"] > item["timeout"]
            forced = None
            if rc is None and (stop or overdue):
                kill_tree(proc.pid, proc=proc)
                forced = "stopped" if stop else "timeout"
                try:
                    rc = proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    rc = None
            elif rc is None:
                continue
            for h in handles:
                h.close()
            del running[item["name"]]
            finish_item(item, rc, forced, "stopped on request" if forced == "stopped" else
                        "no answer within %s" % human_secs(item["timeout"]) if forced == "timeout" else "")
        if stop_all:
            for item in queue:
                finish_item(item, None, "stopped", "stopped before it started")
            queue = []
    write_status(run_dir, plan, True)


def spawn_supervisor(run_dir: Path, parallel: int) -> int:
    argv = [sys.executable, str(Path(__file__).resolve()), "_supervise", str(run_dir), "--parallel", str(parallel)]
    log = open(run_dir / "supervisor.log", "ab")
    kwargs = {"stdin": subprocess.DEVNULL, "stdout": log, "stderr": log, "cwd": str(run_dir), "close_fds": True}
    if IS_WINDOWS:
        flags = 0x00000008 | subprocess.CREATE_NEW_PROCESS_GROUP  # DETACHED_PROCESS
        try:
            proc = subprocess.Popen(argv, creationflags=flags | 0x01000000, **kwargs)  # CREATE_BREAKAWAY_FROM_JOB
        except OSError:
            proc = subprocess.Popen(argv, creationflags=flags, **kwargs)
    else:
        proc = subprocess.Popen(argv, start_new_session=True, **kwargs)
    log.close()
    return proc.pid


def cmd_run(args) -> int:
    depth = int(os.environ.get(DEPTH_ENV, "0") or 0)
    if depth > 0:
        raise CrossreviewError("this process is itself a crossreview reviewer (%s=%d): review the brief directly "
                               "instead of starting another fan-out" % (DEPTH_ENV, depth))
    brief = Path(args.brief).expanduser()
    if not brief.is_file():
        raise CrossreviewError("brief %s does not exist (write it with `crossreview.py brief`)" % brief)
    entries, min_reviewers, timeout, source = select_entries(args)
    run_dir = new_run_dir(args.run_dir)
    plan = prepare_run(entries, brief, run_dir, args.cwd, args.timeout or timeout,
                       args.min_reviewers or min_reviewers, source, resolve_host(args.host, required=False),
                       args.only, args.parallel)
    print("run: %s" % run_dir)
    print("reviewers from %s; working directory %s; paths below are relative to the run" % (source, plan["cwd"]))
    for r in plan["reviewers"]:
        if r["status"] == "queued":
            print("  %s: %s (timeout %s)" % (r["name"], r["display"], human_secs(r["timeout"])))
        else:
            print("  %s: %s" % (r["name"], r["note"]))
    if args.foreground:
        supervise(run_dir, args.parallel)
        print()
        return print_status(run_dir)
    pid = spawn_supervisor(run_dir, args.parallel)
    deadline = time.time() + 10
    while time.time() < deadline and not (run_dir / "status.json").exists():
        time.sleep(0.1)
    if not (run_dir / "status.json").exists():
        raise CrossreviewError("the supervisor (pid %d) did not start; see %s" % (pid, run_dir / "supervisor.log"))
    print("started in the background (supervisor pid %d). Next: crossreview.py wait %s --max 240" % (pid, run_dir))
    return EXIT_OK


def _supervisor_gone(status: dict) -> bool:
    # The supervisor rewrites the status about twice a second; it is gone only
    # when its pid is dead and the file has gone stale (a retry hands over to a
    # new supervisor, which takes a moment to write its first status).
    if status.get("done"):
        return False
    if pid_alive(int(status.get("supervisor_pid") or 0)):
        return False
    return time.time() - float(status.get("updated") or 0) > 10


def load_status(run_dir: Path) -> dict:
    status = _read_json(run_dir / "status.json", None)
    if status is None:
        raise CrossreviewError("%s is not a crossreview run (no status.json)" % run_dir)
    if _supervisor_gone(status):
        time.sleep(1.0)
        status = _read_json(run_dir / "status.json", status)
        if _supervisor_gone(status):
            # Its reviewers would go on writing outputs nobody tracks (and a
            # retry would race them): stop them and write the outcome down.
            for r in status["reviewers"]:
                if r["status"] == "running" and r.get("pid"):
                    kill_tree(int(r["pid"]), grace=2.0)
                if r["status"] in ("queued", "running"):
                    r["status"] = "lost"
                    r["note"] = "the supervisor is gone (killed with the shell that started it?)"
            status["done"] = True
            status["lost"] = True
            _write_json(run_dir / "status.json", status)
    return status


def format_status(status: dict) -> str:
    rows = [("NAME", "STATUS", "TIME", "SIZE", "VERDICT / NOTE")]
    now = time.time()
    for r in status["reviewers"]:
        st = r["status"]
        if st in ("host", "skipped"):
            rows.append((r["name"], st, "-", "-", r.get("note", "")))
            continue
        secs = now - r.get("started", now) if st == "running" else r.get("seconds")
        size = human_bytes(r["bytes"]) if r.get("bytes") is not None else "-"
        note = r.get("verdict") or ""
        if r.get("note"):
            note = (note + "; " if note else "") + r["note"]
        if st in ("failed", "empty") and r.get("rc") not in (None, 0):
            note = "rc=%s: %s" % (r["rc"], note)
        rows.append((r["name"], st, human_secs(secs), size, note))
    widths = [max(len(str(row[i])) for row in rows) for i in range(4)]
    lines = ["run: %s" % status["run_dir"]]
    for row in rows:
        lines.append("  ".join(str(row[i]).ljust(widths[i]) for i in range(4)) + "  " + row[4])
    cli = [r for r in status["reviewers"] if r["status"] not in ("host", "skipped")]
    answered = sum(1 for r in cli if r["status"] in ANSWERED)
    pending = [r["name"] for r in cli if r["status"] in ("queued", "running")]
    hosted = [r["name"] for r in status["reviewers"] if r["status"] == "host"]
    min_rev = int(status.get("min_reviewers", DEFAULT_MIN_REVIEWERS))
    if pending:
        lines.append("answered %d of %d so far; still running: %s. Call wait again." % (
            answered, len(cli), ", ".join(pending)))
        return "\n".join(lines)
    if answered >= min_rev:
        quorum = "quorum met"
    elif answered + len(hosted) >= min_rev:
        quorum = "quorum met once the internal reviewers you run answer"
    else:
        quorum = "INSUFFICIENT QUORUM: treat the findings as advisory"
    lines.append("finished: %d of %d CLI reviewers answered%s; min_reviewers %d: %s." % (
        answered, len(cli), " (+%d internal for you to run: %s)" % (len(hosted), ", ".join(hosted)) if hosted else "",
        min_rev, quorum))
    lines.append("Read the reviews: crossreview.py collect %s" % status["run_dir"])
    return "\n".join(lines)


def print_status(run_dir: Path) -> int:
    status = load_status(run_dir)
    print(format_status(status))
    return EXIT_OK if status.get("done") else EXIT_RUNNING


def cmd_status(args) -> int:
    run_dir = Path(args.run).expanduser()
    if args.json:
        print(json.dumps(load_status(run_dir), indent=2, ensure_ascii=False))
        return EXIT_OK
    return print_status(run_dir)


def cmd_wait(args) -> int:
    run_dir = Path(args.run).expanduser()
    deadline = time.time() + args.max
    while True:
        status = load_status(run_dir)
        if status.get("done") or time.time() >= deadline:
            break
        time.sleep(min(2.0, max(0.1, deadline - time.time())))
    print(format_status(status))
    return EXIT_OK if status.get("done") else EXIT_RUNNING


def cmd_collect(args) -> int:
    run_dir = Path(args.run).expanduser()
    status = load_status(run_dir)
    for r in status["reviewers"]:
        head = "===== %s (%s%s): %s" % (r["name"], r.get("agent") or "?",
                                        ", %s" % r["model"] if r.get("model") else "", r["status"])
        if r.get("seconds") is not None:
            head += ", %s" % human_secs(r["seconds"])
        print(head + " =====")
        out = Path(r["out"])
        if r["status"] in ("host", "skipped"):
            if out.exists() and out.stat().st_size:
                print(out.read_text(encoding="utf-8", errors="replace").rstrip())
            else:
                print("(%s)" % r.get("note", ""))
        elif out.exists() and out.stat().st_size:
            text = out.read_text(encoding="utf-8", errors="replace")
            if len(text) > args.max_chars:
                text = text[:args.max_chars] + "\n[... cut at %d characters; the whole review is %s]" % (
                    args.max_chars, out)
            print(text.rstrip())
        else:
            print("(no review: %s)" % (r.get("note") or r["status"]))
            err = tail_text(r["err"], 1500).strip()
            if err:
                print("stderr, last lines:")
                print("\n".join(err.splitlines()[-15:]))
        print()
    if not status.get("done"):
        print("(the run is still going: some reviews above are partial or missing)")
    return EXIT_OK if status.get("done") else EXIT_RUNNING


def cmd_stop(args) -> int:
    run_dir = Path(args.run).expanduser()
    status = load_status(run_dir)
    if args.names:
        for name in args.names:
            (run_dir / ("stop.%s" % name)).write_text("", encoding="utf-8")
    else:
        (run_dir / "stop").write_text("", encoding="utf-8")
    if not pid_alive(int(status.get("supervisor_pid") or 0)):
        for r in status["reviewers"]:
            if r["status"] == "running" and r.get("pid") and (not args.names or r["name"] in args.names):
                kill_tree(int(r["pid"]))
        return print_status(run_dir)
    deadline = time.time() + 20
    while time.time() < deadline:
        status = load_status(run_dir)
        busy = [r for r in status["reviewers"] if r["status"] == "running"
                and (not args.names or r["name"] in args.names)]
        if not busy:
            break
        time.sleep(0.5)
    return print_status(run_dir)


def cmd_retry(args) -> int:
    run_dir = Path(args.run).expanduser()
    status = load_status(run_dir)
    if not status.get("done"):
        raise CrossreviewError("the run is still going: wait for it (or stop it) before retrying")
    plan = _read_json(run_dir / "run.json", None)
    ended = {r["name"]: r for r in status["reviewers"]}
    names = {r["name"] for r in plan["reviewers"] if r["status"] not in ("host", "skipped")}
    unknown = [n for n in args.names if n not in names]
    if unknown:
        raise CrossreviewError("no CLI reviewer named %s (the names are: %s)" % (
            ", ".join(unknown), ", ".join(sorted(names))))
    for item in plan["reviewers"]:
        if item["name"] in args.names:
            for suffix in (".md", ".err", ".log"):
                path = Path(item["out"]).with_suffix(suffix)
                if path.exists() and path.stat().st_size:
                    n = 1
                    while path.with_suffix(".attempt%d%s" % (n, suffix)).exists():
                        n += 1
                    os.replace(str(path), str(path.with_suffix(".attempt%d%s" % (n, suffix))))
            for key in ("rc", "ended", "seconds", "bytes", "verdict", "note", "pid", "started"):
                item.pop(key, None)
            item["status"] = "queued"
            if args.timeout:
                item["timeout"] = args.timeout
        elif item["name"] in ended:
            item["status"] = ended[item["name"]]["status"]
    for name in args.names:
        stop = run_dir / ("stop.%s" % name)
        if stop.exists():
            stop.unlink()
    if (run_dir / "stop").exists():
        (run_dir / "stop").unlink()
    _write_json(run_dir / "run.json", plan)
    status["done"] = False
    status["updated"] = time.time()
    for r in status["reviewers"]:
        if r["name"] in args.names:
            r["status"] = "queued"
    _write_json(run_dir / "status.json", status)
    pid = spawn_supervisor(run_dir, int(plan.get("parallel") or 0))
    deadline = time.time() + 10
    while time.time() < deadline and _read_json(run_dir / "status.json", {}).get("supervisor_pid") != pid:
        time.sleep(0.1)
    print("retrying %s in %s (supervisor pid %d). Next: crossreview.py wait %s --max 240" % (
        ", ".join(args.names), run_dir, pid, run_dir))
    return EXIT_OK


def cmd_probe(args) -> int:
    entries, _, _, source = select_entries(args)
    run_dir = new_run_dir(args.run_dir)
    brief = run_dir / "brief.md"
    brief.write_text(PONG_BRIEF, encoding="utf-8")
    plan = prepare_run(entries, brief, run_dir, None, args.timeout, 1, source, None, args.only)
    print("probing %d reviewer(s) from %s with a one-word brief (timeout %s)..." % (
        sum(1 for r in plan["reviewers"] if r["status"] == "queued"), source, human_secs(args.timeout)))
    supervise(run_dir, 0)
    status = load_status(run_dir)
    bad = 0
    for r in status["reviewers"]:
        if r["status"] in ("host", "skipped"):
            print("  %-28s skipped (%s)" % (r["name"], r.get("note", "")))
            continue
        text = ""
        if Path(r["out"]).exists():
            text = Path(r["out"]).read_text(encoding="utf-8", errors="replace").strip()
        ok = r["status"] == "done" and "pong" in text.lower()
        bad += 0 if ok else 1
        detail = "answered in %s" % human_secs(r.get("seconds")) if ok else "%s: %s" % (
            r["status"], r.get("note") or text[:120] or "no output")
        print("  %-28s %s %s" % (r["name"], "ok  " if ok else "FAIL", detail))
    print("run directory: %s" % run_dir)
    return EXIT_OK if bad == 0 else EXIT_ERROR


# ------------------------------------------------------------------ main


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="crossreview.py", description=__doc__.split("\n\n")[0],
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = p.add_subparsers(dest="cmd", metavar="COMMAND")
    sub.required = True

    s = sub.add_parser("detect", help="list the installed reviewer CLIs")
    s.add_argument("agent", nargs="*", help="only these agents")
    s.add_argument("--json", action="store_true")
    s.set_defaults(func=cmd_detect)

    s = sub.add_parser("models", help="list the model ids an agent offers")
    s.add_argument("agent")
    s.add_argument("--binary", help="the binary to ask (default: the first one installed)")
    s.set_defaults(func=cmd_models)

    def host_arg(s):
        s.add_argument("--host", help="the agent you are: %s, or your own name (default: $CROSSREVIEW_HOST)"
                                      % ", ".join(HOSTS))

    def roster_args(s):
        host_arg(s)
        s.add_argument("--roster", help="roster file (default: $CROSSREVIEW_ROSTER, then your local roster of this "
                                        "project, then your global one)")
        s.add_argument("--scope", choices=SCOPES, help="only the local or only the global roster")
        s.add_argument("--workspace", help="the project (default: the current directory)")

    s = sub.add_parser("roster", help="show the roster and whether it can be used")
    roster_args(s)
    s.add_argument("--json", action="store_true")
    s.set_defaults(func=cmd_roster)

    s = sub.add_parser("init", help="write the roster")
    host_arg(s)
    s.add_argument("--scope", choices=SCOPES, help="local: your folder of this project; global: your home folder")
    s.add_argument("--workspace", help="the project (default: the current directory)")
    s.add_argument("reviewers", nargs="*", metavar="SPEC",
                   help="agent:model (coddy:codex/gpt-5.6-sol), or internal/<host>:<model>")
    s.add_argument("--import", dest="import_path", metavar="FILE", help="copy an existing roster (Coddy's) instead")
    s.add_argument("--refresh", action="store_true",
                   help="with --import: rewrite the CLI entries with the current templates")
    s.add_argument("--path", help="write exactly this file instead")
    s.add_argument("--min-reviewers", type=int, default=DEFAULT_MIN_REVIEWERS)
    s.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT, help="seconds per reviewer")
    s.add_argument("--add", action="store_true", help="append to an existing roster")
    s.add_argument("--force", action="store_true", help="replace an existing roster")
    s.set_defaults(func=cmd_init)

    s = sub.add_parser("trust", help="approve the project's roster (only after the user agreed)")
    host_arg(s)
    s.add_argument("--workspace")
    s.add_argument("--roster", help="the roster file inside the project (default: your local roster)")
    s.add_argument("--sha256", help="the digest `roster` printed: refuse when the file no longer has it")
    s.set_defaults(func=cmd_trust)

    s = sub.add_parser("brief", help="write a review brief")
    s.add_argument("--out", required=True, help="where to write the brief")
    s.add_argument("--workspace", help="repository (default: the current directory)")
    scope = s.add_mutually_exclusive_group()
    scope.add_argument("--base", help="the branch since its merge base with BASE, committed or not")
    scope.add_argument("--range", help="exactly `git diff RANGE` (A..B or A...B)")
    scope.add_argument("--staged", action="store_true", help="the staged changes only")
    scope.add_argument("--files", nargs="+", help="whole files instead of a diff")
    scope.add_argument("--doc", help="a document or plan instead of code")
    s.add_argument("--exclude", nargs="+", metavar="PATHSPEC", help="leave these paths out (lockfiles, generated code)")
    s.add_argument("--intent", help="what the change is meant to do, in a few sentences")
    s.add_argument("--intent-file", help="the same, read from a file")
    s.add_argument("--scope", help="describe the scope in your own words")
    s.add_argument("--notes-file", help="your own notes for the reviewers: facts about the code, decisions already "
                                         "taken. Never another reviewer's answer")
    s.add_argument("--tools", choices=sorted(TOOLS_LINE), default="none",
                   help="none: answer from the brief alone (default); read: reviewers may read the working directory")
    s.add_argument("--language", default="English", help="language of the reviews (default English)")
    s.add_argument("--unified", type=int, default=5, help="diff context lines (default 5)")
    s.add_argument("--no-untracked", action="store_true", help="leave untracked files out")
    s.add_argument("--max-file-bytes", type=int, default=200_000, help="largest untracked file to inline")
    s.set_defaults(func=cmd_brief)

    def run_args(s):
        roster_args(s)
        s.add_argument("--reviewer", action="append", metavar="SPEC",
                       help="one-off reviewer agent:model instead of the roster (repeatable)")
        s.add_argument("--only", nargs="+", metavar="NAME", help="run only these roster entries")
        s.add_argument("--run-dir", help="where the run lives (default: a new directory under the temp dir)")

    s = sub.add_parser("run", help="start the reviewers in the background")
    run_args(s)
    s.add_argument("--brief", required=True)
    s.add_argument("--cwd", help="reviewers' working directory (default: an empty directory of the run). "
                                 "Give a checkout only together with a `--tools read` brief")
    s.add_argument("--timeout", type=int, help="seconds per reviewer (default: the roster's, else 2700)")
    s.add_argument("--min-reviewers", type=int)
    s.add_argument("--parallel", type=int, default=0, help="at most N reviewers at once (default: all)")
    s.add_argument("--foreground", action="store_true", help="wait here instead of in the background")
    s.set_defaults(func=cmd_run)

    s = sub.add_parser("wait", help="wait for a run, then print its status")
    s.add_argument("run")
    s.add_argument("--max", type=float, default=240, help="seconds to wait at most (default 240); "
                                                          "keep it under your shell tool's timeout")
    s.set_defaults(func=cmd_wait)

    s = sub.add_parser("status", help="print a run's status")
    s.add_argument("run")
    s.add_argument("--json", action="store_true")
    s.set_defaults(func=cmd_status)

    s = sub.add_parser("collect", help="print every review of a run")
    s.add_argument("run")
    s.add_argument("--max-chars", type=int, default=60_000, help="cut a single review at this length")
    s.set_defaults(func=cmd_collect)

    s = sub.add_parser("stop", help="stop a run or some of its reviewers")
    s.add_argument("run")
    s.add_argument("names", nargs="*")
    s.set_defaults(func=cmd_stop)

    s = sub.add_parser("retry", help="run some reviewers of a finished run again (earlier output is kept aside)")
    s.add_argument("run")
    s.add_argument("names", nargs="+")
    s.add_argument("--timeout", type=int, help="new time limit in seconds")
    s.set_defaults(func=cmd_retry)

    s = sub.add_parser("probe", help="check that reviewers answer, with a one-word brief")
    run_args(s)
    s.add_argument("--timeout", type=int, default=180)
    s.set_defaults(func=cmd_probe)

    s = sub.add_parser("_supervise")
    s.add_argument("run")
    s.add_argument("--parallel", type=int, default=0)
    s.set_defaults(func=lambda a: supervise(Path(a.run), a.parallel) or EXIT_OK)
    return p


def main(argv: Optional[List[str]] = None) -> int:
    if hasattr(sys.stdout, "reconfigure"):
        try:
            sys.stdout.reconfigure(encoding="utf-8", errors="replace")
            sys.stderr.reconfigure(encoding="utf-8", errors="replace")
        except (AttributeError, ValueError):
            pass
    args = build_parser().parse_args(argv)
    try:
        return int(args.func(args) or 0)
    except CrossreviewError as exc:
        print("crossreview: %s" % exc, file=sys.stderr)
        return EXIT_ERROR
    except KeyboardInterrupt:
        return EXIT_ERROR
    except BrokenPipeError:
        try:
            sys.stdout = open(os.devnull, "w")
        except OSError:
            pass
        return EXIT_OK


if __name__ == "__main__":
    sys.exit(main())
