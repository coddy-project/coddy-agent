#!/usr/bin/env python3
"""Capture the console connecting its MCP servers after the first frame.

Two stdio servers from mcp_stub_server.py are configured: one answers at
once, one never answers. The console draws before either has, the footer
counts them (`MCP 1/2`), and a prompt sent while the second is still in its
handshake opens on `Connecting MCP servers`. Two PNGs (and the HTML and text
grids beside them) go to the assets folder of the console page:

  21-mcp-connecting.png       the first frame with the footer count
  22-mcp-connecting-turn.png  a turn waiting for the tool list

Usage: python3 capture_mcp.py [repo] [outdir]   (default docs/assets/cli-tui)
Needs a cli-tagged build/coddy (make build TAGS=cli), pexpect, pyte and
chromium or google-chrome for the PNG. No model is contacted: the prompt is
still waiting for the servers when the capture is taken.
"""

import os
import shutil
import sys
import tempfile
import time
from pathlib import Path

REPO = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path(__file__).resolve().parents[2]
OUT = Path(sys.argv[2]).resolve() if len(sys.argv) > 2 else REPO / "docs" / "assets" / "cli-tui"
sys.path.insert(0, str(REPO / "examples" / "cli"))
os.environ.setdefault("CODDY_BIN", str(REPO / "build" / "coddy"))
# A short terminal, so the status line and the footer fit the rendered frame
# under the header the first frame draws.
os.environ.setdefault("CLI_E2E_ROWS", "24")
import capture  # noqa: E402
from cli_tui_driver import CR, CoddyTUI  # noqa: E402

STUB = str(REPO / "examples" / "cli" / "mcp_stub_server.py")
MODEL = "rpa/qwen3.6-35b-a3b"  # the demo provider row that reports no account usage


SHOTS = ["21-mcp-connecting", "22-mcp-connecting-turn"]


def main() -> int:
    OUT.mkdir(parents=True, exist_ok=True)
    servers = [
        {"name": "filesystem", "type": "stdio", "command": sys.executable, "args": [STUB, "--name", "filesystem"]},
        {"name": "github", "type": "stdio", "command": sys.executable, "args": [STUB, "--name", "github", "--hang"]},
    ]
    # The grids are rendered in a folder of their own: render_pngs draws every
    # HTML file it finds, and the assets folder holds the other captures.
    work = Path(tempfile.mkdtemp(prefix="coddy-capture-mcp-"))
    tui = CoddyTUI("capture-mcp", model=MODEL, mcp_servers=servers)
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.wait_for("MCP 1/2", timeout=10)
        tui.pump(0.3)
        capture.snapshot(tui, work, SHOTS[0])

        tui.type_text("list the open pull requests of this repository")
        tui.send(CR)
        tui.wait_for("Connecting MCP servers", timeout=10)
        tui.pump(1.2)
        capture.snapshot(tui, work, SHOTS[1])
        time.sleep(0.1)
    finally:
        tui.close()
    capture.render_pngs(work)
    for name in SHOTS:
        png = work / f"{name}.png"
        if not png.exists():
            raise SystemExit(f"no PNG for {name}: is chromium or google-chrome installed?")
        shutil.copy2(png, OUT / png.name)
    shutil.rmtree(work, ignore_errors=True)
    print(f"captured {', '.join(SHOTS)} into {OUT}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
