# Claude Code bridge for Cursor rules

This repository keeps detailed project rules in `.cursor/rules/*.mdc`. Claude Code agents should treat those files as repo rules and read the relevant entries before changing matching files.

## Rule index

| Cursor rule | Applies to |
|-------------|------------|
| [architecture.mdc](../.cursor/rules/architecture.mdc) | Go architecture, layers, optional build tags |
| [api-layer.mdc](../.cursor/rules/api-layer.mdc) | HTTP API handlers, OpenAPI, HTTP docs |
| [code-style.mdc](../.cursor/rules/code-style.mdc) | Go formatting, linting, code comments |
| [core-modules.mdc](../.cursor/rules/core-modules.mdc) | Main `internal/*` package boundaries |
| [gateway.mdc](../.cursor/rules/gateway.mdc) | Messenger gateway: session store, Telegram adapter, Sender streaming, proxy |
| [implementation-order.mdc](../.cursor/rules/implementation-order.mdc) | Layered implementation order for new behavior |
| [testing.mdc](../.cursor/rules/testing.mdc) | Go test commands, tags, and conventions |
| [ui-spa.mdc](../.cursor/rules/ui-spa.mdc) | Embedded UI source and SPA behavior |
| [ui-verification.mdc](../.cursor/rules/ui-verification.mdc) | UI verification and screenshots |
| [workflow.mdc](../.cursor/rules/workflow.mdc) | BDD/TDD workflow and final checks |

## Operating rule

Keep `.cursor/rules/` as the single source of truth. When a Cursor rule is added, renamed, or removed, update this index and the `AGENTS.md` bridge section in the same change.
