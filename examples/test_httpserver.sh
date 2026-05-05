#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

export CODDY_CONFIG="${CODDY_CONFIG:-$ROOT/examples/config.rpa-gpt-oss-120b.yaml}"
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

"$BIN" http --disable-session -H 127.0.0.1 -P "$PORT" &
HTTP_PID=$!
sleep 0.4
if ! kill -0 "$HTTP_PID" 2>/dev/null; then
  echo "http server failed to start" >&2
  exit 1
fi

out="$(curl -sS "http://127.0.0.1:$PORT/v1/models")"
echo "$out" | head -c 500
echo
if ! echo "$out" | grep -q '"object"'; then
  echo "unexpected /v1/models response" >&2
  exit 1
fi

echo "ok httpserver /v1/models"
