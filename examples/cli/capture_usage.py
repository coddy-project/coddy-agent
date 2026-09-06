#!/usr/bin/env python3
"""Capture the console status bar with NeuralDeep account usage.

Stands a local GET /limits (a fake hub with a pro wallet key), points the
neuraldeep provider at it through CODDY_NEURALDEEP_BASE_URL, starts
build/coddy cli in a pty and dumps three states through capture.py's
renderer (text, styled HTML, PNG): the footer line, the 80 % warning with
its notice and /usage, and a hit limit. No real key and no model call.

Usage: python3 capture_usage.py [repo] [outdir]   (default docs/assets/cli-tui)
Needs a cli-tagged build/coddy (make build TAGS=cli), pexpect, pyte and
chromium for the PNGs. CAPTURE_WORKDIR names the folder shown in the footer.
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

STATE = {"used": 407, "blocked": False}

def payload():
    used = STATE["used"]
    decision = {"scope": "chat", "can_request": True, "blockers": [], "retry_after_sec": None}
    if STATE["blocked"]:
        decision = {"scope": "chat", "can_request": False, "blockers": ["session_exhausted"], "retry_after_sec": 5820}
        used = 15000
    return {
        "schema": 1, "observed_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "tier": "pro",
        "tier_expires_at": None, "unlimited_volume": False, "options": [], "bypass": False, "fair_use": True,
        "key": {"name": "coddy", "status": "ok", "billing_mode": "wallet", "cap": None},
        "decision": decision,
        "chat": {
            "session": {"used": used, "limit": 15000, "remaining": max(0, 15000 - used), "reset_in_sec": 5820,
                        "resets_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 5820)), "window": "3h"},
            "week": {"used": 41230, "limit": 150000, "remaining": 108770, "reset_in_sec": 190000,
                     "resets_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 190000)), "window": "iso-week"},
            "rpm": {"used": 3, "limit": 120, "remaining": 117, "reset_in_sec": 41}, "cooldown_sec": 0, "scope": "account"},
        "daily_capacity": {"pct_used": 0.0, "exhausted": False, "resets_at": "2026-09-07T00:00:00+00:00"},
        "night": {"enabled": True, "active": False, "capacity_factor": 2},
        "wallet": {"balance_rub": 1250.0, "spent_rub_30d": 3470.5}, "kimi": None,
    }

class H(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != "/limits":
            self.send_response(404); self.end_headers(); return
        body = json.dumps(payload()).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)
    def log_message(self, *a):
        pass

srv = HTTPServer(("127.0.0.1", 0), H)
threading.Thread(target=srv.serve_forever, daemon=True).start()
base = f"http://127.0.0.1:{srv.server_port}"

home = Path(tempfile.mkdtemp(prefix="coddy-usage-shot-home-"))
work = Path(os.environ.get("CAPTURE_WORKDIR", tempfile.mkdtemp(prefix="coddy-agent-")))
work.mkdir(parents=True, exist_ok=True)
(home / "providers" / "neuraldeep").mkdir(parents=True)
(home / "providers" / "neuraldeep" / "neuraldeep-auth.json").write_text(json.dumps({
    "api_key": "sk-shot-fake-key-0123456789abcdef", "hub": "https://hub.neuraldeep.ru", "client": "coddy",
    "key_name": "coddy", "obtained_at": "2026-09-06T17:00:00Z"}))
(home / "config.yaml").write_text("""providers:
  - name: neuraldeep
    type: neuraldeep
models:
  - model: neuraldeep/qwen3.8-27b
    max_context_tokens: 262144
agent:
  model: neuraldeep/qwen3.8-27b
""")

class Shot:
    def __init__(self):
        self.screen = pyte.Screen(COLS, ROWS)
        self.stream = pyte.ByteStream(self.screen)
        env = dict(os.environ)
        env.update({"TERM": "xterm-256color", "COLORTERM": "truecolor", "CODDY_HOME": str(home),
                    "LANG": "en_US.UTF-8", "LC_ALL": "en_US.UTF-8", "CODDY_NEURALDEEP_BASE_URL": base,
                    "NEURALDEEP_API_KEY": ""})
        for k in ("HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "ALL_PROXY"):
            env.pop(k, None)
        self.child = pexpect.spawn(os.environ["CODDY_BIN"], ["cli", "--theme", "dark"], env=env, cwd=str(work),
                                   dimensions=(ROWS, COLS), encoding=None, timeout=5)
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
    """PNG per capture through headless chromium, tall enough for the whole
    35-row frame (capture.render_pngs stops at 720 px and would re-render the
    other captures of the folder), cropped to the last painted row when
    Pillow is around."""
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
tui.wait_for("3h 3%")
tui.pump(0.5)
capture.snapshot(tui, OUT, "09-usage-footer")
print("footer captured")

# The threshold: the hub now reports 85 %; the deferred refresh lands after the floor.
STATE["used"] = 12750
tui.send("/usage" + CR)
tui.wait_for("You've used 85%", timeout=25)
tui.pump(0.5)
capture.snapshot(tui, OUT, "10-usage-warning")
print("warning captured")

# A hit limit.
STATE["blocked"] = True
time.sleep(16)
tui.send("/usage" + CR)
tui.wait_for("limit reached", timeout=25)
tui.pump(0.5)
capture.snapshot(tui, OUT, "11-usage-blocked")
print("blocked captured")
tui.send("\x03"); time.sleep(0.2); tui.send("\x03")
tui.pump(1)
render_pngs(OUT, ["09-usage-footer", "10-usage-warning", "11-usage-blocked"])
