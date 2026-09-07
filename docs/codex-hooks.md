# Codex hooks for project rules

This page is about **contributors running the Codex CLI against this repository**. It is not about Coddy's own rules discovery, which is a product feature documented in [Rules](rules.md) - that one describes how the `coddy` binary injects `{{.Rules}}` into its system prompt.

## Why a hook is needed

Codex resolves its instruction chain **once per session**, walking from the repository root down to the directory it was launched in, and concatenating at most one `AGENTS.md` per directory. Two consequences matter here:

- a session started at the repository root never loads a nested `AGENTS.md`, no matter which files it goes on to edit;
- there is no glob-based attachment. Codex has no equivalent of the `globs` field in `.cursor/rules/*.mdc`, so scope can only be expressed by where a file sits in the tree.

This repository keeps its detailed rules in `.cursor/rules/*.mdc`. The previous arrangement pointed Codex at `.codex/rules.md` and asked it to open the relevant rule file before editing. That is advisory, and it gets skipped.

`.codex/hooks/attach_rules.py` closes the gap by injecting rule bodies into the session directly, so compliance no longer depends on the model choosing to read a file.

## What fires when

| Event | Matcher | What is injected |
|---|---|---|
| `SessionStart` | `startup\|resume\|clear\|compact` | every rule whose frontmatter has `alwaysApply: true` |
| `PreToolUse` | `^(apply_patch\|Edit\|Write)$` | rules whose `globs` cover a file in the pending patch |

Configuration lives in `.codex/hooks.json`. Both handlers run the same script; it branches on `hook_event_name` from the hook payload.

For this repository that currently means `architecture`, `code-style`, `testing` and `workflow` arrive at session start (about 9.5 KB total), and the remaining six attach on demand. A patch touching `external/httpserver/server.go` pulls in `api-layer` and `implementation-order`; one touching `external/ui/src/` pulls in `ui-spa` and `ui-verification`.

## How it works

The script parses the `.mdc` frontmatter itself (`description`, `globs`, `alwaysApply`), so `.cursor/rules/` stays the single source of truth and a new rule file needs no wiring. Points worth knowing:

- **Glob matching is hand-rolled.** `fnmatch` is unusable because its `*` also crosses `/`, which makes `external/httpserver/**/*.go` miss `external/httpserver/server.go`. The script translates globs to a regex where `**/` becomes "zero or more directories", `*` stays inside one segment, and a bare `**` spans anything.
- **Paths come from the patch itself.** `*** Add File:`, `*** Update File:`, `*** Delete File:` and the `*** Move to:` destination are all read, and absolute paths are made repo-relative before matching.
- **Each rule is injected at most once per session.** State is a JSON file under `<tempdir>/codex-coddy-rules/<session_id>.json`, so re-editing the same area does not re-send the same 3 KB. A `SessionStart` with `source: "clear"` resets it.
- **It fails open.** Malformed JSON on stdin, an unparsable rule file, or an unwritable state directory all exit 0 with no output. A broken rule can never block an edit.

## Context budget

Codex caps model-visible hook output at roughly 2500 tokens by default, then **spills** the remainder to `<temp_dir>/hook_outputs/` and shows the model a head-and-tail preview instead. Both handlers therefore raise `additionalContextLimit`, to 8000 at session start and 6000 per patch. Those are ceilings, not reservations; the real payloads sit well under them.

Keep this in mind when adding rules. The budget is shared with every other hook and plugin in the session, and oversized always-on context degrades the model rather than helping it.

## Trust

Codex requires explicit approval before a non-managed command hook can run, and records that approval against the **hash of the hook definition**. An unapproved hook is skipped without a hard error, which looks exactly like a hook that is not firing.

- Run `/hooks` once per clone to review and trust both handlers.
- Run it again after **any** edit to `attach_rules.py` or `hooks.json`.
- Project-local hooks load only when the `.codex/` layer is trusted. In an untrusted checkout Codex still loads user and system hooks, but not these.

This mirrors the workspace trust gate Coddy applies to project-local `.coddy/mcp.json` (see [MCP Integration](mcp-integration.md)): opening a repository must not by itself grant it code execution.

## Verifying and debugging

Drive the script by hand with a synthetic payload. It reads JSON on stdin and writes JSON on stdout, so no Codex session is needed:

```bash
echo '{"hook_event_name":"PreToolUse","session_id":"probe","tool_input":{"command":"*** Begin Patch\n*** Update File: external/httpserver/server.go\n*** End Patch"}}' | python3 .codex/hooks/attach_rules.py
```

Session-start behaviour:

```bash
echo '{"hook_event_name":"SessionStart","session_id":"probe","source":"startup"}' | python3 .codex/hooks/attach_rules.py
```

Empty output is a valid answer and means one of three things: no glob matched, the rule was already sent in this session, or the input was not understood. Clear the dedup state to re-test:

```bash
rm -rf "${TMPDIR:-/tmp}/codex-coddy-rules"
```

If a hook produces nothing inside a real session, check `/hooks` first. Trust is the usual cause.

## Adding or changing a rule

Edit `.cursor/rules/*.mdc` as before. The hook needs no change, but two things do:

- a rule is only reachable from Codex if its frontmatter carries `globs` or `alwaysApply: true`;
- the index in `.codex/rules.md` and the bridge section in `AGENTS.md` are refreshed in the same change that adds, renames, or removes a rule file.

## Not to be confused with Codex `.rules`

Codex uses the word "rules" for a second, unrelated mechanism: `.rules` files written in Starlark under a `rules/` directory next to an active config layer, which decide **which shell commands may run outside the sandbox**. Those are an execution policy, not instructions, and they are validated with:

```bash
codex execpolicy check --pretty --rules ~/.codex/rules/default.rules -- go test ./...
```

This repository ships no `.codex/rules/` directory; command policy is a per-developer concern and lives in `~/.codex/rules/`. Note that Codex only decomposes a `bash -lc` script into separate commands when it is a plain chain of words joined by `&&`, `||`, `;` or `|`. A script containing `VAR=x`, `$(...)`, a wildcard, or control flow is matched as one opaque invocation, which is why auto-recorded allow-list entries tend to be unusable on any later command.

## Equivalent setups in other agents

| Agent | Always-on rules | Scoped rules |
|---|---|---|
| Cursor | `alwaysApply: true` in `.cursor/rules/*.mdc` | `globs` in the same frontmatter |
| Claude Code | `CLAUDE.md` (symlinked to `AGENTS.md` here) | `.claude/rules/*.md` referenced from it |
| Codex | `AGENTS.md` plus the `SessionStart` hook | `PreToolUse` hook, this page |

Upstream reference: [Hooks](https://developers.openai.com/codex/hooks) and [Custom instructions with AGENTS.md](https://developers.openai.com/codex/guides/agents-md).
