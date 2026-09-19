#!/usr/bin/env python3
"""Capture the console's turn progress line and its /tasks overlay.

Stands a local OpenAI-compatible endpoint that scripts one turn - a silent
wait, a `run_command` call with `background: true`, then a slowly streamed
answer reporting usage - so the shots need no provider and no key. It starts
build/coddy cli in a pty and renders three PNGs: the status line of the
running turn (`<clock> · <tokens> · 1 running task · Responding`), the /tasks
overlay listing the task, and the overlay with the task's output open.

Usage: python3 capture_tasks.py [repo] [outdir]   (default docs/assets/cli-tui)
Needs a cli-tagged build/coddy (make build TAGS=cli), pexpect, pyte and
chromium for the PNG.
"""
import json, os, sys, tempfile, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

REPO = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path(__file__).resolve().parents[2]
OUT = Path(sys.argv[2]).resolve() if len(sys.argv) > 2 else REPO / "docs" / "assets" / "cli-tui"
sys.path.insert(0, str(REPO / "examples" / "cli"))
os.environ.setdefault("CODDY_BIN", str(REPO / "build" / "coddy"))
import pexpect, pyte  # noqa: E402
import capture  # noqa: E402
from cli_tui_driver import COLS, ROWS, CR  # noqa: E402

MODEL = "stub/coddy-demo"
ANSWER = (
    "The test suite is running in the background as task bg_1, so this turn does not have to "
    "sit through it. While it runs: the session manager admits a turn before it takes the turn "
    "lock, the agent loop freezes the system prompt for the turn and streams every model call "
    "to the sender, and the status line above counts the turn's clock, the tokens generated in "
    "it and the tasks still running. I will read the task's output once it has something to "
    "report and summarise the failures if there are any."
)
# The answer is held open while the capture takes its shot of the running turn.
release_answer = threading.Event()


def sse(payload):
    return f"data: {json.dumps(payload)}\n\n".encode()


def chunk(delta, finish=None, usage=None):
    body = {"id": "chatcmpl-stub", "object": "chat.completion.chunk", "created": int(time.time()),
            "model": MODEL, "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}
    if usage:
        body["usage"] = usage
    return body


class H(BaseHTTPRequestHandler):
    def do_GET(self):
        if not self.path.endswith("/models"):
            self.send_response(404); self.end_headers(); return
        self._json({"object": "list", "data": [{"id": "coddy-demo", "object": "model", "owned_by": "stub"}]})

    def do_POST(self):
        if not self.path.endswith("/chat/completions"):
            self.send_response(404); self.end_headers(); return
        raw = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        try:
            body = json.loads(raw or b"{}")
        except ValueError:
            body = {}
        messages = body.get("messages", [])
        if not body.get("tools"):
            # The title request: no tools, answered at once.
            self._json({"id": "cmpl-title", "object": "chat.completion", "model": MODEL,
                        "choices": [{"index": 0, "finish_reason": "stop",
                                     "message": {"role": "assistant", "content": "Background test run"}}]})
            return
        ran = any(m.get("role") == "tool" for m in messages)
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        try:
            if not ran:
                time.sleep(1.5)
                call = {"index": 0, "id": "call_1", "type": "function",
                        "function": {"name": "run_command", "arguments": ""}}
                self.wfile.write(sse(chunk({"role": "assistant", "tool_calls": [call]})))
                args = json.dumps({"command": "make test", "background": True, "expected_seconds": 300})
                half = len(args) // 2
                for piece in (args[:half], args[half:]):
                    self.wfile.write(sse(chunk({"tool_calls": [{"index": 0, "function": {"arguments": piece}}]})))
                    self.wfile.flush()
                self.wfile.write(sse(chunk({}, "tool_calls", {"prompt_tokens": 900, "completion_tokens": 64, "total_tokens": 964})))
            else:
                words = ANSWER.split(" ")
                for i, word in enumerate(words):
                    self.wfile.write(sse(chunk({"content": word + " "})))
                    self.wfile.flush()
                    # Most of the answer streams at a readable pace; the tail waits
                    # for the capture, so the turn is still running when it shoots.
                    time.sleep(0.04)
                    if i == len(words) - 12:
                        release_answer.wait(timeout=90)
                self.wfile.write(sse(chunk({}, "stop", {"prompt_tokens": 1100, "completion_tokens": 1500, "total_tokens": 2600})))
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass

    def _json(self, payload):
        body = json.dumps(payload).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)

    def log_message(self, *a):
        pass


srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
threading.Thread(target=srv.serve_forever, daemon=True).start()

home = Path(tempfile.mkdtemp(prefix="coddy-tasks-shot-home-"))
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
    reasoning_levels: []
agent:
  model: {MODEL}
tools:
  permission_mode: bypass
memory:
  enable: false
""")
# The background command the scripted turn starts: it prints like a test run and
# stays alive long enough for the overlay to show it running.
(work / "Makefile").write_text(
    "test:\n\t@echo '=== RUN   TestSessionManager'; "
    "for i in 1 2 3 4 5 6 7 8; do echo \"ok   internal/pkg$$i  0.$${i}s\"; sleep 0.2; done; sleep 120\n")


class Shot:
    """The pty stand of capture_usage.py: a coddy console on a pyte screen."""

    def __init__(self):
        self.screen = pyte.Screen(COLS, ROWS)
        self.stream = pyte.ByteStream(self.screen)
        env = dict(os.environ)
        env.update({"TERM": "xterm-256color", "COLORTERM": "truecolor", "CODDY_HOME": str(home),
                    "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8"})
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


NAMES = ["14-turn-progress", "15-tasks-overlay", "16-tasks-output"]
OUT.mkdir(parents=True, exist_ok=True)
tui = Shot()
try:
    tui.wait_for("coddy v", timeout=30)
    tui.pump(1.0)
    tui.send("Run the test suite in the background and tell me how a turn is admitted." + CR)
    # Before the first token the line is the turn clock and the waiting phrase.
    tui.wait_for("Waiting for the model", timeout=20)
    if " tokens" in tui.text():
        raise AssertionError(f"tokens on the line before the model produced any:\n{tui.text()}")
    # Mid-turn: the clock, the generated tokens and the running task lead the line.
    tui.wait_for("1 running task", timeout=60)
    tui.wait_for(" tokens", timeout=30)
    tui.pump(0.5)
    for row in (" tokens · 1 running task · ", "run_command"):
        if row not in tui.text():
            raise AssertionError(f"{row!r} is not on the captured screen:\n{tui.text()}")
    capture.snapshot(tui, OUT, NAMES[0])
    print("turn progress captured")
    release_answer.set()
    tui.wait_for("summarise the failures", timeout=60)
    tui.wait_for("1 task running (/tasks)", timeout=20)

    tui.send("/tasks" + CR)
    tui.wait_for("Background tasks", timeout=10)
    tui.wait_for("1 running", timeout=10)
    for row in ("shell", "make test", "enter output"):
        if row not in tui.text():
            raise AssertionError(f"{row!r} is not in the overlay:\n{tui.text()}")
    capture.snapshot(tui, OUT, NAMES[1])
    print("tasks overlay captured")

    tui.send(CR)
    tui.wait_for("ok   internal/pkg8", timeout=20)
    for row in ("$ make test", "s stop"):
        if row not in tui.text():
            raise AssertionError(f"{row!r} is not in the open task:\n{tui.text()}")
    capture.snapshot(tui, OUT, NAMES[2])
    print("task output captured")

    # Stopping from the overlay ends the command this script started.
    tui.send("s")
    tui.wait_for("stopped", timeout=20)
    print("task stopped from the overlay")
finally:
    tui.send("\x1b"); tui.send("\x1b")
    tui.send("\x03"); time.sleep(0.2); tui.send("\x03")
    tui.pump(1)
render_pngs(OUT, NAMES)
for name in NAMES:
    for ext in (".txt", ".html"):
        (OUT / f"{name}{ext}").unlink(missing_ok=True)
