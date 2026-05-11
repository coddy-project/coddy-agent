#!/usr/bin/env python3
"""HTTP e2e: model uses scheduler tools, on-disk jobs, and daemon runs a tick.

Requires a **running** ``coddy http`` (same port as ``BASE_URL``) with ``--scheduler-enabled``,
and the same ``CODDY_HOME`` and ``WORK_DIR`` the server was started with.

Steps: chat instructs run_command marker, ``coddy_scheduler_job_create``, then the harness waits
for cron or fallback job file, scheduler run produces ``SCHEDULER_RUN_RESULT.txt``, log lines, and
``.state`` for the job.

Environment: ``BASE_URL`` (must end with ``/v1``), ``CODDY_HOME``, ``WORK_DIR``, ``MODEL`` (optional).
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "shared"))
import scheduler_e2e_common as sce


def main() -> int:
    base = os.environ.get("BASE_URL", "").strip()
    if not base:
        print("BASE_URL required (e.g. http://127.0.0.1:19876/v1)", file=sys.stderr)
        return 1
    home_s = os.environ.get("CODDY_HOME", "").strip()
    work_s = os.environ.get("WORK_DIR", "").strip()
    if not home_s or not work_s:
        print("CODDY_HOME and WORK_DIR must be set to the server's home and cwd", file=sys.stderr)
        return 1
    home = Path(home_s).expanduser().resolve()
    work = Path(work_s).expanduser().resolve()
    if not home.is_dir() or not work.is_dir():
        print("CODDY_HOME and WORK_DIR must be existing directories", file=sys.stderr)
        return 1

    return sce.run_http_scheduler_agent_e2e_existing(base, home, work)


if __name__ == "__main__":
    raise SystemExit(main())
