#!/usr/bin/env bash
# Runs the TLS e2e on the built-in certificates against a built binary (coddy tls, docs/operate/certificates.md).
#
# Needs python3 and nothing else: no openssl, the certificates are Coddy's own. The stand is real: four machines' state (four
# CODDY_HOMEs), a swarm relay and a node terminating TLS with `auto: true`, and the borrower's own client (coddy -t --dry-run).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
bin="${CODDY_BIN:-$root/build/coddy}"

if [ ! -x "$bin" ]; then
  echo "building $bin with -tags \"http swarm\""
  (cd "$root" && make build TAGS="http swarm")
fi

CODDY_BIN="$bin" "${PYTHON:-python3}" "$here/tls_e2e_builtin.py"
