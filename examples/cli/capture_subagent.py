#!/usr/bin/env python3
"""Capture the console transcript of a delegated run and a loaded skill.

Stands a local OpenAI-compatible endpoint that scripts four turns - a
`load_skill` call, a `spawn_agent` call, the child's report, the parent's
answer - so the shot needs no provider and no key, starts build/coddy cli
in a pty and renders one PNG: a `load_skill <skill>` box above a
`spawn_agent <subagent> · <task>` box carrying the delegated prompt and
the child's report.

Usage: python3 capture_subagent.py [repo] [outdir]   (default docs/assets/cli-tui)
Needs a cli-tagged build/coddy (make build TAGS=cli), pexpect, pyte and
chromium for the PNG.
"""
import json, os, sys, tempfile, threading, time
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

REPO = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path(__file__).resolve().parents[2]
OUT = Path(sys.argv[2]).resolve() if len(sys.argv) > 2 else REPO / "docs" / "assets" / "cli-tui"
sys.path.insert(0, str(REPO / "examples" / "cli"))
os.environ.setdefault("CODDY_BIN", str(REPO / "build" / "coddy"))
import pexpect, pyte  # noqa: E402
import capture  # noqa: E402
from cli_tui_driver import COLS, ROWS, CR  # noqa: E402

MODEL = "stub/coddy-demo"

DELEGATED_PROMPT = (
    "Read external/cli/chat.go and list every tool whose box title names what the "
    "call acts on.\n\nFor each one give the file and line, and say which argument "
    "the title is built from. Report as a short list, no patch."
)

LOAD_SKILL = {"tool": "load_skill", "args": {"name": "code-review"}}
DELEGATE = {"tool": "spawn_agent", "args": {
    "agent": "explore",
    "description": "map the tool box titles",
    "prompt": DELEGATED_PROMPT,
    "timeout_seconds": 300,
}}
CHILD_REPORT = {"text": "run_command titles itself `$ <command>` (chat.go:198), and read, "
                        "write, edit and apply_patch put the path after the tool name "
                        "(chat.go:200). Everything else falls back to the bare tool name."}
PARENT_ANSWER = {"text": "The explore child came back with three title rules: `$ <command>` "
                         "for `run_command`, `<tool> <path>` for the file tools, and the "
                         "bare name for everything else."}


def called(body, name):
    """Whether the conversation already carries a call to this tool. Only the
    assistant's own tool_calls count: every request lists spawn_agent and
    load_skill in its tools, so searching the raw body would match the
    catalog rather than the history."""
    for message in body.get("messages", []):
        for call in message.get("tool_calls") or []:
            if (call.get("function") or {}).get("name") == name:
                return True
    return False


def next_turn(body):
    """Pick the reply from the conversation the request carries rather than
    from a call counter: spawn_agent runs a child that dials this same
    endpoint, and a counter would hand the parent's line to the child the
    moment the child needs one call more than the script assumed."""
    if called(body, "spawn_agent"):
        return PARENT_ANSWER        # the parent, with the child's report in hand
    if DELEGATED_PROMPT[:40] in json.dumps(body.get("messages", [])):
        return CHILD_REPORT         # the child: its whole context is that prompt
    if called(body, "load_skill"):
        return DELEGATE
    return LOAD_SKILL


def sse(payload):
    return f"data: {json.dumps(payload)}\n\n".encode()


def chunk(delta, finish=None):
    return {"id": "chatcmpl-stub", "object": "chat.completion.chunk", "created": int(time.time()),
            "model": MODEL, "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}


class H(BaseHTTPRequestHandler):
    turn = 0
    lock = threading.Lock()

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
        with H.lock:
            H.turn += 1
            step = next_turn(body)
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        if "text" in step:
            for word in step["text"].split(" "):
                self.wfile.write(sse(chunk({"content": word + " "})))
                self.wfile.flush()
            self.wfile.write(sse(chunk({}, "stop")))
        else:
            call = {"index": 0, "id": f"call_{H.turn}", "type": "function",
                    "function": {"name": step["tool"], "arguments": ""}}
            self.wfile.write(sse(chunk({"role": "assistant", "tool_calls": [call]})))
            # Arguments arrive in two chunks, the way a real provider streams them.
            args = json.dumps(step["args"])
            half = len(args) // 2
            for piece in (args[:half], args[half:]):
                self.wfile.write(sse(chunk({"tool_calls": [
                    {"index": 0, "function": {"arguments": piece}}]})))
                self.wfile.flush()
            self.wfile.write(sse(chunk({}, "tool_calls")))
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()

    def _json(self, payload):
        body = json.dumps(payload).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)

    def log_message(self, *a):
        pass


srv = HTTPServer(("127.0.0.1", 0), H)
threading.Thread(target=srv.serve_forever, daemon=True).start()

home = Path(tempfile.mkdtemp(prefix="coddy-subagent-shot-home-"))
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
# The skill the first turn loads has to exist in the catalog for the call to
# succeed; ${{CWD}}/.coddy/skills is one of the default roots.
skill = work / ".coddy" / "skills" / "code-review"
skill.mkdir(parents=True, exist_ok=True)
(skill / "SKILL.md").write_text("""---
name: code-review
description: Review the pending change for correctness and for what the docs still claim.
---

# Code review

Read the diff, then the pages that describe what it changes.
""")

class Shot:
    """The pty stand of capture_usage.py: a coddy console on a pyte screen."""

    def __init__(self):
        self.screen = pyte.Screen(COLS, ROWS)
        self.stream = pyte.ByteStream(self.screen)
        env = dict(os.environ)
        env.update({"TERM": "xterm-256color", "COLORTERM": "truecolor", "CODDY_HOME": str(home),
                    "LANG": "en_US.UTF-8", "LC_ALL": "en_US.UTF-8"})
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
    """One PNG per capture through headless chromium, cropped to the last
    painted row when Pillow is around (capture.render_pngs stops at 720 px
    and would re-render the other captures of the folder)."""
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
tui.send("Delegate the tool title survey to a subagent." + CR)
tui.wait_for("three title rules", timeout=120)
tui.pump(1.0)
# The shot is only worth keeping if the rows it exists to show are on screen:
# a taller transcript or a changed title would otherwise be captured silently.
for row in ("load_skill code-review", "spawn_agent explore", "timeout 300s",
            "list every tool whose box title"):
    if row not in tui.text():
        raise AssertionError(f"{row!r} is not on the captured screen:\n{tui.text()}")
capture.snapshot(tui, OUT, "13-subagent-delegation")
print("delegation captured")
tui.send("\x03"); time.sleep(0.2); tui.send("\x03")
tui.pump(1)
render_pngs(OUT, ["13-subagent-delegation"])
for ext in (".txt", ".html"):
    (OUT / f"13-subagent-delegation{ext}").unlink(missing_ok=True)
