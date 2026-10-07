#!/usr/bin/env python3
"""Capture the console with a session goal: its rows, its footer note and the
goal menu.

Starts build/coddy cli in a pty against a local OpenAI-compatible stub (no
provider, no key) that plays both the agent and the supervisor's check, types
`/goal <objective>`, lets the supervisor find the work unfinished once and
then ask the operator a question, opens the goal menu with a bare `/goal`
and renders one PNG: the goal rows in the transcript, the blocked goal's
notice, the menu with the last check, the checklist and the numbers, and
`◎ goal blocked (/goal)` in the footer.

Usage: python3 capture_goal.py [repo] [outdir]   (default docs/assets/session-supervisor)
Needs a cli-tagged build/coddy (make build TAGS=cli, or CODDY_BIN), pexpect,
pyte and chromium for the PNG (Pillow crops it when present).
"""
import json, os, shutil, subprocess, sys, tempfile, threading, time
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

REPO = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path(__file__).resolve().parents[2]
OUT = Path(sys.argv[2]).resolve() if len(sys.argv) > 2 else REPO / "docs" / "assets" / "session-supervisor"
sys.path.insert(0, str(REPO / "examples" / "cli"))
os.environ.setdefault("CODDY_BIN", str(REPO / "build" / "coddy"))
os.environ.setdefault("CLI_E2E_ROWS", "44")
import pexpect, pyte  # noqa: E402
import capture  # noqa: E402
from cli_tui_driver import COLS, ROWS, CR  # noqa: E402

NAME = "goal-console-menu-dark"
OBJECTIVE = "add a /health endpoint with a handler test"
JUDGE_MARK = "You are the supervisor of a coding agent"

# The supervisor's two checks: the work is not proven yet, then a question
# only the operator can answer.
VERDICTS = [
    {"analysis": "The final message claims the endpoint; no tool result shows it.",
     "checklist": [{"text": "GET /health answers 200", "status": "unverified", "evidence": "no request in the evidence"},
                   {"text": "a handler test covers it", "status": "not_met", "evidence": "no test run"}],
     "verdict": "not_met", "reason": "no tool result shows the endpoint or a passing test",
     "remaining": ["add a handler test for /health", "run go test ./... and show it passing"]},
    {"analysis": "The test passes; the objective does not say what the probe checks.",
     "checklist": [{"text": "GET /health answers 200", "status": "met", "evidence": "go test ./... ok"},
                   {"text": "a handler test covers it", "status": "met", "evidence": "TestHealth passes"},
                   {"text": "the probe checks the database", "status": "unverified", "evidence": "not named in the objective"}],
     "verdict": "needs_user", "reason": "Should /health also check the database connection?",
     "remaining": ["decide whether /health checks the database"]},
]
ANSWER = "Added `GET /health` and `TestHealth`; `go test ./...` passes."


class Stub(BaseHTTPRequestHandler):
    """The model list, the agent's answer and the supervisor's verdicts."""

    checks = 0

    def do_GET(self):
        body = json.dumps({"object": "list", "data": [
            {"id": "coddy-demo", "object": "model", "owned_by": "stub"},
        ]}).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)

    def do_POST(self):
        req = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
        judged = any(JUDGE_MARK in str(m.get("content", "")) for m in req.get("messages", []))
        if judged:
            verdict = VERDICTS[min(Stub.checks, len(VERDICTS) - 1)]
            Stub.checks += 1
            content = json.dumps(verdict)
        else:
            content = ANSWER
        if req.get("stream"):
            self.send_response(200); self.send_header("Content-Type", "text/event-stream"); self.end_headers()
            chunk = {"id": "chatcmpl-stub", "object": "chat.completion.chunk", "created": int(time.time()),
                     "model": "coddy-demo", "choices": [{"index": 0, "delta": {"content": content},
                                                         "finish_reason": "stop"}]}
            self.wfile.write(f"data: {json.dumps(chunk)}\n\ndata: [DONE]\n\n".encode())
            return
        body = json.dumps({"id": "chatcmpl-stub", "object": "chat.completion", "created": int(time.time()),
                           "model": "coddy-demo", "choices": [{"index": 0, "finish_reason": "stop",
                                                               "message": {"role": "assistant", "content": content}}],
                           "usage": {"prompt_tokens": 1200, "completion_tokens": 80, "total_tokens": 1280}}).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)

    def log_message(self, *a):
        pass


srv = HTTPServer(("127.0.0.1", 0), Stub)
threading.Thread(target=srv.serve_forever, daemon=True).start()

home = Path(tempfile.mkdtemp(prefix="coddy-goal-shot-home-"))
work = Path(tempfile.mkdtemp(prefix="coddy-agent-"))
(home / "config.yaml").write_text(f"""providers:
  - name: stub
    type: openai
    api_base: "http://127.0.0.1:{srv.server_port}/v1"
    api_key: "sk-capture-stub"
models:
  - model: stub/coddy-demo
    max_context_tokens: 131072
agent:
  model: stub/coddy-demo
tools:
  permission_mode: ask
""")


class Shot:
    """A coddy console on a pyte screen (the stand of capture_settings.py)."""

    def __init__(self):
        self.screen = pyte.Screen(COLS, ROWS)
        self.stream = pyte.ByteStream(self.screen)
        env = dict(os.environ)
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
                    "--force-device-scale-factor=2", "--window-size=930,1600",
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
tui.send("/goal " + OBJECTIVE + CR)
tui.wait_for("◎ goal blocked", timeout=60)
tui.pump(1.0)
tui.send("/goal" + CR)
tui.wait_for("Session goal", timeout=20)
tui.pump(1.0)
# The shot is only worth keeping if what it exists to show is on screen.
for row in ("◎ Goal continuation 1 of 10", "Goal blocked: Should /health also check the database connection?",
            "Last check: needs user", "the probe checks the database", "Resume", "◎ goal blocked (/goal)"):
    if row not in tui.text():
        raise AssertionError(f"{row!r} is not on the captured screen:\n{tui.text()}")
capture.snapshot(tui, OUT, NAME)
tui.send("\x1b"); time.sleep(0.2)
tui.send("\x03"); time.sleep(0.2); tui.send("\x03")
tui.pump(1)
render_png(OUT, NAME)
for ext in (".txt", ".html"):
    (OUT / f"{NAME}{ext}").unlink(missing_ok=True)
srv.shutdown()
shutil.rmtree(home, ignore_errors=True)
shutil.rmtree(work, ignore_errors=True)
