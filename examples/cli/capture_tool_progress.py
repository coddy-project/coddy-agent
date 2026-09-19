#!/usr/bin/env python3
"""Capture streamed file arguments in a real console with a local fake model.

Build with make build TAGS=cli. Install examples/cli/requirements.txt.
Usage: capture_tool_progress.py [output-directory]
CHROME may name a headless Chromium binary. No real credentials are used.
"""
import json, os, sys, tempfile, threading, time, shutil, subprocess
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import pexpect, pyte
import capture
from cli_tui_driver import COLS, ROWS, CR

REPO = Path(__file__).resolve().parents[2]
OUT = Path(sys.argv[1]) if len(sys.argv)>1 else REPO / "docs/assets/cli-tui"
OUT.mkdir(parents=True, exist_ok=True)
os.environ.setdefault("CODDY_BIN", str(REPO / "build/coddy"))
release = threading.Event()
ready = threading.Event()
MODEL = "stub/draft-demo"
CONTENT = "<!doctype html>\n<html lang=\"en\">\n<head><title>Airships</title></head>\n<body>\n" + "<section><h2>Flight</h2><p>Buoyancy lifts the airship.</p></section>\n" * 80

def frame(delta, finish=None):
    obj = {"id":"draft-demo", "object":"chat.completion.chunk", "model":"draft-demo",
           "choices":[{"index":0, "delta":delta, "finish_reason":finish}]}
    return ("data: " + json.dumps(obj) + "\n\n").encode()

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args): pass
    def do_POST(self):
        body=json.loads(self.rfile.read(int(self.headers.get("Content-Length",0))))
        if not body.get("stream"):
            data=json.dumps({"choices":[{"message":{"role":"assistant","content":"Airship presentation"}}]}).encode()
            self.send_response(200); self.send_header("Content-Type","application/json")
            self.send_header("Content-Length",str(len(data))); self.end_headers(); self.wfile.write(data); return
        self.send_response(200); self.send_header("Content-Type","text/event-stream"); self.end_headers()
        try:
            if not any(m.get("role")=="tool" for m in body.get("messages",[])):
                self.wfile.write(frame({"tool_calls":[{"index":0,"id":"draft1","type":"function","function":{"name":"write","arguments":""}}]})); self.wfile.flush()
                args=json.dumps({"path":"airships.html","content":CONTENT})
                for i in range(0,len(args),256):
                    if i+256>=len(args): ready.set(); release.wait(60)
                    self.wfile.write(frame({"tool_calls":[{"index":0,"function":{"arguments":args[i:i+256]}}]})); self.wfile.flush(); time.sleep(.22)
                self.wfile.write(frame({},"tool_calls"))
            else:
                self.wfile.write(frame({"content":"Created airships.html."},"stop"))
            self.wfile.write(b"data: [DONE]\n\n"); self.wfile.flush()
        except (BrokenPipeError,ConnectionResetError): pass

srv=ThreadingHTTPServer(("127.0.0.1",0),Handler)
threading.Thread(target=srv.serve_forever,daemon=True).start()
home=Path(tempfile.mkdtemp(prefix="coddy-draft-home-"))
work=Path(tempfile.mkdtemp(prefix="coddy-draft-work-"))
(home/"config.yaml").write_text(f"""providers:
  - name: stub
    type: openai
    api_base: http://127.0.0.1:{srv.server_port}/v1
    api_key: fake
models:
  - model: {MODEL}
    max_context_tokens: 131072
    reasoning_levels: []
agent:
  model: {MODEL}
tools:
  permission_mode: ask
memory:
  enable: false
""")
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


tui=Shot()
try:
    tui.wait_for("coddy v",30)
    tui.pump(1)
    tui.send("Create an HTML presentation about airships."+CR)
    if os.environ.get("CODDY_CAPTURE_BASELINE"):
        tui.wait_for("write",30)
        while not ready.is_set(): tui.pump(.3)
        tui.pump(.5)
        capture.snapshot(tui,OUT,"tool-progress-before-dark-1280")
    else:
        tui.wait_for("argument bytes",30)
        while not ready.is_set(): tui.pump(.3)
        tui.pump(.5)
        if (work/"airships.html").exists(): raise AssertionError("file written before arguments completed")
        capture.snapshot(tui,OUT,"tool-progress-collapsed-dark-1280")
        tui.send("\x0f")
        tui.pump(.5)
        tui.wait_for("<section>",5)
        capture.snapshot(tui,OUT,"tool-progress-expanded-dark-1280")
        release.set()
        tui.wait_for("Allow",15)
        capture.snapshot(tui,OUT,"tool-progress-permission-dark-1280")
        if (work/"airships.html").exists(): raise AssertionError("file written before approval")

finally:
    release.set()
    tui.child.close(force=True)
    srv.shutdown()
    shutil.rmtree(home); shutil.rmtree(work)
chrome=os.environ.get("CHROME") or shutil.which("chromium") or shutil.which("google-chrome")
if chrome:
    for html in OUT.glob("tool-progress-*.html"):
        # Adjacent grid rows must not gain extra text-node newlines inside <pre>.
        html.write_text(html.read_text().replace("</div>\n<div>", "</div><div>"))
        subprocess.run([chrome,"--headless=new","--disable-gpu","--hide-scrollbars","--no-sandbox",
          "--window-size=1280,700", "--screenshot="+str(html.with_suffix(".png").resolve()),
          html.resolve().as_uri()],check=True,capture_output=True,timeout=30)
print("Captured draft, expanded preview and permission gate in",OUT)
