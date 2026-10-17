#!/usr/bin/env python3
"""Capture the console while a call to a shared model waits for a free slot.

A real lender (coddy serve with httpserver.shared_models.max_streams: 1)
shares neuraldeep/qwen3.8-27b as qwen38; its NeuralDeep provider points at a
fake hub (CODDY_NEURALDEEP_BASE_URL) that reports the account usage and holds
every chat completion open. One request posted straight to the lender takes
its only slot and keeps it. The console (build/coddy cli, a provider of type
coddy) then sends a prompt: the lender answers 429 kind busy, the turn waits
for the slot, and the status row counts down to the time it gives up while
the footer keeps the account usage of the alias. No real key; HOME and
CODDY_HOME are temporary.

Usage: python3 capture_busy.py [repo] [outdir]   (default docs/assets/cli-tui)
Needs a build/coddy with the cli and http tags (make build TAGS="http cli"),
pexpect, pyte and chromium for the PNG (cropped when Pillow is around).
CAPTURE_WORKDIR names the folder shown in the footer (default
/tmp/coddy-agent).
"""
import json, os, shutil, socket, subprocess, sys, tempfile, threading, time, urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

REPO = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path(__file__).resolve().parents[2]
OUT = Path(sys.argv[2]).resolve() if len(sys.argv) > 2 else REPO / "docs" / "assets" / "cli-tui"
sys.path.insert(0, str(REPO / "examples" / "cli"))
CODDY = os.environ.get("CODDY_BIN", str(REPO / "build" / "coddy"))
import pexpect, pyte  # noqa: E402
import capture  # noqa: E402
from cli_tui_driver import COLS, ROWS, CR  # noqa: E402

NAME = "23-coddy-busy-wait"
TOKEN = "lend-token-for-screenshots"
RELEASE = threading.Event()
HELD = threading.Event()


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def limits():
    now = time.time()
    iso = lambda t: time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(t))  # noqa: E731
    return {
        "schema": 1, "observed_at": iso(now), "tier": "pro", "tier_expires_at": None,
        "unlimited_volume": False, "options": [], "bypass": False, "fair_use": True,
        "key": {"name": "coddy", "status": "ok", "billing_mode": "wallet", "cap": None},
        "decision": {"scope": "chat", "can_request": True, "blockers": [], "retry_after_sec": None},
        "chat": {
            "session": {"used": 4650, "limit": 15000, "remaining": 10350, "reset_in_sec": 5820,
                        "resets_at": iso(now + 5820), "window": "3h"},
            "week": {"used": 41230, "limit": 150000, "remaining": 108770, "reset_in_sec": 190000,
                     "resets_at": iso(now + 190000), "window": "iso-week"},
            "rpm": {"used": 3, "limit": 120, "remaining": 117, "reset_in_sec": 41},
            "cooldown_sec": 0, "scope": "account"},
        "daily_capacity": {"pct_used": 0.0, "exhausted": False, "resets_at": iso(now + 40000)},
        "night": {"enabled": True, "active": False, "capacity_factor": 2},
        "wallet": {"balance_rub": 1250.0, "spent_rub_30d": 3470.5}, "kimi": None,
    }


class Hub(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.rstrip("/").endswith("/limits"):
            body = json.dumps(limits()).encode()
            self.send_response(200); self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)
            return
        self.send_response(404); self.end_headers()

    def do_POST(self):
        # Every model call stays open: the one that holds the lender's slot.
        self.rfile.read(int(self.headers.get("Content-Length") or 0))
        HELD.set()
        RELEASE.wait(timeout=300)
        self.send_response(503); self.end_headers()

    def log_message(self, *a):
        pass


class Shot:
    def __init__(self, env, cwd):
        self.screen = pyte.Screen(COLS, ROWS)
        self.stream = pyte.ByteStream(self.screen)
        self.child = pexpect.spawn(CODDY, ["cli", "--theme", "dark"], env=env, cwd=str(cwd),
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

    def wait_for(self, needle, timeout=40):
        deadline = time.time() + timeout
        while time.time() < deadline:
            self.pump(0.3)
            if needle in self.text():
                return
        raise AssertionError(f"never saw {needle!r}:\n{self.text()}")

    def send(self, text):
        self.child.send(text.encode())


def render_png(outdir, name):
    """The PNG through headless chromium, cropped to the last painted row when
    Pillow is around (as capture_usage.py does)."""
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


def main():
    stand = Path(tempfile.mkdtemp(prefix="coddy-busy-shot-"))
    lender_home, borrower_home, home = stand / "lender", stand / "borrower", stand / "home"
    for d in (lender_home, borrower_home, home):
        d.mkdir()
    # The footer prints the folder as it is: a neutral one, not a home path.
    work = Path(os.environ.get("CAPTURE_WORKDIR", "/tmp/coddy-agent"))
    work.mkdir(parents=True, exist_ok=True)
    port = free_port()

    hub = ThreadingHTTPServer(("127.0.0.1", 0), Hub)
    threading.Thread(target=hub.serve_forever, daemon=True).start()

    (lender_home / "config.yaml").write_text(f"""httpserver:
  host: "127.0.0.1"
  port: {port}
  shared_models:
    tokens:
      - "{TOKEN}"
    max_streams: 1
providers:
  - name: neuraldeep
    type: neuraldeep
    api_key: "sk-shot-fake-key-0123456789abcdef"
models:
  - model: "neuraldeep/qwen3.8-27b"
    max_context_tokens: 262144
    shared_as: "qwen38"
agent:
  model: "neuraldeep/qwen3.8-27b"
""")
    (borrower_home / "config.yaml").write_text(f"""providers:
  - name: studio
    type: coddy
    api_base: "http://127.0.0.1:{port}"
    api_key: "{TOKEN}"
    busy_wait_ms: 180000
models:
  - model: "studio/qwen38"
agent:
  model: "studio/qwen38"
""")
    for f in (lender_home / "config.yaml", borrower_home / "config.yaml"):
        f.chmod(0o600)

    env = {k: v for k, v in os.environ.items()
           if k not in ("HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "ALL_PROXY", "NEURALDEEP_API_KEY")}
    env.update({"HOME": str(home), "LANG": "en_US.UTF-8", "LC_ALL": "en_US.UTF-8"})
    lender_env = dict(env, CODDY_HOME=str(lender_home),
                      CODDY_NEURALDEEP_BASE_URL=f"http://127.0.0.1:{hub.server_port}")
    with open(stand / "lender.log", "w") as log:
        lender = subprocess.Popen([CODDY, "serve"], env=lender_env, cwd=str(home), stdout=log, stderr=log)
    try:
        auth = {"Authorization": f"Bearer {TOKEN}"}
        deadline = time.time() + 30
        while True:
            try:
                req = urllib.request.Request(f"http://127.0.0.1:{port}/coddy/llm/models", headers=auth)
                with urllib.request.urlopen(req, timeout=2) as r:
                    if r.status == 200:
                        break
            except Exception:
                if time.time() > deadline:
                    raise SystemExit(f"the lender never answered; see {stand / 'lender.log'}")
                time.sleep(0.3)

        # Take the lender's only slot with a call the hub never answers.
        def hold():
            body = json.dumps({"protocol": 1, "model": "qwen38",
                               "messages": [{"role": "user", "content": "hold the slot"}]}).encode()
            req = urllib.request.Request(f"http://127.0.0.1:{port}/coddy/llm/completions", data=body,
                                         headers=dict(auth, **{"Content-Type": "application/json"}))
            try:
                with urllib.request.urlopen(req, timeout=300) as r:
                    while r.read(1024):
                        pass
            except Exception:
                pass
        threading.Thread(target=hold, daemon=True).start()
        if not HELD.wait(timeout=30):
            raise SystemExit("the holding call never reached the hub")

        tui = Shot(dict(env, TERM="xterm-256color", COLORTERM="truecolor", CODDY_HOME=str(borrower_home)), work)
        tui.wait_for("3h ")
        tui.pump(0.5)
        tui.send("Summarise the open pull request" + CR)
        tui.wait_for("Waiting for a free slot on the remote")
        tui.pump(2.5)
        OUT.mkdir(parents=True, exist_ok=True)
        capture.snapshot(tui, OUT, NAME)
        print("busy wait captured")
        tui.send("\x1b")  # Esc stops the wait like any turn
        tui.pump(1)
        tui.send("\x03"); time.sleep(0.2); tui.send("\x03")
        tui.pump(1)
    finally:
        RELEASE.set()
        lender.terminate()
        try:
            lender.wait(timeout=10)
        except subprocess.TimeoutExpired:
            lender.kill()
        hub.shutdown()
        shutil.rmtree(stand, ignore_errors=True)

    render_png(OUT, NAME)
    # Only the PNG is kept: the text and HTML twins add nothing a reader of
    # the docs needs.
    for ext in (".txt", ".html"):
        (OUT / f"{NAME}{ext}").unlink(missing_ok=True)


main()
