#!/usr/bin/env python3
"""Capture the console's "@" mention list.

Lays out a workspace with the tracked files of this repository (empty, under
git, so the index honours what the project ignores), starts build/coddy cli
in a pty against a stub provider that is never asked anything, types a
fragment after "@" and renders the list the editor opens: the files ranked
against the fragment, the kind of every candidate, and the scroll line that
says how many matched beyond the fifty the list holds.

Usage: python3 capture_mentions.py [repo] [outdir]   (default docs/assets/cli-tui)
Needs a cli-tagged build/coddy (make build TAGS=cli), git, pexpect, pyte and
chromium for the PNG.
"""
import json, os, subprocess, sys, tempfile, threading, time
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

REPO = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path(__file__).resolve().parents[2]
OUT = Path(sys.argv[2]).resolve() if len(sys.argv) > 2 else REPO / "docs" / "assets" / "cli-tui"
sys.path.insert(0, str(REPO / "examples" / "cli"))
os.environ.setdefault("CODDY_BIN", str(REPO / "build" / "coddy"))
import pexpect, pyte  # noqa: E402
import capture  # noqa: E402
from cli_tui_driver import COLS, ROWS  # noqa: E402

MODEL = "stub/coddy-demo"
NAME = "18-mention-list"


class H(BaseHTTPRequestHandler):
    """A provider that lists one model and is never sent a prompt."""

    def do_GET(self):
        body = json.dumps({"object": "list", "data": [{"id": "coddy-demo", "object": "model", "owned_by": "stub"}]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *a):
        pass


srv = HTTPServer(("127.0.0.1", 0), H)
threading.Thread(target=srv.serve_forever, daemon=True).start()

home = Path(tempfile.mkdtemp(prefix="coddy-mentions-shot-home-"))
work = Path(tempfile.mkdtemp(prefix="coddy-agent-"))
tracked = subprocess.run(["git", "-C", str(REPO), "ls-files", "-z", "--cached", "--others", "--exclude-standard"],
                         capture_output=True, check=True).stdout
for rel in filter(None, tracked.decode().split("\0")):
    p = work / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.touch()
subprocess.run(["git", "-C", str(work), "init", "-q", "-b", "main"], check=True)
subprocess.run(["git", "-C", str(work), "add", "-A"], check=True)
(home / "config.yaml").write_text(f"""providers:
  - name: stub
    type: openai
    api_base: "http://127.0.0.1:{srv.server_port}/v1"
    api_key: "sk-capture-stub"
models:
  - model: {MODEL}
    max_context_tokens: 131072
agent:
  model: {MODEL}
""")


class Shot:
    """The pty stand of capture_subagent.py: a coddy console on a pyte screen."""

    def __init__(self):
        self.screen = pyte.Screen(COLS, ROWS)
        self.stream = pyte.ByteStream(self.screen)
        env = dict(os.environ)
        # HOME is the temp home too: the skills of ~/.agents/skills are the
        # operator's own and have no place in a documentation shot.
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

    def type(self, text):
        for ch in text:
            self.child.send(ch.encode())
            self.pump(0.05)


def render_pngs(outdir, names):
    """One PNG per capture through headless chromium, cropped to the last
    painted row when Pillow is around (the render of capture_subagent.py)."""
    import shutil
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


OUT.mkdir(parents=True, exist_ok=True)
tui = Shot()
tui.wait_for("coddy v", timeout=30)
tui.pump(1.0)
tui.type("explain how @ment")
tui.wait_for("type to narrow", timeout=30)
tui.pump(1.0)
# The shot is only worth keeping if what it exists to show is on screen.
for row in ("internal/mention/", "type to narrow"):
    if row not in tui.text():
        raise AssertionError(f"{row!r} is not on the captured screen:\n{tui.text()}")
capture.snapshot(tui, OUT, NAME)
print("mention list captured")
tui.child.send(b"\x1b")
tui.pump(0.3)
tui.child.send(b"\x03")
time.sleep(0.2)
tui.child.send(b"\x03")
tui.pump(1)
render_pngs(OUT, [NAME])
for ext in (".txt", ".html"):
    (OUT / f"{NAME}{ext}").unlink(missing_ok=True)
