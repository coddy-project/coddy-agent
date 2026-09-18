#!/usr/bin/env python3
"""Capture a background wake in the console and the /tasks marks it leaves.

Runs the scripted model of cmd/tgfake (`--llm`, rules from a JSON script), so
the shots need no provider and no key. The model starts `make test` in the
background with notify_on_finish; the target fails after a few seconds and the
console's waker starts a turn in which the agent reports the failure. That turn
shows nothing of its own: it reads as the agent carrying on. The model then
starts a long `make build` that will wake it too. One PNG: the /tasks overlay
under the transcript, where the build says `wakes the agent` and the failed
test run `woke the agent`.

Usage: python3 capture_wake.py [repo] [outdir]   (default docs/assets/cli-tui)
Needs a cli-tagged build/coddy (make build TAGS=cli), a Go toolchain for
cmd/tgfake, pexpect, pyte and chromium for the PNG.
"""
import json, os, shutil, signal, subprocess, sys, tempfile, time
from pathlib import Path

REPO = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path(__file__).resolve().parents[2]
OUT = Path(sys.argv[2]).resolve() if len(sys.argv) > 2 else REPO / "docs" / "assets" / "cli-tui"
sys.path.insert(0, str(REPO / "examples" / "cli"))
sys.path.insert(0, str(REPO / "examples" / "shared"))
os.environ.setdefault("CODDY_BIN", str(REPO / "build" / "coddy"))
import pexpect, pyte  # noqa: E402
import capture  # noqa: E402
from cli_tui_driver import COLS, ROWS, CR  # noqa: E402
from wake_e2e_common import free_port  # noqa: E402

MODEL = "stub/coddy-demo"
WOKEN_ANSWER = ("The tests failed: `TestParseHeaders` expects the header map to keep the original case, "
                "and the parser lowercases it. I will fix the parser next.")
RULES = [
    {"match": "run the tests", "tool": {"name": "run_command", "arguments": {
        "command": "make test", "background": True, "notify_on_finish": True, "expected_seconds": 5}},
     "answer": "Started the tests in the background; I will pick up the result when they finish."},
    {"match": "start the build", "tool": {"name": "run_command", "arguments": {
        "command": "make build", "background": True, "notify_on_finish": True, "expected_seconds": 600}},
     "answer": "The build is running in the background; I will be woken when it ends."},
    {"match": "background task you asked to be notified about", "answer": WOKEN_ANSWER},
]

tmp = Path(tempfile.mkdtemp(prefix="coddy-wake-shot-"))
home, work = tmp / "home", Path(os.environ.get("CAPTURE_WORKDIR", tmp / "work"))
home.mkdir()
work.mkdir(parents=True, exist_ok=True)
(tmp / "rules.json").write_text(json.dumps(RULES))
# The test run fails after a few seconds; the build stays up for the overlay.
(work / "Makefile").write_text(
    "test:\n\t@echo '=== RUN   TestParseHeaders'; sleep 3; echo '--- FAIL: TestParseHeaders (3.01s)'; exit 1\n"
    "build:\n\t@echo 'building...'; sleep 900\n")

tgfake = tmp / "tgfake"
subprocess.run(["go", "build", "-o", str(tgfake), "./cmd/tgfake"], cwd=REPO, check=True)
model_port = free_port()
model = subprocess.Popen([str(tgfake), "--addr", f"127.0.0.1:{model_port}", "--llm",
                          "--llm-script", str(tmp / "rules.json"), "--llm-delay", "20ms"],
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
(home / "config.yaml").write_text(f"""providers:
  - name: stub
    type: openai
    api_base: "http://127.0.0.1:{model_port}/v1"
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


class Shot:
    """The pty stand of capture_tasks.py: a coddy console on a pyte screen."""

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


NAMES = ["17-tasks-wake"]
OUT.mkdir(parents=True, exist_ok=True)
tui = Shot()
try:
    tui.wait_for("coddy v", timeout=30)
    tui.pump(1.0)
    tui.send("Please run the tests in the background and tell me what fails." + CR)
    tui.wait_for("pick up the result", timeout=30)
    # The test run fails a few seconds later and the agent carries on by itself.
    tui.wait_for("fix the parser next", timeout=60)
    for unwanted in ("background task you asked to be notified about", "Woken by"):
        if unwanted in tui.text():
            raise AssertionError(f"the woken turn shows {unwanted!r}:\n{tui.text()}")
    tui.pump(1.0)
    tui.send("Now start the build in the background too." + CR)
    tui.wait_for("woken when it ends", timeout=30)
    tui.wait_for("1 task running (/tasks)", timeout=20)
    tui.pump(0.5)

    tui.send("/tasks" + CR)
    tui.wait_for("Background tasks", timeout=10)
    tui.wait_for("wakes the agent", timeout=10)
    tui.wait_for("woke the agent", timeout=10)
    capture.snapshot(tui, OUT, NAMES[0])
    print("tasks overlay captured")
finally:
    tui.send("\x1b"); tui.send("\x1b")
    tui.send("\x03"); time.sleep(0.2); tui.send("\x03")
    tui.pump(1)
    model.terminate()
    # A background task outlives the console by design, and a stop from the
    # overlay would wake the agent to report it: the build is ended here, by
    # the process group its record names.
    for meta in home.glob("sessions/**/background/*/meta.json"):
        record = json.loads(meta.read_text())
        if record.get("status") == "running" and record.get("pid"):
            try:
                os.killpg(record["pid"], signal.SIGTERM)
            except ProcessLookupError:
                pass
render_pngs(OUT, NAMES)
for name in NAMES:
    for ext in (".txt", ".html"):
        (OUT / f"{name}{ext}").unlink(missing_ok=True)
if not os.environ.get("KEEP"):
    shutil.rmtree(tmp, ignore_errors=True)
