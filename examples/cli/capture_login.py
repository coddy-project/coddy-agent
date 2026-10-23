#!/usr/bin/env python3
"""Capture the console's /login against a stand-in NeuralDeep hub.

Starts build/coddy cli in a pty on an empty CODDY_HOME - the fresh install,
no config.yaml - with the hub and the API base pointed at a local stand-in
(CODDY_NEURALDEEP_HUB_URL, CODDY_NEURALDEEP_BASE_URL). The stand-in answers
the device sign-in as the real hub does: a code to confirm on its page,
"pending" once, then a key; whoami names the account, the tier status lists
the chat models, and GET /limits feeds the usage line. The shot types /login
and renders one PNG once the sign-in is over: the page and the code, the
signed-in line, what config.yaml gained, and the footer on the new model.
No real hub, no key, no browser.

Usage: python3 capture_login.py [repo] [outdir]   (default docs/assets/cli-tui)
Needs a cli-tagged build/coddy (make build TAGS=cli, or CODDY_BIN), pexpect,
pyte and chromium for the PNG (Pillow crops it when present).
"""
import json, os, shutil, subprocess, sys, tempfile, threading, time
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

REPO = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path(__file__).resolve().parents[2]
OUT = Path(sys.argv[2]).resolve() if len(sys.argv) > 2 else REPO / "docs" / "assets" / "cli-tui"
sys.path.insert(0, str(REPO / "examples" / "cli"))
os.environ.setdefault("CODDY_BIN", str(REPO / "build" / "coddy"))
import pexpect, pyte  # noqa: E402
import capture  # noqa: E402
from cli_tui_driver import ROWS, CR  # noqa: E402

# Wide enough that the page address and the line of added models fit 1280 px.
COLS = 148
capture.COLS = COLS
NAME = "login-signed-in-dark-1280"
CODE = "BCDF-GHJK"
# The address the real hub sends a person to; the stand-in only names it.
PAGE = "https://hub.neuraldeep.ru/app/device?code=" + CODE
MODELS = ["qwen3.8-27b", "qwen3.8-27b-noreason", "qwen3.6-35b-a3b", "kimi-k2.6", "gpt-oss-120b"]
LIMITS = {
    "schema": 1, "observed_at": "2026-10-23T17:47:02Z", "tier": "pro", "tier_expires_at": None,
    "unlimited_volume": False, "options": [], "bypass": False, "fair_use": True,
    "key": {"name": "coddy", "status": "ok", "billing_mode": "wallet", "cap": None},
    "decision": {"scope": "chat", "can_request": True, "blockers": [], "retry_after_sec": None},
    "chat": {"session": {"used": 450, "limit": 15000, "remaining": 14550, "reset_in_sec": 7777,
                         "resets_at": "2026-10-23T20:59:59Z", "window": "3h"},
             "week": {"used": 40500, "limit": 150000, "remaining": 109500, "reset_in_sec": 22378,
                      "resets_at": "2026-10-27T00:00:00Z", "window": "iso-week"},
             "rpm": {"used": 2, "limit": 120, "remaining": 118, "reset_in_sec": 58},
             "cooldown_sec": 0, "scope": "account"},
    "daily_capacity": {"pct_used": 0.0, "exhausted": False, "resets_at": "2026-10-24T00:00:00+00:00"},
    "night": {"enabled": True, "active": False, "capacity_factor": 2},
    "wallet": {"balance_rub": 1250.0, "spent_rub_30d": 640.5}, "kimi": None,
}
polls = []


class Hub(BaseHTTPRequestHandler):
    """The hub's device sign-in, whoami and tier status, and the API's limits."""

    def answer(self, status, payload):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length") or 0))
        path = self.path.split("?")[0]
        if path == "/api/cli/device/start":
            self.answer(200, {"device_code": "dc-capture", "user_code": CODE,
                              "verification_uri": PAGE.split("?")[0], "verification_uri_complete": PAGE,
                              "interval": 1, "expires_in": 900})
        elif path == "/api/cli/device/token":
            polls.append(time.time())
            if len(polls) < 3:
                self.answer(400, {"error": "authorization_pending"})
            else:
                self.answer(200, {"access_token": "sk-capture-login-0123456789abcdef", "token_type": "bearer"})
        else:
            self.answer(404, {})

    def do_GET(self):
        path = self.path.split("?")[0]
        if path == "/api/cli/whoami":
            self.answer(200, {"email": "dev@example.com", "name": "dev", "tier": "pro"})
        elif path == "/api/cli/status":
            self.answer(200, {"tier": "pro", "models": [{"id": m, "mode": "chat", "ctx": 262144} for m in MODELS]})
        elif path.endswith("/limits"):
            self.answer(200, LIMITS)
        else:
            self.answer(404, {})

    def log_message(self, *a):
        pass


srv = HTTPServer(("127.0.0.1", 0), Hub)
threading.Thread(target=srv.serve_forever, daemon=True).start()
stand = f"http://127.0.0.1:{srv.server_port}"

home = Path(tempfile.mkdtemp(prefix="coddy-login-shot-home-"))
work = Path(tempfile.mkdtemp(prefix="coddy-agent-"))


class Shot:
    """A coddy console on a pyte screen (the stand of capture_settings.py)."""

    def __init__(self):
        self.screen = pyte.Screen(COLS, ROWS)
        self.stream = pyte.ByteStream(self.screen)
        env = dict(os.environ)
        # HOME too: skills are also read from ~/.claude and ~/.agents, and the
        # shot should list the bundled ones only.
        env.update({"TERM": "xterm-256color", "COLORTERM": "truecolor", "CODDY_HOME": str(home),
                    "HOME": str(home), "LANG": "en_US.UTF-8", "LC_ALL": "en_US.UTF-8",
                    "CODDY_NEURALDEEP_HUB_URL": stand, "CODDY_NEURALDEEP_BASE_URL": stand,
                    "NO_BROWSER": "1", "NEURALDEEP_API_KEY": ""})
        for k in ("HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "ALL_PROXY", "all_proxy",
                  "DISPLAY", "WAYLAND_DISPLAY", "OPENAI_API_KEY"):
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
                    "--force-device-scale-factor=2", "--window-size=1280,760",
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
tui.wait_for("Type /login", timeout=10)
tui.pump(1.0)
tui.send("/login" + CR)
tui.wait_for(CODE, timeout=20)
tui.wait_for("Signed in to NeuralDeep", timeout=30)
tui.wait_for("neuraldeep) qwen3.8-27b", timeout=20)
tui.wait_for("wallet", timeout=20)
tui.pump(1.0)
# The shot is only worth keeping if what it exists to show is on screen, and
# the fresh-install line it replaces is gone.
for row in (PAGE, CODE, "Signed in to NeuralDeep as dev (tier pro)", "Added to config.yaml", "neuraldeep) qwen3.8-27b"):
    if row not in tui.text():
        raise AssertionError(f"{row!r} is not on the captured screen:\n{tui.text()}")
if "Type /login" in tui.text():
    raise AssertionError(f"the fresh-install line is still on screen:\n{tui.text()}")
capture.snapshot(tui, OUT, NAME)
tui.send("\x03"); time.sleep(0.2); tui.send("\x03")
tui.pump(1)
render_png(OUT, NAME)
for ext in (".txt", ".html"):
    (OUT / f"{NAME}{ext}").unlink(missing_ok=True)
srv.shutdown()
shutil.rmtree(home, ignore_errors=True)
shutil.rmtree(work, ignore_errors=True)
