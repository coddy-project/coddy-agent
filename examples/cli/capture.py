#!/usr/bin/env python3
"""Capture styled screenshots of the coddy console TUI.

Drives `coddy cli` through the shared e2e driver, dumps each state as
plain text plus a styled HTML grid (exact cell colors from the pyte
buffer), and renders PNGs via headless chromium when available.

Usage: python3 capture.py [outdir]   (default docs/assets/cli-tui)
Needs a reachable model (same env contract as test_cli.sh).
"""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
from pathlib import Path

from cli_tui_driver import CR, CTRL_L, COLS, ROWS, REPO_ROOT, CoddyTUI

DEFAULT_BG = "#121212"  # coddy dark --bg
DEFAULT_FG = "#d4d4d4"

NAMED = {
    "black": "#000000", "red": "#cd0000", "green": "#00cd00", "brown": "#cdcd00",
    "blue": "#0000ee", "magenta": "#cd00cd", "cyan": "#00cdcd", "white": "#e5e5e5",
    "brightblack": "#7f7f7f", "brightred": "#ff0000", "brightgreen": "#00ff00",
    "brightbrown": "#ffff00", "brightblue": "#5c5cff", "brightmagenta": "#ff00ff",
    "brightcyan": "#00ffff", "brightwhite": "#ffffff",
}


def css_color(value: object, default: str, light: bool) -> str:
    if value in (None, "default"):
        return default
    if value in NAMED:
        return NAMED[str(value)]
    v = str(value)
    if len(v) == 6:
        try:
            int(v, 16)
            return "#" + v
        except ValueError:
            pass
    return default


def snapshot(tui: CoddyTUI, outdir: Path, name: str, light: bool = False) -> None:
    bg = "#f8f8fa" if light else DEFAULT_BG
    fg = "#18181b" if light else DEFAULT_FG
    (outdir / f"{name}.txt").write_text("\n".join(l.rstrip() for l in tui.text().splitlines()) + "\n")
    rows_html = []
    for y in range(ROWS):
        row = tui.screen.buffer[y]
        parts = []
        run_style, run_chars = None, []

        def flush() -> None:
            if not run_chars:
                return
            cfg, cbg, bold, italic, under, strike = run_style
            esc = "".join(run_chars).replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
            css = f"color:{cfg};background:{cbg};"
            if bold:
                css += "font-weight:bold;"
            if italic:
                css += "font-style:italic;"
            deco = []
            if under:
                deco.append("underline")
            if strike:
                deco.append("line-through")
            if deco:
                css += f"text-decoration:{' '.join(deco)};"
            parts.append(f'<span style="{css}">{esc}</span>')

        for x in range(COLS):
            ch = row[x]
            cfg = css_color(ch.fg, fg, light)
            cbg = css_color(ch.bg, bg, light)
            if ch.reverse:
                cfg, cbg = cbg, cfg
            style = (cfg, cbg, ch.bold, ch.italics, ch.underscore, ch.strikethrough)
            if style != run_style and run_chars:
                flush()
                run_chars = []
            run_style = style
            run_chars.append(ch.data or " ")
        flush()
        rows_html.append("".join(parts))
    body = "\n".join(f"<div>{r}</div>" for r in rows_html)
    html = (
        "<!DOCTYPE html><html><head><meta charset=\"utf-8\"><style>"
        f"body {{ margin:0; background:{bg}; }}"
        "pre { margin:0; padding:14px; font-family:'JetBrains Mono','DejaVu Sans Mono',monospace;"
        " font-size:14px; line-height:1.35; white-space:pre; }"
        "pre div { height:1.35em; } pre span { padding:0.18em 0; }"
        f"</style></head><body><pre>{body}</pre></body></html>\n"
    )
    (outdir / f"{name}.html").write_text(html)
    print(f"[snap] {name}")


def render_pngs(outdir: Path) -> None:
    chromium = shutil.which("chromium") or shutil.which("google-chrome") or shutil.which("chromium-browser")
    if not chromium:
        print("no chromium found; HTML captures only")
        return
    for html in sorted(outdir.glob("*.html")):
        png = html.with_suffix(".png")
        subprocess.run(
            [
                chromium, "--headless=new", "--disable-gpu", "--hide-scrollbars",
                "--force-device-scale-factor=2", "--window-size=930,720",
                f"--screenshot={png}", f"file://{html}",
            ],
            capture_output=True,
            timeout=60,
        )
        print(f"[png] {png.name}")


def main() -> int:
    outdir = Path(sys.argv[1]) if len(sys.argv) > 1 else REPO_ROOT / "docs" / "assets" / "cli-tui"
    outdir.mkdir(parents=True, exist_ok=True)

    tui = CoddyTUI("capture")
    try:
        (tui.workdir / "demo.py").write_text(
            '"""Demo module."""\n\n\ndef greet(name: str) -> str:\n    return f"Hello, {name}!"\n'
        )
        tui.wait_for("coddy v", timeout=30)
        tui.pump(1.0)
        snapshot(tui, outdir, "01-startup")

        tui.type_text("/")
        tui.pump(0.8)
        snapshot(tui, outdir, "02-slash-menu")
        tui.send("\x1b")
        tui.send("\x03")
        tui.pump(0.3)

        tui.send(CTRL_L)
        tui.pump(0.8)
        snapshot(tui, outdir, "03-model-selector")
        tui.send("\x1b")
        tui.pump(0.3)

        tui.prompt("What is 17*23? Explain briefly, then answer.")
        tui.wait_turn_started(timeout=30)
        tui.pump(1.0)
        snapshot(tui, outdir, "04-working")
        tui.wait_idle(timeout=240)
        snapshot(tui, outdir, "05-chat-response")

        tui.prompt("Use the read tool to read demo.py, then say what it defines.")
        tui.wait_tool_call("read", timeout=240)
        tui.wait_idle(timeout=240)
        snapshot(tui, outdir, "06-tool-read")
    finally:
        tui.close()

    light = CoddyTUI("capture-light", extra_args=["--theme", "light"])
    try:
        light.wait_for("coddy v", timeout=30)
        light.pump(1.0)
        snapshot(light, outdir, "07-startup-light", light=True)
    finally:
        light.close()
    return 0


if __name__ == "__main__":
    rc = main()
    outdir = Path(sys.argv[1]) if len(sys.argv) > 1 else REPO_ROOT / "docs" / "assets" / "cli-tui"
    render_pngs(outdir)
    sys.exit(rc)
