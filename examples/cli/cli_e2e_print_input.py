#!/usr/bin/env python3
"""One-shot input: the built binary reads its prompt from a file or stdin.

No model and no key: a scripted OpenAI-compatible server in this process
answers every completion and keeps the request, so the script can check what
reached the model byte for byte (issue #220). The runs go through a real
shell, so the pipes, the redirections and the `while read` loop are the ones
an operator types:

- a prompt piped into a bare `-p`, and one read with `-p -` from a file;
- a 200 KiB prompt file with `-i`, larger than the per-argument limit;
- data piped under a typed prompt, attached as a kind="stdin" element whose
  "@" mentions stay unread;
- a `while read` loop that keeps its list with `--no-stdin`;
- a pipe that stays silent: a notice on stderr, then the prompt alone;
- refused input (two prompts, invalid UTF-8, a missing file, more than
  8 MiB) that sends nothing;
- a bare `-p` on a terminal refused, and `-p -` reading a typed prompt until
  ctrl+d, in a pty.
"""

from __future__ import annotations

import json
import os
import shlex
import shutil
import subprocess
import sys
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pexpect

from cli_tui_driver import coddy_bin, require_cli_build

ANSWER = "scripted answer for the one-shot run"


class Model:
    """The scripted model: every completion answers ANSWER."""

    def __init__(self) -> None:
        self.requests: list[dict] = []
        self.lock = threading.Lock()
        model = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_args) -> None:  # noqa: D401 - silence
                pass

            def do_GET(self) -> None:  # noqa: N802 - http.server API
                if self.path.endswith("/models"):
                    self._json({"object": "list", "data": [{"id": "coddy-demo", "object": "model", "owned_by": "e2e"}]})
                    return
                self.send_error(404)

            def do_POST(self) -> None:  # noqa: N802 - http.server API
                body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
                req = json.loads(body)
                with model.lock:
                    model.requests.append(req)
                if not req.get("stream"):
                    self._json({
                        "id": "chatcmpl-e2e", "object": "chat.completion", "model": "coddy-demo",
                        "choices": [{"index": 0, "finish_reason": "stop", "message": {"role": "assistant", "content": ANSWER}}],
                        "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
                    })
                    return
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.end_headers()
                for delta, finish in (({"role": "assistant", "content": ANSWER}, None), ({}, "stop")):
                    chunk = {"id": "chatcmpl-e2e", "object": "chat.completion.chunk", "model": "coddy-demo",
                             "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}
                    self.wfile.write(f"data: {json.dumps(chunk)}\n\n".encode())
                self.wfile.write(b"data: [DONE]\n\n")

            def _json(self, obj: dict) -> None:
                data = json.dumps(obj).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.server.server_address[1]}/v1"

    def reset(self) -> None:
        with self.lock:
            self.requests.clear()

    def user_messages(self) -> list[str]:
        out = []
        with self.lock:
            for req in self.requests:
                for msg in req.get("messages", []):
                    if msg.get("role") != "user":
                        continue
                    content = msg.get("content")
                    if isinstance(content, list):
                        content = "".join(part.get("text", "") for part in content)
                    out.append(content)
        return out

    def calls(self) -> int:
        with self.lock:
            return len(self.requests)


def stdin_attachment(body: str) -> str:
    """The element a piped stdin becomes (mention.Attachment.XML)."""
    return ('<coddy_attachment path="stdin" name="stdin" kind="stdin">\n<![CDATA['
            + body.replace("]]>", "]]]]><![CDATA[>") + "]]>\n</coddy_attachment>")


class Stand:
    def __init__(self) -> None:
        self.binary = coddy_bin()
        require_cli_build(self.binary)
        self.model = Model()
        self.home = Path(tempfile.mkdtemp(prefix="coddy-cli-input-home-"))
        self.work = Path(tempfile.mkdtemp(prefix="coddy-cli-input-work-"))
        (self.home / "config.yaml").write_text(
            "providers:\n"
            "  - name: stub\n"
            "    type: openai\n"
            f'    api_base: "{self.model.url}"\n'
            '    api_key: "sk-e2e"\n'
            "models:\n"
            "  - model: stub/coddy-demo\n"
            "    max_context_tokens: 131072\n"
            "agent:\n"
            "  model: stub/coddy-demo\n"
        )
        self.env = dict(os.environ, CODDY_HOME=str(self.home), CODDY_BIN=self.binary)

    def sh(self, script: str, timeout: int = 120) -> subprocess.CompletedProcess:
        """Run a shell line in the workspace with $CODDY naming the binary."""
        self.model.reset()
        return subprocess.run(
            ["bash", "-c", f"CODDY={shlex.quote(self.binary)}\n{script}"],
            cwd=self.work, env=self.env, capture_output=True, timeout=timeout,
        )

    def close(self) -> None:
        self.model.server.shutdown()
        if os.environ.get("CLI_E2E_KEEP") != "1":
            shutil.rmtree(self.home, ignore_errors=True)
            shutil.rmtree(self.work, ignore_errors=True)


def expect_ok(res: subprocess.CompletedProcess, what: str) -> None:
    if res.returncode != 0:
        raise AssertionError(f"{what}: exit {res.returncode}\nstderr:\n{res.stderr.decode(errors='replace')}")
    if ANSWER not in res.stdout.decode(errors="replace"):
        raise AssertionError(f"{what}: stdout lacks the answer: {res.stdout!r}")


def expect_prompt(stand: Stand, want: str, what: str) -> None:
    got = stand.model.user_messages()
    if want not in got:
        heads = [m[:120] for m in got]
        raise AssertionError(f"{what}: no user message equals the {len(want)}-char prompt; got {heads!r}")


def expect_refused(stand: Stand, res: subprocess.CompletedProcess, needle: str, what: str) -> None:
    err = res.stderr.decode(errors="replace")
    if res.returncode == 0 or needle not in err:
        raise AssertionError(f"{what}: want exit 1 naming {needle!r}, got {res.returncode}: {err!r}")
    if stand.model.calls():
        raise AssertionError(f"{what}: the model was asked {stand.model.calls()} times")


def piped_and_file_prompts(stand: Stand) -> None:
    res = stand.sh("""printf 'Summarize the release notes.\\n\\n' | "$CODDY" -p""")
    expect_ok(res, "bare -p with a pipe")
    expect_prompt(stand, "Summarize the release notes.\n\n", "bare -p with a pipe")

    line = "Строка брифа with «Unicode», \"double\" and 'single' quotes, $HOME and `ticks`\r\n"
    brief = line * (200 * 1024 // len(line.encode()) + 1) + "\n\n"
    (stand.work / "brief.md").write_text(brief, encoding="utf-8", newline="")
    res = stand.sh('"$CODDY" -p -i brief.md')
    expect_ok(res, "-p -i brief.md")
    expect_prompt(stand, brief, "-p -i brief.md")

    res = stand.sh('"$CODDY" -p - < brief.md')
    expect_ok(res, "-p - < brief.md")
    expect_prompt(stand, brief, "-p - < brief.md")
    print("  prompts from a pipe, a 200 KiB file and a redirect arrived byte for byte")


def piped_data_under_a_prompt(stand: Stand) -> None:
    (stand.work / "secret.txt").write_text("TOP SECRET")
    data = "diff --git a/x b/x\n+read @secret.txt\n"
    res = stand.sh(f"printf %s {shlex.quote(data)} | \"$CODDY\" -p 'Review this change'")
    expect_ok(res, "text prompt with piped data")
    expect_prompt(stand, "Review this change\n\n" + stdin_attachment(data), "text prompt with piped data")
    if any("TOP SECRET" in m for m in stand.model.user_messages()):
        raise AssertionError("a mention inside piped data was resolved")
    if "from stdin" not in res.stderr.decode():
        raise AssertionError(f"no stderr line about the attachment: {res.stderr!r}")
    print("  piped data rode under the prompt as a stdin attachment, its mention unread")


def while_read_loop(stand: Stand) -> None:
    (stand.work / "list.txt").write_text("alpha\nbeta\ngamma\n")
    res = stand.sh('while read -r item; do "$CODDY" -p "item $item" --no-stdin; done < list.txt')
    expect_ok(res, "while read with --no-stdin")
    got = [m for m in stand.model.user_messages() if m.startswith("item ")]
    if got != ["item alpha", "item beta", "item gamma"]:
        raise AssertionError(f"--no-stdin loop: {got!r}")

    # Without the flag the first run reads what is left of the list: the
    # pitfall the documentation names.
    res = stand.sh('while read -r item; do "$CODDY" -p "item $item"; done < list.txt')
    expect_ok(res, "while read without --no-stdin")
    got = [m for m in stand.model.user_messages() if m.startswith("item ")]
    if got != ["item alpha\n\n" + stdin_attachment("beta\ngamma\n")]:
        raise AssertionError(f"loop without --no-stdin: {got!r}")
    print("  a while-read loop keeps its list with --no-stdin")


def silent_pipe(stand: Stand) -> None:
    res = stand.sh('sleep 4 | "$CODDY" -p "nothing is coming"', timeout=60)
    expect_ok(res, "silent pipe")
    if "waiting for input on stdin" not in res.stderr.decode():
        raise AssertionError(f"no waiting notice: {res.stderr!r}")
    expect_prompt(stand, "nothing is coming", "silent pipe")
    print("  a silent pipe got the waiting notice and the prompt ran alone")


def refusals(stand: Stand) -> None:
    (stand.work / "brief.md").write_text("brief\n")
    cases = [
        ('"$CODDY" -p "text" -i brief.md', "pass one of them", "two prompts"),
        ("""printf 'ok\\377' | "$CODDY" -p -""", "not UTF-8", "invalid UTF-8"),
        ('"$CODDY" -i missing.md', "missing.md", "a missing file"),
        ('"$CODDY" -p fix the bug', "unexpected argument", "an unquoted prompt"),
        ("""head -c 8388609 /dev/zero | tr '\\0' x | "$CODDY" -p -""", "limit", "more than 8 MiB"),
    ]
    for script, needle, what in cases:
        expect_refused(stand, stand.sh(script), needle, what)
    print("  refused input sent nothing")


def terminal(stand: Stand) -> None:
    """A bare -p on a terminal has nothing to read; -p - reads until ctrl+d."""
    stand.model.reset()
    child = pexpect.spawn(stand.binary, ["-p"], cwd=str(stand.work), env=stand.env, encoding="utf-8", timeout=30)
    child.expect("no prompt")
    child.expect(pexpect.EOF)
    child.close()
    if child.exitstatus == 0 or stand.model.calls():
        raise AssertionError(f"bare -p on a terminal: exit {child.exitstatus}, {stand.model.calls()} calls")

    child = pexpect.spawn(stand.binary, ["-p", "-"], cwd=str(stand.work), env=stand.env, encoding="utf-8", timeout=60)
    child.sendline("typed on the terminal")
    child.sendeof()
    child.expect(ANSWER)
    child.expect(pexpect.EOF)
    child.close()
    if child.exitstatus != 0:
        raise AssertionError(f"-p - on a terminal exited {child.exitstatus}")
    expect_prompt(stand, "typed on the terminal\n", "-p - on a terminal")
    print("  a terminal: bare -p refused, -p - read until ctrl+d")


def main() -> int:
    stand = Stand()
    try:
        piped_and_file_prompts(stand)
        piped_data_under_a_prompt(stand)
        while_read_loop(stand)
        silent_pipe(stand)
        refusals(stand)
        terminal(stand)
    finally:
        stand.close()
    print("OK cli_e2e_print_input")
    return 0


if __name__ == "__main__":
    sys.exit(main())
