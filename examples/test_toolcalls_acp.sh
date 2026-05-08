#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

export CODDY_BIN="${CODDY_BIN:-$ROOT/build/coddy}"
export CODDY_CONFIG="${CODDY_CONFIG:-$ROOT/examples/config.demo.yaml}"
export SESSION_ROOT="${SESSION_ROOT:-/tmp/coddy-examples-acp}"
export SESSION_ID="${SESSION_ID:-example-acp-toolcalls-persist}"

if [[ ! -x "$CODDY_BIN" ]]; then
  echo "binary not found, run: ./examples/build_coddy_httpserver.sh" >&2
  exit 1
fi

python3 "$ROOT/examples/acp_toolcalls_persist_e2e_demo.py"

echo "ok acp toolcalls e2e"
