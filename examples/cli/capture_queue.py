#!/usr/bin/env python3
"""Capture the console message queue: follow-ups written while a turn works.

Stands a local OpenAI-compatible endpoint whose first streamed answer is held
open, so the console is genuinely mid-turn while two more prompts are typed.
The config names no queue mode, so the first follow-up asks which mode Enter
should use (the first shot); answered with 1 it is queued to steer, and the
second, sent with Tab, waits for after the turn (the second shot).

Usage: python3 capture_queue.py [repo] [outdir]   (default docs/assets/message-queue)
Needs a cli-tagged build/coddy (make build TAGS=cli), pexpect, pyte and
chromium for the PNG.
"""
import json, os, sys, tempfile, threading, time
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

REPO = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path(__file__).resolve().parents[2]
OUT = Path(sys.argv[2]).resolve() if len(sys.argv) > 2 else REPO / "docs" / "assets" / "message-queue"
sys.path.insert(0, str(REPO / "examples" / "cli"))
os.environ.setdefault("CODDY_BIN", str(REPO / "build" / "coddy"))
import pexpect, pyte  # noqa: E402
import capture  # noqa: E402
from cli_tui_driver import COLS, ROWS, CR  # noqa: E402

MODEL = "stub/coddy-demo"
SHOT = "message-queue-console-modes-dark"
SHOT_CHOICE = "message-queue-console-choice-dark"

# How long the first answer is held open. The screen is captured inside this
# window, so it only has to outlast the typing, not a human.
STALL_SECONDS = 45.0

FIRST = [
    "Reading the packaging layout first, then the install routes.\n\n",
    "The man page and the completions live under `packaging/`, and nothing ",
    "generates them from the code, so a renamed flag has to be carried into ",
    "both by hand. Checking what the release archive ships",
]

PROMPT = "Check that a renamed CLI flag is carried into the man page and the completions."
FOLLOW_UPS = [
    "Check the Windows path too, the completions ship for bash and zsh only.",
    "And skip the integration suite for now, the tag matrix covers it on the PR.",
]

calls = {"n": 0}
lock = threading.Lock()


def chunk(delta, finish=None):
    return "data: " + json.dumps({
        "id": "chatcmpl-stub", "object": "chat.completion.chunk",
        "created": int(time.time()), "model": MODEL,
        "choices": [{"index": 0, "delta": delta, "finish_reason": finish}],
    }) + "\n\n"


class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def do_GET(self):
        if not self.path.endswith("/models"):
            self.send_response(404)
            self.end_headers()
            return
        body = json.dumps({"object": "list", "data": [
            {"id": MODEL, "object": "model", "owned_by": "stub"}]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        raw = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        try:
            body = json.loads(raw or b"{}")
        except ValueError:
            body = {}
        # Only the turn streams; anything else (a derived title) is answered at
        # once, so the held-open call is always the one the shot needs.
        if not body.get("stream"):
            out = json.dumps({
                "id": "chatcmpl-stub", "object": "chat.completion",
                "created": int(time.time()), "model": MODEL,
                "choices": [{"index": 0, "finish_reason": "stop",
                             "message": {"role": "assistant", "content": "Packaging sync"}}],
            }).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(out)))
            self.end_headers()
            self.wfile.write(out)
            return
        with lock:
            calls["n"] += 1
            first = calls["n"] == 1
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        try:
            if first:
                for part in FIRST:
                    self.wfile.write(chunk({"content": part}).encode())
                    self.wfile.flush()
                    time.sleep(0.25)
                deadline = time.time() + STALL_SECONDS
                while time.time() < deadline:
                    time.sleep(0.5)
                    self.wfile.write(b": keepalive\n\n")
                    self.wfile.flush()
            else:
                self.wfile.write(chunk({"content": "Understood."}).encode())
                self.wfile.flush()
            self.wfile.write(chunk({}, "stop").encode())
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass


srv = HTTPServer(("127.0.0.1", 0), H)
threading.Thread(target=srv.serve_forever, daemon=True).start()

home = Path(tempfile.mkdtemp(prefix="coddy-queue-shot-home-"))
work = Path(os.environ.get("CAPTURE_WORKDIR", tempfile.mkdtemp(prefix="coddy-agent-")))
work.mkdir(parents=True, exist_ok=True)
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
tools:
  permission_mode: bypass
""")


class Shot:
    """The pty stand of capture_subagent.py: a coddy console on a pyte screen."""

    def __init__(self):
        self.screen = pyte.Screen(COLS, ROWS)
        self.stream = pyte.ByteStream(self.screen)
        env = dict(os.environ)
        # HOME too: the console also reads ~/.agents/skills and the operator's
        # own instructions, and a documentation shot must not show them.
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

    def send(self, text):
        self.child.send(text.encode())


def render_pngs(outdir, names):
    """One PNG per capture through headless chromium, cropped to the last
    painted row when Pillow is around."""
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


OUT.mkdir(parents=True, exist_ok=True)
tui = Shot()
tui.wait_for("coddy v", timeout=30)
tui.pump(1.0)
tui.send(PROMPT + CR)
# The turn is genuinely in flight once its first words are on screen.
tui.wait_for("Checking what the release archive ships", timeout=60)
# The first follow-up asks what Enter should do; 1 saves steer and queues it.
tui.send(FOLLOW_UPS[0] + CR)
tui.wait_for("Choose the default queue mode once", timeout=10)
capture.snapshot(tui, OUT, SHOT_CHOICE)
print("queue mode question captured")
tui.send("1")
tui.wait_for("[steer]", timeout=10)
# Tab sends the second one the other way: after the turn.
tui.send(FOLLOW_UPS[1] + "\t")
tui.wait_for("[after_turn]", timeout=10)
tui.pump(0.8)

# The shot is only worth keeping if the rows it exists to show are on screen.
for row in ("queued messages", "[steer]", "[after_turn]", "Check the Windows path too", "skip the integration suite"):
    if row not in tui.text():
        raise AssertionError(f"{row!r} is not on the captured screen:\n{tui.text()}")
capture.snapshot(tui, OUT, SHOT)
print("queue captured")
tui.send("\x03")
time.sleep(0.2)
tui.send("\x03")
tui.pump(1)
render_pngs(OUT, [SHOT_CHOICE, SHOT])
for name in (SHOT_CHOICE, SHOT):
    for ext in (".txt", ".html"):
        (OUT / f"{name}{ext}").unlink(missing_ok=True)
