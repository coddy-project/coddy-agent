# Subagents

A long investigation or a parallel fan-out (review three modules, run the test matrix per package, research four libraries) fills the one conversation the operator is watching. A **subagent** is a child agent run with its own context window, its own session bundle, and a role prompt the operator wrote in a markdown file. The parent hands it a self-contained task, keeps working, and gets back only the child's final report.

A subagent run is a **task in the background task pool** (`docs/background-tasks.md`). Nothing about scheduling, timeouts, the Tasks panel, the REST surface, drain on shutdown, or persistence under `<session>/background/<task_id>/` is specific to subagents; what is new is a second kind of task (`kind: agent`), a child session behind it, and a tool that starts one.

## When the model delegates

A session that may spawn gets a `## Subagents` section in its system prompt: what `spawn_agent` does, when delegation pays off (work that would flood the context, or independent pieces that can run in parallel with `background: true`), how to collect detached runs, and a catalog of the definitions it may use. The guidance is explicit that a one-step task is done directly, and that the child starts with an **empty context** - the prompt must carry everything, because the child sees none of the parent's conversation. Only the child's final message comes back, and the user never sees it, so the parent is told to restate what matters in its own reply.

The section is rendered in `agent` and `plan` mode (a planner fans out investigation the way Claude Code's Explore does; the child of a plan-mode parent is forced into plan mode) and never in `ask` mode, which is read-only and delegates nothing. It is omitted, and the tool hidden, for a session that cannot spawn: the feature is disabled, the surface has no session manager (a scheduled run), the turn runs in `ask` mode, or the session already sits at `subagents.max_depth`.

## Definition files

### Directories and precedence

Definitions are searched in the directories of `subagents.dirs`, lowest priority first; a later directory replaces an earlier one **by name**. The default list:

1. `${CODDY_HOME}/agents` - user scope, the operator's own files;
2. `${CWD}/.claude/agents` - project scope, read for compatibility (same frontmatter shape, Claude Code's files load unchanged);
3. `${CWD}/.coddy/agents` - project scope.

`${CODDY_HOME}`, `${CWD}` and a leading `~` expand; a relative entry is resolved against the session cwd. Two built-ins sit below every directory, so a user file with the same name replaces a built-in.

Scope is decided on **canonical paths**: the expanded directory and the session cwd both go through the same normalisation the MCP trust store uses (absolute, symlinks resolved, cleaned). A directory at or under the canonical cwd is **project scope** and follows `subagents.project_trust` (below); everything else is **user scope**. A workspace reached through a symlink therefore still owns its `.coddy/agents`.

Inside one directory every `*.md` file is one definition, recursively; dot-prefixed files and directories are skipped. `<dir>/<name>/AGENT.md` is also accepted and takes its default name from the **directory**, not from the `AGENT` stem. Files are visited in lexical order and the first file claiming a name wins; a duplicate is skipped with a logged warning, as is a file without frontmatter, without a `description`, with an invalid name, mode or permission mode, or over the size bound.

### Frontmatter

Only `description` is required. `name` defaults to the file stem (or the directory for `AGENT.md`), lowercased. Unknown keys are ignored so files written for other agents load.

| Field | Aliases | Meaning |
|---|---|---|
| `name` | | Identifier matching `[a-z0-9][a-z0-9_-]*`; what the model passes to `spawn_agent`. |
| `description` | | One line shown to the parent model so it can pick the agent; cut to 200 characters in the catalog. |
| `model` | | A `models[].model` id for the child. An unknown id falls back to the parent's model with a warning in the agent log and a note in the task's output log. |
| `mode` | | `agent` or `plan`. Empty inherits the parent's mode; a read-only parent (`plan`, and `ask` should a spawn ever originate there) always forces its own mode. The parent's mode is the one its turn started in, not the live session mode, so a mode switch landing mid-turn cannot widen a child. |
| `tools` | | Allowlist: a YAML list or a comma-separated string (`tools: read, grep`). Entries are exact tool names, a bare `*`, or a `prefix*` pattern, so `context7__*` admits every tool of one MCP server. Empty means everything the parent has. |
| `disallowed_tools` | `disallowedTools` | Denylist with the same syntax; wins over `tools`. |
| `permission_mode` | `permissionMode` | `ask`, `accept_edits` or `bypass`. Claude Code spellings are accepted (`default` and `prompt` for `ask`, `acceptEdits` / `accept-edits`, `bypassPermissions` / `bypass-permissions` / `dontask`). It can only **narrow** the parent's effective mode. |
| `max_turns` | `maxTurns` | Cap on the child's ReAct rounds. `0` uses `subagents.max_turns`, then `agent.max_turns`. |
| `timeout_seconds` | | The definition's own hard limit for one run (see timeout precedence below). |
| `background` | | `true` forces a detached run even when the model asked for a foreground one. |
| `hidden` | | Keeps the definition out of the model-facing catalog and out of "unknown agent" lists. It is still spawnable by name, and the CLI listing and the HTTP catalog show it with a `hidden` flag. |

### The body is the role

Everything after the frontmatter is the child's role block. The child's system prompt starts with a fixed preamble (`## Your role as a subagent`: you are the subagent *name*, spawned by a parent to complete one self-contained task, you see nothing of the parent's conversation, you cannot ask the user questions, only your final message reaches the parent, so finish with a concise report) followed by the body verbatim. The rest of the prompt is the ordinary agent or plan template, so skills, rules and the environment block are present as in any session.

A minimal definition:

```markdown
---
name: reviewer
description: Reviews a diff or a module for defects and reports findings with file paths and lines.
tools: read, glob, grep, print_tree, run_command
permission_mode: ask
max_turns: 20
---
You review code. Read before you judge, cite `path:line` for every finding,
separate defects from style remarks, and end with a short verdict.
```

### Bounds

The loader and the tool keep a definition tree from flooding the prompt: at most **200** definitions per directory (the rest are skipped with a warning), a file over **256 KiB** is skipped, a role body over **64 KiB** is truncated with a marker, `description` is cut to **200** characters, and a `spawn_agent` `prompt` over **32 KiB** is refused. The task label is `agent <name>: <description>` where `description` is the call's own argument, falling back to the first line of the prompt; the whole label is capped at **60** characters, the pool's rule for command labels.

Every loaded definition is an **immutable value** carrying its scope, its path and a SHA-256 **digest** of the file bytes. The file is read once; trust, the tool set and the role are all decided on that same value, never on a re-read.

## Built-ins

Two definitions ship embedded so delegation works before the operator writes a file. Both are listed with scope `builtin` and path `(embedded)`.

- **`general`** - a general-purpose worker with the **parent's tool set** (no `tools` restriction of its own), for multi-step tasks, research, or independent units of work that can run in parallel. Its role tells it to read before changing anything, keep edits minimal, verify when the task calls for it, never retry a permission the operator did not grant, and report what it did and what the parent must still decide.
- **`explore`** - a read-only explorer for locating files, symbols and usages and gathering evidence before changes are proposed. Its tool list is exactly `read`, `keep_result`, `glob`, `grep`, `print_tree`, `websearch`, `webfetch`, `load_skill`, `background_list`, `background_output`, `background_wait`: no `run_command`, no writes, no MCP tools. Plan mode alone would not be read-only (it still offers the shell), which is why the list is spelled out. Because nothing in that set is an MCP tool, an `explore` child never dials an MCP server.

## Scopes and project trust

A definition that arrived with the checkout directs a child's tools, and the permission gate does not cover everything a child can do: `websearch` and `webfetch` ask nobody, MCP calls bypass the prompt, and the model may delegate on its own because the `description` invites it. So a **project-scope** definition is trusted the way a project-local MCP declaration is: by an out-of-band receipt bound to the workspace and to the file content, not by an in-chat prompt (every sender in the tree auto-allows prompts under `permission_mode: bypass`, so a prompt could be granted by nobody).

`subagents.project_trust` takes the same vocabulary as `mcp.project_trust`:

- **`deny`** - project-scope directories are not read at all. Agent discovery, `coddy agents list` and the HTTP catalog omit them; the test proves the files stay unread.
- **`ask`** (default) - project-scope definitions load and are listed as `needs_approval` until a receipt exists for `(canonical workspace, name, digest)`. Spawning one without a receipt is **refused** in the runtime, on the resolved definition: the tool result says the definition comes from a project file that is not approved for this workspace and names the ways to approve it, so the model can tell the operator and retry afterwards. The catalog block in the prompt lists such an entry by **name only**, with a fixed notice that it is awaiting approval and how to approve it; the file's description is withheld until the receipt exists, so an untrusted checkout cannot put a single line of its own into the parent's system prompt. Descriptions of approved and user-scope definitions are flattened to one line before they are rendered.
- **`allow`** - project-scope definitions behave like user-scope ones.

Built-ins and user-scope files are always `trusted`. Whatever the scope, `permission_mode`, `tools` and `disallowed_tools` only ever narrow the parent's capabilities, and a definition carries no hooks, commands or MCP declarations of its own, so nothing in it starts a process at load time.

Receipts live in **`~/.coddy/subagents-trust.json`** (`<home>/subagents-trust.json`), a sibling of `mcp-trust.json` rather than the same file, so an MCP approval can never read as an agent approval or the reverse:

```json
{
  "version": 1,
  "workspaces": {
    "/home/me/project": [
      { "name": "reviewer", "digest": "3f2a…", "path": "/home/me/project/.coddy/agents/reviewer.md", "approved_at": "2026-09-03T10:15:00Z" }
    ]
  }
}
```

Rewriting an approved file changes its digest and the receipt stops matching, so the next spawn is refused again until it is re-approved. Approving a name replaces any earlier receipt for that name in that workspace. The store re-reads the file on every decision, so an approval granted from a terminal reaches a running agent on its next spawn without a restart.

Approval surfaces:

- **CLI**: `coddy agents list [--cwd DIR]` prints the workspace, the effective policy and the catalog with scope, trust state and flags, followed by a hint when project definitions await approval; `coddy agents trust <name> [--cwd DIR]` prints the effective declaration first (file, model, mode, permission mode, tool lists, digest, receipt path) and then records a receipt for the file as it is on disk right now; `coddy agents untrust <name> [--cwd DIR]` withdraws it. A built-in or user-scope name needs no approval and the command says so. `--cwd` defaults to the process working directory, resolved like `coddy mcp`.
- **HTTP** (`coddy http`): `GET /coddy/subagents?cwd=<dir>` returns the catalog; `POST /coddy/subagents/{name}/trust` and `POST /coddy/subagents/{name}/untrust` with body `{"cwd": "<dir>"}` write and remove receipts. `cwd` must be absolute and defaults to the server's own working directory. Catalog rows carry `scope`, `trust` (`trusted` / `needs_approval`), the booleans `trusted` and `needs_approval`, `digest`, `path`, `builtin` and `hidden`. Details in `docs/http-api.md`.
- **Policy**: a checkout you already trust can run its definitions without receipts by setting `subagents.project_trust: allow`, in `config.yaml`, under **Settings → Subagents** in the web UI (the `subagents` config section: policy and pool bounds), or through the bundled `configure-coddy` skill, which documents the key so the agent can stage `set subagents.project_trust=allow` and commit it through the ordinary permission-gated config commit.

A Settings surface that lists definitions and records approvals is a follow-up; the three surfaces above cover approval today.

## The `spawn_agent` tool

| Argument | Meaning |
|---|---|
| `agent` | Definition name (required). |
| `prompt` | The task, self-contained: goal, relevant paths, constraints, and what the report must contain (required, at most 32 KiB). |
| `description` | Three to five words naming the task; becomes the task label and the child session title. |
| `background` | Return the task id at once instead of waiting for the report. Default `false`. A definition with `background: true` forces it on. |
| `expected_seconds` | The model's own estimate; drives the status ticker and, when no timeout is given, the hard timeout - the same advisory semantics as a backgrounded `run_command`. |
| `timeout_seconds` | Hard limit for the run. |
| `notify_on_finish` | For a background run: wake the parent with the outcome when the child finishes (see `docs/background-tasks.md`). Forced **off** for a foreground spawn, whose report already comes back in the tool result, and for any spawn made by a child. |

The tool is registered when `subagents.enabled` is on and offered in `agent` and `plan` mode, never in `ask` mode. It needs **no permission prompt of its own**: launching a child changes nothing by itself, every tool call the child makes is gated on its own, and project trust is decided inside the runtime hook before anything starts.

A **foreground** spawn (the default) blocks the tool call until the child's turn ends and returns the report in an envelope:

```
<subagent task="bg_3" session="sub_9f1c…" agent="explore" status="succeeded" turns="4">
<![CDATA[
…the child's last assistant message…
]]>
</subagent>
The user did not see this report: restate what matters in your own reply. The full transcript is session sub_9f1c… (Tasks panel → Open transcript).
```

`status` is the pool's verdict for the task (`succeeded`, `failed`, `timed_out`, `stopped`); when it is anything but `succeeded` a line says so and tells the model to treat the report accordingly, and a run that ended with an error names it. `turns` is the number of assistant rounds in the child's transcript. The report is wrapped in CDATA so nothing the child wrote can break the envelope.

A **background** spawn returns at once:

```
Started subagent explore as background task bg_3 (child session sub_9f1c…).
Hard timeout 30m.
Keep working; follow it with background_list or background_output, and collect the report with background_wait.
```

With `notify_on_finish: true` the last line instead tells the model it will be woken with the outcome. From here the run is an ordinary task: `background_list` shows it, `background_output` streams the child's progress log, `background_wait` blocks for it and returns the log ending in the report block, and `background_stop` cancels the child.

Refusals are returned as tool errors that name the knob that applies: an unknown name (with the list of visible definitions), a project file without a receipt (with the approval commands), `subagents.max_depth` reached, a prompt over 32 KiB, `subagents.max_concurrent` runs already in flight, the pool's own per-session limit (`tools.background.max_concurrent`), and the pool draining for shutdown. With `subagents.enabled: false` the tool is not registered at all. A surface without a session manager (a scheduled run) is never advertised the tool, and a call anyway answers that subagents are not available in this session.

## How capabilities narrow

Everything a child may do is derived from the parent at spawn time and can only shrink.

- **Mode.** A read-only parent (`plan`, `ask`) always produces a child in its own mode. Otherwise the definition's `mode` applies, or the parent's mode when it is empty. The parent mode is the mode the spawning turn was admitted in: a `session/set_mode` that lands while the turn runs changes neither the child's mode nor the parent tool set the child is intersected with.
- **Permission mode never widens.** The child runs with the stricter of the parent's effective mode and the definition's request, on the scale `ask` < `accept_edits` < `bypass`. A definition asking for `bypass` under an `ask` parent gets `ask`; an empty request inherits.
- **Tool set intersection.** The child's effective set is computed once at spawn: the parent's own callable set (its mode set, and its own effective set when the parent is itself a child), intersected with the child mode's set, intersected with the definition's allowlist, minus the definition's denylist, minus the mandatory exclusions. MCP tool names are part of the set and matched by exact name or `prefix*`, so a child sees an MCP server only when the parent had it and the definition admits it.
- **Mandatory exclusions.** `question` (a child cannot ask the user; `RequestQuestion` always refuses), `config_set`, `config_changes`, `config_commit`, `config_revert`, `config_rollback` (a child cannot rewrite the agent's own configuration), `plan_exit` (a child cannot leave plan mode on the operator's behalf), and `spawn_agent` for a child at the depth limit.
- **Enforced twice.** The definitions advertised to the child's model are filtered to the set, and every call is checked again before the permission gate runs, MCP calls included: a hallucinated or replayed `run_command` is answered with `tool run_command is not available to this subagent` and never executed.
- **Depth.** `subagents.max_depth` bounds nesting. The default `1` lets a session spawn children that cannot spawn further; the tool is withheld from a child at the limit and a spawn past it is refused. An explicit `0` forbids spawning everywhere.
- **Model.** The child inherits the model the parent session is running with unless the definition's `model` names a configured `models[].model` id; an unknown id keeps the parent's model, and the fallback is noted both in the agent log and in the task's output log.
- **Turns.** The definition's `max_turns`, else `subagents.max_turns`, else `agent.max_turns`.
- **MCP.** Servers are dialed for the child only when its effective set can contain MCP names. Configured servers are re-resolved for the child's cwd through the workspace trust gate exactly as for a new session, and the parent's ACP client-supplied servers are redialed from the declarations the parent retained. The child owns and closes its own clients; nothing is borrowed from the parent, so a parent reload or forget cannot cut a child mid-run.

## Permission prompts of children

A child in `ask` or `accept_edits` mode still hits the permission gate, and the child has no client of its own. Each spawn therefore creates a **relay** that owns the parent's sender and the parent turn that made the spawn (the tool call context), plus the child's own cancel:

- while the spawning turn is alive, a request is forwarded to the parent's client with the **parent's session id** and the title `[subagent <name>] Run: <tool>`, so the ACP editor, the console modal or the web UI shows it in the parent chat. The wait also watches the turn and the child, so a parent turn ending mid-prompt or a stopped child unblocks with a denial;
- once the spawning turn has returned, every request is answered `cancelled` immediately, without touching the transport, and the child's tool sees `permission denied by user`. A **detached** child that runs past its parent's turn therefore needs a definition that narrows to read-only tools, or a mode the operator already accepted, if it is to do anything gated;
- at most **one prompt is in flight per parent session**: an arbiter serialises the requests of every child of that parent, which is also what the HTTP pending-permission record (one per session) can represent. Two children asking at once are prompted one after the other;
- a child spawned by a later turn carries that turn's context, so an earlier detached child's finished turn does not deny it.

The operator's global setting sits above all of this. A config-level `tools.permission_mode: bypass` switches prompting off on every surface, children included, because the parent-facing senders auto-allow under it. A definition's `permission_mode` can narrow the session-level mode (`ask` or `accept_edits`, chosen per session) but cannot re-enable prompts the operator disabled globally. A bypass on the parent does not leak to a narrowed child on any surface: the relay stamps the child's own effective mode on every forwarded request, and every sender that can auto-allow (the ACP server under a global bypass, the HTTP bridge, the console, print mode, the Telegram gateway) decides from that stamp rather than from the parent's session or the global setting. A stamped child below `bypass` is prompted where a human can answer and denied where nobody can (print mode, the gateway, a non-streaming HTTP turn).

## Concurrency and timeouts

Two caps apply to a spawn, and each refusal names its own key:

- **`subagents.max_concurrent`** (default 4) is **process-wide**: the number of child LLM loops that may run at once, whatever session started them. It is a resizable semaphore that refuses rather than queues, matching the pool's "refused, not queued" contract; the message tells the model to wait with `background_wait` or `background_list` and try again. Lowering the cap under load never stops a running child; new spawns are refused until the count drops under the new limit. The value is re-read on every spawn, so a config commit applies without a restart.
- **`tools.background.max_concurrent`** is the pool's **per-session** task limit, and a child's task is registered under the **parent's** session, so subagent runs count against the parent's tasks like its background commands do. A spawn refused because this limit is full names `tools.background.max_concurrent`, so the model can tell the two caps apart.

The hard limit for one run is resolved in this order: the call's `timeout_seconds`, then the definition's `timeout_seconds`, then `expected_seconds × 3` floored at 60 s, then `subagents.default_timeout_seconds` (1800). The result is capped by `tools.background.max_timeout_seconds`, exactly like a background command. Hitting it cancels the child and records the task as `timed_out`, which is a failure, not a success.

## Child sessions

Every run creates a child session with an id of the form **`sub_<24 hex>`**, generated before the pool is involved so the very first task snapshot already carries it. The child is a real session bundle under the sessions root, built the same way `session/new` builds one (skills, rules catalog, persistence), with its title pinned to the task label and its `session.json` carrying `subagentRun: true`, `parentSessionId`, `subagentName`, `subagentTaskId` and `subagentDepth`. While the child runs it is registered as a live session, so a transcript read is served from the one live state; after it finishes the bundle serves the transcript like any closed session.

- **Hidden from History.** Child sessions stay out of every default listing: the web UI History, `GET /coddy/sessions`, `coddy sessions list`, `coddy -c`, and ACP `session/list`. `GET /coddy/sessions?include_subagents=true` includes them.
- **Read-only transcripts.** Resuming or messaging a child is out of scope. Any prompt against a `sub_` session that is not the child's own task turn (a composer `POST /v1/responses`, an ACP `session/prompt`, a console prompt, a run-plan request, the background waker) is refused with `subagent sessions are read-only transcripts: <child> belongs to <parent>`; over HTTP that is a **409** naming the parent. The SPA renders the child's transcript with the composer replaced by a notice linking back to the parent chat.
- **From the Tasks panel.** An agent task shows an `agent` badge with the agent name, the detail pane shows the role name instead of a shell command, and **Open transcript** routes to `#/s/<child id>`. The live progress log in the output pane is the report while it runs; the transcript shows the child's tool calls and its final answer.
- **Deletion cascades.** `DELETE /coddy/sessions/{id}` removes the whole tree through one path: the requested session plus every descendant found by `parentSessionId`, root to leaf. Every node's representing task is stopped and awaited first (a child's task lives under its parent, so this reaches a running child and a running descendant alike), then any remaining tasks of every node, and only then are the bundles removed deepest first, the requested session last. Before any of that, an active turn of any node is cancelled and awaited and a turn arriving during the deletion is refused; a turn that ignores its cancellation past the settle timeout (15 s) aborts the deletion with nothing removed (HTTP `409`). The tree is rescanned after it is marked until no new descendant appears, so a child created while the deletion starts is removed with it rather than orphaned. Nothing writes into a removed bundle afterwards.

## Lifecycle rules

- **One turn.** A child runs exactly one prompt turn through the session manager's normal path, so it takes the turn lock, reports activity edges, honours cross-process cancel and persists like any session.
- **Retirement settles the child's own work first.** A `general` child inherits backgrounded `run_command` and, below the depth limit, `spawn_agent`, so it can leave a command or a detached grandchild running when its turn returns. A retired child is a read-only transcript with no turn left to collect them, so before the live entry is dropped every task the child owns is stopped and awaited (a grandchild's handle cancels that grandchild, whose own retirement recurses the same way). Finished task records stay in the child's bundle. The time this settlement takes, bounded by the pool's 3 s stop grace per task, counts toward the child's own hard timeout. Then the child's MCP clients are closed and its live entry dropped; the bundle stays on disk.
- **Nobody is woken on a child's behalf.** A spawn made by a child never carries `notify_on_finish`, and a woken turn aimed at a child session is refused by the read-only guard, so a task finishing around retirement starts no turn.
- **Parent turn cancelled (Stop).** A foreground child runs on a context derived from the parent's tool call, so it is cancelled with the parent and the tool result reports the final status. A detached child runs on a context detached from the tool call, because that context ends the moment the tool returns; it keeps running by design and is stopped from the Tasks panel or with `background_stop`.
- **Shutdown and drain.** Server drain stops agent tasks like commands: each handle cancels its child, the child is retired and its limiter slot released. Nothing new starts once the pool is draining.
- **Every exit path releases.** Pool refusal, a failure to create the child session (recorded as a `failed` task), timeout, stop, parent cancellation, a panic in the run (recovered and reported as `failed` with the panic text) and normal completion all go through one idempotent finish, so a slot is never leaked and never freed twice.
- **Parent forgotten or reloaded.** The child has its own state and its own MCP clients, so nothing it depends on is closed underneath it.

## Task rows and the output log

An agent task row (`background_list`, `GET .../background-tasks`, the persisted `meta.json`) carries `kind: "agent"`, the label `agent <name>: <description>`, the parent's `session_id`, the spawning `tool_call_id` (so the transcript row keeps its live chip), and `agent: {"name": "<name>", "session_id": "sub_…"}`. It reports no pid: the handle behind it is not an OS process, so the survivor probe never mistakes it for one.

The child never writes to the parent's stream. Its progress goes to the task's output sink as compact log lines, which is what `background_output` and the panel show while it runs:

```
subagent explore (task bg_3, session sub_9f1c…) starting
→ grep
✓ grep
[assistant] The runtime is wired in four places…
=== subagent report ===
agent: explore | task: bg_3 | session: sub_9f1c… | outcome: end_turn | turns: 4 | duration: 41s
--- report ---
…the child's last assistant message…
```

`✗ <tool> (failed)` marks a tool call that failed or was refused, including one outside the child's tool set. `outcome` is the child's stop reason (`end_turn`, `cancelled`, `failed`, or another ACP stop reason), `turns` is the number of assistant rounds in the child's transcript (the same count the foreground envelope carries), and an `error:` line precedes the report when the run ended with one.

## Configuration

All knobs are ordinary `config.yaml` keys under `subagents:`; the field table is in `docs/config-reference.md` (section `subagents`), the web UI edits them under **Settings → Subagents**, and the bundled `configure-coddy` skill can change them through the staged config tools.

```yaml
subagents:
  enabled: true
  dirs:
    - "${CODDY_HOME}/agents"
    - "${CWD}/.claude/agents"
    - "${CWD}/.coddy/agents"
  project_trust: ask          # ask | allow | deny
  max_concurrent: 4           # child runs in flight across the whole process
  max_depth: 1                # 1: children cannot spawn; 0: nobody spawns
  default_timeout_seconds: 1800
  max_turns: 0                # 0 follows agent.max_turns
```

`tools.background` still bounds the pool a run lives in: its per-session `max_concurrent`, the `max_timeout_seconds` cap, and `output_buffer_bytes` for the progress log.

## Tests

- Happy paths are Gherkin specs run by godog: `features/subagents.feature` (definitions offered to the model, foreground and background spawns, isolation of the parent's stream, depth, permission narrowing, project trust refusal and receipts, `deny`, a client-supplied MCP server inherited by a child, the permission relay and arbiter, read-only child sessions, settlement of a child's own tasks, the concurrency cap; harness `internal/agent/bdd_subagents_test.go`, scripted providers over a real `session.Manager`), `features/subagents_http.feature` (the agent task row, hidden and included child sessions, live and finished child transcripts, the catalog and trust routes, tree deletion; `external/httpserver/bdd_subagents_test.go`, `-tags http`), `features/subagents_catalog.feature` (the CLI listing across scopes and trust states; `internal/subagents/bdd_catalog_test.go`), and one scenario in `features/cli_tui.feature` for the `Running subagent <name>` status line (`external/cli/bdd_cli_tui_test.go`, `-tags cli`).
- Edge cases are ordinary unit tests next to the code: frontmatter aliases and comma-separated tools, `AGENT.md` naming, duplicates, oversized and invalid files, precedence and canonical scope, digests and the receipt store, permission narrowing, tool set intersection and MCP patterns, the limiter (`internal/subagents`); `Launch` ordering and `Snapshot.Agent` persistence (`internal/bgtask`); child session meta, the read-only guard, list filtering and tree deletion (`internal/session`); the spawn hook's exit paths, timeouts, the sink and the CDATA envelope (`internal/agent`); config defaults and validation (`internal/config`).
- End-to-end against a real model: `examples/acp/acp_e2e_subagents.py`, `examples/httpserver/http_e2e_subagents.py` and `examples/cli/cli_e2e_subagents.py`, wired into the three runners. Each copies the fixture `examples/agents_fixture/.coddy/agents/marker-reporter.md` into the workspace, approves it up front (`coddy agents trust` for the CLI and ACP runs, `POST /coddy/subagents/{name}/trust` for HTTP) so the run stays unattended, writes a marker file, and asks the model to delegate reading it. They assert the `agent` task under the parent's bundle, the `sub_` child bundle with its parent link, the marker in the child's transcript, in the report block and in the parent's final answer; the HTTP script also drives the task row, the read-only child transcript, the sessions list with and without `include_subagents`, and the catalog.

## Out of scope

Follow-ups, deliberately not part of this change: resuming or messaging a running child, worktree isolation for a child, definition-level hooks and MCP servers, `SubagentStart` / `SubagentStop` hooks, a Settings tab for editing and approving definitions (the CLI and the HTTP routes cover approval, the `configure-coddy` skill covers widening the policy), queueing instead of refusing when the pool is full, and a live SSE relay for a child (the transcript is read from the live state or the bundle instead). The design record with the alternatives considered is `docs/plans/subagents.md`.

## Screenshots

Captured from the bundled SPA against a stub model (`docs/assets/subagents/`):

- `tasks-panel-agent-running-dark.png`, `tasks-panel-agent-running-light.png`: the Tasks panel with a running subagent card and its `AGENT` badge.
- `tasks-detail-agent-running-dark.png`: the detail pane of a running subagent with the live log and **Open transcript**.
- `tasks-detail-agent-finished-dark.png`, `tasks-detail-agent-finished-light.png`, `tasks-detail-agent-narrow-dark.png`: the finished run with its report block, wide and narrow.
- `tasks-panel-agent-finished-light.png`: the finished row under **Finished N**.
- `child-transcript-readonly-dark.png`, `child-transcript-readonly-light.png`: the child session opened from the panel, with the read-only notice in place of the composer.
