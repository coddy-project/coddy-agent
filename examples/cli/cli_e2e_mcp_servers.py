#!/usr/bin/env python3
"""Drive the console with an MCP server of every kind and call each one's tool.

Four servers offer one tool each, get_token, which answers with the server's
own token: a native program (compiled from Go here and run by its path), an
npm package started through the real `npx -y` (a local folder, so no registry
is asked), a remote server over streamable HTTP and one over the legacy
HTTP+SSE transport, both served by this script. A scripted model, served by
this script too, calls the four tools one after another and answers with what
they returned. The console must draw its first frame while the servers come
up, list all four as connected in /mcp, and show the four tokens in the
answer; the model must have received each server's own token. The temporary
home, workspace and npm cache are removed at the end.

Needs pexpect and pyte (examples/cli/requirements.txt), go and npx on PATH,
and a cli-tagged binary (CODDY_BIN, default build/coddy).
"""

import itertools
import json
import os
import queue
import shutil
import subprocess
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pexpect
import pyte


PROMPT = "ask every server"
SERVERS = ("native", "packaged", "remote", "legacy")
TOKENS = {"native": "NATIVE-OK", "packaged": "NPX-OK", "remote": "HTTP-OK", "legacy": "SSE-OK"}
TOOL = "get_token"

# tool name -> the result the model received for it, filled by the model
received = {}
received_lock = threading.Lock()


# ---- the MCP servers ----

NATIVE_SERVER = r"""package main

import (
	"bufio"
	"encoding/json"
	"os"
)

func main() {
	token := os.Getenv("CODDY_E2E_TOKEN")
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<16), 1<<22)
	out := json.NewEncoder(os.Stdout)
	for in.Scan() {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(in.Bytes(), &req) != nil || req.ID == nil {
			continue
		}
		var result any = map[string]any{}
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]any{"name": "native", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "get_token", "description": "Answer with this server's token",
				"inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": token}}}
		}
		_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
}
"""

NPX_SERVER = r"""#!/usr/bin/env node
const readline = require('readline');
const token = process.env.CODDY_E2E_TOKEN || '';
readline.createInterface({ input: process.stdin }).on('line', (line) => {
  let req;
  try { req = JSON.parse(line); } catch (e) { return; }
  if (req.id === undefined || req.id === null) return;
  let result = {};
  if (req.method === 'initialize') {
    result = { protocolVersion: '2024-11-05', capabilities: { tools: {} }, serverInfo: { name: 'packaged', version: '1' } };
  } else if (req.method === 'tools/list') {
    result = { tools: [{ name: 'get_token', description: "Answer with this server's token", inputSchema: { type: 'object' } }] };
  } else if (req.method === 'tools/call') {
    result = { content: [{ type: 'text', text: token }] };
  }
  process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id: req.id, result }) + '\n');
});
"""


def mcp_answer(message, token):
    """The JSON-RPC response to one MCP message, or None for a notification."""
    if message.get("id") is None:
        return None
    method = message.get("method")
    result = {}
    if method == "initialize":
        result = {"protocolVersion": "2024-11-05", "capabilities": {"tools": {}},
                  "serverInfo": {"name": "e2e", "version": "1"}}
    elif method == "tools/list":
        result = {"tools": [{"name": TOOL, "description": "Answer with this server's token",
                             "inputSchema": {"type": "object"}}]}
    elif method == "tools/call":
        result = {"content": [{"type": "text", "text": token}]}
    return {"jsonrpc": "2.0", "id": message["id"], "result": result}


class StreamableHTTP(BaseHTTPRequestHandler):
    """Streamable HTTP: every message is POSTed and answered in the body."""

    def log_message(self, *_args):
        pass

    def do_POST(self):
        message = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))) or b"{}")
        reply = mcp_answer(message, TOKENS["remote"])
        if reply is None:
            self.send_response(202)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        data = json.dumps(reply).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


sse_sessions = {}
sse_lock = threading.Lock()
sse_ids = itertools.count(1)


class LegacySSE(BaseHTTPRequestHandler):
    """HTTP+SSE: a GET opens the event stream, which names the endpoint the
    messages are POSTed to and then carries every response."""

    def log_message(self, *_args):
        pass

    def do_GET(self):
        out = queue.Queue()
        with sse_lock:
            session = str(next(sse_ids))
            sse_sessions[session] = out
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        try:
            self.wfile.write(f"event: endpoint\ndata: /messages?session={session}\n\n".encode())
            self.wfile.flush()
            while True:
                try:
                    data = out.get(timeout=1)
                except queue.Empty:
                    # A comment line keeps the stream alive and finds out
                    # when the client has gone, so the handler ends with it.
                    self.wfile.write(b": ping\n\n")
                    self.wfile.flush()
                    continue
                self.wfile.write(b"event: message\ndata: " + data + b"\n\n")
                self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass
        finally:
            with sse_lock:
                sse_sessions.pop(session, None)

    def do_POST(self):
        session = self.path.split("session=", 1)[-1]
        message = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))) or b"{}")
        with sse_lock:
            out = sse_sessions.get(session)
        self.send_response(202 if out else 404)
        self.send_header("Content-Length", "0")
        self.end_headers()
        reply = mcp_answer(message, TOKENS["legacy"])
        if out is not None and reply is not None:
            out.put(json.dumps(reply).encode())


# ---- the model ----

def texts_of(message):
    content = message.get("content")
    if isinstance(content, list):
        return " ".join(part.get("text", "") for part in content if isinstance(part, dict))
    return str(content or "")


def tool_results(body):
    """tool name -> content of its result, from the request's history."""
    names = {}
    results = {}
    for message in body.get("messages", []):
        for call in message.get("tool_calls") or []:
            names[call.get("id")] = call.get("function", {}).get("name", "")
        if message.get("role") == "tool":
            results[names.get(message.get("tool_call_id"), "")] = texts_of(message)
    return results


class Model(BaseHTTPRequestHandler):
    """A scripted OpenAI-compatible model: a turn that carries PROMPT calls
    the tool of every server in turn, then answers with their results; any
    other request (a title) is answered with a word."""

    def log_message(self, *_args):
        pass

    def do_GET(self):
        self.reply(200, "application/json", json.dumps({"data": [{"id": "stub/demo", "owned_by": "stub"}]}).encode())

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
        stream = bool(body.get("stream"))
        users = [texts_of(m) for m in body.get("messages", []) if m.get("role") == "user"]
        if not any(PROMPT in text for text in users):
            return self.answer("Ready.", stream)
        results = tool_results(body)
        for name in SERVERS:
            tool = f"{name}__{TOOL}"
            if tool not in results:
                return self.call(tool, "call_" + name, stream)
        with received_lock:
            received.update(results)
        parts = [f"{name}={results[f'{name}__{TOOL}']}" for name in SERVERS]
        return self.answer("Answers: " + ", ".join(parts), stream)

    def reply(self, code, kind, data):
        self.send_response(code)
        self.send_header("Content-Type", kind)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def chunk(self, delta, finish):
        return "data: " + json.dumps({"id": "stub", "object": "chat.completion.chunk", "created": 1, "model": "stub/demo",
                                       "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}) + "\n\n"

    def answer(self, text, stream):
        if not stream:
            return self.reply(200, "application/json", json.dumps({
                "id": "stub", "object": "chat.completion", "created": 1, "model": "stub/demo",
                "choices": [{"index": 0, "finish_reason": "stop", "message": {"role": "assistant", "content": text}}]}).encode())
        payload = self.chunk({"role": "assistant", "content": text}, None) + self.chunk({}, "stop") + "data: [DONE]\n\n"
        return self.reply(200, "text/event-stream", payload.encode())

    def call(self, tool, call_id, stream):
        function = {"id": call_id, "type": "function", "function": {"name": tool, "arguments": "{}"}}
        if not stream:
            return self.reply(200, "application/json", json.dumps({
                "id": "stub", "object": "chat.completion", "created": 1, "model": "stub/demo",
                "choices": [{"index": 0, "finish_reason": "tool_calls",
                             "message": {"role": "assistant", "content": None, "tool_calls": [function]}}]}).encode())
        payload = (self.chunk({"role": "assistant", "tool_calls": [dict(function, index=0)]}, None)
                   + self.chunk({}, "tool_calls") + "data: [DONE]\n\n")
        return self.reply(200, "text/event-stream", payload.encode())


# ---- the console ----

def serve(handler):
    server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
    server.daemon_threads = True
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


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
            return text
    raise AssertionError(f"timed out waiting for {label}:\n{text}")


def build_native(root):
    """Compile the native server; the console runs it by its path."""
    go = shutil.which("go")
    if not go:
        raise SystemExit("cli_e2e_mcp_servers: go is not on PATH (it builds the native MCP server)")
    src = root / "native"
    src.mkdir()
    (src / "main.go").write_text(NATIVE_SERVER)
    binary = src / ("mcp-native.exe" if os.name == "nt" else "mcp-native")
    env = dict(os.environ, GOFLAGS="", GO111MODULE="on")
    subprocess.run([go, "build", "-o", str(binary), "main.go"], cwd=src, env=env, check=True)
    return binary


def write_package(root):
    """A local npm package whose bin is the stdio server npx starts."""
    if not shutil.which("npx"):
        raise SystemExit("cli_e2e_mcp_servers: npx is not on PATH (it starts the packaged MCP server)")
    pkg = root / "package"
    pkg.mkdir()
    (pkg / "package.json").write_text(json.dumps(
        {"name": "coddy-e2e-mcp", "version": "1.0.0", "bin": {"coddy-e2e-mcp": "server.js"}}))
    (pkg / "server.js").write_text(NPX_SERVER)
    os.chmod(pkg / "server.js", 0o755)
    return pkg


def main():
    root = Path(tempfile.mkdtemp(prefix="coddy-mcp-kinds-"))
    home, cwd = root / "home", root / "work"
    home.mkdir()
    cwd.mkdir()
    servers = [serve(Model), serve(StreamableHTTP), serve(LegacySSE)]
    model, remote, legacy = servers
    child = None
    try:
        native = build_native(root)
        package = write_package(root)
        mcp_servers = [
            {"name": "native", "command": str(native),
             "env": [{"name": "CODDY_E2E_TOKEN", "value": TOKENS["native"]}]},
            {"name": "packaged", "command": "npx", "args": ["-y", str(package)],
             "env": [{"name": "CODDY_E2E_TOKEN", "value": TOKENS["packaged"]},
                     {"name": "npm_config_cache", "value": str(root / "npm-cache")},
                     {"name": "npm_config_update_notifier", "value": "false"}]},
            {"name": "remote", "type": "http", "url": f"http://127.0.0.1:{remote.server_port}/mcp"},
            {"name": "legacy", "type": "sse", "url": f"http://127.0.0.1:{legacy.server_port}/sse"},
        ]
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
        # The servers are declared where Coddy reads them: <home>/mcp.json.
        entries = {}
        for server in mcp_servers:
            entry = {key: value for key, value in server.items() if key != "name"}
            if "env" in entry:
                entry["env"] = {pair["name"]: pair["value"] for pair in entry["env"]}
            entries[server["name"]] = entry
        (home / "mcp.json").write_text(json.dumps({"mcpServers": entries}, indent=2) + "\n")
        env = dict(os.environ, CODDY_HOME=str(home), TERM="xterm-256color", COLORTERM="truecolor")
        binary = os.path.abspath(env.get("CODDY_BIN", "build/coddy"))
        started = time.monotonic()
        child = pexpect.spawn(binary, ["cli", "--theme", "dark"],
                              cwd=str(cwd), env=env, dimensions=(35, 140), encoding=None, timeout=5)
        screen = pyte.Screen(140, 35)
        screen.stream = pyte.ByteStream(screen)
        wait_for(screen, child, lambda s: "coddy v" in s, "the first frame", timeout=5)
        first_frame = time.monotonic() - started

        child.send(PROMPT.encode() + b"\r")
        wanted = [f"{name}={TOKENS[name]}" for name in SERVERS]
        wait_for(screen, child, lambda s: all(w in s for w in wanted), "the answer with every token", timeout=90)
        with received_lock:
            got = {name: received.get(f"{name}__{TOOL}", "") for name in SERVERS}
        for name in SERVERS:
            if TOKENS[name] not in got[name]:
                raise AssertionError(f"the model got {got[name]!r} from {name}, want {TOKENS[name]!r}: {got}")

        child.send(b"/mcp\r")
        wait_for(screen, child, lambda s: all(name in s for name in SERVERS) and s.count("connected") >= 4,
                 "four connected servers in /mcp")
        child.send(b"\x1b")
        print(f"pty MCP servers: first frame in {first_frame * 1000:.0f} ms; "
              "a native binary, an npx package, streamable HTTP and SSE all connected and answered their tool")
    finally:
        if child is not None:
            child.close(force=True)
        for server in servers:
            server.shutdown()
        shutil.rmtree(root, ignore_errors=True)


if __name__ == "__main__":
    main()
