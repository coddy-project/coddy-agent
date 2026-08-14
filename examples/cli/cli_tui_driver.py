#!/usr/bin/env python3
"""Shared driver for the coddy console TUI e2e scripts.

Spawns `coddy cli --plain --theme dark` in a pty (pexpect), feeds the output
through a pyte terminal emulator, and exposes wait/send/assert helpers.
Assertions favor on-disk session artifacts; screen greps are limited to
deterministic chrome (headers, spinner label, fixture tokens).

Linux-only (pty). Requires: pexpect, pyte (examples/cli/requirements.txt).
"""

from __future__ import annotations

import json
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path

try:
    import pexpect
    import pyte
except ImportError as exc:  # pragma: no cover - guarded by the runner
    raise SystemExit(
        f"missing python dependency: {exc}. Install with: "
        "python3 -m pip install -r examples/cli/requirements.txt"
    )

REPO_ROOT = Path(__file__).resolve().parents[2]
COLS = int(os.environ.get("CLI_E2E_COLS", "100"))
ROWS = int(os.environ.get("CLI_E2E_ROWS", "35"))
DEFAULT_MODEL = os.environ.get("MODEL", "neuraldeep/qwen3.8-27b")

ESC = "\x1b"
CR = "\r"
CTRL_C = "\x03"
CTRL_D = "\x04"
CTRL_L = "\x0c"
CTRL_O = "\x0f"
CTRL_T = "\x14"


def coddy_bin() -> str:
    env = os.environ.get("CODDY_BIN")
    if env:
        return env
    candidate = REPO_ROOT / "build" / "coddy"
    if candidate.exists():
        return str(candidate)
    found = shutil.which("coddy")
    if found:
        return found
    raise SystemExit("no coddy binary: build with `make build TAGS=\"cli scheduler memory\"` or set CODDY_BIN")


def require_cli_build(binary: str) -> None:
    """Fail fast when the binary lacks the cli tag."""
    out = subprocess.run([binary, "cli", "--help"], capture_output=True, text=True, timeout=30)
    combined = (out.stdout or "") + (out.stderr or "")
    if "not built in" in combined:
        raise SystemExit("coddy binary lacks the cli tag: rebuild with make build TAGS=\"cli scheduler memory\"")


class CoddyTUI:
    """One interactive coddy console session in an emulated terminal."""

    def __init__(
        self,
        name: str,
        model: str = DEFAULT_MODEL,
        extra_args: list[str] | None = None,
        home: str | None = None,
        workdir: str | None = None,
        permission_mode: str | None = None,
        env_extra: dict[str, str] | None = None,
    ) -> None:
        self.name = name
        self.model = model
        self.home = Path(home) if home else Path(tempfile.mkdtemp(prefix=f"coddy-cli-{name}-home-"))
        self.workdir = Path(workdir) if workdir else Path(tempfile.mkdtemp(prefix=f"coddy-cli-{name}-work-"))
        self.home.mkdir(parents=True, exist_ok=True)
        self.workdir.mkdir(parents=True, exist_ok=True)
        self._render_config()
        self._seed_env_file()

        self.screen = pyte.Screen(COLS, ROWS)
        self.stream = pyte.ByteStream(self.screen)

        env = dict(os.environ)
        env.update(
            {
                "TERM": "xterm-256color",
                "COLORTERM": "truecolor",
                "CODDY_HOME": str(self.home),
                "LANG": "en_US.UTF-8",
                "LC_ALL": "en_US.UTF-8",
            }
        )
        if env_extra:
            env.update(env_extra)
        args = ["cli", "--plain", "--theme", "dark", "--model", model]
        if permission_mode:
            args += ["--permission-mode", permission_mode]
        if extra_args:
            args += extra_args
        binary = coddy_bin()
        require_cli_build(binary)
        self.child = pexpect.spawn(
            binary,
            args,
            env=env,
            cwd=str(self.workdir),
            dimensions=(ROWS, COLS),
            encoding=None,
            timeout=5,
        )

    def _render_config(self) -> None:
        template = (REPO_ROOT / "examples" / "config.demo.yaml").read_text()
        resolved = template.replace("__E2E_LOG_PATH__", str(self.home / "e2e.log"))
        resolved = resolved.replace(
            'model: "rpa/gpt-oss:120b"\n  max_turns', f'model: "{self.model}"\n  max_turns'
        )
        resolved = resolved.replace(
            'memory:\n  enabled: true\n  model: "rpa/gpt-oss:120b"',
            f'memory:\n  enabled: true\n  model: "{self.model}"',
        )
        (self.home / "config.yaml").write_text(resolved)
        (self.home / "sessions").mkdir(exist_ok=True)
        (self.home / "skills_fixture").mkdir(exist_ok=True)

    def _seed_env_file(self) -> None:
        """Copy exactly the NEURALDEEP_API_KEY line into the temp home .env."""
        if os.environ.get("NEURALDEEP_API_KEY"):
            (self.home / ".env").write_text(
                f"NEURALDEEP_API_KEY={os.environ['NEURALDEEP_API_KEY']}\n"
            )
            return
        global_env = Path.home() / ".coddy" / ".env"
        if global_env.exists():
            for line in global_env.read_text().splitlines():
                if line.startswith("NEURALDEEP_API_KEY="):
                    (self.home / ".env").write_text(line + "\n")
                    return

    # --- pty pumping ---

    def pump(self, seconds: float) -> None:
        deadline = time.time() + seconds
        while time.time() < deadline:
            try:
                data = self.child.read_nonblocking(size=65536, timeout=0.1)
                if data:
                    self.stream.feed(data)
            except pexpect.TIMEOUT:
                continue
            except pexpect.EOF:
                break

    def text(self) -> str:
        return "\n".join(self.screen.display)

    def wait_for(self, needle: str, timeout: float = 120.0) -> None:
        deadline = time.time() + timeout
        while time.time() < deadline:
            self.pump(0.3)
            if needle in self.text():
                return
        self.dump(f"wait_for({needle!r}) timed out")
        raise AssertionError(f"screen never showed {needle!r}")

    def wait_gone(self, needle: str, timeout: float = 180.0) -> None:
        deadline = time.time() + timeout
        while time.time() < deadline:
            self.pump(0.3)
            if needle not in self.text():
                return
        self.dump(f"wait_gone({needle!r}) timed out")
        raise AssertionError(f"screen still shows {needle!r}")

    def wait_idle(self, timeout: float = 240.0) -> None:
        """Wait until the working spinner label disappears."""
        self.pump(1.0)
        self.wait_gone("Working...", timeout=timeout)

    def send(self, data: str) -> None:
        self.child.send(data.encode())

    def type_text(self, text: str, per_char_delay: float = 0.01) -> None:
        for ch in text:
            self.child.send(ch.encode())
            self.pump(per_char_delay)

    def prompt(self, text: str) -> None:
        self.type_text(text)
        self.send(CR)
        self.pump(0.5)

    def dump(self, reason: str) -> None:
        sys.stderr.write(f"--- screen dump ({self.name}: {reason}) ---\n")
        sys.stderr.write(self.text() + "\n")
        sys.stderr.write("--- end dump ---\n")

    # --- session artifacts ---

    def sessions_root(self) -> Path:
        return self.home / "sessions"

    def session_dirs(self) -> list[Path]:
        root = self.sessions_root()
        if not root.exists():
            return []
        return sorted([p for p in root.iterdir() if p.is_dir()])

    def single_session_dir(self) -> Path:
        dirs = self.session_dirs()
        if len(dirs) != 1:
            raise AssertionError(f"expected one session dir, found {[d.name for d in dirs]}")
        return dirs[0]

    def wait_session_file(self, relative: str, timeout: float = 240.0) -> Path:
        deadline = time.time() + timeout
        while time.time() < deadline:
            self.pump(0.3)
            for d in self.session_dirs():
                p = d / relative
                if p.exists():
                    return p
            time.sleep(0.2)
        raise AssertionError(f"no session produced {relative}")

    def messages(self) -> list[dict]:
        path = self.single_session_dir() / "messages.json"
        if not path.exists():
            return []
        data = json.loads(path.read_text())
        if isinstance(data, dict):
            return data.get("messages", [])
        return data

    def tool_call_names(self) -> set[str]:
        names: set[str] = set()
        for d in self.session_dirs():
            tc = d / "tool_calls"
            if not tc.exists():
                continue
            for call in tc.iterdir():
                meta = call / "meta.json"
                if meta.exists():
                    try:
                        names.add(json.loads(meta.read_text()).get("name", ""))
                    except json.JSONDecodeError:
                        continue
        return names

    def wait_tool_call(self, tool_name: str, timeout: float = 240.0) -> None:
        deadline = time.time() + timeout
        while time.time() < deadline:
            self.pump(0.3)
            if tool_name in self.tool_call_names():
                return
            time.sleep(0.2)
        self.dump(f"tool {tool_name} never persisted")
        raise AssertionError(f"tool call {tool_name} not found; saw {self.tool_call_names()}")

    # --- lifecycle ---

    def quit(self) -> None:
        try:
            self.send(CTRL_C)
            self.pump(0.2)
            self.send(CTRL_C)
            self.pump(0.5)
            self.child.expect(pexpect.EOF, timeout=10)
        except Exception:
            pass
        finally:
            try:
                # pexpect makes the child a session leader; killing its whole
                # process group reaps shell children (background sleeps etc.).
                os.killpg(self.child.pid, signal.SIGKILL)
            except Exception:
                pass
            try:
                self.child.terminate(force=True)
            except Exception:
                pass

    def close(self, keep_dirs: bool = False) -> None:
        self.quit()
        if not keep_dirs and os.environ.get("CLI_E2E_KEEP", "") != "1":
            shutil.rmtree(self.home, ignore_errors=True)
            shutil.rmtree(self.workdir, ignore_errors=True)


def install_skill_fixture(home: Path) -> None:
    """Copy the shared demo skill into the temp home skills dir."""
    src = REPO_ROOT / "examples" / "skills_fixture" / "coddy_slash_demo"
    dst = home / "skills_fixture" / "coddy_slash_demo"
    if src.exists():
        shutil.copytree(src, dst, dirs_exist_ok=True)


def install_rules_fixture(workdir: Path) -> None:
    src = REPO_ROOT / "examples" / "rules_fixture" / ".coddy"
    dst = workdir / ".coddy"
    if src.exists():
        shutil.copytree(src, dst, dirs_exist_ok=True)


def ok(name: str) -> int:
    print(f"OK {name}")
    return 0
