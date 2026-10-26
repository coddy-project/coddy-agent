# Kimi Code hooks for project rules

This page is for contributors running Kimi Code against this repository. It is the Kimi Code counterpart of [Codex hooks](codex-hooks.md) and [ZCode hooks](zcode-hooks.md): all three read the same `.cursor/rules/*.mdc` files through their client's lifecycle hooks.

## Why a hook is needed

Kimi Code reads the repository's `AGENTS.md`, but it does not interpret the `globs` and `alwaysApply` fields in `.cursor/rules/*.mdc`. Asking the model to open the relevant rule before every edit is advisory and can be skipped.

`.kimi-code/hooks/attach_rules.py` reads that frontmatter directly and hands the rule bodies to the model. The Cursor tree is the adapter input, not a universal policy format, and the adapter maintains no separate Kimi Code copy of a rule body.

## What fires when

Kimi Code, checked against version 2.3.2, gives a hook two ways to reach the model. It appends the `message` a `UserPromptSubmit` hook prints to the user's prompt, and it returns the reason a `PreToolUse` hook gives for refusing a call as the result of that call. Kimi Code discards what a `SessionStart` hook prints and what a `PreToolUse` hook prints while letting the call run, so the adapter uses neither.

| Event | Matcher | What the hook does |
|---|---|---|
| `UserPromptSubmit` | none | appends every rule whose frontmatter has `alwaysApply: true` to the first prompt of a session |
| `PreToolUse` | `^(Edit\|Write)$` | refuses the first call that touches files covered by scoped rules not sent yet, with those rules as the reason; the model repeats the call, and the repeated call runs |
| `PostCompact` | none | forgets which rules were sent, so they arrive again after a compaction |

All three entries run the same script, which branches on `hook_event_name` from the JSON payload. Kimi Code passes the path of an `Edit` or `Write` call in `tool_input.path`. The script also recognizes other common path fields, nested ones included, and reads a relative path from the folder the session works in.

A refusal carries at most 45,000 characters, because Kimi Code moves a longer tool result into a file and shows the model only its beginning. When the rules for a call are longer, each attempt carries the next part, and the call runs once every part has arrived. For a Go file of this repository that usually takes two refusals.

## Enabling

Kimi Code reads hook declarations only from the **user-level** `~/.kimi-code/config.toml` (or `$KIMI_CODE_HOME/config.toml`) and runs them in every project, from the folder the session works in. The repository therefore ships `.kimi-code/hooks.toml` as a fragment to merge, not as a config file Kimi Code loads by itself.

1. Append the three `[[hooks]]` entries from `.kimi-code/hooks.toml` to the user-level `config.toml`, keeping the hooks already there. Do not replace the whole config with the fragment.
2. Run `kimi doctor` to check the file, then start a new Kimi Code session.

Like the Codex hook, each command runs `"$(git rev-parse --show-toplevel)/.kimi-code/hooks/attach_rules.py"`, the script of the checkout the session works in. One set of entries therefore covers every checkout and worktree of this repository, and a session started in a subfolder too.

Each command ends in `|| exit 0`. In another project, or outside a git repository, Python cannot open the script and exits with code 2, and Kimi Code reads exit code 2 as a refusal of the prompt or the edit. `|| exit 0` turns that into a hook that does nothing.

Kimi Code runs the commands through `sh`, and through `cmd.exe` on Windows. `cmd.exe` does not expand `$(...)`, so on Windows the hook does nothing.

## How it works

- **The frontmatter parser matches the other Python adapters.** It accepts scalar and YAML-list `globs`, quoted commas, escapes, comments, and multiline flow lists without a third-party YAML dependency.
- **Glob matching is hand-rolled.** `**/` means zero or more directories, `*` stays inside one path segment, and a bare `**` spans anything. Absolute paths inside the checkout become repo-relative and relative paths are read from the session's folder; paths that end up outside the checkout are ignored.
- **Each rule reaches a session at most once until the next compaction.** State lives under `<tempdir>/kimi-code-attach-rules-<repository-hash>/<session_id>.json`, so clones cannot suppress one another. A resumed session keeps its state, and `/new` or `/clear` starts a session with a new id. Concurrent processes serialize delivery with a per-session lock, and state updates use atomic replacement.
- **A refusal is issued only once it is recorded.** If the state cannot be locked, written or read back, the edit runs without the scoped rules, so a retry is never refused again for the same rules. The always-on rules are still appended to the prompt in that case and may repeat.
- **It fails open.** Malformed JSON, an unreadable rule or an unwritable state directory refuses nothing. The hook delivers instructions and enforces nothing.

## Verifying and debugging

Run the repository adapter contract, which covers Kimi Code together with the other hosts and reads the hook's output the way Kimi Code 2.3.2 does:

```bash
make test-agent-rules
```

Drive the real script with synthetic Kimi Code payloads from the root of the checkout. Without `cwd` in the payload, the script takes its own working folder for the session's folder.

```bash
echo '{"hook_event_name":"UserPromptSubmit","session_id":"kimi-probe","prompt":"hello"}' | python3 .kimi-code/hooks/attach_rules.py
echo '{"hook_event_name":"PreToolUse","session_id":"kimi-edit-probe","tool_name":"Edit","tool_input":{"path":"cmd/coddy/providers.go"}}' | python3 .kimi-code/hooks/attach_rules.py
```

The first command prints a JSON object whose `message` holds the always-on rules. The second prints a refusal: `hookSpecificOutput.permissionDecision` is `deny` and `permissionDecisionReason` holds the rules. Repeat it to get the next part, until it prints nothing. Empty output with exit code 0 means that no glob matched, the rule was already sent in that session, or the input was not understood. Use a fresh session id for another probe, set `KIMI_CODE_RULES_STATE_DIR` to a throwaway directory, or remove `<tempdir>/kimi-code-attach-rules-*` to clear the state. `KIMI_CODE_RULES_DIR` can point at a fixture rule tree inside the checkout for isolated tests.

After changing the user-level config, verify in a **new** Kimi Code session rather than assuming an existing session has reloaded its hooks. The first prompt shows a `UserPromptSubmit hook` block with the always-on rules, and the first edit of a Go file is refused with the scoped rules before it runs.

## Relationship to the other adapters

This script shares its frontmatter parser, glob matching, locking and state files with `.zcode/hooks/attach_rules.py`: both receive edited paths as JSON fields rather than requiring Codex `apply_patch` headers. The delivery differs, because Kimi Code reads neither `additionalContext` nor the output of `SessionStart`. Like the [OpenCode plugin](opencode-hooks.md), this adapter refuses a write once to bring the scoped rules in. Keep the shared parts aligned when changing the adapters, and cover every changed adapter through `make test-agent-rules`.

Upstream references: [Kimi Code hooks](https://www.kimi.com/code/docs/en/kimi-code-cli/customization/hooks.html) and [configuration files](https://www.kimi.com/code/docs/en/kimi-code-cli/configuration/config-files.html).
