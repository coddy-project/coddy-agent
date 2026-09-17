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
instead of blocking an edit. Configuration lives in `.codex/hooks.json`.

Codex tracks hooks by content hash and skips untrusted ones without a hard error. Run
`/hooks` once per clone, and again after any edit to `attach_rules.py` or `hooks.json`.
Project-local hooks load only when the `.codex/` layer is trusted.

To see what a given patch would pull in, no Codex session needed:

```bash
echo '{"hook_event_name":"PreToolUse","session_id":"probe","tool_input":{"command":"*** Begin Patch\n*** Update File: src/api/server.py\n*** End Patch"}}' | python3 .codex/hooks/attach_rules.py
```

## Rule index

<!-- TEMPLATE: regenerate this table from the real frontmatter of .cursor/rules/*.mdc.
     "always" = alwaysApply: true; otherwise list the globs that trigger attachment. -->

| Cursor rule | Applies to | Attachment |
|-------------|------------|------------|
| [workflow.mdc](../.cursor/rules/workflow.mdc) | BDD/TDD workflow, final checks, Rules Sync | always |
| [code-style.mdc](../.cursor/rules/code-style.mdc) | Formatting, linting, comments | always |
| [architecture.mdc](../.cursor/rules/architecture.mdc) | Layers, modules, allowed dependencies | always |
| [testing.mdc](../.cursor/rules/testing.mdc) | Test layout, naming, commands | always |
| [implementation-order.mdc](../.cursor/rules/implementation-order.mdc) | Layer-by-layer order for new behavior | `src/**/*.py`, `main.py` |
| [api-layer.mdc](../.cursor/rules/api-layer.mdc) | HTTP/RPC handlers and docs | `src/api/**/*.py` |
| [core-modules.mdc](../.cursor/rules/core-modules.mdc) | Domain modules | `src/core/**/*.py` |

Keep the always-on set small (workflow, code-style, architecture, testing) and let the
rest attach by glob. Codex caps model-visible hook output (roughly 2500 tokens by default;
`additionalContextLimit` in `hooks.json` raises it), and oversized always-on context
degrades the model instead of helping it.

## Operating rule

Keep `.cursor/rules/` as the single source of truth. The hook needs no update when rules
change, but this index and the `AGENTS.md` bridge section do: refresh both in the same
change that adds, renames, or removes a rule file. A rule is only reachable from Codex if
its frontmatter carries `globs` or `alwaysApply: true`.
