#!/usr/bin/env python3
"""Drive /mcp through a real pty and inspect the tools of consecutive turns.

A global MCP server offers one tool. The console runs a turn, switches the
server off from /mcp, and runs another turn: the model must be offered the
tool on the first request and not on the second. Requests are matched by the
prompt they carry, not by their order, so an extra request the console makes
(a title, a retry) cannot shift the check. The temporary home and workspace
are removed at the end.
"""

import json
import os
import shutil
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

import pexpect
import pyte


PROMPTS = ("before", "after")

# prompt of the turn -> the tool names of each request that turn made
offered = {}
offered_lock = threading.Lock()


def turn_prompt(body):
    """The latest of PROMPTS among the user messages of a request, or ""."""
    texts = []
    for message in body.get("messages", []):
        if message.get("role") != "user":
            continue
        content = message.get("content")
        if isinstance(content, list):
            content = " ".join(part.get("text", "") for part in content if isinstance(part, dict))
        texts.append(str(content or ""))
    for prompt in reversed(PROMPTS):
        if any(prompt in text for text in texts):
            return prompt
    return ""


class Model(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def do_GET(self):
        data = json.dumps({"data": [{"id": "stub/demo", "owned_by": "stub"}]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
        if body.get("stream"):
            tools = [tool.get("function", {}).get("name", "") for tool in body.get("tools", [])]
            with offered_lock:
                offered.setdefault(turn_prompt(body), []).append(tools)
            payload = []
            for delta, finish in [({"content": "Ready."}, None), ({}, "stop")]:
                payload.append("data: " + json.dumps({"id": "stub", "object": "chat.completion.chunk", "created": 1,
                    "model": "stub/demo", "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}) + "\n\n")
            payload.append("data: [DONE]\n\n")
            data = "".join(payload).encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
        else:
            data = json.dumps({"id": "stub", "object": "chat.completion", "created": 1, "model": "stub/demo",
                "choices": [{"index": 0, "finish_reason": "stop", "message": {"role": "assistant", "content": "Ready."}}]}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


def tools_for(prompt):
    with offered_lock:
        seen = offered.get(prompt)
        return list(seen[-1]) if seen else None


def wait_for(screen, child, predicate, label, timeout=20):
    end = time.monotonic() + timeout
    text = ""
    while time.monotonic() < end:
        try:
            screen.stream.feed(child.read_nonblocking(size=65536, timeout=0.1))
        except pexpect.TIMEOUT:
            pass
        text = "\n".join(screen.display)
        if predicate(text):
            return
    raise AssertionError(f"timed out waiting for {label}:\n{text}")


def main():
    model = HTTPServer(("127.0.0.1", 0), Model)
    threading.Thread(target=model.serve_forever, daemon=True).start()
    home = Path(tempfile.mkdtemp(prefix="coddy-mcp-pty-home-"))
    cwd = Path(tempfile.mkdtemp(prefix="coddy-mcp-pty-work-"))
    helper = home / "mcp_server.py"
    helper.write_text("""import json, sys
for line in sys.stdin:
    req = json.loads(line)
    if 'id' not in req: continue
    method = req.get('method')
    if method == 'initialize':
        result = {'protocolVersion':'2024-11-05','capabilities':{},'serverInfo':{'name':'toggle','version':'1'}}
    elif method == 'tools/list':
        result = {'tools':[{'name':'ping','description':'Ping','inputSchema':{'type':'object'}}]}
    else: result = {}
    print(json.dumps({'jsonrpc':'2.0','id':req['id'],'result':result}), flush=True)
""")
    (home / "mcp.json").write_text(json.dumps({"mcpServers": {
        "toggle-mcp": {"command": sys.executable, "args": [str(helper)]}}}))
    (home / "config.yaml").write_text(f"""providers:
  - name: stub
    type: openai
    api_base: http://127.0.0.1:{model.server_port}/v1
    api_key: sk-test
models:
  - model: stub/demo
    max_context_tokens: 8192
agent:
  model: stub/demo
tools:
  permission_mode: bypass
""")
    env = dict(os.environ, CODDY_HOME=str(home), TERM="xterm-256color", COLORTERM="truecolor")
    child = pexpect.spawn(env.get("CODDY_BIN", "build/coddy"), ["cli", "--theme", "dark"],
                          cwd=str(cwd), env=env, dimensions=(35, 128), encoding=None, timeout=5)
    screen = pyte.Screen(128, 35)
    screen.stream = pyte.ByteStream(screen)
    try:
        wait_for(screen, child, lambda s: "coddy v" in s, "console startup")
        child.send(b"before\r")
        wait_for(screen, child, lambda _s: tools_for("before") is not None, "the turn before the switch")
        child.send(b"/mcp\r")
        wait_for(screen, child, lambda s: "toggle-mcp" in s and "connected" in s, "MCP list")
        child.send(b"\r")  # open the selected server
        wait_for(screen, child, lambda s: "Toggle server" in s, "MCP controls")
        child.send(b"\r")  # switch it off
        wait_for(screen, child, lambda s: "toggle-mcp" in s and "disabled" in s, "disabled MCP list")
        child.send(b"\x1b")
        child.send(b"after\r")
        wait_for(screen, child, lambda _s: tools_for("after") is not None, "the turn after the switch")
        before, after = tools_for("before"), tools_for("after")
        if "toggle-mcp__ping" not in before:
            raise AssertionError(f"MCP tool missing before the switch: {before}")
        if "toggle-mcp__ping" in after:
            raise AssertionError(f"switched-off MCP tool offered on the next turn: {after}")
        print("pty /mcp: server tool present before the switch and absent on the next turn")
    finally:
        child.close(force=True)
        model.shutdown()
        shutil.rmtree(home, ignore_errors=True)
        shutil.rmtree(cwd, ignore_errors=True)


if __name__ == "__main__":
    main()
