#!/usr/bin/env python3
"""A stdio MCP server for stands and captures: one tool, and a start it can delay.

Speaks just enough of the protocol for Coddy to connect: it answers
`initialize` and `tools/list` (one tool, `probe`) and echoes anything else
as an empty result. `--delay SECONDS` holds the `initialize` answer for that
long, and `--hang` never answers it at all - the shape of a server stuck in
its handshake, which is what the console's background connect and the MCP
footer count are about (coddy-project/coddy-agent#319).

Usage: mcp_stub_server.py [--delay SECONDS | --hang] [--name NAME]
"""

from __future__ import annotations

import argparse
import json
import sys
import time


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--delay", type=float, default=0.0, help="seconds to hold the initialize answer")
    ap.add_argument("--hang", action="store_true", help="never answer initialize")
    ap.add_argument("--name", default="stub", help="the server's name in serverInfo")
    args = ap.parse_args()

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            request = json.loads(line)
        except ValueError:
            continue
        if "id" not in request:
            continue  # a notification
        method = request.get("method")
        result: dict = {}
        if method == "initialize":
            if args.hang:
                while True:
                    time.sleep(3600)
            if args.delay > 0:
                time.sleep(args.delay)
            result = {
                "protocolVersion": "2024-11-05",
                "capabilities": {"tools": {}},
                "serverInfo": {"name": args.name, "version": "1"},
            }
        elif method == "tools/list":
            result = {
                "tools": [
                    {
                        "name": "probe",
                        "description": "Answers with the text it was given.",
                        "inputSchema": {"type": "object", "properties": {"text": {"type": "string"}}},
                    }
                ]
            }
        elif method == "tools/call":
            text = str((request.get("params") or {}).get("arguments", {}).get("text", ""))
            result = {"content": [{"type": "text", "text": text or "probe"}]}
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}) + "\n")
        sys.stdout.flush()
    return 0


if __name__ == "__main__":
    sys.exit(main())
