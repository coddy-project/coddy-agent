#!/usr/bin/env bash
# Smoke coddy http inside docker compose (see repo docker-compose.yml). Needs docker and docker compose.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
HTTP_DIR="$ROOT/examples/httpserver"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker not found" >&2
  exit 1
fi
if ! docker compose version >/dev/null 2>&1; then
  echo "docker compose not found" >&2
  exit 1
fi

PORT="${PORT:-12345}"
export PORT

TMP_DIR="$(mktemp -d -t coddy-docker-http-XXXXXX)"
cleanup() {
  docker compose -f docker-compose.yml down -v --remove-orphans >/dev/null 2>&1 || true
  if [[ "${KEEP_TMP:-0}" != "1" ]]; then
    rm -rf "$TMP_DIR" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

CODDY_CWD="${CODDY_CWD:-$TMP_DIR/workspace}"
CODDY_HOME="${CODDY_HOME:-$TMP_DIR/coddy_home}"
CODDY_CONFIG="${CODDY_CONFIG:-$TMP_DIR/config.yaml}"

mkdir -p "$CODDY_CWD" "$CODDY_HOME"

python3 - "$ROOT/examples/config.demo.yaml" "$CODDY_CONFIG" "$PORT" <<'PY'
from __future__ import annotations

import sys

src_path = sys.argv[1]
dst_path = sys.argv[2]
port = sys.argv[3]

raw = open(src_path, "r", encoding="utf-8").read()
raw = raw.replace('host: "127.0.0.1"', 'host: "0.0.0.0"')
raw = raw.replace("port: 19876", f"port: {port}", 1)

# Make container logs visible in `docker compose logs`.
raw = raw.replace('outputs: ["file"]', 'outputs: ["stderr"]')
raw = raw.replace('file: "__E2E_LOG_PATH__"', 'file: ""')

open(dst_path, "w", encoding="utf-8").write(raw)
PY

if [[ ! -f "$CODDY_CONFIG" ]]; then
  echo "config path is not a file: $CODDY_CONFIG" >&2
  ls -la "$(dirname "$CODDY_CONFIG")" >&2 || true
  exit 1
fi

if [[ ! -s "$CODDY_CONFIG" ]]; then
  echo "config file is empty: $CODDY_CONFIG" >&2
  exit 1
fi

export CODDY_CWD CODDY_HOME CODDY_CONFIG

docker compose -f docker-compose.yml up -d --build coddy

ready=0
for _ in $(seq 1 120); do
  if curl -sf -o /dev/null "http://127.0.0.1:${PORT}/v1/models"; then
    ready=1
    break
  fi
  sleep 0.25
done
if [[ "$ready" != 1 ]]; then
  echo "http server did not become ready on port ${PORT}" >&2
  docker compose -f docker-compose.yml ps -a || true
  docker compose -f docker-compose.yml logs coddy || true
  exit 1
fi

export BASE_URL="http://127.0.0.1:${PORT}/v1"
python3 "$HTTP_DIR/http_smoke_gateway.py"

echo "ok docker httpserver tests"
