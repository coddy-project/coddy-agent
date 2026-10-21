#!/usr/bin/env bash
# Live check of the acp provider (docs/features/acp-provider.md): Coddy serves a
# local ACP agent as a model. For every agent named in ACP_E2E_AGENTS that is
# installed, the script writes an acp row into a throwaway CODDY_HOME and runs
#
#   1. coddy -p in agent mode: the agent must answer "pong";
#   2. coddy -c -p, the next turn of the same conversation: the agent must
#      recall the word it was given in the first turn;
#   3. coddy -p in ask mode: the row maps ask to a read-only mode of the agent.
#
# The agents run one after another. A check passes when coddy exits 0 and its
# standard output (the answer, never the log) holds the expected word.
#
# This calls real models through the agents and is not part of CI. Each agent
# must already be able to answer on its own (signed in, or configured with a
# provider); the agent inherits this environment, plus what the per-agent
# variables below add to its acp.env.
#
# Variables:
#   CODDY_BIN          the coddy binary with the console (tags cli); default: build it
#   ACP_E2E_AGENTS     agents to try, from: claude codex opencode zcode coddy
#                      (default: all five)
#   ACP_E2E_<AGENT>_ENV  space-separated NAME=value pairs for that agent's acp.env,
#                      e.g. ACP_E2E_OPENCODE_ENV="HOME=/tmp/oc XDG_CONFIG_HOME=/tmp/oc/.config"
#   ACP_E2E_CODEX_AUTH the codex-acp authenticate method (default: api-key)
#   ACP_E2E_CHILD_CODDY_HOME  CODDY_HOME of the coddy acp child, with a model of its own;
#                      coddy is skipped without it (the child would read this
#                      script's home and be served by an agent of this list)
#   ACP_E2E_WORKSPACE  the folder the sessions work in (default: a temporary one)

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

CODDY_BIN="${CODDY_BIN:-}"
if [[ -z "$CODDY_BIN" ]]; then
  CODDY_BIN="$WORK/coddy"
  (cd "$ROOT" && go build -tags=cli -o "$CODDY_BIN" ./cmd/coddy)
fi
WORKSPACE="${ACP_E2E_WORKSPACE:-$WORK/ws}"
mkdir -p "$WORKSPACE" "$WORK/home"
AGENTS="${ACP_E2E_AGENTS:-claude codex opencode zcode coddy}"

# row <name> <command> <args yaml> <plan mode> <ask mode> <auth method> <env pairs>
row() {
  local name="$1" command="$2" args="$3" plan="$4" ask="$5" auth="$6" env="$7"
  {
    echo "  - name: $name"
    echo "    type: acp"
    echo "    acp:"
    echo "      command: \"$command\""
    echo "      args: $args"
    [[ -n "$auth" ]] && echo "      auth_method: $auth"
    echo "      modes: {plan: $plan, ask: $ask}"
    if [[ -n "$env" ]]; then
      echo "      env:"
      for pair in $env; do
        echo "        ${pair%%=*}: \"${pair#*=}\""
      done
    fi
  } >>"$WORK/providers.yaml"
  echo "  - model: $name/default" >>"$WORK/models.yaml"
}

: >"$WORK/providers.yaml"
: >"$WORK/models.yaml"
tried=()
for agent in $AGENTS; do
  case "$agent" in
    claude)
      command -v claude-agent-acp >/dev/null || { echo "skip claude: claude-agent-acp not on PATH"; continue; }
      row claude claude-agent-acp "[]" plan plan "" "${ACP_E2E_CLAUDE_ENV:-}" ;;
    codex)
      command -v codex-acp >/dev/null || { echo "skip codex: codex-acp not on PATH"; continue; }
      row codex codex-acp "[]" read-only read-only "${ACP_E2E_CODEX_AUTH:-api-key}" "${ACP_E2E_CODEX_ENV:-}" ;;
    opencode)
      command -v opencode >/dev/null || { echo "skip opencode: opencode not on PATH"; continue; }
      row opencode opencode "[acp]" plan plan "" "${ACP_E2E_OPENCODE_ENV:-}" ;;
    zcode)
      command -v zcode-acp-server >/dev/null || { echo "skip zcode: zcode-acp-server not on PATH"; continue; }
      row zcode zcode-acp-server "[]" plan plan "" "${ACP_E2E_ZCODE_ENV:-}" ;;
    coddy)
      [[ -n "${ACP_E2E_CHILD_CODDY_HOME:-}" ]] || { echo "skip coddy: set ACP_E2E_CHILD_CODDY_HOME to a home with a model of its own"; continue; }
      row coddy "$CODDY_BIN" "[acp]" plan ask "" "CODDY_HOME=$ACP_E2E_CHILD_CODDY_HOME ${ACP_E2E_CODDY_ENV:-}" ;;
    *) echo "unknown agent $agent" >&2; exit 2 ;;
  esac
  tried+=("$agent")
done
if [[ ${#tried[@]} -eq 0 ]]; then
  echo "no agent to try" >&2
  exit 1
fi
{
  echo "providers:"
  cat "$WORK/providers.yaml"
  echo "models:"
  cat "$WORK/models.yaml"
  echo "agent:"
  echo "  model: ${tried[0]}/default"
} >"$WORK/home/config.yaml"

export CODDY_HOME="$WORK/home"
fail=0
# check <agent> <what> <want> <coddy args...>: runs coddy, then wants exit 0
# and the word on standard output.
check() {
  local agent="$1" what="$2" want="$3"
  shift 3
  local out status=0
  out="$(timeout 300 "$CODDY_BIN" "$@" 2>"$WORK/stderr")" || status=$?
  if [[ $status -eq 0 ]] && grep -qi -- "$want" <<<"$out"; then
    echo "ok    $agent: $what"
  else
    echo "FAIL  $agent: $what (want \"$want\", exit $status): $(tr '\n' ' ' <<<"$out" | cut -c1-300)"
    echo "      stderr: $(tail -n 5 "$WORK/stderr" | tr '\n' ' ' | cut -c1-300)"
    fail=1
  fi
}

cd "$WORKSPACE"
for agent in "${tried[@]}"; do
  check "$agent" "answers in agent mode" "pong" --model "$agent/default" -p "Remember the word kiwi. Reply with exactly: pong"
  check "$agent" "continues the conversation" "kiwi" -c --model "$agent/default" -p "Which word did I ask you to remember? Reply with that word only."
  check "$agent" "answers in ask mode" "pong" --model "$agent/default" --mode ask -p "Reply with exactly: pong"
done
exit "$fail"
