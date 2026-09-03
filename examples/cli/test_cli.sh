#!/usr/bin/env bash
# Run every coddy console TUI e2e script against a live model.
#
# The scripts drive `coddy cli --plain` in a pty (pexpect + pyte, Linux-only)
# and assert on persisted session artifacts plus deterministic screen chrome.
#
#   MODEL             model id (default neuraldeep/qwen3.8-27b)
#   NEURALDEEP_API_KEY  provider key; when unset the driver copies the single
#                       NEURALDEEP_API_KEY line from ~/.coddy/.env into each
#                       temp CODDY_HOME/.env
#   CODDY_BIN         binary override (default <repo>/build/coddy)
#   CLI_E2E_ONLY      run only the script whose stem matches (e.g. cli_smoke)
#   CLI_E2E_CORE=1    run the fast core subset only
#   CLI_E2E_KEEP=1    keep temp homes/workdirs for debugging
set -u

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CLI_DIR="$ROOT/examples/cli"
export MODEL="${MODEL:-neuraldeep/qwen3.8-27b}"

# Build a current binary unless the caller pinned one (CODDY_BIN) or opted
# out (CLI_E2E_NO_BUILD=1). Testing a stale build is worse than waiting.
if [ -z "${CODDY_BIN:-}" ] && [ "${CLI_E2E_NO_BUILD:-0}" != "1" ]; then
  echo "building coddy with TAGS=\"cli scheduler memory\"..."
  (cd "$ROOT" && make build TAGS="cli scheduler memory" >/dev/null)
fi
export CODDY_BIN="${CODDY_BIN:-$ROOT/build/coddy}"

if [ ! -x "$CODDY_BIN" ]; then
  echo "error: no coddy binary at $CODDY_BIN" >&2
  echo "build one with: make build TAGS=\"cli scheduler memory\"" >&2
  exit 1
fi
if ! "$CODDY_BIN" cli --help >/dev/null 2>&1; then
  echo "error: $CODDY_BIN lacks the cli build tag" >&2
  echo "rebuild with: make build TAGS=\"cli scheduler memory\"" >&2
  exit 1
fi
if ! python3 -c "import pexpect, pyte" 2>/dev/null; then
  echo "error: python deps missing (pexpect, pyte)" >&2
  echo "install with: python3 -m pip install -r examples/cli/requirements.txt" >&2
  exit 1
fi

CORE_SCRIPTS=(
  cli_smoke.py
  cli_e2e_print.py
  cli_e2e_remote.py
  cli_e2e_models.py
  cli_e2e_permissions.py
  cli_e2e_resume.py
  cli_e2e_compact.py
  cli_e2e_toolcalls_persist.py
)
ALL_SCRIPTS=(
  cli_smoke.py
  cli_e2e_print.py
  cli_e2e_remote.py
  cli_e2e_models.py
  cli_e2e_toolcalls_persist.py
  cli_e2e_permissions.py
  cli_e2e_resume.py
  cli_e2e_web.py
  cli_e2e_todo.py
  cli_e2e_skills_slash.py
  cli_e2e_rules.py
  cli_e2e_config.py
  cli_e2e_background.py
  cli_e2e_compact.py
  cli_e2e_plan_files.py
  cli_e2e_ask_mode.py
  cli_e2e_memory.py
  cli_e2e_scheduler_agent.py
)

scripts=("${ALL_SCRIPTS[@]}")
if [ "${CLI_E2E_CORE:-0}" = "1" ]; then
  scripts=("${CORE_SCRIPTS[@]}")
fi
if [ -n "${CLI_E2E_ONLY:-}" ]; then
  scripts=("${CLI_E2E_ONLY%.py}.py")
fi

failures=0
for script in "${scripts[@]}"; do
  echo "=== $script (MODEL=$MODEL) ==="
  if (cd "$CLI_DIR" && timeout "${CLI_E2E_TIMEOUT:-600}" python3 "$script"); then
    echo "--- PASS $script"
  else
    echo "--- FAIL $script" >&2
    failures=$((failures + 1))
  fi
done

if [ "$failures" -gt 0 ]; then
  echo "cli e2e: $failures script(s) failed" >&2
  exit 1
fi
echo "cli e2e: all ${#scripts[@]} scripts passed"
