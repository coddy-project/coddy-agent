# ZCode hooks for project rules

This page is about **contributors running the ZCode client against this repository**. It is the ZCode counterpart of [Codex hooks for project rules](codex-hooks.md); both deliver the same `.cursor/rules/*.mdc` files to the model, each through its own client's hook mechanism.

## Why a hook is needed

ZCode loads its instruction file (`AGENTS.md`) once per session, searching from the working directory up to the project root. Two consequences mirror the Codex case:

- a session started at the repository root never loads a nested `AGENTS.md`;
- there is no glob-based attachment. ZCode has no equivalent of the `globs` field in `.cursor/rules/*.mdc`, so scope can only be expressed by where a file sits in the tree.

This repository keeps a native Cursor representation under `.cursor/rules/*.mdc`, and the ZCode adapter reads those files directly. The Cursor tree is the adapter input, not a universal policy format. Pointing the model at another file and asking it to read it remains advisory and can be skipped.

`.zcode/hooks/attach_rules.py` closes the scoped-rule gap by injecting rule bodies into the session directly, without maintaining a separate ZCode copy.

## What fires when

| Event | Matcher | What is injected |
|---|---|---|
| `SessionStart` | `startup\|resume\|clear\|compact` | every rule whose frontmatter has `alwaysApply: true` |
| `PreToolUse` | `^(Edit\|Write\|MultiEdit\|ApplyPatch)$` | rules whose `globs` cover a file in the pending tool call |

Configuration lives in `.zcode/config.json` under `hooks.events`. Configuration-file hooks are disabled by default, so the config sets `hooks.enabled: true`. Both handlers run the same script; it branches on `hook_event_name` from the hook payload. The `ApplyPatch` alias is included in the `PreToolUse` matcher because ZCode aliases `Write`/`Edit` ← `ApplyPatch`.

For this repository, `russian-wording` arrives at session start. The Go, HTTP, gateway and UI topic rules are path-scoped and attach on demand. An edit touching `external/httpserver/server.go` pulls in `api-layer`, `architecture`, `code-style`, `implementation-order`, `testing` and `workflow`; one touching `external/ui/src/` also pulls in `ui-spa` and `ui-verification`.

## How it works

The project adapter parses `.mdc` frontmatter itself (`description`, scalar or list `globs`, and `alwaysApply`). Quoted commas, doubled single quotes, double-quoted escapes and YAML comments in valid scalar, single-line or multiline flow-list, and block-list forms are decoded or stripped as appropriate. It reads `.cursor/rules/` as its input and keeps no separate ZCode copy of a rule body. A new Cursor rule file needs no adapter wiring. Points worth knowing:

- **Glob matching is hand-rolled.** `fnmatch` is unusable because its `*` also crosses `/`, which makes `external/httpserver/**/*.go` miss `external/httpserver/server.go`. The script translates globs to a regex where `**/` becomes "zero or more directories", `*` stays inside one segment, and a bare `**` spans anything.
- **Paths come from structured tool fields.** ZCode hands the hook a JSON object, so the script walks `tool_input` and keeps string values under known path-carrying keys (`file_path`, `path`, `notebook_path`, `source`, `destination`, `new_path`, `old_path`, ...). The Codex sibling accepts those fields too and additionally parses `apply_patch` headers. Nested and absolute paths are normalised before matching.
- **Each scoped rule is injected at most once per active session context.** State is a JSON file under `<tempdir>/zcode-attach-rules-<repository-hash>/<session_id>.json`, keyed by repository and session id, so clones cannot suppress one another. `resume` preserves scoped dedupe; `startup`, `compact` and `clear` reset state to the always-on set. Concurrent processes serialize delivery with a per-session lock, and state updates use atomic replacement. Rule ids are persisted only after context output succeeds, so failed output leaves them eligible for a later attempt. If locking or persistence is unavailable, matching rule context is still emitted and deduplication may temporarily degrade to duplicate delivery.
- **It fails open.** Malformed JSON on stdin, an unparsable rule file, or an unwritable state directory all exit 0 with no output. A broken rule can never block an edit.

## Enabling

ZCode configuration-file hooks are disabled until `hooks.enabled: true` is set, so the committed `.zcode/config.json` already turns the runner on. Unlike Codex, there is no per-hook trust gate: a workspace config with `enabled: true` runs unconditionally, which is the intended behaviour for a repository-shared setup. Open the repository in ZCode and the two handlers are active; nothing else is required.

## Windows interpreter

The hook command invokes `python`, not `python3`. On Windows the `python3` name is often a Microsoft Store stub that prints nothing and exits non-zero, while the real interpreter is `python` (or the `py` launcher). On Unix-like systems `python` is typically present as well; if a system only provides `python3`, point the command in `.zcode/config.json` at it. The `${ZCODE_PROJECT_DIR}` template variable expands to the repository root inside the ZCode session.

## Verifying and debugging

Run the repository adapter contract first:

```bash
make test-agent-rules
```

It covers scalar and YAML-list `globs`, structured edit paths, provider-rule activation, OpenCode delivery and the absence of a manual Codex index.

Drive the script by hand with a synthetic payload. It reads JSON on stdin and writes JSON on stdout, so no ZCode session is needed:

```bash
echo '{"hook_event_name":"PreToolUse","tool_name":"Edit","session_id":"probe","tool_input":{"file_path":"external/httpserver/server.go"}}' | python .zcode/hooks/attach_rules.py
```

Session-start behaviour:

```bash
echo '{"hook_event_name":"SessionStart","session_id":"probe","source":"startup"}' | python .zcode/hooks/attach_rules.py
```

Empty output is a valid answer and means one of three things: no glob matched, the rule was already sent in this session, or the input was not understood. Clear the dedup state to re-test:

```bash
rm -rf "${TMPDIR:-/tmp}"/zcode-attach-rules-*
```

To probe against a throwaway rules directory and state without touching the session-shared defaults, set `ZCODE_RULES_DIR` and `ZCODE_RULES_STATE_DIR`.

## Adding or changing a rule

Edit `.cursor/rules/*.mdc` as before. The hook needs no change when a topic is added or renamed because it reads the directory on each event.

- a rule is reachable from ZCode only when its frontmatter carries `globs` or `alwaysApply: true`;
- update the deliberate Claude Code counterpart in the same change and keep the bodies equivalent;
- run `make test-agent-rules` after changing rule frontmatter or either Python adapter.

## Relationship to the Codex hook

This script and `.codex/hooks/attach_rules.py` share the same goal and most of their logic (`parse_rule`, `glob_to_regex`, per-session state, the `SessionStart`/`PreToolUse` split, fail-open). Both accept known structured path fields; the Codex variant additionally parses `apply_patch` headers. Keep shared logic in sync and cover both adapters through `make test-agent-rules`.

Upstream reference: ZCode hooks and configuration live under `~/.zcode/cli/config.json` (user scope) and `<repo>/.zcode/config.json` (workspace scope); the supported events are `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PostToolUseFailure`, and `Stop`.
