#!/usr/bin/env python3
"""Resume: a second launch pinned to the session replays the transcript."""

from __future__ import annotations

import sys
import tempfile
from pathlib import Path

from cli_tui_driver import CoddyTUI, ok

SESSION_ID = "cli-e2e-resume"


def main() -> int:
    home = Path(tempfile.mkdtemp(prefix="coddy-cli-resume-home-"))
    work = Path(tempfile.mkdtemp(prefix="coddy-cli-resume-work-"))

    first = CoddyTUI("resume-1", home=str(home), workdir=str(work), extra_args=["--session-id", SESSION_ID])
    try:
        first.wait_for("coddy v", timeout=30)
        first.prompt("Reply with exactly: RESUME_E2E_MARKER")
        first.wait_idle(timeout=240)
        joined = "\n".join(m.get("content", "") for m in first.messages())
        if "RESUME_E2E_MARKER" not in joined:
            raise AssertionError("first session did not persist the marker")
    finally:
        first.close(keep_dirs=True)

    second = CoddyTUI("resume-2", home=str(home), workdir=str(work), extra_args=["--session-id", SESSION_ID])
    try:
        second.wait_for("coddy v", timeout=30)
        second.wait_for("RESUME_E2E_MARKER", timeout=60)  # replayed transcript
    finally:
        second.close(keep_dirs=True)

    # The interactive --resume picker must land on the same transcript.
    from cli_tui_driver import CR

    third = CoddyTUI("resume-3", home=str(home), workdir=str(work), extra_args=["--resume"])
    try:
        third.wait_for("Resume Session", timeout=30)
        third.send(CR)  # first (most recent) row
        third.wait_for("RESUME_E2E_MARKER", timeout=60)
        return ok("cli_e2e_resume")
    finally:
        third.close()


if __name__ == "__main__":
    sys.exit(main())
