#!/usr/bin/env bash
# Telegram gateway e2e with no Telegram: starts cmd/tgfake (fake Bot API plus a
# scripted model), boots coddy serve against it in a temporary home, sends a
# message through the fake and checks that the bot answered. Expects a coddy
# built with a gateway tag (examples/build_coddy.sh). Works in Git Bash on
# Windows too: paths handed to coddy go through cygpath when it exists.
#
#   ./examples/gateway/tg_e2e_offline.sh            # run and clean up
#   TG_E2E_KEEP=1 ./examples/gateway/tg_e2e_offline.sh   # leave the stand up, print the page URL
#   RICH_MESSAGES=true ./examples/gateway/tg_e2e_offline.sh
#
# Knobs: TG_PORT (18790), LLM_DELAY (50ms), RICH_MESSAGES (false), TG_VERBOSE
# (unset), CODDY_BIN (build/coddy).

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

EXE="$(go env GOEXE)"
BIN="${CODDY_BIN:-$ROOT/build/coddy$EXE}"
TG_PORT="${TG_PORT:-18790}"
LLM_DELAY="${LLM_DELAY:-50ms}"
RICH_MESSAGES="${RICH_MESSAGES:-false}"
ORIGIN="http://127.0.0.1:$TG_PORT"

if [[ ! -x "$BIN" ]]; then
  echo "coddy binary not found at $BIN; build it with a gateway tag: ./examples/build_coddy.sh" >&2
  exit 1
fi
for tool in curl mktemp; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "$tool not found" >&2
    exit 1
  fi
done

# A path coddy can open: Git Bash hands out /tmp/..., which means nothing to a
# Windows executable.
if command -v cygpath >/dev/null 2>&1; then
  hostpath() { cygpath -w "$1"; }
else
  hostpath() { printf '%s' "$1"; }
fi

TMP="$(mktemp -d -t coddy-tg-XXXXXX)"
HOME_DIR="$TMP/home"
WORK_DIR="$TMP/work"
mkdir -p "$HOME_DIR" "$WORK_DIR"
TGFAKE_PID=""
CODDY_PID=""

cleanup() {
  if [[ -n "${TG_E2E_KEEP:-}" ]]; then
    echo "stand left running: chat page $ORIGIN/  (coddy pid $CODDY_PID, tgfake pid $TGFAKE_PID, home $HOME_DIR)"
    wait
    return
  fi
  [[ -n "$CODDY_PID" ]] && kill "$CODDY_PID" 2>/dev/null || true
  [[ -n "$TGFAKE_PID" ]] && kill "$TGFAKE_PID" 2>/dev/null || true
  wait 2>/dev/null || true
  rm -rf "$TMP"
}
trap cleanup EXIT

# Built rather than `go run`: on Windows a kill of the go run parent leaves
# the child listening.
go build -o "$TMP/tgfake$EXE" ./cmd/tgfake
"$TMP/tgfake$EXE" --addr "127.0.0.1:$TG_PORT" --llm --llm-delay "$LLM_DELAY" ${TG_VERBOSE:+--verbose} &
TGFAKE_PID=$!
for _ in $(seq 1 40); do
  if curl -sf -o /dev/null "$ORIGIN/bot1/getMe"; then break; fi
  sleep 0.25
done
curl -sf -o /dev/null "$ORIGIN/bot1/getMe" || { echo "tgfake did not come up on $ORIGIN" >&2; exit 1; }

CFG="$HOME_DIR/config.yaml"
cat >"$CFG" <<EOF
providers:
  - name: stub
    type: openai
    api_base: "$ORIGIN/v1"
    api_key: "sk-tgfake"
models:
  - model: stub/coddy-demo
    max_context_tokens: 131072
agent:
  model: stub/coddy-demo
tools:
  permission_mode: bypass
httpserver:
  enable: false
gateways:
  telegram:
    enable: true
    token: "123456:fake"
    rich_messages: $RICH_MESSAGES
logger:
  level: info
  format: text
  outputs: [stderr]
  levels:
    - component: gateway.telegram
      level: debug
EOF

export CODDY_TELEGRAM_API_BASE="$ORIGIN"
"$BIN" serve --config "$(hostpath "$CFG")" --home "$(hostpath "$HOME_DIR")" --cwd "$(hostpath "$WORK_DIR")" --gateway --http=false &
CODDY_PID=$!

# The bot is up once it has introduced itself to the fake.
ready=0
for _ in $(seq 1 120); do
  if curl -sf "$ORIGIN/sim/outbox/count?method=getMe" | grep -Eq '"count":[1-9]'; then ready=1; break; fi
  if ! kill -0 "$CODDY_PID" 2>/dev/null; then echo "coddy serve exited before connecting" >&2; exit 1; fi
  sleep 0.25
done
[[ "$ready" == "1" ]] || { echo "coddy serve never called getMe on $ORIGIN" >&2; exit 1; }

curl -sf -X POST -H 'Content-Type: application/json' \
  -d '{"chat_id":4242,"user_id":4242,"username":"alice","text":"hello"}' \
  "$ORIGIN/sim/message" >/dev/null

answered=0
for _ in $(seq 1 120); do
  if curl -sf "$ORIGIN/sim/chat/4242?format=text" | grep -q 'bot: You said: hello'; then answered=1; break; fi
  sleep 0.25
done
if [[ "$answered" != "1" ]]; then
  echo "the bot never answered; chat:" >&2
  curl -s "$ORIGIN/sim/chat/4242?format=text" >&2 || true
  exit 1
fi

final="sendMessage"
[[ "$RICH_MESSAGES" == "true" ]] && final="sendRichMessage"
curl -sf "$ORIGIN/sim/outbox/count?method=$final" | grep -Eq '"count":[1-9]' \
  || { echo "no $final in the outbox" >&2; exit 1; }

echo "ok telegram offline stand"
