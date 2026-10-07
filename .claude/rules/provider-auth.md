---
description: Provider-row authentication isolation and CLI login resolution
paths:
  - "internal/config/resolve.go"
  - "internal/llm/**/*.go"
  - "internal/session/provider_usage_sources.go"
  - "internal/session/context_window.go"
  - "external/httpserver/*auth*_http.go"
  - "external/httpserver/providers_models_http.go"
  - "internal/dryrun/providers.go"
  - "cmd/coddy/providers.go"
  - "cmd/coddy/codex.go"
  - "cmd/coddy/devin.go"
---
# Provider authentication isolation

- A machine-wide Codex or Devin CLI login serves exactly one configured row: the sole row, or among multiple rows the row named `codex` for Codex and `devin` for Devin.
- Completion, model discovery, context-window lookup, usage, labels, dry-run, and sign-in all propagate `NoCLILogin`.
- A second credential-less row cannot consume another row's login.
- An unsaved retyped sign-in probes the requested provider type and only the current proxy.
- Usage invalidation applies only to the owning row.
- Provider requests still obey the provider-proxy rule.
