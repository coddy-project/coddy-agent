#!/usr/bin/env python3
"""ACP e2e: model drives scheduler tools, on-disk job markdown, and daemon runs a tick.

Build: go build -tags=scheduler ./cmd/coddy
Uses shared examples/config.demo.yaml (logger.file __E2E_LOG_PATH__ substituted at runtime).
Environment: CODDY_BIN (optional).
"""

from __future__ import annotations

import os
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "shared"))
import scheduler_e2e_common as sce


def main() -> int:
    bin_path = Path(os.environ.get("CODDY_BIN", sce.default_coddy_bin())).expanduser()
    bad = sce.validate_coddy_scheduler_bin(bin_path, need_http_help=False)
    if bad is not None:
        return bad

    with tempfile.TemporaryDirectory(prefix="coddy-acp-scheduler-e2e-") as tmp:
        work = Path(tmp) / "work"
        work.mkdir(parents=True, exist_ok=True)
        home = Path(tmp) / "coddy_home"
        home.mkdir(parents=True, exist_ok=True)
        cfg_path = Path(tmp) / "config.yaml"
        cfg_path.write_text(sce.load_e2e_config(work), encoding="utf-8")
        glo = work / "coddy-e2e-global.log"
        glo.write_text("", encoding="utf-8")

        return sce.run_acp(bin_path, cfg_path, home, work)


if __name__ == "__main__":
    raise SystemExit(main())
