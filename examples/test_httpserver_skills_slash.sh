#!/usr/bin/env bash
# Starts coddy http with the shared examples/config.demo.yaml and runs http_skills_slash_e2e_demo.py.
# Copies examples/skills_fixture into ${CODDY_HOME}/skills_fixture (see skills.dirs in config.demo.yaml).
# Prerequisites: ../../build/coddy with TAGS=http (see examples/build_coddy.sh).

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PORT="${1:-19876}"
BIN="${ROOT}/build/coddy"
SRC_CFG="${CODDY_CONFIG:-$ROOT/examples/config.demo.yaml}"

if ! command -v curl >/dev/null 2>&1; then
  echo "curl not found" >&2
  exit 1
fi
if [[ ! -x "$BIN" ]]; then
  echo "binary not found; run: make build TAGS=http  (see examples/build_coddy.sh)" >&2
  exit 1
fi

cleanup() { kill "$HTTP_PID" 2>/dev/null || true; }
trap cleanup EXIT

HOME_DIR="$(mktemp -d -t coddy-skills-demo-home-XXXXXX)"
WORK_DIR="$(mktemp -d -t coddy-skills-demo-work-XXXXXX)"
export CODDY_HOME="$HOME_DIR"
export WORK_DIR
export BASE_URL="http://127.0.0.1:$PORT/v1"
export MODEL="${MODEL:-agent}"

mkdir -p "$HOME_DIR/skills_fixture"
cp -a "$ROOT/examples/skills_fixture/coddy_slash_demo" "$HOME_DIR/skills_fixture/"

LOG_F="$HOME_DIR/e2e.log"
CFG="$HOME_DIR/config.demo.resolved.yaml"
sed "s|__E2E_LOG_PATH__|$LOG_F|g" "$SRC_CFG" >"$CFG"
export CODDY_CONFIG="$CFG"

"$BIN" http --config "$CFG" --home "$HOME_DIR" --cwd "$WORK_DIR" -H 127.0.0.1 -P "$PORT" &
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

python3 "$ROOT/examples/http_skills_slash_e2e_demo.py"
echo "ok httpserver skills slash demo"
