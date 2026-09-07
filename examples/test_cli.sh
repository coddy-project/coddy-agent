#!/usr/bin/env bash
# Wrapper: run the console TUI e2e suite (see examples/cli/test_cli.sh).
exec "$(cd "$(dirname "$0")" && pwd)/cli/test_cli.sh" "$@"
