#!/usr/bin/env python3
"""Drive /mcp through a real pty and inspect tools on consecutive turns."""

import json
import os
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

import pexpect
import pyte


requests = []


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
            requests.append([tool.get("function", {}).get("name", "") for tool in body.get("tools", [])])
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


def wait_for(screen, child, predicate, label, timeout=20):
    end = time.monotonic() + timeout
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
        wait_for(screen, child, lambda _s: len(requests) >= 1, "first model request")
        child.send(b"/mcp\r")
        wait_for(screen, child, lambda s: "toggle-mcp" in s and "connected" in s, "MCP list")
        child.send(b"\r")  # open the selected server
        wait_for(screen, child, lambda s: "Toggle server" in s, "MCP controls")
        child.send(b"\r")  # disable it
        wait_for(screen, child, lambda s: "toggle-mcp" in s and "disabled" in s, "disabled MCP list")
        child.send(b"\x1b")
        child.send(b"after\r")
        wait_for(screen, child, lambda _s: len(requests) >= 2, "next model request")
        if "toggle-mcp__ping" not in requests[0]:
            raise AssertionError(f"MCP tool missing before disable: {requests[0]}")
        if "toggle-mcp__ping" in requests[1]:
            raise AssertionError(f"disabled MCP tool offered on next turn: {requests[1]}")
        print("pty /mcp: server tool present before disable and absent on next turn")
    finally:
        child.close(force=True)
        model.shutdown()


if __name__ == "__main__":
    main()
