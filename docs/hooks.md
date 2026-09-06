# Hooks

A hook is a command of your own that Coddy runs at a lifecycle point of a session: before a tool call, after it, and (see the event table) at the other points of a turn. The command reads one JSON document on stdin, does whatever it wants, and answers with an exit code and optional JSON on stdout. With that it can block a call, approve it past the permission prompt, rewrite its arguments, hand the model extra context, or just log what happened.

Hooks run with your permissions, before any permission prompt, on every matching call. That is the point of the feature and the reason files that arrive with a checkout are held until you approve them (see [Project files and trust](#project-files-and-trust)). Review every hook you enable.

Definitions use the file shape of Claude Code, and the event names, payload fields and answer fields follow it too, so a hook written for Claude Code (or Codex, which uses the same shape) works in Coddy unchanged for the covered events. The design record with the comparison of six agents is `docs/plans/hooks.md`.

## Definition files

Coddy reads the files listed in `hooks.files`, lowest priority first. The default list:

| File | Scope | Notes |
|---|---|---|
| `~/.coddy/hooks.json` | user | your own file (`${CODDY_HOME}/hooks.json`) |
| `<workspace>/.claude/settings.json` | project | Claude Code compatibility: only its `hooks` key is read |
| `<workspace>/.claude/settings.local.json` | project | same |
| `<workspace>/.coddy/hooks.json` | project | the workspace's own file |

`${CODDY_HOME}`, `${CWD}` and a leading `~` expand; a relative entry resolves against the session cwd. A file that does not exist is skipped. Every matching hook from every file runs; priority only orders the catalog and the run order (user file first, then the project files in list order, then definition order inside a file).

Scope is decided on canonical paths (absolute, symlinks resolved): a file at or under the session cwd is **project scope** and follows `hooks.project_trust`; everything else is **user scope** and always runs.

### File shape

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "run_command",
        "hooks": [
          { "type": "command", "command": ".coddy/hooks/guard.sh", "timeout": 30 }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "edit|write|apply_patch",
        "hooks": [
          { "type": "command", "command": "gofmt -l ." }
        ]
      }
    ]
  }
}
```

Three levels: the **event** (`PreToolUse`), a **matcher group** (which tools it applies to) and the **handlers** that run. A group without `matcher`, or with `"*"`, applies to every occurrence of the event.

Handler fields:

| Field | Meaning |
|---|---|
| `type` | `command` is the only type that runs. Claude Code's `http`, `prompt`, `agent` and Codex's `mcp_tool` parse but are skipped with a warning and shown as unsupported in the catalog. |
| `command` | The command line. Without `args` it runs through the host shell (bash or sh; pwsh, PowerShell or cmd on Windows, the same detection `run_command` uses). |
| `args` | Optional. When the key is present the command is spawned directly with these arguments and no shell (Claude Code's exec form). |
| `commandWindows` | Optional replacement for `command` when Coddy runs on Windows (Codex's key; `command_windows` is accepted too). |
| `timeout` | Seconds. Default `hooks.default_timeout_seconds` (60). A hook that overruns is terminated with its whole process group. |
| `async` | `true` runs the hook detached: its output is ignored and it can never block or decide (so `failClosed` on an async handler is dropped with a warning). At most 8 detached hooks run at once across the process; the rest wait for a slot. |
| `failClosed` | `true` turns a crash, a timeout, an interruption by the turn's cancellation, or invalid output into a block instead of a non-blocking error (`fail_closed` is accepted too). Use it for policy hooks. |

`statusMessage`, `shell`, `once` and `additionalContextLimit` are accepted and ignored. Claude Code's `if` filter is ignored with a logged warning: the hook runs for every matching call, so a policy that relied on `if` must check the arguments itself.

### Matchers

Claude Code's rules apply:

- empty or `*` matches everything;
- a value made only of letters, digits, `_`, `-`, spaces, `,` and `|` is an exact name or a list of exact names: `run_command`, `edit|write`, `Edit, Write`;
- anything else is an unanchored regular expression (Go syntax): `^run_.*`, `mcp__filesystem__.*`. An invalid expression never matches.

Tool events compare the matcher with the tool name. Coddy's own names are the ones the model sees (`run_command`, `read`, `write`, `edit`, `apply_patch`, `glob`, `grep`, `webfetch`, `websearch`, `spawn_agent`, `question`, ...). MCP tools are `server__tool` and also match the `mcp__server__tool` spelling. The names Claude Code and Codex use are accepted as aliases, so `Bash` matches `run_command`, `Edit` and `Write` match `edit`, `write` and `apply_patch`, `Read` matches `read`, `Task` and `Agent` match `spawn_agent`, `WebFetch` and `WebSearch` match `webfetch` and `websearch`. Argument names are not translated: `tool_input` carries Coddy's fields (`command` for `run_command`; `path` and `content` for `write`; `path`, `pattern` and friends for the filesystem tools), so a script ported from Claude Code that reads `tool_input.file_path` must read `tool_input.path`.

## Events

| Event | Fires | Matcher subject | Can block |
|---|---|---|---|
| `SessionStart` | `session/new` (`startup`) and `session/load` or a reopen (`resume`), synchronously, before the session is returned | source | no; context only |
| `UserPromptSubmit` | when the user submits a prompt, after the built-in `/compact` and `/plugin` commands are recognised and before the prompt becomes a message | none | yes: the prompt is rejected |
| `PreToolUse` | before a tool call runs, after the mode and subagent checks and before the permission prompt, whatever the permission mode; it runs again when a permission that was persisted over HTTP is resumed, on the arguments the prompt showed (a deny still denies; a hook that changes those arguments again cancels the call, because the answer covered what the user saw) | tool name | yes: deny, or force or skip the prompt |
| `PostToolUse` | after a tool returned without error | tool name | no; feedback and context only |
| `PostToolUseFailure` | after a tool returned an error (not after a permission denial or a hook denial) | tool name | no; context only |
| `Stop` | when the ReAct loop is about to end the turn with `end_turn` (not on cancel, an error, or the turn cap) | none | yes: the agent is sent back to work |
| `PreCompact` | before `/compact` (`manual`) or an automatic compaction (`auto`) | trigger | yes: the compaction is vetoed |
| `PostCompact` | after a compaction | trigger | no |
| `SubagentStart` | in the parent, when `spawn_agent` is about to start a child, after the definition and its trust were resolved | subagent name | yes: the spawn is refused |
| `SubagentStop` | in the parent, when the child's turn ended, before its report reaches the parent | subagent name | no |
| `Notification` | when a permission prompt is about to be sent to the client | notification type (`permission_prompt`) | no |

Every event also fires inside a child session for the child's own turn (tool calls, prompt, stop, compaction), with the `subagent` block in the payload naming the child; `SubagentStart` and `SubagentStop` fire in the parent's session.

## What a hook receives

One JSON object on stdin. The session fields come first, the event fields after them:

```json
{
  "session_id": "20260906-131500-a1b2c3",
  "hook_event_name": "PreToolUse",
  "cwd": "/home/op/project",
  "transcript_path": "/home/op/.coddy/sessions/20260906-131500-a1b2c3/messages.json",
  "permission_mode": "ask",
  "mode": "agent",
  "model": "openai/gpt-5",
  "turn": 3,
  "tool_name": "run_command",
  "tool_input": { "command": "rm -rf build" },
  "tool_use_id": "call_01"
}
```

| Field | Meaning |
|---|---|
| `session_id` | the session the call belongs to |
| `hook_event_name` | the event |
| `cwd` | the session working directory (also the hook's working directory) |
| `transcript_path` | the session's `messages.json`; empty when persistence is off |
| `permission_mode` | `ask`, `accept_edits` or `bypass` |
| `mode` | `agent`, `plan` or `ask` |
| `model` | the effective model id |
| `turn` | the user turn index |
| `subagent` | present inside a child session: `{"name", "parent_session_id", "depth"}` |
| `tool_name`, `tool_input`, `tool_use_id` | tool events: the tool, its arguments as an object, the call id |
| `tool_response` | `PostToolUse`: the text the model receives |
| `error` | `PostToolUseFailure`: the error text |
| `duration_ms` | `PostToolUse` and `PostToolUseFailure`: how long the tool took |
| `source` | `SessionStart`: `startup` or `resume` |
| `prompt` | `UserPromptSubmit`: the prompt text |
| `stop_hook_active`, `last_assistant_message` | `Stop`: whether a Stop hook already sent the agent back to work in this turn, and the assistant's final text |
| `trigger`, `custom_instructions` | `PreCompact`: `manual` or `auto`, and the text after `/compact` |
| `trigger`, `summary` | `PostCompact`: the trigger and the generated summary (cut at 4,000 characters) |
| `agent_name`, `agent_session_id`, `prompt`, `background` | `SubagentStart`: the definition name, the child session id, the task prompt and whether the run is detached |
| `agent_name`, `agent_session_id`, `task_id`, `status`, `report`, `turns` | `SubagentStop`: the child's outcome (`end_turn`, `cancelled`, `failed`, ...), its final report (cut at 4,000 characters) and its assistant rounds |
| `notification_type`, `message`, `tool_name`, `tool_input`, `tool_use_id` | `Notification`: `permission_prompt` with the prompt body and the call waiting for an answer |

The process also gets `CODDY_PROJECT_DIR` and `CLAUDE_PROJECT_DIR` (the session cwd), `CODDY_SESSION_ID`, `CODDY_HOOK_EVENT` and `CODDY_HOME` in its environment, on top of Coddy's own environment.

## What a hook answers

| Exit code | Effect |
|---|---|
| `0`, empty stdout | no decision: the ordinary flow applies. Silence never approves a call that would otherwise ask. |
| `0`, stdout is a JSON object | the fields below are applied |
| `0`, other stdout | plain text; ignored on tool events (it becomes context on the prompt and session events) |
| `2` | block: the call is denied with the JSON `reason` when there is one, else stderr, else a generic reason |
| anything else, a crash, a timeout, an interruption by the turn's cancellation, invalid JSON | a non-blocking error: logged and recorded once per session as a notice row, the event proceeds as if the hook were absent, unless the handler has `failClosed: true`, which turns it into a block with the error as the reason. A call whose hooks were interrupted by a cancellation does not run. |

JSON fields:

```json
{
  "continue": true,
  "stopReason": "shown to the user when continue is false",
  "systemMessage": "shown to the user as a notice row in the transcript, never to the model",
  "decision": "block",
  "reason": "why",
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "deny",
    "permissionDecisionReason": "why",
    "updatedInput": { "command": "echo replaced" },
    "additionalContext": "a note the model reads next to the result"
  }
}
```

- `continue: false` ends the turn after the current tool batch; `stopReason` is shown to the user. On `PreToolUse` the call is denied as well; on `UserPromptSubmit` the prompt is rejected; on `PreCompact` the compaction is vetoed; on `Stop` the turn simply ends.
- `PreToolUse`: `permissionDecision` is `deny` (the call is not executed; the reason goes to the model as the tool result `blocked by hook: <reason>`), `allow` (the permission prompt is skipped) or `ask` (the prompt is forced, even in a mode that would auto-approve). `updatedInput` replaces the whole argument object before the call runs; the tool call card shows the rewritten arguments. A top-level `decision: "block"` (or `"deny"`) with `reason` is the legacy spelling of deny.
- `PostToolUse`: `decision: "block"` with `reason` appends `Hook feedback: <reason>` to the tool result; the tool already ran, nothing is undone.
- `additionalContext` is appended to the tool result as `Hook context: ...` on the three tool events.
- `UserPromptSubmit`: `decision: "block"` with `reason` (or exit 2) rejects the prompt: it is not added to the transcript, the model is not called, and the turn ends with `prompt rejected by hook: <reason>`, which the SPA shows as an error row. `additionalContext` and plain stdout become the turn's part of the **hook context block** (below).
- `Stop`: `decision: "block"` with `reason` (or exit 2) sends the agent back to work. The reason, plus any `additionalContext`, is submitted as the next user message, persisted with the prefix `[Stop hook] ` so the transcript explains the continuation, and the loop continues in the same turn; the hook sees `stop_hook_active: true` on the next stop. At most `hooks.stop_loop_limit` continuations per turn (5), then the turn ends; the ReAct turn cap still applies.
- `SessionStart`: `additionalContext` and plain stdout are stored on the session (`hookContext` in `session.json`) and rendered in the hook context block of every system prompt of that session; a resume re-runs the hooks and replaces the stored text, clearing it when no hook runs any more. `systemMessage` becomes a notice row.
- `Stop`: a follow-up is submitted only while an iteration of the ReAct turn is left; on the last one the turn ends instead of leaving a dangling message.
- `PreCompact`: `decision: "block"` with `reason` (or exit 2) vetoes the compaction: `/compact` fails with `compaction blocked by hook: <reason>`, an automatic compaction is skipped for that check and the turn continues uncompacted.
- `SubagentStart`: `decision: "block"` with `reason` (or exit 2) refuses the spawn; the `spawn_agent` tool result reads `spawn of subagent "<name>" blocked by hook: <reason>` and no child session is created. `additionalContext` is prepended to the child's task prompt as `Hook context: ...`.
- `SubagentStop` and `Notification` are observational: `systemMessage` and errors are reported, decisions are ignored. A hook that pings a chat or raises a desktop notification should carry `"async": true` so the permission prompt is not delayed by it.

The **hook context block** is a `## Hook context` section appended to the system prompt after the environment block (so a custom `prompts.dir` template carries it too): first the session-level text from `SessionStart`, then the turn-level text from `UserPromptSubmit`. The turn-level part is not persisted; the session-level part is.

Several matching hooks run one after another, in catalog order; each sees the input as rewritten by the previous one, and all of them run even after a deny, so an audit hook sees every call. Decisions merge with the most restrictive winning (`deny` > `ask` > `allow`); every `additionalContext` is kept in order. Texts a hook hands over are capped at `hooks.max_output_chars` characters (10,000) and truncated with a marker past it. Stdout and stderr are captured up to 256 KiB each; a JSON answer that runs past that limit is reported as cut, not as invalid.

## Configuration

```yaml
hooks:
  enabled: true
  files:
    - "${CODDY_HOME}/hooks.json"
    - "${CWD}/.claude/settings.json"
    - "${CWD}/.claude/settings.local.json"
    - "${CWD}/.coddy/hooks.json"
  project_trust: ask          # ask | allow | deny
  default_timeout_seconds: 60
  stop_loop_limit: 5
  max_output_chars: 10000
```

| Key | Default | Meaning |
|---|---|---|
| `enabled` | `true` | load and run hooks at all |
| `files` | the four above | definition files, lowest priority first |
| `project_trust` | `ask` | what a project-scope file may do: `ask` (listed, held until approved), `allow` (runs like your own file), `deny` (never read) |
| `default_timeout_seconds` | `60` | per handler when the definition gives no `timeout` |
| `stop_loop_limit` | `5` | how many times per turn a `Stop` hook may send the agent back to work |
| `max_output_chars` | `10000` | cap, in characters, on every text a hook hands to the model or the user |

The keys are ordinary `config.yaml` keys, so the Settings page and the bundled `configure-coddy` skill can change them. Definitions are re-read at the start of every turn: editing a file takes effect on the next turn without a restart.

## Project files and trust

A hooks file inside the workspace arrived with the checkout. Under the default policy `ask` it is parsed and listed, but none of its hooks runs until you approve that exact file for that workspace on the machine running Coddy. The receipt is bound to a digest of the file, so editing an approved file withdraws the approval and asks again. Your own file (user scope) never needs approval.

The first turn that finds a held file records a notice in the session (the SPA shows it as a system row after the turn, once per session and file, without a retry control; the agent log carries the same line), so you learn that hooks exist and are held:

| Dark | Light |
|---|---|
| ![Held hooks file notice, dark](assets/screenshot-hooks-notice-dark.png) | ![Held hooks file notice, light](assets/screenshot-hooks-notice-light.png) |

Approval surfaces:

```
coddy hooks list [--cwd DIR]
coddy hooks trust <file> [--cwd DIR]
coddy hooks untrust <file> [--cwd DIR]
```

`list` prints the workspace, the effective `hooks.project_trust` and a table with one row per file: `FILE` (the name receipts use: the workspace-relative path for a project file, the absolute path for your own), `SCOPE`, `TRUST` (`trusted` or `needs_approval`) and a summary of its hooks (`PreToolUse(run_command); PostToolUse(*)`, or `invalid: <error>` for a file that does not parse), then a hint when project files await approval. `trust` prints the hooks it is about to approve and records the receipt in `~/.coddy/hooks-trust.json`, keyed by the canonical workspace path, the file and its digest; a user-scope file needs no approval and the command says so, an invalid file cannot be approved. `untrust` withdraws a receipt. `--cwd` defaults to the process working directory.

Over HTTP, `GET /coddy/hooks?cwd=<absolute path>` returns the same catalog with every handler as a row, and `POST /coddy/hooks/trust` / `POST /coddy/hooks/untrust` with the body `{"cwd": ..., "file": ".coddy/hooks.json"}` record or withdraw a receipt; `cwd` must be the session's server-side workspace, so this route is the approval path for a remote console or an ACP client, whose local `coddy hooks trust` would write a receipt on the wrong machine. The catalog's policy and the config keys can be changed in Settings > Hooks or with the bundled `configure-coddy` skill (`set hooks.project_trust=allow` for a checkout you trust; under `allow` project files need no receipt, under `deny` they are never read).

The receipt file is a sibling of `mcp-trust.json` and `subagents-trust.json`, never shared with them: one kind of approval must never read as another. A write is a transaction: an in-process lock shared by every store of the same path, a file lock (`hooks-trust.json.lock`) shared with other processes, and an atomic replace through a unique temporary file, so approvals from the CLI and the HTTP server cannot lose each other.

## Examples

Block destructive shell commands, whatever the permission mode (`~/.coddy/hooks.json`):

```json
{
  "hooks": {
    "PreToolUse": [
      { "matcher": "run_command", "hooks": [{ "type": "command", "command": "~/.coddy/hooks/no-rm-rf.sh", "failClosed": true }] }
    ]
  }
}
```

```bash
#!/bin/sh
# ~/.coddy/hooks/no-rm-rf.sh
command=$(jq -r '.tool_input.command // ""')
case "$command" in
  *"rm -rf"*)
    printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"rm -rf is not allowed here"}}'
    ;;
esac
exit 0
```

Run the formatter after every edit and tell the model what it changed:

```json
{
  "hooks": {
    "PostToolUse": [
      { "matcher": "edit|write|apply_patch", "hooks": [{ "type": "command", "command": "~/.coddy/hooks/gofmt.sh" }] }
    ]
  }
}
```

```bash
#!/bin/sh
path=$(jq -r '.tool_input.path // ""')
case "$path" in
  *.go)
    if out=$(gofmt -l "$path" 2>&1) && [ -n "$out" ]; then
      gofmt -w "$path"
      printf '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"gofmt reformatted %s"}}' "$path"
    fi
    ;;
esac
exit 0
```

Log every tool call to a file without touching the flow (an `async` hook cannot slow the turn down):

```json
{
  "hooks": {
    "PreToolUse": [
      { "hooks": [{ "type": "command", "command": "cat >> ~/.coddy/hook-audit.jsonl", "async": true }] }
    ]
  }
}
```

## Windows

Shell-form commands go through the shell `run_command` detected (pwsh, PowerShell or cmd), so write them in that shell's syntax or give `commandWindows`. Exec-form handlers (`args` present) need a real executable, not a `.cmd` shim. Timeouts terminate the hook's whole process tree.

## Differences from Claude Code

- Only `command` handlers run; `http`, `prompt`, `agent` and `mcp_tool` are skipped.
- `tool_input` carries Coddy's argument names.
- The `if` filter is ignored.
- Matching hooks run sequentially rather than in parallel, so `updatedInput` chains deterministically.
- A `PreToolUse` `allow` skips Coddy's permission prompt; there are no deny rules it could be subordinate to.
- Project files are approved out of band by a digest-bound receipt, never by an in-chat prompt.

## Testing

- Executable specs in `features/`: `hooks_tool_calls.feature` (tool events, the payload, the exit-code contract, Claude Code aliases), `hooks_project_trust.feature` (held and approved project files, the policies, the notice row, the HTTP catalog and approval routes), `hooks_turn_lifecycle.feature` (prompt rejection and context, the stop loop, session-start context, compaction veto and summary), `hooks_subagents.feature` (hooks inside a child, `SubagentStart` / `SubagentStop`, the permission-prompt notification). The godog harnesses live in `internal/agent/bdd_hooks_test.go`, `bdd_hooks_subagents_test.go` and `external/httpserver/bdd_hooks_test.go`; the hook process is the test binary itself re-executed through `internal/hooks/hooktest`, so no shell scripts are involved and the runner is covered on Windows too (`internal/hooks` is in the Windows CI job).
- Unit tests: `internal/hooks/hooks_test.go` (file shape, matchers, loader scopes, runner contract), `internal/hooks/trust_test.go` (receipts, catalog), `internal/config/hooks_test.go`, `cmd/coddy/hooks_test.go`.
- End-to-end against a real model on every surface, wired into the three runners: `examples/acp/acp_e2e_hooks.py` (ACP over stdio; `coddy hooks list` and `coddy hooks trust` between the turns), `examples/httpserver/http_e2e_hooks.py` (`GET /coddy/hooks` with the held project file, the `notice` row in `uiLog`, `POST /coddy/hooks/trust`, a second turn that runs the approved hook, `POST /coddy/hooks/untrust`) and `examples/cli/cli_e2e_hooks.py` (the console TUI with the same recorder hooks, the notice in the session's `ui_log.json`, approval through the CLI). Each records every `PreToolUse` and `PostToolUse` payload of a real `run_command` through user-scope hooks, checks that the project-scope hook stays held, approves it and checks that the next turn of the same session runs it.
