#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

export CODDY_BIN="${CODDY_BIN:-$ROOT/build/coddy}"
export CODDY_CONFIG="${CODDY_CONFIG:-$ROOT/examples/config.rpa-gpt-oss-120b.yaml}"
export SESSION_ROOT="${SESSION_ROOT:-/tmp/coddy-examples-acp}"
export SESSION_ID="${SESSION_ID:-example-acp}"

if [[ ! -x "$CODDY_BIN" ]]; then
  echo "binary not found, run: ./examples/build_coddy_httpserver.sh" >&2
  exit 1
fi

python3 "$ROOT/examples/acp_smoke_basic.py"

if [[ "${RUN_E2E:-0}" == "1" ]]; then
  python3 "$ROOT/examples/acp_agent_todo_e2e_demo.py"
  python3 "$ROOT/examples/acp_memory_copilot_e2e_demo.py"
fi
