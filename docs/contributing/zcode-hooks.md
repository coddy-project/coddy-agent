# ZCode hooks for project rules

This page is about **contributors running the ZCode client against this repository**. It is the ZCode counterpart of [Codex hooks for project rules](codex-hooks.md); both deliver the same `.cursor/rules/*.mdc` files to the model, each through its own client's hook mechanism.

## Why a hook is needed

ZCode loads its instruction file (`AGENTS.md`) once per session, searching from the working directory up to the project root. Two consequences mirror the Codex case:

- a session started at the repository root never loads a nested `AGENTS.md`;
- there is no glob-based attachment. ZCode has no equivalent of the `globs` field in `.cursor/rules/*.mdc`, so scope can only be expressed by where a file sits in the tree.

This repository keeps its detailed rules in `.cursor/rules/*.mdc`. Pointing the model at a rule file and asking it to read it is advisory and gets skipped.

`.zcode/hooks/attach_rules.py` closes the gap by injecting rule bodies into the session directly, so compliance no longer depends on the model choosing to read a file.

## What fires when

| Event | Matcher | What is injected |
|---|---|---|
| `SessionStart` | `startup\|resume\|clear\|compact` | every rule whose frontmatter has `alwaysApply: true` |
| `PreToolUse` | `^(Edit\|Write\|MultiEdit\|ApplyPatch)$` | rules whose `globs` cover a file in the pending tool call |

Configuration lives in `.zcode/config.json` under `hooks.events`. Configuration-file hooks are disabled by default, so the config sets `hooks.enabled: true`. Both handlers run the same script; it branches on `hook_event_name` from the hook payload. The `ApplyPatch` alias is included in the `PreToolUse` matcher because ZCode aliases `Write`/`Edit` ← `ApplyPatch`.

For this repository, `architecture`, `code-style`, `testing`, and `workflow` arrive at session start (the `alwaysApply: true` rules), and the remaining six attach on demand. An edit touching `external/httpserver/server.go` pulls in `api-layer` and `implementation-order`; one touching `external/ui/src/` pulls in `ui-spa` and `ui-verification`.

## How it works

The script parses the `.mdc` frontmatter itself (`description`, `globs`, `alwaysApply`), so `.cursor/rules/` stays the single source of truth and a new rule file needs no wiring. Points worth knowing:

- **Glob matching is hand-rolled.** `fnmatch` is unusable because its `*` also crosses `/`, which makes `external/httpserver/**/*.go` miss `external/httpserver/server.go`. The script translates globs to a regex where `**/` becomes "zero or more directories", `*` stays inside one segment, and a bare `**` spans anything.
- **Paths come from the tool payload, not a patch blob.** Unlike the Codex sibling (which scans for `*** Update File:` lines inside an `apply_patch` command), ZCode hands the hook a JSON object. The script walks `tool_input` and keeps the string values found under known path-carrying keys (`file_path`, `path`, `notebook_path`, `source`, `destination`, `new_path`, `old_path`, ...), so a nested or absolute path is still matched once normalised.
- **Each rule is injected at most once per session.** State is a JSON file under `<tempdir>/zcode-coddy-rules/<session_id>.json`, keyed by the session id from the payload (also available as `${CLAUDE_SESSION_ID}`), so re-editing the same area does not re-send the same rule. A `SessionStart` with `source: "clear"` drops the dynamic history and reseeds it with the always-on set.
- **It fails open.** Malformed JSON on stdin, an unparsable rule file, or an unwritable state directory all exit 0 with no output. A broken rule can never block an edit.

## Enabling

ZCode configuration-file hooks are disabled until `hooks.enabled: true` is set, so the committed `.zcode/config.json` already turns the runner on. Unlike Codex, there is no per-hook trust gate: a workspace config with `enabled: true` runs unconditionally, which is the intended behaviour for a repository-shared setup. Open the repository in ZCode and the two handlers are active; nothing else is required.

## Windows interpreter

The hook command invokes `python`, not `python3`. On Windows the `python3` name is often a Microsoft Store stub that prints nothing and exits non-zero, while the real interpreter is `python` (or the `py` launcher). On Unix-like systems `python` is typically present as well; if a system only provides `python3`, point the command in `.zcode/config.json` at it. The `${ZCODE_PROJECT_DIR}` template variable expands to the repository root inside the ZCode session.

## Verifying and debugging

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
rm -rf "${TMPDIR:-/tmp}/zcode-coddy-rules"
```

To probe against a throwaway rules directory and state without touching the session-shared defaults, set `ZCODE_RULES_DIR` and `ZCODE_RULES_STATE_DIR`.

## Adding or changing a rule

Edit `.cursor/rules/*.mdc` as before. The hook needs no change, but two things do:

- a rule is only reachable from ZCode if its frontmatter carries `globs` or `alwaysApply: true`;
- the index in `.codex/rules.md` and the bridge sections in `AGENTS.md` are refreshed in the same change that adds, renames, or removes a rule file.

## Relationship to the Codex hook

This script and `.codex/hooks/attach_rules.py` share the same goal and most of their logic (`parse_rule`, `glob_to_regex`, per-session state, the `SessionStart`/`PreToolUse` split, fail-open). They differ in one place: how the edited file paths are recovered. The Codex variant parses an embedded `apply_patch` blob (`*** Update File:` lines); this ZCode variant walks the `tool_input` JSON fields. Keep the two in sync when changing shared logic, and test both after edits.

Upstream reference: ZCode hooks and configuration live under `~/.zcode/cli/config.json` (user scope) and `<repo>/.zcode/config.json` (workspace scope); the supported events are `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PostToolUseFailure`, and `Stop`.
