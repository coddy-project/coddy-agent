#!/usr/bin/env python3
"""Scheduler E2E over coddy HTTP (OpenAI-compatible API).

Build: go build -tags=http,scheduler ./cmd/coddy
Uses shared examples/config.demo.yaml (logger.file __E2E_LOG_PATH__ substituted at runtime).
HTTP listen port defaults to httpserver.port in that file unless --port overrides.
Environment: CODDY_BIN (optional), MODEL (optional override of agent.model in demo config).
"""

from __future__ import annotations

import argparse
import os
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "shared"))
import scheduler_e2e_common as sce


def main() -> int:
    demo_port = sce.httpserver_port_from_demo()
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument(
        "--port",
        type=int,
        default=demo_port,
        help=f"HTTP listen port (default {demo_port} from config.demo.yaml)",
    )
    args = ap.parse_args()

    bin_path = Path(os.environ.get("CODDY_BIN", sce.default_coddy_bin())).expanduser()
    bad = sce.validate_coddy_scheduler_bin(bin_path, need_http_help=True)
    if bad is not None:
        return bad

    with tempfile.TemporaryDirectory(prefix="coddy-http-scheduler-e2e-") as tmp:
        work = Path(tmp) / "work"
        work.mkdir(parents=True, exist_ok=True)
        home = Path(tmp) / "coddy_home"
        home.mkdir(parents=True, exist_ok=True)
        cfg_path = Path(tmp) / "config.yaml"
        demo_raw = sce.load_e2e_config(work)
        if args.port != demo_port:
            demo_raw = demo_raw.replace(f"port: {demo_port}", f"port: {args.port}", 1)
        cfg_path.write_text(demo_raw, encoding="utf-8")
        glo = work / "coddy-e2e-global.log"
        glo.write_text("", encoding="utf-8")

        return sce.run_http(bin_path, cfg_path, home, work, args.port)


if __name__ == "__main__":
    raise SystemExit(main())
