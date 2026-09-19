#!/usr/bin/env python3
"""Capture the console's help: the built-in documentation on F1.

Starts build/coddy cli in a pty, presses F1, searches for a section and opens
it, and renders two PNGs: the search list with the snippet of the selected
section, and the page opened at that section. The documentation is read out
of the binary, so the shots need no model: the configured provider is never
called.

Usage: python3 capture_docs.py [repo] [outdir]   (default docs/assets/cli-tui)
Needs a cli-tagged build/coddy (make build TAGS=cli), pexpect, pyte and
chromium for the PNG.
"""
import os, sys, tempfile, time
from pathlib import Path

REPO = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path(__file__).resolve().parents[2]
OUT = Path(sys.argv[2]).resolve() if len(sys.argv) > 2 else REPO / "docs" / "assets" / "cli-tui"
sys.path.insert(0, str(REPO / "examples" / "cli"))
os.environ.setdefault("CODDY_BIN", str(REPO / "build" / "coddy"))
import pexpect, pyte  # noqa: E402
import capture  # noqa: E402
from cli_tui_driver import COLS, ROWS  # noqa: E402

F1 = "\x1bOP"
ENTER = "\r"

home = Path(tempfile.mkdtemp(prefix="coddy-docs-shot-home-"))
work = Path(tempfile.mkdtemp(prefix="coddy-agent-"))
(home / "config.yaml").write_text("""providers:
  - name: stub
    type: openai
    api_base: "http://127.0.0.1:9/v1"
    api_key: "sk-capture-stub"
models:
  - model: stub/coddy-demo
    max_context_tokens: 131072
    reasoning_levels: []
agent:
  model: stub/coddy-demo
memory:
  enable: false
""")


class Shot:
    """A coddy console on a pyte screen."""

    def __init__(self):
        self.screen = pyte.Screen(COLS, ROWS)
        self.stream = pyte.ByteStream(self.screen)
        env = dict(os.environ)
        env.update({"TERM": "xterm-256color", "COLORTERM": "truecolor", "CODDY_HOME": str(home),
                    "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8"})
        self.child = pexpect.spawn(os.environ["CODDY_BIN"], ["cli", "--theme", "dark"], env=env,
                                   cwd=str(work), dimensions=(ROWS, COLS), encoding=None, timeout=5)

    def pump(self, seconds):
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

    def text(self):
        return "\n".join(self.screen.display)

    def wait_for(self, needle, timeout=20):
        deadline = time.time() + timeout
        while time.time() < deadline:
            self.pump(0.3)
            if needle in self.text():
                return
        raise AssertionError(f"never saw {needle!r}:\n{self.text()}")

    def send(self, text):
        self.child.send(text.encode())


def render_pngs(outdir, names):
    """One PNG per capture through headless chromium, cropped to the last painted row."""
    import shutil, subprocess
    chromium = shutil.which("chromium") or shutil.which("google-chrome") or shutil.which("chromium-browser")
    if not chromium:
        print("no chromium found; HTML captures only")
        return
    for name in names:
        html, png = outdir / f"{name}.html", outdir / f"{name}.png"
        subprocess.run([chromium, "--headless=new", "--disable-gpu", "--hide-scrollbars",
                        "--force-device-scale-factor=2", "--window-size=930,1320",
                        f"--screenshot={png}", f"file://{html}"], capture_output=True, timeout=60)
        try:
            from PIL import Image
        except ImportError:
            print(f"[png] {png.name} (uncropped)")
            continue
        im = Image.open(png).convert("RGB")
        w, h = im.size
        bg, px, last = im.getpixel((5, 5)), im.load(), 0
        for y in range(h):
            if any(px[x, y] != bg for x in range(0, w, 4)):
                last = y
        im.crop((0, 0, w, min(h, last + 60))).save(png, optimize=True)
        print(f"[png] {png.name}")


NAMES = ["19-docs-search", "20-docs-page"]
OUT.mkdir(parents=True, exist_ok=True)
tui = Shot()
try:
    tui.wait_for("coddy v", timeout=30)
    tui.pump(1.0)
    tui.send(F1)
    tui.wait_for("Coddy docs", timeout=10)
    tui.wait_for("Quickstart", timeout=10)
    for ch in "telegram proxy":
        tui.send(ch)
        time.sleep(0.03)
    tui.wait_for("Telegram gateway › Proxy", timeout=10)
    tui.pump(0.5)
    capture.snapshot(tui, OUT, NAMES[0])
    print("help search captured")

    tui.send(ENTER)
    tui.wait_for("Coddy docs › Telegram gateway", timeout=10)
    tui.pump(0.5)
    for row in ("page ", "n/p next/previous page"):
        if row not in tui.text():
            raise AssertionError(f"{row!r} is not on the page view:\n{tui.text()}")
    capture.snapshot(tui, OUT, NAMES[1])
    print("help page captured")
finally:
    tui.send("\x1b"); tui.send("\x1b")
    tui.send("\x03"); time.sleep(0.2); tui.send("\x03")
    tui.pump(1)
render_pngs(OUT, NAMES)
for name in NAMES:
    for ext in (".txt", ".html"):
        (OUT / f"{name}{ext}").unlink(missing_ok=True)
