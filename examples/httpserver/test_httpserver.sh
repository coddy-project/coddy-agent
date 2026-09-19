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
# http_e2e_login boots servers of its own; keep them off the port this suite is
# already listening on, or its probe reaches this server and reads it as a
# login screen that never turned on.
export LOGIN_PORT="${LOGIN_PORT:-$((PORT + 41))}"
export MODEL="${MODEL:-rpa/qwen3.6-35b-a3b}"

LOG_F="$HOME_DIR/e2e.log"
CFG="$HOME_DIR/config.resolved.yaml"
sed "s|__E2E_LOG_PATH__|$LOG_F|g" "$CODDY_CFG_SRC" >"$CFG"
export CODDY_CONFIG="$CFG"
: >"$LOG_F"

# The demo config declares two providers, and the model harness switches to a
# row of the second one. Seed its key into the temp home the way the console
# runner does (examples/cli/cli_tui_driver.py: _seed_env_into), or that switch
# comes back as a 401 that looks like a coddy bug.
if [[ -n "${NEURALDEEP_API_KEY:-}" ]]; then
  printf 'NEURALDEEP_API_KEY=%s\n' "$NEURALDEEP_API_KEY" >"$HOME_DIR/.env"
elif [[ -f "$HOME/.coddy/.env" ]]; then
  grep '^NEURALDEEP_API_KEY=' "$HOME/.coddy/.env" >"$HOME_DIR/.env" || true
fi

mkdir -p "$HOME_DIR/skills_fixture"
cp -a "$ROOT/examples/skills_fixture/coddy_slash_demo" "$HOME_DIR/skills_fixture/"
mkdir -p "$WORK_DIR/.coddy"
cp -a "$ROOT/examples/rules_fixture/.coddy/rules" "$WORK_DIR/.coddy/"
echo 'package main

func main() {}' >"$WORK_DIR/main.go"

"$BIN" serve --config "$CODDY_CONFIG" --home "$HOME_DIR" --cwd "$WORK_DIR" --scheduler -H 127.0.0.1 -P "$PORT" &
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
python3 "$HTTP_DIR/http_e2e_rules.py"
# Ranged @mentions: picker read, attachments[].source lines, typed grammar, 400 past EOF.
python3 "$HTTP_DIR/http_e2e_mentions.py"
python3 "$HTTP_DIR/http_e2e_background.py"
# Self-boots the scripted model of cmd/tgfake and its own coddy serve: no key.
python3 "$HTTP_DIR/http_e2e_background_wake.py"
python3 "$HTTP_DIR/http_e2e_subagents.py"
python3 "$HTTP_DIR/http_e2e_hooks.py"
python3 "$HTTP_DIR/http_e2e_toolcalls_persist.py"
python3 "$HTTP_DIR/http_e2e_compact.py"
# Self-contained: boots its own coddy with a model that has no max_context_tokens,
# checks the web UI window, usage_update and the auto-compaction trigger agree
# (issue #245). Uses NEURALDEEP_API_KEY (or the .env seeded above); SKIP without it.
python3 "$HTTP_DIR/http_e2e_compact_auto.py"
# Self-contained: three clients of one session (sender, a tab on the composer stream,
# an idle viewer) read the context usage fall after /compact, REST compact and an
# automatic compaction. Uses NEURALDEEP_API_KEY (or the .env seeded above); SKIP without it.
python3 "$HTTP_DIR/http_e2e_compact_clients.py"
python3 "$HTTP_DIR/http_e2e_scheduler_agent.py"
python3 "$HTTP_DIR/http_e2e_plan_files.py"
# Ask profile reads but never writes; agent on the same session still writes.
python3 "$HTTP_DIR/http_e2e_ask_mode.py"
# Stages, commits, and rolls back a config edit; leaves the server config unchanged.
python3 "$HTTP_DIR/http_e2e_config.py"
# Self-contained: boots its own authenticated + local coddy serve instances.
python3 "$HTTP_DIR/http_e2e_remote.py"
# Self-contained: boots its own coddy behind the web UI sign-in and drives it as
# an anonymous browser, a signed-in one and a bearer client.
python3 "$HTTP_DIR/http_e2e_login.py"
# Self-contained: boots its own coddy, kills it mid-task, and makes a fresh one
# reap what the killed run left behind.
python3 "$HTTP_DIR/http_e2e_background_reap.py"

echo "ok httpserver tests"
