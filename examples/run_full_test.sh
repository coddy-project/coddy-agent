#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

./examples/build_coddy.sh
./examples/test_acp.sh
./examples/test_httpserver.sh
