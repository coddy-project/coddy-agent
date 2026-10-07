---
description: Immediate turn admission and autonomous retry semantics
paths:
  - "internal/session/manager_turn_lock*.go"
  - "internal/session/turn_busy.go"
  - "internal/agent/background_notify*.go"
  - "internal/serve/wake.go"
  - "cmd/coddy/acp_wake*.go"
  - "external/cli/background.go"
  - "external/httpserver/composer_stream_relay.go"
  - "external/httpserver/turn_mirror.go"
  - "external/httpserver/*wake*.go"
  - "external/gateway/*/wake.go"
---
# Turn admission

- The turn lock refuses immediately and never queues.
- An autonomous caller preserves and propagates `ErrSessionTurnBusy` for retry; it is not success, permanent failure, or a consumed notification.
- Retry absorbs tasks completed while waiting and avoids starting a second turn.
- Acquire the turn before replacing live relay ownership.
- Every new autonomous path gets an end-to-end test with the real manager lock.
