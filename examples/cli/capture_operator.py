#!/usr/bin/env python3
"""Capture the console's operator command: the modal and the running command.

Stands a local OpenAI-compatible endpoint that scripts one turn - an
`operator_command` call for a command that prints three steps over six
seconds, then an answer that reads its output - so the shots need no provider
and no key. It starts build/coddy cli in a pty and renders two PNGs: the modal
that shows the command (selection on Decline), and the tool box streaming the
command's output after `r` ran it.

Usage: python3 capture_operator.py [repo] [outdir]   (default docs/assets/operator-commands)
Needs a cli-tagged build/coddy (make build TAGS=cli), pexpect, pyte and
chromium for the PNG. On Linux, CAPTURE_HOSTNAME=demo-host runs the console in a
UTS namespace of its own (unshare -Ur -u) so the modal shows that host name.
"""
import json, os, shutil, sys, tempfile, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

REPO = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path(__file__).resolve().parents[2]
OUT = Path(sys.argv[2]).resolve() if len(sys.argv) > 2 else REPO / "docs" / "assets" / "operator-commands"
sys.path.insert(0, str(REPO / "examples" / "cli"))
os.environ.setdefault("CODDY_BIN", str(REPO / "build" / "coddy"))
import pexpect, pyte  # noqa: E402
import capture  # noqa: E402
from cli_tui_driver import COLS, ROWS, CR  # noqa: E402

MODEL = "stub/coddy-demo"
COMMAND = 'for step in build test upload; do echo "$step..."; sleep 2; echo "$step done"; done'
REASON = "Deploying needs the release key in your keychain, which run_command cannot read."
ANSWER = "The release went out: build, test and upload all finished in your shell."


def sse(payload):
    return f"data: {json.dumps(payload)}\n\n".encode()


def chunk(delta, finish=None):
    return {"id": "chatcmpl-stub", "object": "chat.completion.chunk", "created": int(time.time()),
            "model": MODEL, "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}


class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.0"

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
        tools = [t.get("function", {}).get("name") for t in body.get("tools") or []]
        if not tools:
            self._json({"id": "cmpl-title", "object": "chat.completion", "model": MODEL,
                        "choices": [{"index": 0, "finish_reason": "stop",
                                     "message": {"role": "assistant", "content": "Release deploy"}}]})
            return
        ran = any(m.get("role") == "tool" for m in body.get("messages", []))
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Connection", "close")
        self.end_headers()
        if not ran and "operator_command" in tools:
            args = json.dumps({"command": COMMAND, "reason": REASON})
            call = {"index": 0, "id": "call_1", "type": "function",
                    "function": {"name": "operator_command", "arguments": args}}
            self.wfile.write(sse(chunk({"role": "assistant", "content": "I cannot run this myself, so I am asking you to."})))
            self.wfile.write(sse(chunk({"tool_calls": [call]})))
            self.wfile.write(sse(chunk({}, "tool_calls")))
        else:
            self.wfile.write(sse(chunk({"role": "assistant", "content": ANSWER if ran else "operator_command was not offered."})))
            self.wfile.write(sse(chunk({}, "stop")))
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()

    def _json(self, payload):
        body = json.dumps(payload).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)

    def log_message(self, *a):
        pass


srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
threading.Thread(target=srv.serve_forever, daemon=True).start()

home = Path(tempfile.mkdtemp(prefix="coddy-operator-shot-home-"))
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
memory:
  enable: false
""")


class Shot:
    """The pty stand of capture_usage.py: a coddy console on a pyte screen."""

    def __init__(self):
        self.screen = pyte.Screen(COLS, ROWS)
        self.stream = pyte.ByteStream(self.screen)
        env = dict(os.environ)
        # HOME too: the skills and rules of the real home stay out of the shot.
        env.update({"TERM": "xterm-256color", "COLORTERM": "truecolor", "CODDY_HOME": str(home), "HOME": str(home),
                    "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8"})
        for k in ("HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "ALL_PROXY"):
            env.pop(k, None)
        cmd, args = os.environ["CODDY_BIN"], ["cli", "--theme", "dark"]
        host = os.environ.get("CAPTURE_HOSTNAME")
        if host and shutil.which("unshare"):
            args = ["-Ur", "-u", "sh", "-c", f'hostname {host} && exec "$0" cli --theme dark', cmd]
            cmd = "unshare"
        self.child = pexpect.spawn(cmd, args, env=env, cwd=str(work), dimensions=(ROWS, COLS),
                                   encoding=None, timeout=5)

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
    import subprocess
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


NAMES = ["operator-command-console-modal-dark", "operator-command-console-running-dark"]
OUT.mkdir(parents=True, exist_ok=True)
tui = Shot()
try:
    tui.wait_for("coddy v", timeout=30)
    tui.pump(1.0)
    tui.send("Please deploy the release." + CR)
    tui.wait_for("Run this command yourself?", timeout=30)
    tui.pump(0.5)
    for row in ("Agent's reason", "Decline", "Run", "upload"):
        if row not in tui.text():
            raise AssertionError(f"{row!r} is not in the modal:\n{tui.text()}")
    capture.snapshot(tui, OUT, NAMES[0])
    print("modal captured")
    tui.send("r")
    tui.wait_for("test...", timeout=20)
    tui.pump(0.3)
    capture.snapshot(tui, OUT, NAMES[1])
    print("running command captured")
    tui.wait_for("release went out", timeout=30)
    print("the turn read the output and answered")
finally:
    tui.send("\x1b"); tui.send("\x1b")
    tui.send("\x03"); time.sleep(0.2); tui.send("\x03")
    tui.pump(1)
render_pngs(OUT, NAMES)
for name in NAMES:
    for ext in (".txt", ".html"):
        (OUT / f"{name}{ext}").unlink(missing_ok=True)
