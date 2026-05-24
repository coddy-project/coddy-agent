#!/usr/bin/env bash
# Full ACP stdio e2e. Expects ./build/coddy (examples/build_coddy.sh links scheduler with HTTP for one binary).

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

python3 "$ACP_DIR/acp_smoke_gateway.py"
python3 "$ACP_DIR/acp_e2e_models.py"
python3 "$ACP_DIR/acp_e2e_web.py"
python3 "$ACP_DIR/acp_e2e_todo.py"
python3 "$ACP_DIR/acp_e2e_skills_slash.py"
python3 "$ACP_DIR/acp_e2e_rules.py"
python3 "$ACP_DIR/acp_e2e_memory.py"
python3 "$ACP_DIR/acp_e2e_toolcalls_persist.py"
python3 "$ACP_DIR/acp_e2e_scheduler_agent.py"
python3 "$ACP_DIR/acp_e2e_plan_files.py"

echo "ok acp tests"
