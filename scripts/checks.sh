#!/usr/bin/env bash
# checks.sh — the project's commit-time verification gate: run the test suite
# and the linter, exit non-zero if anything fails. Invoked by the git
# pre-commit hook (.githooks/pre-commit); also runnable by hand.
#
# Knobs (env vars) so the gate has one shared policy:
#
#   CODDY_HOOK_TESTS  fast|full   (default: full)
#       fast  go test ./...       quick base-tag unit tests (~seconds)
#       full  make test           every build-tag combo + UI (the full matrix)
#
#   CODDY_HOOK_LINT   0|1         (default: 1)   run `make lint` (golangci-lint)
#   CODDY_HOOK_SKIP   1           bypass the whole gate (prints a warning)
#
# Exit code: 0 = everything passed (or skipped), non-zero = tests or lint failed.
set -uo pipefail

log() { printf 'checks: %s\n' "$*" >&2; }

if [ "${CODDY_HOOK_SKIP:-0}" = "1" ]; then
  log "CODDY_HOOK_SKIP=1 — gate bypassed, nothing run."
  exit 0
fi

# Resolve repo root so the gate works from any caller's cwd.
if root=$(git rev-parse --show-toplevel 2>/dev/null); then
  :
else
  root=$(cd "$(dirname "$0")/.." && pwd)
fi
cd "$root" || { log "cannot cd to repo root '$root'"; exit 1; }

if ! command -v go >/dev/null 2>&1; then
  log "Go toolchain not found on PATH — cannot run the gate."
  exit 127
fi

mode="${CODDY_HOOK_TESTS:-full}"
status=0

# --- tests ---
case "$mode" in
  fast) log "tests: go test ./..." ; go test ./... || status=1 ;;
  full) log "tests: make test"     ; make test     || status=1 ;;
  *)    log "unknown CODDY_HOOK_TESTS='$mode' (want fast|full)" ; exit 2 ;;
esac

# --- lint --- (run even if tests failed, so the whole picture is reported)
if [ "${CODDY_HOOK_LINT:-1}" = "1" ]; then
  if command -v golangci-lint >/dev/null 2>&1; then
    log "lint: make lint" ; make lint || status=1
  else
    log "golangci-lint not installed — skipping lint (set CODDY_HOOK_LINT=0 to silence)."
  fi
fi

if [ "$status" -eq 0 ]; then
  log "PASS (tests=$mode, lint=${CODDY_HOOK_LINT:-1})"
else
  log "FAIL — fix the reported tests/lint before committing (bypass once: git commit --no-verify)."
fi
exit "$status"
