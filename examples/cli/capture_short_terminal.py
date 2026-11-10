#!/usr/bin/env python3
"""Capture a console selector in a terminal too short for it.

Starts build/coddy cli in a nine-row pty against a stub provider that is never
asked anything, opens the ctrl+L model selector, walks the highlight down past
the window and renders what the terminal shows: the title, the highlighted row,
the help line and the footer, with the list scrolled rather than the modal.

Usage: python3 capture_short_terminal.py [outdir]   (default docs/assets/cli-tui)
Needs a cli-tagged build/coddy (make build TAGS=cli), pexpect, pyte and
chromium for the PNG. CODDY_BIN names another binary, CLI_E2E_ROWS another
height, which is how the same script captures the state before a fix.
"""
import json, os, shutil, subprocess, sys, tempfile, threading, time
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
OUT = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else REPO / "docs" / "assets" / "cli-tui"
sys.path.insert(0, str(REPO / "examples" / "cli"))
os.environ.setdefault("CODDY_BIN", str(REPO / "build" / "coddy"))
# A phone-sized terminal: the selector has to give up rows, not scroll itself.
os.environ.setdefault("CLI_E2E_ROWS", "9")
os.environ.setdefault("CLI_E2E_COLS", "72")
import pexpect, pyte  # noqa: E402
import capture  # noqa: E402
from cli_tui_driver import COLS, ROWS  # noqa: E402

NAME = os.environ.get("CAPTURE_NAME", "23-short-terminal-selector")
MODELS = ["coddy-demo", "coddy-demo-mini", "coddy-demo-pro", "coddy-demo-nano",
          "coddy-demo-flash", "coddy-demo-air", "coddy-demo-max", "coddy-demo-lite"]


class H(BaseHTTPRequestHandler):
    """A provider that lists models and is never sent a prompt."""

    def do_GET(self):
        body = json.dumps({"object": "list", "data": [
            {"id": m, "object": "model", "owned_by": "stub"} for m in MODELS]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *a):
        pass


srv = HTTPServer(("127.0.0.1", 0), H)
threading.Thread(target=srv.serve_forever, daemon=True).start()

home = Path(tempfile.mkdtemp(prefix="coddy-short-term-home-"))
work = Path(tempfile.mkdtemp(prefix="coddy-agent-"))
(home / "config.yaml").write_text(
    "providers:\n"
    "  - name: stub\n"
    "    type: openai\n"
    f'    api_base: "http://127.0.0.1:{srv.server_port}/v1"\n'
    '    api_key: "sk-capture-stub"\n'
    "models:\n"
    + "".join(f"  - model: stub/{m}\n    max_context_tokens: 131072\n" for m in MODELS)
    + f"agent:\n  model: stub/{MODELS[0]}\n"
)


class Shot:
    """One coddy console on a pyte screen of the requested size."""

    def __init__(self):
        self.screen = pyte.Screen(COLS, ROWS)
        self.stream = pyte.ByteStream(self.screen)
        env = dict(os.environ)
        # HOME is the temp home too: the operator's own skills have no place
        # in a documentation shot.
        env.update({"TERM": "xterm-256color", "COLORTERM": "truecolor", "CODDY_HOME": str(home),
                    "HOME": str(home), "LANG": "en_US.UTF-8", "LC_ALL": "en_US.UTF-8"})
        for k in ("HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "ALL_PROXY"):
            env.pop(k, None)
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

    def wait_for(self, needle, timeout=30):
        deadline = time.time() + timeout
        while time.time() < deadline:
            self.pump(0.3)
            if needle in self.text():
                return
        raise AssertionError(f"never saw {needle!r}:\n{self.text()}")


def render_png(outdir, name):
    """One PNG through headless chromium, cropped to the last painted row."""
    chromium = shutil.which("chromium") or shutil.which("google-chrome") or shutil.which("chromium-browser")
    if not chromium:
        print("no chromium found; HTML capture only")
        return
    html, png = outdir / f"{name}.html", outdir / f"{name}.png"
    subprocess.run([chromium, "--headless=new", "--disable-gpu", "--hide-scrollbars",
                    "--force-device-scale-factor=2", "--window-size=700,400",
                    f"--screenshot={png}", f"file://{html}"], capture_output=True, timeout=60)
    try:
        from PIL import Image
    except ImportError:
        print(f"[png] {png.name} (uncropped)")
        return
    im = Image.open(png).convert("RGB")
    w, h = im.size
    bg, px, last = im.getpixel((2, 2)), im.load(), 0
    for y in range(h):
        if any(px[x, y] != bg for x in range(0, w, 4)):
            last = y
    im.crop((0, 0, w, min(h, last + 28))).save(png, optimize=True)
    print(f"[png] {png.name}")


OUT.mkdir(parents=True, exist_ok=True)
tui = Shot()
tui.wait_for("coddy")
tui.pump(1.0)
tui.child.send(b"\x0c")  # ctrl+l: the model selector
tui.pump(1.0)
for _ in range(5):
    tui.child.send(b"\x1b[B")
    tui.pump(0.15)
tui.pump(0.6)
capture.snapshot(tui, OUT, NAME)
print(tui.text())
tui.child.send(b"\x1b")
tui.pump(0.3)
tui.child.send(b"\x03")
time.sleep(0.2)
tui.child.send(b"\x03")
tui.pump(1)
render_png(OUT, NAME)
for ext in (".txt", ".html"):
    (OUT / f"{NAME}{ext}").unlink(missing_ok=True)
