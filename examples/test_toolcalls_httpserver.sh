#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

export CODDY_CONFIG="${CODDY_CONFIG:-$ROOT/examples/config.demo.yaml}"
PORT="${1:-19876}"
BIN="${ROOT}/build/coddy"

if ! command -v timeout >/dev/null 2>&1; then
  echo "timeout command not found" >&2
  exit 1
fi
if [[ ! -x "$BIN" ]]; then
  echo "binary not found, run: ./examples/build_coddy_httpserver.sh" >&2
  exit 1
fi

cleanup() { kill "$HTTP_PID" 2>/dev/null || true; }
trap cleanup EXIT

HOME_DIR="$(mktemp -d -t coddy-http-home-XXXXXX)"
WORK_DIR="$(mktemp -d -t coddy-http-work-XXXXXX)"
export CODDY_HOME="$HOME_DIR"
export WORK_DIR
export BASE_URL="http://127.0.0.1:$PORT/v1"
export MODEL="${MODEL:-rpa/gpt-oss:120b}"

"$BIN" http --config "$CODDY_CONFIG" --home "$HOME_DIR" --cwd "$WORK_DIR" -H 127.0.0.1 -P "$PORT" &
HTTP_PID=$!
if ! kill -0 "$HTTP_PID" 2>/dev/null; then
  echo "http server failed to start" >&2
  exit 1
fi
ready=0
for _ in $(seq 1 120); do
  if curl -sf -o /dev/null "http://127.0.0.1:${PORT}/v1/models"; then ready=1; break; fi
  sleep 0.25
done
if [[ "$ready" != 1 ]]; then
  echo "http server did not become ready on port ${PORT}" >&2
  exit 1
fi

python3 "$ROOT/examples/http_toolcalls_persist_e2e_demo.py"

echo "ok http toolcalls e2e"
