#!/usr/bin/env bash
# Full ACP stdio e2e. Expects ./build/coddy (examples/build_coddy.sh links scheduler with HTTP for one binary).
# Optional LLM-heavy scheduler tools plus cron: SCHEDULER_AGENT_E2E=1.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

ACP_DIR="$ROOT/examples/acp"

export CODDY_BIN="${CODDY_BIN:-$ROOT/build/coddy}"
export CODDY_CONFIG="${CODDY_CONFIG:-$ROOT/examples/config.demo.yaml}"
export SESSION_ROOT="${SESSION_ROOT:-/tmp/coddy-examples-acp}"
export SESSION_ID="${SESSION_ID:-example-acp}"

if [[ ! -x "$CODDY_BIN" ]]; then
  echo "binary not found, run: ./examples/build_coddy.sh" >&2
  exit 1
fi

python3 "$ACP_DIR/acp_smoke_basic.py"
python3 "$ACP_DIR/acp_models_e2e_demo.py"
python3 "$ACP_DIR/acp_agent_todo_e2e_demo.py"
python3 "$ACP_DIR/acp_memory_copilot_e2e_demo.py"
python3 "$ACP_DIR/acp_toolcalls_persist_e2e_demo.py"

if [[ "${SCHEDULER_AGENT_E2E:-}" == "1" ]]; then
  python3 "$ACP_DIR/acp_scheduler_e2e_demo.py"
fi

echo "ok acp tests"
