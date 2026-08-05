# Codex bridge for Cursor rules

This repository keeps detailed project rules in `.cursor/rules/*.mdc`, and that directory
is their single source of truth. Codex does not read `.mdc` files on its own, so
`.codex/hooks/attach_rules.py` delivers them.

## How delivery works

| Trigger | What is attached |
|---------|------------------|
| `SessionStart` | every rule with `alwaysApply: true` |
| `PreToolUse` on `apply_patch` / `Edit` / `Write` | rules whose `globs` cover the files in the patch, once per rule per session |

The hook parses `.mdc` frontmatter (`description`, `globs`, `alwaysApply`) directly, so a
new rule file is picked up with no wiring. It fails open: a malformed rule exits quietly
instead of blocking an edit. Configuration lives in `.codex/hooks.json`; the full guide,
including trust, context budget, and debugging, is [docs/codex-hooks.md](../docs/codex-hooks.md).

Codex tracks hooks by content hash and skips untrusted ones without a hard error. Run
`/hooks` once per clone, and again after any edit to `attach_rules.py`. Project-local
hooks load only when the `.codex/` layer is trusted.

To see what a given patch would pull in:

```bash
echo '{"hook_event_name":"PreToolUse","session_id":"probe","tool_input":{"command":"*** Begin Patch\n*** Update File: external/httpserver/server.go\n*** End Patch"}}' | python3 .codex/hooks/attach_rules.py
```

## Rule index

| Cursor rule | Applies to | Attachment |
|-------------|------------|------------|
| [architecture.mdc](../.cursor/rules/architecture.mdc) | Go architecture, layers, optional build tags | always |
| [code-style.mdc](../.cursor/rules/code-style.mdc) | Go formatting, linting, code comments | always |
| [testing.mdc](../.cursor/rules/testing.mdc) | Go test commands, tags, and conventions | always |
| [workflow.mdc](../.cursor/rules/workflow.mdc) | BDD/TDD workflow and final checks | always |
| [api-layer.mdc](../.cursor/rules/api-layer.mdc) | HTTP API handlers, OpenAPI, HTTP docs | `external/httpserver/**/*.go` |
| [core-modules.mdc](../.cursor/rules/core-modules.mdc) | Main `internal/*` package boundaries | `internal/**/*.go` |
| [gateway.mdc](../.cursor/rules/gateway.mdc) | Messenger gateway: session store, Telegram adapter, Sender streaming, proxy | `external/gateway/**/*.go`, `internal/config/gateway.go` |
| [implementation-order.mdc](../.cursor/rules/implementation-order.mdc) | Layered implementation order for new behavior | `cmd`, `internal`, `external`, `lib`, `tools` |
| [ui-spa.mdc](../.cursor/rules/ui-spa.mdc) | Embedded UI source and SPA behavior | `external/ui/**/*` |
| [ui-verification.mdc](../.cursor/rules/ui-verification.mdc) | UI verification and screenshots | `external/ui/**/*` |

## Operating rule

Keep `.cursor/rules/` as the single source of truth. The hook needs no update when rules
change, but this index and the `AGENTS.md` bridge section do: refresh both in the same
change that adds, renames, or removes a rule file. A rule is only reachable from Codex if
its frontmatter carries `globs` or `alwaysApply: true`.
