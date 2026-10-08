#!/usr/bin/env bash
# Runs the TLS e2e on self-signed material against a built binary (the recipes of docs/operate/certificates.md).
#
# Needs openssl and python3; skips (exit 0) without openssl. The stand is real: coddy serve processes terminating TLS, a swarm relay in
# front of one of them, and the borrower's own client (coddy -t --dry-run).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
bin="${CODDY_BIN:-$root/build/coddy}"

if [ ! -x "$bin" ]; then
  echo "building $bin with -tags \"http swarm\""
  (cd "$root" && make build TAGS="http swarm")
fi

CODDY_BIN="$bin" "${PYTHON:-python3}" "$here/tls_e2e_selfsigned.py"
