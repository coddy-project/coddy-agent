#!/usr/bin/env bash
# Full HTTP gateway e2e. Expects repo ./build/coddy from examples/build_coddy.sh (TAGS include http, scheduler, memory).
# Optional Docker smoke: examples/httpserver/docker.sh (from repo root).

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

CODDY_CFG_SRC="${CODDY_CONFIG:-$ROOT/examples/config.demo.yaml}"
PORT="${1:-19876}"
BIN="${ROOT}/build/coddy"
HTTP_DIR="$ROOT/examples/httpserver"

if ! command -v timeout >/dev/null 2>&1; then
  echo "timeout command not found" >&2
  exit 1
fi
if [[ ! -x "$BIN" ]]; then
  echo "binary not found, run: ./examples/build_coddy.sh" >&2
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

LOG_F="$HOME_DIR/e2e.log"
CFG="$HOME_DIR/config.resolved.yaml"
sed "s|__E2E_LOG_PATH__|$LOG_F|g" "$CODDY_CFG_SRC" >"$CFG"
export CODDY_CONFIG="$CFG"
: >"$LOG_F"

mkdir -p "$HOME_DIR/skills_fixture"
cp -a "$ROOT/examples/skills_fixture/coddy_slash_demo" "$HOME_DIR/skills_fixture/"

"$BIN" http --config "$CODDY_CONFIG" --home "$HOME_DIR" --cwd "$WORK_DIR" --scheduler-enabled -H 127.0.0.1 -P "$PORT" &
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
if [[ "$ready" != "1" ]]; then
  echo "http server did not become ready on port ${PORT}" >&2
  exit 1
fi

python3 "$HTTP_DIR/http_smoke_gateway.py"
python3 "$HTTP_DIR/http_e2e_scheduler_api.py"
python3 "$HTTP_DIR/http_e2e_models.py"
python3 "$HTTP_DIR/http_e2e_web.py"
python3 "$HTTP_DIR/http_e2e_todo.py"
python3 "$HTTP_DIR/http_e2e_memory.py"
python3 "$HTTP_DIR/http_e2e_skills_slash.py"
python3 "$HTTP_DIR/http_e2e_toolcalls_persist.py"
python3 "$HTTP_DIR/http_e2e_scheduler_agent.py"
python3 "$HTTP_DIR/http_e2e_plan_files.py"

echo "ok httpserver tests"
