#!/usr/bin/env python3
"""Capture the console footer after the session settings commands.

Starts build/coddy cli in a pty against a local OpenAI-compatible stub (no
provider, no key), types `/permissions bypass`, then a chained line that
switches the model for one turn and the reasoning level for three, and
renders one PNG: the two notices in the transcript, the footer's permission
mode in the warning colour and the line of turn overrides under it.

Usage: python3 capture_settings.py [repo] [outdir]   (default docs/assets/session-settings)
Needs a cli-tagged build/coddy (make build TAGS=cli, or CODDY_BIN), pexpect,
pyte and chromium for the PNG (Pillow crops it when present).
"""
import json, os, shutil, subprocess, sys, tempfile, threading, time
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

REPO = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path(__file__).resolve().parents[2]
OUT = Path(sys.argv[2]).resolve() if len(sys.argv) > 2 else REPO / "docs" / "assets" / "session-settings"
sys.path.insert(0, str(REPO / "examples" / "cli"))
os.environ.setdefault("CODDY_BIN", str(REPO / "build" / "coddy"))
import pexpect, pyte  # noqa: E402
import capture  # noqa: E402
from cli_tui_driver import COLS, ROWS, CR  # noqa: E402

NAME = "session-settings-console-footer-dark"


class Stub(BaseHTTPRequestHandler):
    """The model list and a one-line answer: the shot runs no turn, but the
    console lists the provider's models when it starts."""

    def do_GET(self):
        body = json.dumps({"object": "list", "data": [
            {"id": "coddy-demo", "object": "model", "owned_by": "stub"},
            {"id": "coddy-mini", "object": "model", "owned_by": "stub"},
        ]}).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)

    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length") or 0))
        self.send_response(200); self.send_header("Content-Type", "text/event-stream"); self.end_headers()
        chunk = {"id": "chatcmpl-stub", "object": "chat.completion.chunk", "created": int(time.time()),
                 "model": "coddy-demo", "choices": [{"index": 0, "delta": {"content": "ok"},
                                                     "finish_reason": "stop"}]}
        self.wfile.write(f"data: {json.dumps(chunk)}\n\ndata: [DONE]\n\n".encode())

    def log_message(self, *a):
        pass


srv = HTTPServer(("127.0.0.1", 0), Stub)
threading.Thread(target=srv.serve_forever, daemon=True).start()

home = Path(tempfile.mkdtemp(prefix="coddy-settings-shot-home-"))
work = Path(tempfile.mkdtemp(prefix="coddy-agent-"))
(home / "config.yaml").write_text(f"""providers:
  - name: stub
    type: openai
    api_base: "http://127.0.0.1:{srv.server_port}/v1"
    api_key: "sk-capture-stub"
models:
  - model: stub/coddy-demo
    max_context_tokens: 131072
    reasoning_levels: [low, medium, high]
  - model: stub/coddy-mini
    max_context_tokens: 131072
    reasoning_levels: [low, medium, high]
agent:
  model: stub/coddy-demo
tools:
  permission_mode: ask
""")


class Shot:
    """A coddy console on a pyte screen (the stand of capture_subagent.py)."""

    def __init__(self):
        self.screen = pyte.Screen(COLS, ROWS)
        self.stream = pyte.ByteStream(self.screen)
        env = dict(os.environ)
        # HOME too: skills are also read from ~/.claude and ~/.agents, and the
        # shot should list the bundled ones only.
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

    def wait_for(self, needle, timeout=20):
        deadline = time.time() + timeout
        while time.time() < deadline:
            self.pump(0.3)
            if needle in self.text():
                return
        raise AssertionError(f"never saw {needle!r}:\n{self.text()}")

    def send(self, text):
        self.child.send(text.encode())


def render_png(outdir, name):
    """One PNG through headless chromium, cropped to the last painted row
    when Pillow is around."""
    chromium = shutil.which("chromium") or shutil.which("google-chrome") or shutil.which("chromium-browser")
    if not chromium:
        print("no chromium found; HTML capture only")
        return
    html, png = outdir / f"{name}.html", outdir / f"{name}.png"
    subprocess.run([chromium, "--headless=new", "--disable-gpu", "--hide-scrollbars",
                    "--force-device-scale-factor=2", "--window-size=930,1320",
                    f"--screenshot={png}", f"file://{html}"], capture_output=True, timeout=60)
    try:
        from PIL import Image
    except ImportError:
        print(f"[png] {png.name} (uncropped)")
        return
    im = Image.open(png).convert("RGB")
    w, h = im.size
    bg, px, last = im.getpixel((5, 5)), im.load(), 0
    for y in range(h):
        if any(px[x, y] != bg for x in range(0, w, 4)):
            last = y
    im.crop((0, 0, w, min(h, last + 60))).save(png, optimize=True)
    print(f"[png] {png.name}")


OUT.mkdir(parents=True, exist_ok=True)
tui = Shot()
tui.wait_for("coddy v", timeout=30)
tui.pump(1.0)
tui.send("/permissions bypass" + CR)
tui.wait_for("bypass", timeout=20)
tui.pump(0.5)
tui.send("/model stub/coddy-mini --once /reasoning high --count=3" + CR)
tui.wait_for("coddy-mini", timeout=20)
tui.pump(1.0)
# The shot is only worth keeping if what it exists to show is on screen.
for row in ("bypass", "coddy-mini", "high"):
    if row not in tui.text():
        raise AssertionError(f"{row!r} is not on the captured screen:\n{tui.text()}")
capture.snapshot(tui, OUT, NAME)
tui.send("\x03"); time.sleep(0.2); tui.send("\x03")
tui.pump(1)
render_png(OUT, NAME)
for ext in (".txt", ".html"):
    (OUT / f"{NAME}{ext}").unlink(missing_ok=True)
srv.shutdown()
shutil.rmtree(home, ignore_errors=True)
shutil.rmtree(work, ignore_errors=True)
