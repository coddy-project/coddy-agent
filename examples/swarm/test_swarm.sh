#!/usr/bin/env bash
# Runs every swarm harness against a freshly built binary.
#
# The stand it boots is real: three relays wired into a ring, one agent that is
# dialled and one that can only dial out.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
bin="${CODDY_BIN:-$root/build/coddy}"

if [ ! -x "$bin" ]; then
  echo "building $bin with -tags \"http swarm\""
  (cd "$root" && make build TAGS="http swarm")
fi

python="${PYTHON:-python3}"
"$python" "$here/swarm_e2e.py"
