#!/usr/bin/env bash
# Wrapper: run the Telegram bot against the offline stand (see examples/gateway/tg_e2e_offline.sh).
exec "$(cd "$(dirname "$0")" && pwd)/gateway/tg_e2e_offline.sh" "$@"
