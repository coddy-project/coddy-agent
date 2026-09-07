#!/usr/bin/env python3
"""Print mode: -p runs one prompt without a terminal; -c -p continues it."""

from __future__ import annotations

import subprocess
import sys
import tempfile
from pathlib import Path

from cli_tui_driver import DEFAULT_MODEL, coddy_bin, prepare_home


def run_print(home: Path, work: Path, args: list[str]) -> subprocess.CompletedProcess:
    import os

    env = dict(os.environ)
    env["CODDY_HOME"] = str(home)
    return subprocess.run(
        [coddy_bin(), *args],
        cwd=str(work),
        env=env,
        capture_output=True,
        text=True,
        timeout=300,
    )


def main() -> int:
    # Config and key come from the shared scaffold; the coddy process itself
    # runs without a pty (that is the point of print mode).
    home, work = prepare_home("print")

    first = run_print(
        home,
        work,
        ["-p", "Reply with exactly: PRINT_E2E_OK and remember the token PRINT_MARBLE", "--model", DEFAULT_MODEL],
    )
    if first.returncode != 0:
        sys.stderr.write(first.stderr)
        raise AssertionError(f"print run failed with {first.returncode}")
    if "PRINT_E2E_OK" not in first.stdout:
        raise AssertionError(f"stdout lacks the token:\n{first.stdout!r}")

    sessions = [p for p in (home / "sessions").iterdir() if p.is_dir()]
    if len(sessions) != 1:
        raise AssertionError(f"expected one persisted session, found {len(sessions)}")

    second = run_print(
        home,
        work,
        ["-c", "-p", "Which token did I ask you to remember? Reply with the token only.", "--model", DEFAULT_MODEL],
    )
    if second.returncode != 0:
        sys.stderr.write(second.stderr)
        raise AssertionError(f"continue print run failed with {second.returncode}")
    if "PRINT_MARBLE" not in second.stdout:
        raise AssertionError(f"continued run lost the session context:\n{second.stdout!r}")

    sessions = [p for p in (home / "sessions").iterdir() if p.is_dir()]
    if len(sessions) != 1:
        raise AssertionError("-c -p must reuse the session, not create a second one")

    import shutil

    shutil.rmtree(home, ignore_errors=True)
    shutil.rmtree(work, ignore_errors=True)
    print("OK cli_e2e_print")
    return 0


if __name__ == "__main__":
    sys.exit(main())
