# Plan: hooks (operator commands at lifecycle points of a session)

Status: design record written 2026-09-06 on branch `claude/hooks` and
implemented the same day, sub-feature by sub-feature on that branch (section
6, five commits). Reference for the shipped behaviour is `docs/hooks.md`;
deviations found while implementing are recorded in section 8.

## 1. What

Let an operator attach their own commands to lifecycle points of a Coddy
session, the way Claude Code, Cursor, Codex and Gemini CLI do: before a tool
call (observe, block, auto-approve, rewrite the arguments, add context),
after it (feedback, audit), when the user submits a prompt, when the agent
stops (force it to keep working), when a session starts, before and after
compaction, and around subagent runs. A hook is a shell command that receives
one JSON document on stdin and answers with an exit code plus optional JSON on
stdout. Definitions live in JSON files: the operator's own `~/.coddy/hooks.json`
and, per workspace, `.coddy/hooks.json` plus the Claude Code files
`.claude/settings.json` / `.claude/settings.local.json` read for compatibility.

The problems being solved:

- **policy** that does not depend on the model choosing to comply: block
  `rm -rf`, `git push --force`, writes outside a directory, calls to an MCP
  server the operator does not know, whatever the permission mode;
- **automation around edits**: run a formatter or a linter after every
  `edit`/`write`, notify a chat when a permission prompt is waiting, log every
  tool call to an audit file;
- **guard rails at turn boundaries**: reject a prompt, add facts to the
  context on session start, keep the agent working until a checklist is done
  (the `/goal` pattern), veto an automatic compaction;
- **portability**: repositories already carrying Claude Code hooks work with
  Coddy unchanged for the common events, as they do in Cursor.

## 2. What the analogs do (research 2026-09-06)

Six agents were read from their official docs and, for Codex and Gemini,
their `main` sources; the full references with links are in the research
files kept with this plan's review notes. The points that decide this design:

| | Claude Code | Cursor | Codex CLI | Gemini CLI |
|---|---|---|---|---|
| Definition | `hooks` key in `settings.json` (user, project, local, managed), plugin `hooks/hooks.json`, skill and agent frontmatter | `hooks.json` at enterprise, team, project (`.cursor/`), user (`~/.cursor/`) | `hooks.json` or `[hooks]` in `config.toml` at `~/.codex/` and `<repo>/.codex/`, `requirements.toml`, plugins | `hooks` key in `settings.json` (project `.gemini/`, user, system), extension `hooks/hooks.json` |
| Shape | event -> `{matcher, hooks[]}` -> `{type, command, timeout, ...}` | event -> `[{command, matcher, timeout, loop_limit, failClosed}]` | same as Claude Code | same as Claude Code plus `sequential`, `name` |
| Handler types | command, http, mcp_tool, prompt, agent | command, prompt | command, mcp_tool | command |
| Events | 33 (PascalCase) | 21 (camelCase) | 12 (PascalCase) | 11 (PascalCase, `BeforeTool`/`AfterTool`) |
| Matcher | exact list or unanchored regex; `mcp__server__tool` | per-hook string against a hook-specific subject | exact list fast path, else regex; `mcp__server__tool`; aliases `Edit`/`Write`/`Agent` | regex for tool events, exact for lifecycle; `mcp_server_tool` |
| Input | stdin JSON: `session_id`, `transcript_path`, `cwd`, `permission_mode`, `hook_event_name`, `tool_name`, `tool_input`, `tool_response`, ... | stdin JSON with `conversation_id`, `workspace_roots`, per-hook fields | stdin JSON, same field names as Claude Code plus `model`, `turn_id` | stdin JSON, same base names plus `timestamp`, `llm_request` |
| Block | exit 2; `permissionDecision: deny`; `decision: block` | exit 2; `permission: deny`; `continue: false` | exit 2 with stderr; `permissionDecision: deny`; `decision: block` | exit 2; `decision: deny` |
| Skip the prompt | `PreToolUse` `allow` (deny rules still apply); `PermissionRequest` | `permission: allow`, `ask` forces a prompt | only `PermissionRequest`; `PreToolUse` `allow` just carries `updatedInput` | not possible; `ask` forces the prompt |
| Rewrite input | `updatedInput` (whole object) | `updated_input` | `updatedInput` with `allow` | `hookSpecificOutput.tool_input` (merge) |
| Context | `additionalContext` on most events, plain stdout on four | `additional_context` on two events | `additionalContext`, plain stdout on start and prompt events; spilled to disk past ~2500 tokens | `additionalContext`, wrapped in `<hook_context>` |
| Stop / continue | `continue: false`; `Stop` `decision: block` with an 8-block cap | `followup_message` with `loop_limit` (5) | `continue: false`; `Stop` block = continuation prompt, `stop_hook_active` | `continue: false`; `AfterAgent` deny = retry with reason |
| Failure | non-blocking unless exit 2; timeout = no decision | fail-open unless `failClosed` | failed run, operation continues; exit 2 with empty stderr = failure | any non-zero with text denies (source) |
| Timeout | 600 s (30 s on prompt events) | "platform default" | 600 s; SessionEnd 1 s | 60000 ms |
| Several hooks | parallel, most restrictive wins, all context kept | priority of the source wins | concurrent; any deny wins | parallel, OR of decisions; `sequential` chains outputs into inputs |
| Env | `CLAUDE_PROJECT_DIR`, plugin roots, `CLAUDE_ENV_FILE` | `CURSOR_PROJECT_DIR`, `CLAUDE_PROJECT_DIR` alias | captured session snapshot, plugin roots, `CLAUDE_*` aliases | `GEMINI_PROJECT_DIR`, `GEMINI_SESSION_ID`, `GEMINI_CWD`, `CLAUDE_PROJECT_DIR` alias |
| Project trust | settings hooks held until the workspace trust dialog; `-p` runs trust the folder (`--bare`, `disableAllHooks` mitigate) | only in trusted workspaces; CLI headless needs `--trust` | per-hook review, `sha256` of the normalized definition in user config, startup review prompt, project layer must be trusted | folder trust off by default; `name:command` fingerprint auto-trusted after a one-time warning |
| Subagents | settings hooks fire inside subagents with `agent_id`/`agent_type`; `SubagentStart`/`SubagentStop` | `subagentStart` (can deny), `subagentStop` (`followup_message`) | `SubagentStart`/`SubagentStop`; tool hooks inside with parent `session_id` | not documented |
| Claude Code compat | - | reads `.claude/settings*.json` with an event and tool-name map | imports hooks from other agents | `gemini hooks migrate --from-claude` |

OpenCode, pi and oh-my-pi have no shell-command hooks at all: each runs
TypeScript modules inside its own process (OpenCode plugins with
`tool.execute.before` / `tool.execute.after` / `chat.message` and friends, pi
extensions with 36 events such as `tool_call` / `tool_result` /
`before_agent_start`, oh-my-pi's fork of the same). Three of their choices
still inform this design: a blocked call surfaces the reason to the model as
the tool's error result (all three); handlers run sequentially in load order
with `tool_call` short-circuiting on the first block (pi, oh-my-pi); and
oh-my-pi's `session_stop` accepts the Claude and Codex `{decision: "block",
reason}` shape and caps consecutive continuations at eight. Project-local
modules load without consent in OpenCode and oh-my-pi; pi asks once per
directory and records the answer. A JavaScript runtime is not an option for a
static Go binary, so Coddy takes the shell-command model.

Sources read on 2026-09-06: Claude Code `code.claude.com/docs/en/hooks` and
`hooks-guide`, `permissions`, `sub-agents`, `settings-reference`; Cursor
`cursor.com/docs/hooks`, `docs/reference/third-party-hooks`, the CLI
changelog; Codex `developers.openai.com/codex/hooks` and the `codex-rs/hooks`
sources at release `rust-v0.153.0`; Gemini CLI `docs/hooks/*` and
`packages/core/src/hooks` on `main`; OpenCode `opencode.ai/docs/plugins` and
`packages/plugin/src/index.ts` (1.18.29); pi `packages/coding-agent/docs/extensions.md`
(0.85.1); oh-my-pi `docs/hooks.md`, `docs/extensions.md` (18.1.11).

Common denominator adopted here: the Claude Code file shape and event names
(three of the four use them, and both Cursor and Gemini map `.claude/settings.json`
onto their own model); stdin JSON in, exit code plus optional stdout JSON out;
exit 2 blocks; a `PreToolUse` that can deny, allow, ask, rewrite and add
context; `Stop` continuation with a loop cap; project files gated by trust
bound to content; `CLAUDE_PROJECT_DIR` exported as an alias for portable
scripts. Where the four differ, Coddy takes the stricter reading: trust is a
digest-bound receipt like Codex's hash (Gemini's auto-trust after a warning is
the outlier), and an unsupported output field is ignored rather than failing
the hook.

## 3. Approach

Coddy has the machinery around the feature; what is new is one package that
loads, matches and runs hook definitions, plus a handful of call sites.

- `internal/agent/react.go` is the single place every tool call passes
  through (`executeToolCall`, both from the ReAct loop and from the HTTP
  permission resume), the single place a turn ends with `end_turn`, and the
  place the user prompt becomes a message (`Run`). Tool, prompt and stop
  events attach there.
- `session.Manager` owns session creation and restore (`HandleSessionNew`,
  `loadSessionFromDisk`, `EnsureHTTPSession`); `SessionStart` attaches there.
- `internal/agent/compact.go` owns manual and automatic compaction;
  `PreCompact` / `PostCompact` attach there.
- `internal/agent/subagent.go` spawns and awaits children; `SubagentStart` /
  `SubagentStop` attach there, and tool events fire inside a child unchanged,
  because a child runs the same `Agent`.
- `internal/platform` already knows how to spawn the host shell, detach a
  process group and terminate it; the runner reuses it so Windows works
  without new OS-specific code.
- `internal/mcp` and `internal/subagents` already implement the workspace
  trust model (policy `project_trust`, receipts bound to canonical workspace
  and content digest, CLI and HTTP approval routes). Hooks are the third kind
  of project-local content that executes code, so they follow the same model
  with a sibling store.
- `internal/config/ui_schema.go` generates the Settings sections, so the
  `hooks` section reaches the SPA through the schema plus i18n labels.

### 3.1 Definitions (`internal/hooks`, new package)

**Files.** `hooks.files` lists definition files, lowest priority first
(priority only orders the catalog and the run order; all matching hooks run).
Defaults:

1. `${CODDY_HOME}/hooks.json` - user scope, the operator's own file;
2. `${CWD}/.claude/settings.json` - project scope, Claude Code compatibility
   (only its `hooks` key is read; other keys are ignored);
3. `${CWD}/.claude/settings.local.json` - project scope, same;
4. `${CWD}/.coddy/hooks.json` - project scope.

`${CODDY_HOME}`, `${CWD}` and a leading `~` expand as in `subagents.dirs`; a
relative entry resolves against the session cwd. Scope is decided on
canonical paths (`mcp.CanonicalWorkspace`): a file at or under the canonical
cwd is **project scope** and follows `hooks.project_trust` (3.2); everything
else is **user scope**. A missing file is silently skipped; a file that does
not parse is skipped with a logged warning and shows as `invalid` in the
catalog.

**Shape.** The Claude Code shape, so `.claude/settings.json` needs no
translation and a hook written for Claude Code works in Coddy:

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
      { "matcher": "edit|write", "hooks": [{ "type": "command", "command": "gofmt -l ." }] }
    ]
  }
}
```

Handler fields: `type` (only `command` in v1; `http`, `prompt`, `agent`,
`mcp_tool` are skipped with a logged warning and an `unsupported` flag in the
catalog), `command` (required), `commandWindows` (also `command_windows`;
Codex's Windows override, used instead of `command` when Coddy runs on
Windows), `args` (optional; when present the command is spawned directly with
these arguments and no shell, Claude Code's exec form), `timeout` (seconds;
default `hooks.default_timeout_seconds`), `async` (run detached, output
ignored, can never block), `failClosed` (also accepted as `fail_closed`: a
crash, a timeout or invalid JSON counts as a block instead of being ignored).
`statusMessage`, `shell`, `if`, `once` and `additionalContextLimit` are
accepted and ignored in v1 (`if` is logged as unsupported so a policy hook
does not silently widen).

**Matcher.** Claude Code's rules: empty or `*` matches everything; a value made
only of letters, digits, `_`, `-`, spaces, `,` and `|` is an exact name or a
list of exact names; anything else is an unanchored Go regular expression.
The matcher is compared with the event's subject: the tool name for tool
events, the source for `SessionStart`, the trigger for compaction events, the
subagent name for subagent events, the notification type for
`Notification`. MCP tools are matched by Coddy's `server__tool` name and, for
compatibility, also by `mcp__server__tool`. Claude Code tool names are
accepted as aliases everywhere: `Bash` -> `run_command`, `Edit` -> `edit`,
`Write` -> `write`, `Read` -> `read`, `Glob` -> `glob`, `Grep` -> `grep`,
`WebFetch` -> `webfetch`, `WebSearch` -> `websearch`, `Task` / `Agent` ->
`spawn_agent`. Argument names are not translated (`tool_input` carries
Coddy's own fields: `command`, `path`, `content`, ...); the docs list them.

### 3.2 Configuration (`config.Hooks`, top-level `hooks:`)

| Key | Default | Meaning |
|---|---|---|
| `enabled` | `true` | load and run hooks at all |
| `files` | the four above | definition files, lowest priority first |
| `project_trust` | `ask` | `ask`, `allow`, `deny` for project-scope files, same vocabulary as `mcp.project_trust` and `subagents.project_trust` |
| `default_timeout_seconds` | `60` | per handler when the definition gives none |
| `stop_loop_limit` | `5` | how many times per turn a `Stop` hook may send the agent back to work |
| `max_output_chars` | `10000` | cap on `additionalContext`, `systemMessage`, reasons and plain stdout that reach the model or the user; longer values are truncated with a marker |

**Project-scope trust.** A hooks file that arrived with the checkout names
commands Coddy would run with the operator's full permissions, on every tool
call, before any permission prompt. So a project-scope file is trusted the way
a project MCP declaration or a project subagent definition is: by an
out-of-band receipt bound to the canonical workspace and to the digest of the
file bytes.

- `deny`: project-scope files are not read at all;
- `ask` (default): project-scope files are parsed and listed with
  `needs_approval`; none of their hooks runs until `<home>/hooks-trust.json`
  holds a record for `(workspace, file, digest)`. The first turn of a session
  that finds an unapproved file logs a warning and records one UI notice
  naming the approval commands, so the operator learns the hooks exist
  and are held. There is no in-chat prompt, for the reason recorded in the
  subagents plan: every sender auto-allows under `permission_mode: bypass`.
  Rewriting the file changes the digest and the receipt stops matching;
- `allow`: project-scope files behave like the user file.

Approval surfaces: `coddy hooks list [--cwd DIR]`, `coddy hooks trust <file>
[--cwd DIR]`, `coddy hooks untrust <file>`, where `<file>` is the
workspace-relative path (`.coddy/hooks.json`); `GET /coddy/hooks?cwd=`,
`POST /coddy/hooks/trust` and `/untrust` with body `{"cwd", "file"}`. The
store is `hooks.TrustStore` (`<home>/hooks-trust.json`, version 1,
`workspaces: {canonical path: [{file, digest, approved_at}]}`), a sibling of
the MCP and subagent stores, never shared with them.

### 3.3 Events

Names follow Claude Code (PascalCase) so definitions port both ways. The
"subject" column is what the matcher is compared with.

| Event | Fires | Subject | Can block | Extra input | Output honoured |
|---|---|---|---|---|---|
| `SessionStart` | `session/new` (`startup`), `session/load` and reopen (`resume`) | source | no | `source`, `model` | `additionalContext`, plain stdout -> stored on the session, rendered as a `## Hook context` block appended to every system prompt of that session (also with a custom template, like `<environment_context>`) |
| `UserPromptSubmit` | `Agent.Run` after the built-in `/compact` and `/plugin` checks, before the user message is added | none | yes | `prompt` | `decision: block` + `reason` (prompt not added, turn ends with `refused`, reason shown to the user); `additionalContext` and plain stdout -> appended to the user message for the model (persisted as part of the turn's context, marked as hook context) |
| `PreToolUse` | `executeToolCall`, after the mode and subagent refusals, before the permission gate; also on the HTTP permission resume path | tool name | yes | `tool_name`, `tool_input` (object), `tool_use_id` | `permissionDecision` `allow` / `deny` / `ask` + `permissionDecisionReason`, `updatedInput` (whole object, chained through later hooks), `additionalContext` (appended to the tool result); exit 2 = deny with stderr as reason |
| `PostToolUse` | after a tool returned without error | tool name | no | `tool_response` (string the model sees), `duration_ms` | `decision: block` + `reason` and `additionalContext` are appended to the tool result as hook feedback |
| `PostToolUseFailure` | after a tool returned an error (not after a permission denial or a hook denial) | tool name | no | `error`, `duration_ms` | `additionalContext` appended to the error result |
| `Stop` | the ReAct loop is about to return `end_turn` | none | yes | `stop_hook_active`, `last_assistant_message` | `decision: block` + `reason`: the reason is submitted as the next user message (persisted, prefixed `[Stop hook]`, the Cursor `followup_message` model) and the loop continues; capped by `stop_loop_limit` per turn; `additionalContext` appended to that message |
| `SubagentStart` | the parent spawned a child, before the child's turn | subagent name | no | `subagent` block | `additionalContext` -> prepended to the child's task prompt |
| `SubagentStop` | the child's turn ended | subagent name | no (v1) | `subagent` block, `status`, `report` (truncated) | none in v1; `decision: block` reserved |
| `PreCompact` | before `/compact` (`manual`) or auto-compaction (`auto`) | trigger | yes | `trigger`, `custom_instructions` | `decision: block` + `reason`: manual compaction fails with the reason, automatic compaction is skipped for this check |
| `PostCompact` | after compaction | trigger | no | `trigger`, `summary` (truncated) | none |
| `Notification` | a permission prompt or a question is sent to the client | notification type (`permission_prompt`, `question`) | no | `message`, `tool_name` | none |

Every event also honours the universal fields `continue: false` +
`stopReason` (end the turn now, reason shown to the user) and `systemMessage`
(shown to the user, never to the model).

Out of scope for v1, listed for the docs: `SessionEnd` (a Coddy session
does not end; the ACP process exits and HTTP sessions persist),
`PermissionRequest` (covered by `PreToolUse` `allow`/`deny`), `PreModelSwitch`
and the other Claude Code events, Cursor's `.cursor/hooks.json` (different
event names and output fields), `http` / `prompt` / `agent` handler types.

### 3.4 Payload (stdin JSON)

Common fields, every event:

```json
{
  "session_id": "20260906-...",
  "hook_event_name": "PreToolUse",
  "cwd": "/abs/workspace",
  "transcript_path": "/home/op/.coddy/sessions/<id>/messages.json",
  "permission_mode": "ask",
  "mode": "agent",
  "model": "provider/model",
  "turn": 3,
  "subagent": { "name": "reviewer", "parent_session_id": "...", "depth": 1 }
}
```

`transcript_path` is empty when persistence is off; `subagent` is absent in an
ordinary session. `permission_mode` uses Coddy's values (`ask`,
`accept_edits`, `bypass`). Event-specific fields are in 3.3. The process
also receives `CODDY_PROJECT_DIR` (session cwd), `CODDY_SESSION_ID`,
`CODDY_HOOK_EVENT`, `CODDY_HOME` and, for scripts written for Claude Code,
`CLAUDE_PROJECT_DIR` as an alias, on top of Coddy's own environment.

### 3.5 Output contract

- exit `0`: stdout is parsed as JSON when it is one object; otherwise it is
  plain text (context on `SessionStart` and `UserPromptSubmit`, logged
  elsewhere). No output means no decision: on `PreToolUse` the ordinary
  permission flow applies, silence never approves;
- exit `2`: block on the events that can block; the reason is the JSON
  `reason` / `permissionDecisionReason` when present, else stderr;
- any other exit code: non-blocking error; logged, one UI notice
  (`<file>: hook exited N`), the event proceeds as if the hook were absent,
  unless the handler has `failClosed: true`, in which case it blocks with the
  error as the reason;
- timeout: the process group is terminated; treated like a non-zero exit
  (no decision, or a block under `failClosed`).

JSON fields: universal `continue`, `stopReason`, `systemMessage`; top-level
`decision` (`block` only) and `reason` for `UserPromptSubmit`, `PostToolUse`,
`Stop`, `PreCompact`; `hookSpecificOutput` with `hookEventName`,
`permissionDecision`, `permissionDecisionReason`, `updatedInput`,
`additionalContext` for `PreToolUse` (and `additionalContext` for the other
events that take it). Unknown fields are ignored.

**Several matching hooks** run one after another in catalog order (user file,
then project files in `hooks.files` order, then definition order inside a
file), each seeing the input as rewritten by the previous one; all of them
run even after a deny, so an audit hook sees every call. Decisions merge with
the most restrictive winning (`deny` > `ask` > `allow`; any `block` blocks);
every `additionalContext` is kept, in order. The same handler present in two
files runs once per file, as the files are distinct sources. `async` hooks
are started in order and not awaited.

### 3.6 Runner

`hooks.Runner` is built per turn from the loaded definitions and a
`hooks.Session` value (id, cwd, transcript path, mode, permission mode, model,
turn, subagent block). `Run(ctx, Event) Outcome` selects the matching
handlers, runs each with the payload on stdin, `cwd` as working directory,
the environment above, the host shell from `platform.CurrentShell()` for the
shell form or a direct `exec` for the exec form, `platform.DetachProcessGroup`
before start and `platform.TerminateProcessGroup` on timeout or context
cancellation, and captures stdout and stderr up to a fixed ceiling (256 KiB
each; the rest is dropped with a marker). It returns the merged decision,
the rewritten input, the collected context, the user-facing messages and a
list of non-blocking errors. The runner is LLM-free and has no session
dependency, so it is unit-tested with marker scripts, and the agent-level
harness proves the wiring.

Hook processes are never adopted by the background pool and never shown in
the Tasks drawer: they are part of the turn, bounded by the timeout, and
cancelled with it.

### 3.7 Wiring

- `internal/agent`: `hooks.go` (new) builds the runner at the start of
  `Run` (definitions are re-read every turn, so an edit or an approval takes
  effect on the next turn without a restart, like the trust stores),
  exposes `a.hookPreToolUse`, `a.hookPostToolUse`, `a.hookUserPrompt`,
  `a.hookStop`, `a.hookCompact`, `a.hookSubagent`; `react.go` calls them at the
  points in 3.3; `compact.go` and `subagent.go` likewise. A hook deny is
  recorded through `finishToolCall` with status `cancelled` and the result
  `blocked by hook: <reason>`, exactly like a permission denial, so every
  UI shows it on the tool call card and the model reads the reason.
- `internal/session`: `manager.go` fires `SessionStart` from
  `HandleSessionNew` and `loadSessionFromDisk`; `state.go` stores the hook
  context (`SetHookContext` / `GetHookContext`, persisted in `session.json`)
  and a per-session flag that the unapproved-file notice was shown.
- `internal/prompts` / `system_prompt.go`: append `## Hook context` after the
  environment block when the session has one.
- `cmd/coddy/hooks.go`: `coddy hooks list|trust|untrust`.
- `external/httpserver/hooks_http.go`: `GET /coddy/hooks`, `POST
  /coddy/hooks/trust`, `POST /coddy/hooks/untrust`; `openapi.go`; the UI log
  gains a `notice` level and the SPA renders it without the Retry control.
- `external/ui`: i18n labels for the generated `hooks` Settings section, the
  notice row.
- Docs: `docs/hooks.md` (new guide), `docs/config-reference.md`,
  `docs/config.schema.json`, `config.example.yaml`, `UISchemaMap`,
  `configure-coddy` skill, `docs/http-api.md`, `docs/cli.md`,
  `docs/architecture.md`, README features and docs index, `AGENTS.md` and
  `CLAUDE.md` navigation row.

## 4. Alternatives considered

- **In-process plugins (OpenCode, pi).** Rejected: Coddy is a static Go
  binary with no script runtime, and a plugin API would be a second product.
  Shell commands with a JSON contract are language-neutral and portable
  across the four agents that already use them.
- **Inline definitions in `config.yaml`.** Deferred: the Settings SPA renders
  the schema generically and nested arrays of handler objects would not be
  editable there, and the file shape is what makes Claude Code files load
  unchanged. `hooks.files` can point at any file, including one under the
  coddy home the operator edits by hand.
- **Parallel execution of matching hooks (Claude Code, Codex).** Deferred:
  sequential order keeps `updatedInput` chaining deterministic and the
  harness simple; the contract does not change if concurrency is added
  later for hooks that do not rewrite input.
- **An in-chat approval prompt for project files.** Rejected for the reason
  the subagents plan recorded: every sender in the tree auto-allows under
  `permission_mode: bypass`, so a prompt could be granted by nobody. Receipts
  are out of band, as for MCP servers and subagent definitions.
- **Per-hook trust hashes (Codex).** Not adopted: Coddy approves a file, not
  a handler, because the operator reviews the file as a whole and the digest
  already withdraws the approval on any edit. Finer granularity can be added
  without changing the store's shape (records carry the file path).
- **Cursor `.cursor/hooks.json` compatibility.** Deferred: different event
  names, matchers and output fields; a mapping layer is a follow-up once the
  Claude Code shape is in.

## 5. Files to change

New:

- `internal/hooks/` - `definition.go` (file shape, handler fields, aliases),
  `matcher.go`, `loader.go` (sources, scope, digest), `trust.go` (store,
  `Decide`), `runner.go` (spawn, timeout, stdin/stdout contract, merge),
  `payload.go` (event payloads), `catalog.go` (rows for CLI and HTTP),
  `hooks_test.go`, `bdd_*_test.go` where the spec belongs to the package;
- `internal/config/hooks.go`, `internal/config/hooks_test.go`;
- `internal/agent/hooks.go` (glue), `internal/agent/bdd_hooks_test.go`;
- `cmd/coddy/hooks.go`;
- `external/httpserver/hooks_http.go`;
- `features/hooks_tool_calls.feature`, `features/hooks_project_trust.feature`,
  `features/hooks_turn_lifecycle.feature`, `features/hooks_subagents.feature`;
- `docs/hooks.md`, `examples/acp/acp_e2e_hooks.py`.

Changed:

- `internal/config/types.go`, `config.go`, `ui_schema.go`,
  `docs/config.schema.json`, `docs/config-reference.md`, `config.example.yaml`,
  `internal/skills/bundled/configure-coddy/SKILL.md`;
- `internal/agent/react.go`, `compact.go`, `subagent.go`, `system_prompt.go`;
- `internal/session/manager.go`, `state.go`, `filesystem.go`, `ui_log.go`;
- `cmd/coddy/main.go` (dispatch and usage);
- `external/httpserver/openapi.go`, `docs/http-api.md`;
- `external/ui/src/ui/i18n/messages/{en,ru}.ts` (Settings labels), the UI log
  row renderer for the `notice` level;
- `docs/architecture.md`, `docs/cli.md`, `docs/mcp-integration.md` (the
  "third kind of project-local approval" paragraph), `README.md`, `AGENTS.md`
  and `CLAUDE.md` navigation rows, `.github/workflows/tests-on-pr.yaml`
  (Windows test job gains `internal/hooks`).

## 6. Delivery flow

One branch (`claude/hooks`), one sub-feature at a time, each through the
BDD loop of `.claude/rules/workflow.md`: feature spec in `features/`, red
harness, implementation, `make test`, docs, `make lint`, commit.

1. **hooks-core** - `config.Hooks` with schema, docs and skill catalog;
   `internal/hooks` (definition parsing, matcher with aliases, runner with
   timeouts and process groups, outcome merging); `PreToolUse`,
   `PostToolUse`, `PostToolUseFailure` in `executeToolCall`; user-scope file
   only (nothing project-local runs yet). Spec `features/hooks_tool_calls.feature`,
   harness `internal/agent/bdd_hooks_test.go` (scripted provider, marker
   scripts in a temp home); unit tests in `internal/hooks/hooks_test.go` and
   `internal/config/hooks_test.go`.
2. **hooks-trust** - project-scope sources (`.coddy/hooks.json`,
   `.claude/settings*.json`), `TrustStore`, `Decide`, catalog, the
   unapproved-file notice, `coddy hooks`, `GET/POST /coddy/hooks...`,
   OpenAPI, Settings labels, notice rendering. Spec
   `features/hooks_project_trust.feature` (`@acp` scenarios in
   `internal/agent` or `internal/session`, `@http` scenario in
   `external/httpserver`).
3. **hooks-turn** - `UserPromptSubmit`, `Stop` with the loop limit,
   `SessionStart` with the stored context, `PreCompact`, `PostCompact`. Spec
   `features/hooks_turn_lifecycle.feature`.
4. **hooks-subagents** - `SubagentStart`, `SubagentStop`, the `subagent`
   payload block inside child sessions, `Notification`. Spec
   `features/hooks_subagents.feature`.
5. **hooks-examples** - `examples/acp/acp_e2e_hooks.py` live check, README,
   final pass over `docs/hooks.md`.

## 7. Risks

- **Hooks run with the operator's permissions before any permission prompt.**
  That is the point of the feature and the reason project files are
  receipt-gated; the docs say so in the first paragraph, as Claude Code's do.
- **Latency.** Every tool call pays for its matching hooks. Sequential
  execution keeps `updatedInput` deterministic at the cost of wall time;
  documented, and `async` exists for fire-and-forget hooks.
- **Fail-open by default.** A mistyped path silently disables a policy hook
  (Claude Code has the same trap). The catalog shows every handler and the
  notice reports non-zero exits; `failClosed` is the opt-in for security
  hooks, as in Cursor.
- **Stop loops.** A `Stop` hook that always blocks would burn turns;
  `stop_loop_limit` and `stop_hook_active` bound it.
- **Windows.** Shell form goes through the detected shell (pwsh, powershell,
  cmd); exec form needs a real executable. `make check-windows` and
  `make lint-windows` run because `internal/hooks` spawns processes; the
  package is added to the Windows CI test job.
- **Claude Code compatibility is partial.** Same file shape, same event
  names, same output fields for the covered events; `tool_input` field names
  are Coddy's, and unsupported handler types or events are skipped with a
  catalog flag rather than failing the load.

## 8. Implementation notes (deviations from the text above)

Recorded while implementing; the code and `docs/hooks.md` describe the
shipped behaviour.

- `UserPromptSubmit` context is not appended to the user message (which the
  transcript would show as if the user had typed it); it goes into the
  `## Hook context` block of the turn's system prompt, next to the persisted
  `SessionStart` context. The turn-level part is not persisted.
- A hook that stalls past its timeout is a non-blocking error, as planned;
  stdout that starts with `{` but does not parse is an error too (Codex's
  reading), not plain text (Claude Code's), so a hook that meant to print JSON
  is never silently ignored.
- Trust is per file, and the CLI and HTTP routes name a file by the
  workspace-relative path (`.coddy/hooks.json`) rather than by a source id.
- The held-file notice is a `notice`-level UI log row, a new level next to
  `error`; the SPA renders it without the retry control.
- `SessionStart` runs synchronously inside `session/new` and `session/load`,
  so session creation waits for the hooks (bounded by their timeouts); the
  manager owns that call, not the agent.
- The `Stop` follow-up is persisted as a user message prefixed `[Stop hook] `
  (Cursor's `followup_message` model) rather than as an ephemeral nudge, so a
  reloaded transcript still explains the continuation.
- `PreCompact` vetoes report `compaction blocked by hook: <reason>` from
  `/compact` and are logged at info level for automatic compaction.
- `SubagentStart` can refuse a spawn (Cursor's `subagentStart` deny), not
  only add context; the refusal is the `spawn_agent` tool result.
  `SubagentStop` stays observational: a block would need a second child turn,
  which the runtime does not offer. Both fire in the parent, inside the
  spawning turn, so a background child that finishes later still reports to
  the parent's runner (guarded by a mutex).
- `Notification` covers `permission_prompt` only; the `question` tool's
  prompt is a follow-up.

## 9. Cross-review (2026-09-06)

Three reviewers read the finished branch: Codex (`gpt-5.6-sol`, high
reasoning, through the codex-review plugin, reading the tree itself), Cursor
Agent (`auto`, from a brief with the production diff inline) and Coddy on
`neuraldeep/qwen3.8-27b` (same brief, core diff only). Every finding was
checked against the code before it was acted on.

Codex, all five confirmed and fixed:

1. A permission persisted over HTTP resumed with the model's original
   arguments, skipping the PreToolUse rewrite. PreToolUse now runs on the
   resume path too (allow and ask are moot there); regression test
   `TestResumeAfterPermissionAppliesHookRewrite`.
2. A resume kept a stale `SessionStart` context when the hooks were removed,
   disabled or withdrawn. Every run of the manager's hook now replaces the
   stored text, clearing it when nothing runs; test
   `TestSessionStartHooksReplaceStaleContext`.
3. `systemMessage` and non-blocking errors only reached the log. Both are
   notice rows now (errors once per session and message); scenario "A hook's
   message for the user reaches the session's UI log".
4. The receipts file was written in place under a per-instance mutex while
   HTTP creates an instance per request. Writes go through a temp file and
   rename, and every instance of one path shares a process-wide lock; test
   `TestTrustStoreSerialisesConcurrentInstances`.
5. A Stop follow-up on the last ReAct iteration was persisted with no
   iteration left to read it. The loop ends the turn instead; test
   `TestStopHookDoesNotContinuePastTheTurnCap`.

Cursor Agent, nine findings: seven confirmed and fixed (`failClosed` on an
async handler is dropped with a warning; `max_output_chars` counts characters,
not bytes; the cached runner's turn index is updated under the lock; the
runner is built outside the lock and installed under it; without a session
cwd the entries that need one are skipped instead of resolving against the
root as user scope; a hook interrupted by the turn's cancellation is a failure
that honours `failClosed`, and the interrupted call does not run;
`coddy hooks untrust` resolves the file the way `trust` does), one was a
comment fix, one was declined: the four `open` calls per turn for absent
files are cheaper than a cache and the re-read is what makes approvals take
effect without a restart.

Codex, second iteration, three more, all confirmed and fixed:

1. The approval was still not bound to the arguments the prompt showed: the
   resume reloaded the model's original arguments and re-ran the hooks on
   them, so a hook removed or edited while the permission was pending would
   have let the original arguments run. The resume now takes the arguments
   the bundle persisted before the prompt (`tool_calls/<id>/args.json`,
   written after the PreToolUse rewrite) for execution and for grants, and a
   hook that changes them again on the resume cancels the call; tests
   `TestResumeAfterPermissionRunsTheApprovedArguments` (hook file removed
   before the resume) and
   `TestResumeAfterPermissionRefusesArgumentsChangedAfterTheApproval`.
2. The receipts lock was process-local and the temporary file fixed, while
   the CLI and the HTTP server are separate processes. A write is now a
   transaction under the in-process mutex plus a file lock next to the
   receipts (flock on Unix, LockFileEx on Windows) with a unique temporary;
   test `TestTrustStoreSerialisesConcurrentProcesses` approves eight files
   from eight re-executed test binaries.
3. The async-and-failClosed warning hid the `if` warning on the same
   handler; both are reported.

Codex, third iteration, two more, both confirmed and fixed:

1. The resume compared the shown and the re-hooked arguments as bytes, while
   the bundle stores them pretty-printed and a hook answers them compact, so
   the very hook that produced the approved arguments cancelled the resume.
   The comparison is canonical now (`sameToolArgs` decodes both sides); test
   `TestResumeAfterPermissionRunsWhenTheSameHookAnswersAgain`.
2. Persistence failed open: a rewrite that could not be written was ignored
   and a resume fell back to the history on every read error. The write now
   happens before the prompt and a failure cancels the call
   (`TestRewrittenArgumentsThatCannotBePersistedCancelBeforeThePrompt`); the
   resume fails with the pending gate kept when the file cannot be read
   (`TestResumeAfterPermissionFailsClosedWhenTheApprovedArgumentsCannotBeRead`).

Codex, fourth iteration, three more, all confirmed and fixed:

1. A missing arguments file was still read as "legacy" and ran the
   history's arguments, the very fail-open the previous round meant to
   close. The bundle always carries the file (written when the call starts
   and again after a rewrite), so a resume without it fails closed now, and
   the initial write failing is remembered like the rewrite's: no prompt is
   issued for a call whose arguments are not on disk. Tests
   `TestResumeAfterPermissionFailsClosedWithoutPersistedArguments`,
   `TestRewrittenArgumentsThatCannotBePersistedCancelBeforeThePrompt`.
2. The canonical comparison decoded numbers as float64, so integers past
   2^53 compared equal, and `WriteToolCallArgs` rounded them the same way on
   its round trip through `interface{}`. Both keep the literals now
   (`UseNumber` on the decode, `json.Indent` on the raw bytes), and so do the
   payload a hook reads and the answer it returns; tests
   `TestSameToolArgsKeepsLargeIntegersApart`,
   `TestWriteToolCallArgsKeepsLargeIntegers`.
3. A refusal read the arguments first, so an unreadable file left a refused
   call pending. The refusal is processed before anything is read; test
   `TestResumeAfterPermissionRejectsWithoutReadingTheArguments`.

Codex approved the branch on the fifth iteration (2026-09-06), after every
finding of the four rounds before it had been reproduced by a test that fails
on the previous commit and fixed.

Coddy (`neuraldeep/qwen3.8-27b`) answered only once the model entry carried
`stream: false`: the endpoint drops streamed answers to long prompts, the
first-token guard (90 s) cut the 122 KB brief, and a single shell argument
cannot exceed 128 KB anyway, so the brief it reviewed was the 31 KB core
(loader, trust, runner). Seven findings: two confirmed and fixed (a hook
whose grandchild kept a pipe open past the drain delay reported exit 0
instead of its real code, so a block could read as an allow; the final wait
after a kill was unbounded), two accepted as hardening (detached hooks are
capped at eight in flight; a JSON answer cut by the 256 KiB capture limit is
reported as cut, not as invalid), three declined or reworded: the empty-cwd
scope is already fail-closed after the loader change (entries that need a
cwd are skipped), `truncate` caps the content and the comment says so now,
and `HasHandlers` skipping subject matching only costs one payload
encoding when a matcher does not match.

Follow-up outside this branch: the MCP and subagent receipt stores share the
per-instance mutex pattern that finding 4 fixed here.
