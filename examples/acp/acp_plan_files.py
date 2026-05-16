#!/usr/bin/env python3
"""Deprecated demo wrapper; use ``acp_e2e_plan_files.py`` for the full e2e harness."""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

if __name__ == "__main__":
    target = Path(__file__).resolve().parent / "acp_e2e_plan_files.py"
    raise SystemExit(subprocess.call([sys.executable, str(target)]))
