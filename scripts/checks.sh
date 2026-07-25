#!/usr/bin/env bash
# checks.sh — the project's commit-time gate. By default it runs the linter
# only (quick); the full test matrix is slow, so tests are opt-in here and
# belong in CI / before push. Invoked by .githooks/pre-commit; also runnable
# by hand. Exits non-zero when a requested check fails.
#
# Knobs (env vars) so the gate has one shared policy:
#
#   CODDY_HOOK_LINT   0|1              (default: 1)   run `make lint` (golangci-lint)
#
#   CODDY_HOOK_TESTS  off|fast|full    (default: off) additionally run tests:
#       off   no tests (lint only — the quick default)
#       fast  go test ./...            quick base-tag unit tests (~seconds)
#       full  make test                every build-tag combo + UI (minutes)
#
#   CODDY_HOOK_SKIP   1                bypass the whole gate (prints a warning)
#
# Exit code: 0 = everything requested passed (or skipped), non-zero = a failure.
set -uo pipefail

log() { printf 'checks: %s\n' "$*" >&2; }

# git exports repo-location vars (GIT_DIR, GIT_INDEX_FILE, GIT_PREFIX, ...) into
# the hooks it runs. When this gate is invoked from .githooks/pre-commit they
# leak into every `git` a test shells out to (e.g. internal/gitws's worktree
# tests) and point it at the wrong repo/index. Strip them so any test we run
# sees a pristine git environment, exactly as if run from a normal shell.
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_PREFIX GIT_COMMON_DIR \
      GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_INDEX_VERSION \
      GIT_NAMESPACE GIT_REFLOG_ACTION

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

tests="${CODDY_HOOK_TESTS:-off}"
lint="${CODDY_HOOK_LINT:-1}"
status=0

# --- lint (the quick default check) ---
if [ "$lint" = "1" ]; then
  if command -v golangci-lint >/dev/null 2>&1; then
    log "lint: make lint" ; make lint || status=1
  else
    log "golangci-lint not installed — skipping lint (set CODDY_HOOK_LINT=0 to silence)."
  fi
fi

# --- tests (opt-in; off by default because the full matrix is slow) ---
case "$tests" in
  off)  : ;;
  fast) log "tests: go test ./..." ; go test ./... || status=1 ;;
  full) log "tests: make test"     ; make test     || status=1 ;;
  *)    log "unknown CODDY_HOOK_TESTS='$tests' (want off|fast|full)" ; exit 2 ;;
esac

if [ "$status" -eq 0 ]; then
  log "PASS (lint=$lint, tests=$tests)"
else
  log "FAIL — fix the reported issues before committing (bypass once: git commit --no-verify)."
fi
exit "$status"
