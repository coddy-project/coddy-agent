# Plan: subagents (user-defined child agents on the background task pool)

Status: approved by Codex cross-review (plan iteration 6, 2026-09-03) on branch `claude/subagents-pool-config-d0d8da`; implemented, see section 9 for the deviations the implementation recorded.
Reference for the shipped behavior will be `docs/subagents.md`; this file stays
as the design record. Review deltas are marked inline as `[rev]` (iteration 1),
`[rev2]` (iteration 2), `[rev3]` (iteration 3) and `[rev4]` (iteration 4).

## 1. What

Let the agent delegate a bounded piece of work to a **subagent**: a child agent
run with its own context window, its own session bundle, and a role prompt the
operator authored in a markdown file. The parent gets back only the child's
final report. Operators can:

- define their own subagents as markdown files with YAML frontmatter
  (`.coddy/agents/*.md` in the project, `~/.coddy/agents/*.md` globally);
- bound the pool: how many subagents may run at once process-wide, how deep
  spawning may nest, how long one run may take;
- watch a subagent while it runs and read its report afterwards in the same UI
  that already shows background tasks, and open its full transcript;
- change the pool bounds through the bundled `configure-coddy` skill, because
  the knobs are ordinary `config.yaml` keys.

The problem being solved is context: a long investigation or a parallel
fan-out (review three modules, run the test matrix per package, research four
libraries) currently fills the one conversation the operator is watching.
Subagents isolate that work and return a summary.

## 2. What the analogs do (research 2026-09-03)

All four coding agents converge on the same shape. The full comparison with
sources is in the scratch report; the points that decide this design:

| | Claude Code | Codex CLI | OpenCode | Cursor |
|---|---|---|---|---|
| Definition | `.claude/agents/*.md`, `~/.claude/agents/*.md`, YAML frontmatter, body = system prompt | `.codex/agents/*.toml`, `~/.codex/agents/*.toml`, `developer_instructions` | `.opencode/agents/*.md`, `~/.config/opencode/agents/*.md`, frontmatter | `.cursor/agents/*.md` plus `.claude/agents/` and `.codex/agents/` for compatibility |
| Required fields | `name`, `description` | `name`, `description`, `developer_instructions` | `description` | none (name defaults to filename) |
| Common optional fields | `tools`, `disallowedTools`, `model`, `permissionMode`, `maxTurns`, `background` | `model`, `sandbox_mode`, `mcp_servers` | `model`, `mode`, `permission`, `steps`, `hidden` | `model`, `readonly`, `is_background` |
| Tool | `Agent(subagent_type, prompt, description, run_in_background)` | `spawn_agent` + `wait_agent` / `send_input` / `close_agent` | `task(subagent_type, prompt, description, background)` | Task tool |
| Isolation | fresh context, only the final text returns | forks history by default | fresh context, last text part returns | fresh context, final summary returns |
| Concurrency | `CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS` (20) | `agents.max_concurrent_threads_per_session` (6 / 4) | none documented | none documented |
| Depth | `CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH` (3) | `agents.max_depth` (1) | `subagent_depth` (1) | fixed 2 |
| Built-ins | Explore, Plan, general-purpose | default, explorer, worker | general, explore, scout | Explore, Bash, Browser |
| Report to the user | transcript row plus `/tasks` panel, transcript files | `/agent` thread switcher, Subagents panel | child sessions in the sidebar | transcript path via hooks |
| Permissions | frontmatter mode, parent's bypass wins; prompts of background children surface in the main session | inherit sandbox, parent's live overrides win | child gets the parent's deny rules plus its own | inherit tools, `readonly` narrows |
| Project trust | frontmatter hooks and MCP servers from `.claude/agents` wait for the workspace trust dialog | project agents load only in trusted projects (third-party claim) | none documented | none documented |

Common denominator adopted here: markdown plus frontmatter, project wins over
user on name conflicts, a single spawn tool with a name and a prompt, the child
runs in a clean context and returns one text result, foreground and background
modes, a small default depth, a concurrency cap, a read-only `explore` and a
`general` built-in, permissions flow down and never widen, and the child is
inspectable as its own session.

## 3. Approach

Coddy already has most of the machinery, so the feature is wiring rather than
a new subsystem:

- `internal/bgtask` is the scheduler. `Spec.Kind` carries a reserved
  `KindAgent`, `Runner`/`Handle` are interfaces, and `docs/background-tasks.md`
  names "a second Runner plus a tool that builds the Spec" as the intended
  extension. A subagent run is a task in that pool, so `background_list`,
  `background_output`, `background_wait`, `background_stop`,
  `notify_on_finish`, the REST surface, the Tasks panel, drain on shutdown, and
  persistence under `<session>/background/<task_id>/` all apply unchanged.
- `session.Manager` already owns session creation, registration, the turn
  lock, activity tracking, cross-process cancel, MCP dialing through
  `TrustGate`, and persistence. `[rev]` The child session is created and run
  through the manager, never by constructing `session.State` in
  `internal/agent`.
- `internal/mcp/trust.go` already records out-of-band approvals keyed by
  canonical workspace and a content digest, and project-local MCP servers stay
  cold until approved through the CLI, the HTTP route or Settings. `[rev3]`
  Project-scope definitions follow exactly that model: no in-chat prompt, an
  out-of-band receipt, refusal until it exists.
- `internal/skills/loader.go` already parses frontmatter; the definitions
  loader mirrors its layout rules.
- `session.SessionMeta.SchedulerRun` and `ExcludedFromComposerSessionList`
  already hide service sessions from History; subagent sessions use the same
  mechanism.

### 3.1 Definitions (`internal/subagents`, new package, layer 4)

Search order, lowest to highest priority, configurable as `subagents.dirs`:

1. `${CODDY_HOME}/agents` (user scope)
2. `${CWD}/.claude/agents` (project scope, compatibility: same frontmatter
   shape, read only)
3. `${CWD}/.coddy/agents` (project scope)

`[rev2]` Scope is decided on canonical paths: both the expanded directory and
the session cwd go through `mcp.CanonicalWorkspace` (absolute, symlinks
resolved, cleaned); a directory under the canonical cwd is **project scope**,
everything else is **user scope**. Project scope is governed by
`subagents.project_trust` (3.2).

Each `*.md` file (recursively, like Claude Code) is one definition;
`<dir>/<name>/AGENT.md` is also accepted and `[rev]` takes its default name
from the directory, not from the `AGENT` stem. Later directories override
earlier ones by name. `[rev]` Inside one directory files are visited in lexical
order and the first occurrence of a name wins; the duplicate is skipped with a
logged warning. Two built-ins ship embedded and sit below every directory:
`general` (parent's tool set) and `explore` (read-only). A user file with the
same name replaces a built-in.

`[rev]` Bounds: at most 200 definitions per directory (the rest are skipped
with a warning), a file over 256 KiB is skipped, a role body over 64 KiB is
truncated with a marker, `description` is cut to 200 characters in the catalog,
labels reuse the pool's 60-character `deriveLabel`, and a `spawn_agent`
`prompt` over 32 KiB is refused.

Frontmatter (only `description` is required; `name` defaults as above):

| Field | Meaning |
|---|---|
| `name` | slash-safe identifier `[a-z0-9][a-z0-9_-]*` |
| `description` | one line, shown to the parent model so it can pick the agent |
| `model` | `models[].model` id; unknown ids fall back to the parent's model with a warning in the task log |
| `mode` | `agent` or `plan`; a plan-mode parent always forces `plan` |
| `tools` | allowlist of tool names (YAML list or comma-separated string, Claude Code style); exact names or a `prefix*` pattern so `context7__*` can admit one MCP server |
| `disallowed_tools` | denylist, wins over `tools`; `disallowedTools` accepted as alias |
| `permission_mode` | `ask`, `accept_edits`, `bypass`; Claude aliases `default`, `acceptEdits`, `bypassPermissions` mapped; may only **narrow** the parent's effective mode |
| `max_turns` | cap on ReAct rounds (`maxTurns` alias) |
| `timeout_seconds` | hard limit for one run |
| `background` | `true` forces detached runs even when the model asked for foreground |
| `hidden` | keep out of the catalog shown to the model and out of "unknown agent" error lists; still spawnable by name; listed by the CLI and the HTTP catalog with `hidden: true` |

The body is the child's role block. Unknown fields are ignored so files written
for other agents load. Every loaded definition carries `Scope`, `Path` and
`Digest` (SHA-256 of the file bytes). `[rev3]` A resolved definition is an
immutable value: the loader reads the file once, computes the digest from the
same bytes, and every later decision (trust, tool set, role) uses that value,
never a re-read.

### 3.2 Configuration (`config.Subagents`, top-level `subagents:`)

| Key | Default | Meaning |
|---|---|---|
| `enabled` | `true` | register `spawn_agent`, render the catalog |
| `dirs` | the three above | definition directories, `${CODDY_HOME}` and `${CWD}` expand |
| `project_trust` | `ask` | `[rev2]` trust policy for project-scope definitions: `ask`, `allow`, `deny`, same vocabulary as `mcp.project_trust` |
| `max_concurrent` | `4` | process-wide cap on subagents running at once (Codex uses 4 and 6, Claude Code 20) |
| `max_depth` | `1` | nesting; `1` means a subagent cannot spawn (Codex and OpenCode default); an explicit `0` forbids spawning everywhere |
| `default_timeout_seconds` | `1800` | hard limit when neither the definition nor the call gives one |
| `max_turns` | `agent.max_turns` | default ReAct cap for a child |

`[rev2]` **Project-scope trust.** A definition that arrived with the checkout
directs a child's tools, and the existing permission gate does not cover
everything a child can do: `websearch` and `webfetch` ask nobody, MCP calls
bypass the registry gate, and the model may delegate on its own because the
`description` invites it. So a project-scope definition is trusted the same way
a project-local MCP declaration is, by an out-of-band receipt bound to the
workspace and to the file content:

- `deny`: project-scope directories are not read at all; agent discovery, the
  CLI listing and the HTTP catalog all omit them (tests prove the files stay
  unread).
- `ask` (default): project-scope definitions load and are listed with a
  `needs_approval` state until `<home>/subagents-trust.json` holds a record for
  `(canonical workspace, name, digest)`. `[rev3]` Spawning one without a
  receipt is **refused**: the tool result says the definition comes from a
  project file that is not approved for this workspace and names the ways to
  approve it (`coddy agents trust <name>`, `POST /coddy/subagents/{name}/trust`,
  or `subagents.project_trust: allow` through the staged config tools), so
  the model can tell the operator and retry afterwards. The refusal happens in
  the runtime, on the resolved definition, and does not go through
  `session/request_permission` at all: every sender in the tree auto-allows
  permission requests under `permission_mode: bypass` (the ACP `serverRef`, the
  HTTP bridge, the gateway and scheduler senders), so an in-chat approval could
  be granted by nobody. Rewriting the file changes the digest and the receipt
  stops matching. Approved and user-scope definitions follow the ordinary
  narrowing rules below.
- `allow`: project-scope definitions behave like user-scope ones.

Approval surfaces: `coddy agents trust <name> [--cwd DIR]` and `coddy agents
untrust <name>`, `POST /coddy/subagents/{name}/trust` and `/untrust` (body
`{"cwd": ...}`), and the catalog rows (`trusted`, `needs_approval`, `digest`).
A Settings tab is a follow-up (3.6); until then the bundled `configure-coddy`
skill documents the `project_trust` key so an operator in the web UI can widen
the policy for a trusted checkout through the staged, permission-gated config
commit. The store is `subagents.TrustStore` (`<home>/subagents-trust.json`,
version 1, `workspaces: {canonical path: [{name, digest, path, approved_at}]}`),
a sibling of `mcp.TrustStore` rather than a reuse of it: that file is typed for
MCP declarations, and mixing the two receipts would let an MCP approval read as
an agent approval or the reverse.

Whatever the scope, `permission_mode`, `tools` and `disallowed_tools` only
narrow the parent's capabilities, and a definition carries no hooks, commands
or MCP declarations of its own, so nothing in it starts a process at load
time.

The concurrency cap is a resizable semaphore in `internal/subagents`
(`Limiter`), process-wide so the number the operator sets is the number of
LLM loops that can run in parallel, whatever session started them. The pool's
own per-session `tools.background.max_concurrent` still applies to the task
count. Starting past either limit is refused with a message naming the key
that applies, matching the pool's existing "refused, not queued" contract.
`[rev]` Lowering the cap below current usage never stops a running child; new
spawns are refused until the count drops under the new limit. Hot reload goes
through `Limiter.SetLimit`, applied on each turn like `pool.SetConfig`.

`[rev]` Timeout precedence for one run: the call's `timeout_seconds`, then the
definition's `timeout_seconds`, then `expected_seconds × 3` floored at 60 s,
then `subagents.default_timeout_seconds`; the result is capped by
`tools.background.max_timeout_seconds`, exactly like a background command.

### 3.3 Spawning (`internal/tools` tool, `session.Manager` runtime, `internal/agent` runner)

Tool `spawn_agent`:

| Argument | Meaning |
|---|---|
| `agent` | definition name (required) |
| `prompt` | the task, self-contained: the child sees no parent history (required, at most 32 KiB) |
| `description` | 3 to 5 words; becomes the task label and the child session title |
| `background` | detach and return the task id at once; default false waits for the report |
| `expected_seconds`, `notify_on_finish`, `timeout_seconds` | same semantics as `run_command` background; `[rev2]` `notify_on_finish` is forced off for a foreground spawn, whose report already comes back in the tool result |

The tool is registered when `subagents.enabled` is on, offered in both agent
and plan mode (a planner fans out investigation like Claude Code's Explore),
and hidden from a child that has reached `max_depth`. The registry only sees
one hook, `tooling.Env.SpawnAgent func(ctx, SpawnRequest) (SpawnResult,
error)`, wired by `internal/agent` like `LoadSkillBody` and `ReloadConfig`, so
`internal/tools` keeps its layer. `[rev3]` The tool itself needs no permission
prompt: launching a child changes nothing by itself, every tool call the child
makes is gated on its own, and project trust is decided inside the hook.

`[rev]` **Ownership.** `internal/agent` gets a `SubagentRuntime` interface
(`CreateSubagentSession`, `RunSubagentTurn`, `RetireSubagentSession`) that
`session.Manager` implements; every surface that builds the runner closure
(`cmd/coddy/main.go` acp, `external/httpserver/commands.go`,
`external/cli/run.go`, `cmd/coddy/gateway.go`) calls
`loop.SetSubagentRuntime(mgr)`. A scheduler run has no manager and answers
`spawn_agent` with "subagents are not available in scheduled runs". The child
carries its settings on its own state (`session.State.Subagent()` returns
`{Name, ParentSessionID, TaskID, Depth, Role, Tools}`), so `agent.NewAgent`
configures itself from the state and no call site needs to know it is running a
child.

The hook does, in order:

1. Resolve the definition for the parent's cwd under the project policy into
   one immutable value; refuse unknown names with the list of visible ones.
   `[rev3]` For a project-scope definition under `ask`, check the receipt for
   `(canonical workspace, name, digest)` on that same value and refuse without
   one (3.2). Validate the prompt size and the arguments.
2. Acquire a `Limiter` slot; refuse when full. `[rev]` The slot is released by
   one `sync.Once` guarded `finish` that every exit path calls: pool refusal,
   launch failure, timeout, stop, parent cancellation, panic (recovered in the
   run goroutine and reported as `failed` with the panic text), and normal
   completion.
3. `[rev3]` Generate the child session id (`session.NewSubagentSessionID()`,
   `sub_<hex>`) **before** the pool is involved and place it in `Spec.Agent =
   &AgentInfo{Name, SessionID}`, so every snapshot the pool ever publishes or
   persists carries the final identity and nothing is written through a shared
   pointer afterwards. No bundle exists yet: an id is a string, and a pool
   refusal leaves nothing behind.
4. `Pool.Launch(spec, launch)`: a new public method on the single
   `start(spec, launch)` path, next to `Adopt`. `[rev2]` Its callback is
   `LaunchFunc func(taskID string, out io.Writer) (Handle, error)`: the pool
   admits the spec, assigns and registers the task id, and only then calls
   back, so the child session is created **inside the callback** with both ids
   known. A child-creation failure inside the callback is returned to the
   pool, which records the task as `failed`. `Spec.Kind = KindAgent`, label
   `agent <name>: <description>`, `ToolCallID` so the transcript row gets its
   live chip, `TimeoutSeconds` resolved per 3.2. The `Handle` cancels the child
   context on `Stop`, waits on the run's done channel, and reports `PID() ==
   0`, so the survivor probe never mistakes it for a process.
5. Inside the callback, `Manager.CreateSubagentSession(spec)`: the
   pre-generated id, bundle via `FileStore.EnsureLayout`, the same construction
   path as `session/new` (skills, rules catalog), mode and permission mode
   narrowed as above, `SelectedModelID` from the definition, meta
   `SubagentRun`, `ParentSessionID`, `SubagentName`, `SubagentTaskID`,
   registered in the live session map so a transcript read while the child
   runs is served from the one live state. `[rev2]` **MCP.** The parent state
   retains the declarations it dialed: configured servers are re-resolved for
   the child's cwd through `TrustGate` as for any new session, and ACP
   client-supplied servers, which exist only on the parent, are redialed from
   the retained `[]config.MCPServerConfig` (`State.SessionMCPDeclarations()`),
   ungated like the original connect because the editor supplied them over the
   wire. Dialing happens only when the child's effective tool set can contain
   MCP names. `[rev]` No client is borrowed from the parent; the child owns and
   closes its own.
6. `Manager.RunSubagentTurn` is `HandleSessionPromptWithSender` on the child
   id with the child sender and an internal option marking the call as the
   child's own task turn (see the read-only rule in 3.4): turn lock, activity
   edges (`GET /coddy/events` reports the child turn), cross-process cancel,
   persistence, all reused. `[rev]` Contexts: a foreground child runs on a
   context derived from the tool call context, so parent cancellation
   propagates, plus the handle's own cancel; a detached child runs on
   `context.WithoutCancel(toolCtx)` plus the handle's cancel, because the tool
   call context ends the moment the tool returns. `RetireSubagentSession` runs
   from `finish`. `[rev4]` **Work launched by the child settles first.** A
   `general` child inherits `run_command` with `background: true` and, below the
   depth limit, `spawn_agent`, so it can leave a background command or a
   detached grandchild running when its own turn returns; a retired child is a
   read-only transcript with no turn to collect them, and the root parent's
   background tools are scoped to its own session. So, before dropping the live
   entry, retirement calls `pool.StopSession(childID)` and waits for every task
   the child owns to settle (a grandchild task's handle cancels that grandchild,
   whose own retirement recurses the same way), then closes the child's MCP
   clients and drops the live entry, leaving the bundle on disk, so later
   transcript reads load the finished bundle like any closed session. Finished
   task records stay in the child's bundle. Tasks started from a child session
   (commands and spawns alike) are launched with `notify_on_finish` forced off,
   because a woken turn against a read-only child would be refused anyway.
7. The child sender writes progress into the task's output sink (assistant
   text per line, `→ tool` on start, `✓`/`✗ tool` on finish) and never
   touches the parent's stream, so an ACP editor or an SSE relay never
   receives updates for a session id it does not know. `[rev3]` **Permission
   relay and arbiter.** Each spawn creates a `permissionRelay` that owns the
   parent's sender **and the parent turn context of that spawn** (the tool
   call context, which the manager cancels when that turn returns) plus the
   child's own cancel context; the relay lives exactly as long as its child.
   A `permissionArbiter` per parent session (a process-wide map in the
   runtime, created on first spawn, dropped when the last child retires)
   holds only the serialization mutex, so at any moment at most one prompt is
   in flight for the parent, which is also what the HTTP
   `pending_permission.json` (one record per session) can represent. A child
   spawned by a later turn therefore carries that later turn's context and is
   not denied because an earlier detached child's turn ended. While the
   relay's turn context is alive a request is forwarded with the parent's
   session id and the title `[subagent <name>] Run: <tool>`, so the ACP
   editor, the TUI modal or the HTTP SSE bridge shows it in the parent chat;
   the wait selects on that turn context and on the child's cancel, so a
   parent turn ending mid-prompt or a stopped child unblocks with a denial.
   Once the turn context is done, every request answers `cancelled`
   immediately without touching the transport, and the child's tool gets
   "permission denied by user". A detached child therefore needs a definition
   that narrows to read-only tools or a mode the operator accepted;
   `docs/subagents.md` says so. `RequestQuestion` always refuses.
8. `[rev]` **Capability intersection.** The child's effective tool set is
   computed once at spawn: parent effective set (its mode set, and its own
   effective set when the parent is itself a child) ∩ child mode set ∩
   definition allowlist − definition denylist − mandatory exclusions
   (`question`, `config_set`, `config_changes`, `config_commit`,
   `config_revert`, `config_rollback`, `plan_exit`, and `spawn_agent` at the
   depth limit). MCP names are part of the set and matched by exact name or
   `prefix*`. The set is enforced twice: `currentToolDefinitions` advertises
   only its members, and `executeToolCall` refuses any call outside it before
   the permission check, MCP calls included, so a hallucinated `spawn_agent`
   or `run_command` is answered with "not available to this subagent" and
   never executed. The `explore` built-in is exactly `read`, `keep_result`,
   `glob`, `grep`, `print_tree`, `websearch`, `webfetch`, `load_skill`,
   `background_list`, `background_output`, `background_wait`: no `run_command`
   and no MCP tools, because plan mode alone is not read-only.
9. On finish the sink gets a report block: status, rounds, duration, the
   child's last assistant text, the child session id. Foreground calls return
   that block as the tool result: `<subagent task="bg_3" session="sub_…"
   agent="explore" status="succeeded">` followed by the report `[rev]` wrapped
   in CDATA (the existing `wrapXMLCDATA`), then a reminder that the user did
   not see it. Background calls return the task id and the collection
   instructions the pool already uses.

The child's system prompt reuses the existing templates with two new
`TemplateData` fields: `Subagents` (catalog block for parents, plus guidance
on when to delegate) and `SubagentRole` (preamble "you are subagent X spawned by
a parent agent, work autonomously, finish with a concise report" plus the
definition body). The identity line stays, so gateway attribution keeps
working.

### 3.4 Session, HTTP, UI, CLI

- `session.SessionMeta` gains `subagentRun`, `parentSessionId`,
  `subagentName`, `subagentTaskId`; `ExcludedFromComposerSessionList` hides
  `sub_*` bundles; `GET /coddy/sessions?include_subagents=true` includes them.
- `[rev3]` **Child sessions are read-only transcripts.** Resuming or messaging
  a child is out of scope, so `HandleSessionPromptWithSender` rejects a prompt
  for a `SubagentRun` session unless the call carries the runtime's internal
  task-turn option: an external `POST /v1/responses`, an ACP `session/prompt`
  or a console prompt against `sub_*` answers `ErrSubagentReadOnly`, mapped to
  **409** over HTTP with a message naming the parent. `RunPlan`, permission
  resume and the background waker go through the same check. The SPA reads
  `subagent {parentSessionId, name, taskId}` from `GET .../messages` and
  renders the transcript with the composer replaced by a read-only notice
  linking back to the parent chat.
- `[rev3]` **Deletion.** `Manager.DeleteSessionTree(id)` is the one path both
  `DELETE /coddy/sessions/{id}` and future callers use. It first collects the
  tree (the requested session plus every descendant found by
  `parentSessionId`, `sub_*` bundles only), then **stops every node's
  representing task** through `pool.Stop(meta.ParentSessionID,
  meta.SubagentTaskID)`, root to leaf, which waits for each child run to
  settle, then stops any remaining tasks of every node (`pool.StopSession`),
  and only then removes bundles deepest first, the requested session last.
  Tested with a running child deleted directly, a parent deleted while a
  nested descendant runs (depth 2 configuration), and an assertion that no
  write lands in a removed bundle afterwards.
- Task rows already serialize `kind`; `agent {name, session_id}` is added to
  `bgtask.Snapshot` and therefore to `GET .../background-tasks` and the
  persisted `meta.json`. `GET /coddy/subagents?cwd=` returns the catalog for a
  workspace (name, description, source, scope, path, digest, model, mode,
  built-in, hidden, `trusted`, `needs_approval`), and `POST
  /coddy/subagents/{name}/trust` and `/untrust` write and remove receipts for
  the given `cwd`. `openapi.go` and `docs/http-api.md` follow.
- Tasks panel (`external/ui/src/ui/tasks/`): agent tasks show an `agent` badge
  and the agent name, the detail pane shows the role name instead of a shell
  command and gains **Open transcript**, which routes to `#/s/<child id>` (the
  child is an ordinary persisted session, so the existing messages endpoint
  and transcript renderer show tool calls and the final answer; while the
  child runs the live state is served, afterwards the bundle). The live
  progress log in the output pane is the "report while it runs". i18n keys in
  `en.ts` and `ru.ts`; `DESIGN.md` and `docs/ui.md` updated; screenshots in
  the PR.
- CLI: `coddy agents list [--cwd DIR]` prints the catalog like `coddy rules
  list`, with scope, trust state and hidden markers; `coddy agents trust |
  untrust <name> [--cwd DIR]` manage receipts; the TUI status line names
  `Running subagent <name>` while `spawn_agent` executes.
- ACP and gateway: nothing protocol-level changes. The parent's `tool_call`
  rows carry the spawn and its result; child updates never leave the process.

### 3.5 Shutdown and cleanup invariants `[rev]`

- Server drain and process exit: `bgtask.Default().StopAll()` stops agent
  tasks like commands; each handle cancels its child, `finish` retires the
  session and releases the slot.
- Parent turn cancelled (Stop): foreground children are cancelled through the
  derived context; detached children keep running by design and can be stopped
  from the Tasks panel or `background_stop`.
- `[rev4]` Child turn returns: every task the child launched (background
  commands, detached grandchildren) is stopped and awaited before the child is
  retired; a notification-enabled task finishing around retirement wakes
  nobody, because tasks launched from a child never carry `notify_on_finish`
  and the waker's prompt against a `SubagentRun` session is refused.
- Parent session deleted, child deleted: see 3.4.
- Parent session forgotten from memory (`ForgetLiveSession`) or reloaded: the
  child has its own MCP clients and its own state, so nothing it depends on is
  closed underneath it.
- Limit lowered under load: see 3.2.

### 3.6 Out of scope (follow-ups, documented)

Resuming or messaging a running child, worktree isolation, definition-level
hooks and MCP servers, `SubagentStart`/`SubagentStop` hooks, a Settings tab for
editing definitions and approving them (the CLI and the HTTP routes cover
approval; the `configure-coddy` skill covers widening the policy), queueing
instead of refusing when the pool is full, a live SSE relay for the child (the
transcript is read from the live state or the bundle).

## 4. Alternatives considered

- **A second `Runner` registered by kind** instead of `Pool.Launch`. The
  runner needs the parent's live context, sender and configuration, none of
  which belong in a serializable `Spec`. `Adopt` already established the
  launch-callback shape on the one scheduling path, so `Launch` reuses it
  without a payload field of type `any`.
- **Constructing the child `session.State` inside `internal/agent`** (v1).
  `[rev]` Rejected: the HTTP read path loads unknown ids from disk into the
  manager, which would create a second state for a running child and stale
  or racing transcripts. The manager owns child sessions.
- **Borrowing the parent's MCP clients** (v1). `[rev]` Rejected: closing on the
  parent side (reload, forget) would cut the child mid-run. The child dials its
  own; `[rev2]` client-supplied declarations are retained on the parent and
  redialed so `general` really does see the parent's MCP tools.
- **A `restricted` policy that only forces `ask` mode** (v2). `[rev2]`
  Rejected: `websearch`, `webfetch` and MCP calls are not behind the permission
  gate, so a checkout's role could still act unapproved.
- **Approving a project definition through the permission prompt** (v3).
  `[rev3]` Rejected: senders auto-allow under `permission_mode: bypass`, so a
  prompt is not a trust decision; and a prompt-then-resolve sequence reads the
  file twice. Replaced by the MCP-style out-of-band receipt with refusal until
  it exists, checked on the one resolved value.
- **Reusing `mcp.TrustStore` for definitions.** Rejected: the file and its
  records are typed for MCP declarations; a sibling store with the same layout
  keeps the two kinds of approval apart.
- **Filling the child session id from inside the launch callback** (v3).
  `[rev3]` Rejected: it would mutate a published snapshot through a shared
  pointer. The id is generated first and travels in the spec.
- **One permission arbiter holding the first turn's context** (v3). `[rev3]`
  Rejected: a later spawn would inherit a cancelled context. Per-spawn relays
  hold their own turn context; the per-session arbiter only serializes.
- **Queueing spawns when the pool is full.** Friendlier, but the pool's
  contract today is "refused, not queued", `StatusQueued` is unused, and a
  queued child that starts minutes later still needs a parent turn to consume
  its report. Refuse with a clear message; the model can `background_wait`.
- **Streaming child updates into the parent's transcript.** Garbles the
  parent's SSE stream and sends unknown session ids to ACP editors. The sink
  plus the child's own session covers observability.
- **A dedicated `subagent_*` tool family for lifecycle.** Duplicates the
  background tools, the drawer and the REST surface for no gain.

## 5. Files to change

Layered order, lowest first (`implementation-order.md`):

1. `internal/config`: `subagents.go` (struct, defaults, validate, policy
   constants), `types.go`, `config.go` (defaults chain), `ui_schema.go`,
   `jsondto.go`, `schema_ui_defaults.go`; `docs/config.schema.json`,
   `docs/config-reference.md`, `docs/config.md`, `config.example.yaml`,
   `internal/skills/bundled/configure-coddy/SKILL.md`.
2. `internal/bgtask`: `Pool.Launch` with `LaunchFunc(taskID, out)`,
   `Spec.Agent` / `Snapshot.Agent`, `deriveLabel` for `KindAgent`, tests.
3. `internal/subagents` (new): `definition.go` (frontmatter, aliases,
   bounds, digest, narrowing helpers, tool set intersection), `loader.go`
   (canonical scopes, policy, precedence), `trust.go` (`TrustStore`,
   receipts), `bundled.go` plus `bundled/general.md`, `bundled/explore.md`,
   `limiter.go`, `catalog.go` (prompt block, CLI listing), tests.
4. `internal/session`: meta fields and `Subagent()` settings, retained
   session MCP declarations, `NewSubagentSessionID`, `SubagentSpec`,
   `CreateSubagentSession`, `RunSubagentTurn`, `RetireSubagentSession`,
   `ErrSubagentReadOnly` and the prompt guard, `DeleteSessionTree`,
   `ExcludedFromComposerSessionList`, filesystem save and list, tests.
5. `internal/tooling/env.go`: `SpawnAgent` hook, `SubagentDepth`;
   `internal/tools/spawn_agent.go`; registry wiring; `toolsets.go` plan list.
6. `internal/agent`: `subagent.go` (runtime interface, spawn hook, trust
   check, handle, sender, permission relay and arbiter, report),
   `system_prompt.go` / `prompts` templates and `TemplateData`, `react.go`
   and `resume_permission.go` env wiring, execution-time tool set enforcement,
   `toolsets.go`, tests and the godog harness.
7. `cmd/coddy/main.go` and `cmd/coddy/gateway.go`, `external/cli/run.go`,
   `external/httpserver/commands.go`: `SetSubagentRuntime(mgr)`;
   `cmd/coddy/main.go`: `agents list | trust | untrust`;
   `external/cli/status.go` label.
8. `external/httpserver`: sessions list filter, tree delete, read-only 409,
   subagents catalog and trust routes, `openapi.go`, godog harness;
   `docs/http-api.md`.
9. `external/ui`: `tasks/types.ts`, `BackgroundTasksPanel.tsx`,
   `taskStatus.ts`, read-only child transcript notice, i18n, tests; rebuild
   embedded assets.
10. Docs and rules: `docs/subagents.md` (new), `README.md`,
    `docs/background-tasks.md`, `docs/architecture.md`, `docs/cli.md`,
    `docs/ui.md`, `DESIGN.md`, `docs/acp-protocol.md` note, `docs/mcp-integration.md`
    cross-reference, `AGENTS.md` table, `.claude/rules/core-modules.md` and
    `architecture.md` mirrored to `.cursor/rules/*.mdc`, the project-local
    configuration review rule extended to definitions.
11. E2E: `examples/agents_fixture/.coddy/agents/marker-reporter.md`,
    `examples/acp/acp_e2e_subagents.py` (passes a stdio MCP server in
    `session/new` and asserts the child's transcript shows the inherited MCP
    tool), `examples/httpserver/http_e2e_subagents.py`,
    `examples/cli/cli_e2e_subagents.py`, wired into the three runners; the
    fixture is approved with `coddy agents trust` (CLI, ACP) or `POST
    /coddy/subagents/{name}/trust` (HTTP) so the runs stay unattended.

## 6. Delivery flow

Each numbered layer follows the same loop, and the whole feature is committed
in layer-sized steps on this branch:

1. **Executable specification first.** Happy paths as Gherkin in `features/`:
   - `features/subagents.feature` (godog harness `internal/agent/bdd_subagents_test.go`,
     stub LLM providers over a real `session.Manager`): a project-local
     definition is offered to the model; a foreground spawn returns the child's
     report and persists the child session with the parent link; a background
     spawn shows up in the pool as an agent task and its report is collected
     with `background_wait`; child updates never reach the parent's client
     stream; a child at the depth limit is not offered `spawn_agent`; a
     definition only narrows the permission mode; `[rev3]` an unapproved
     project-scope definition is refused with the approval hint, also when the
     parent runs with `permission_mode: bypass`; a receipt recorded with the
     trust command lets the same file spawn; under `deny` project definitions
     are not loaded; a child of a parent with a client-supplied stdio MCP
     server (the repo's re-exec helper) is offered the same MCP tool; a
     permission request from a child is answered in the parent chat while the
     parent turn is alive and denied once it ended; `[rev3]` a child spawned by
     a later turn is still answered after an earlier detached child's turn
     ended; two children asking at once are prompted one after the other; the
     concurrency cap refuses the extra spawn with a message naming it; `[rev3]`
     a prompt sent to a child session from outside is refused as read-only.
   - `features/subagents_http.feature` (`external/httpserver/bdd_subagents_test.go`):
     the task row exposes `kind: agent` and the child session id; the child is
     hidden from the sessions list and returned with `include_subagents=true`;
     the child transcript is readable while the child runs and after it
     finished, and answers 409 to a composer prompt; the catalog endpoint lists
     built-ins and the project definition with its trust state, and the trust
     route flips it; deleting a running child stops its task first; deleting a
     parent removes a nested descendant that is still running and nothing is
     written into the removed bundles afterwards.
   - `features/subagents_catalog.feature` (`internal/subagents`): the CLI
     listing names project, user and built-in definitions with their scopes and
     trust states.
   - `features/cli_tui.feature` gains one scenario: the status line reads
     `Running subagent <name>` while `spawn_agent` is in flight.
2. **Edge tests** as ordinary unit tests next to the code: frontmatter aliases
   and comma-separated tools, `AGENT.md` directory naming, duplicate names in
   one directory, invalid or oversized files skipped, precedence and override,
   canonical scope with a symlinked cwd, digest changes after an edit, receipt
   store round trip, unknown model fallback, permission narrowing table, tool
   set intersection including MCP names and patterns, execution-time refusal of
   a call outside the set, depth gate, limiter resize and refusal and release
   on every exit path, `Launch` assigns the id before the callback and leaves
   nothing behind on refusal, snapshots carry the child id from the first
   publish, foreground spawn forces `notify_on_finish` off, timeout precedence
   and `timed_out`, `background_stop` cancels the child, foreground child
   cancelled with the parent, detached child survives the tool return, panic
   reported as `failed`, sink formatting, CDATA wrapping, `Snapshot.Agent`
   persistence round trip, `ExcludedFromComposerSessionList`, read-only guard,
   tree delete ordering, `[rev4]` a child returning while its background
   command still runs, a child returning while its detached grandchild still
   runs, and a notification-enabled child task completing around retirement
   (all work settles, limiter slots are released, no wake targets the child,
   no write lands after retirement), config validation and defaults, schema
   drift, UI badge, transcript button and read-only notice, i18n parity.
3. **Implementation** until the layer's specs and unit tests pass.
4. **Targeted tests** for the touched packages, then **`make test`** (the
   full tag matrix, including `ui-build`).
5. **Docs and rules** for the layer, config schema sync, OpenAPI sync, UI
   screenshots, rules mirrored to `.cursor/rules/`.
6. **`make lint`**, `make check-windows` where a shared signature changed.
7. Commit the layer; after the last layer, cross-review the code with Codex,
   fix what it finds, run the live e2e scripts for cli, acp and http, open the
   PR.

## 7. Risks

- **Two limits, one message.** A spawn can fail on the pool's per-session cap
  or the subagents cap; both messages name the key that applies.
- **HTTP permission prompts of a detached child.** After the parent's turn
  ended nothing can answer; the relay denies without blocking and the report
  says so. Documented; read-only definitions or a definition with an accepted
  mode avoid it.
- **First spawn of a project definition is refused.** Under the default policy
  an operator approves the file once (`coddy agents trust`, the HTTP route, or
  `project_trust: allow` for a checkout they trust); unattended runs do so up
  front; the docs and the e2e scripts show it.
- **MCP dial cost per child.** A child with MCP tools in its set dials the
  configured and client-supplied servers itself. Bounded by `max_concurrent`;
  `explore` never dials.
- **Token cost.** A child is a full ReAct loop. `max_turns`, the timeout, the
  concurrency cap and `maxWakesPerSession` bound it; the prompt guidance tells
  the model when delegation pays off.
- **Windows.** No new OS-specific code; the handle is process-less. `make
  check-windows` runs anyway because `bgtask` signatures change.

## 8. Addressed concerns

### Iteration 4 (and the unchanged resubmission counted as iteration 5)

1. *Work launched by a child outlives its read-only retirement.* Accepted:
   retirement stops and awaits every task the child session owns, recursing
   through detached grandchildren, before the live entry goes; finished task
   records stay in the child bundle; tasks started from a child never carry
   `notify_on_finish`; the three requested tests are listed in 6.2 (3.3 step 6,
   3.5). The v4 text resubmitted as iteration 5 was a tooling slip, not a
   disagreement.

### Iteration 3

1. *Bypass senders would auto-approve a trust prompt.* Accepted: no prompt.
   Trust is an out-of-band receipt like project MCP servers; an unapproved
   definition is refused in the runtime with the approval hint (3.2, 3.3 step
   1); tested with a `bypass` parent (6.1).
2. *Approval must bind to the resolved definition.* Fixed: the loader
   produces one immutable value with its digest; trust, tool set and role are
   all decided on that value; there is no second resolution and no permission
   hook to record receipts (3.1, 3.3 step 1).
3. *No mutation of `Snapshot.Agent` after registration.* Fixed: the child id
   is generated before `Launch` and travels in `Spec.Agent` (3.3 step 3).
4. *Arbiter keyed by session retains the first turn's context.* Fixed:
   per-spawn relays own their turn and child contexts; the per-session
   arbiter only serializes (3.3 step 7); scenario for a later turn's child.
5. *Child transcripts must be read-only.* Fixed: `ErrSubagentReadOnly` guard
   in the manager for every external prompt path, 409 over HTTP, composer
   replaced by a notice in the SPA (3.4).
6. *Recursive deletion ordering.* Fixed: stop every node's representing task
   root to leaf and wait, then remaining tasks, then bundles deepest first;
   test asserts no write after removal (3.4).

### Iteration 2

1. `restricted` replaced by `project_trust` with a digest-bound receipt (3.2);
   scope paths canonicalized (3.1).
2. `Launch` admits and registers first and hands the id to the callback (3.3
   step 4).
3. Deletion through `DeleteSessionTree` (3.4).
4. Foreground `notify_on_finish` forced off (3.3 table).
5. Client-supplied MCP declarations retained and redialed (3.3 step 5).
6. Serialization of concurrent children's permission requests (3.3 step 7).

### Iteration 1

1. Project-local trust: superseded by the receipt store (iteration 2 and 3).
2. Child-session ownership through the manager (3.3 steps 5 and 6).
3. Capability intersection (3.3 step 8).
4. Lifecycle and cleanup (3.3 step 2, 3.5).
5. Permission forwarding lifetime (3.3 step 7).
6. Smaller ambiguities (3.1, 3.2, 3.3 step 9).

## 9. Implementation notes (deviations from the text above)

Recorded while implementing; the code and `docs/subagents.md` describe the
shipped behaviour.

- The task label is `agent <name>: <description>` (the call's argument, else
  the first prompt line), and the whole label is capped at 60 characters, the
  same cap `deriveLabel` applies to command labels. The pool takes a non-empty
  `Spec.Label` as is, so this cap is the only one.
- An unknown `model` in a definition is logged through the agent logger and
  also noted in the task's output log, so the fallback is visible in the panel.
- `turns` in the report block and in the foreground envelope counts the
  assistant messages in the child's transcript (assistant rounds), not the
  user turns.
- Catalog rows carry no separate `source` field: `scope`, `path` and `builtin`
  identify the origin.
- The ACP live e2e (`examples/acp/acp_e2e_subagents.py`) does not pass a stdio
  MCP server; the fixture definition is read-only and could not admit one. MCP
  inheritance is covered by the godog scenario with the re-exec helper.
- A foreground child cancelled through the parent context ends with pool
  status `failed` (exit 1, no termination claim on the task); the report block
  says `outcome: cancelled`.
- The "unknown subagent" refusal lists every loaded definition except hidden
  ones, so an unapproved project definition is still named (its refusal comes
  with the approval hint when spawned).
- `notify_on_finish` is forced off for every task a child starts, commands
  included (`internal/tools/shell/background.go`), exactly as 3.3 step 6 asks.
- The foreground envelope reads the pool's terminal verdict through
  `Pool.Wait` before returning, because the pool records the status on its
  supervisor goroutine after the handle closes.
- The child's own turn skips the plan-mention heuristics (`RunPlan`
  delegation and `@plans` hydration), so a prompt like "implement the plan X"
  reaches the child verbatim instead of being rerouted.
- The `Launch` callback returns the handle at once and creates the child
  session on the run goroutine, so `Stop` and the timeout reach a creation
  that blocks.
- MCP dialing for a child is bounded by the manager's 30 s reload timeout
  (`mcpReloadTimeout`).
- `EnsureHTTPSession` refuses to mint a fresh session under the reserved
  `sub_` prefix.
- The branch route (`POST /coddy/sessions/{id}/branches`) refuses child
  sessions.
- `DeleteSessionTree` cancels an active turn of each node, rejects new turns
  while the deletion runs, and awaits settlement before removing bundles.
- The `/compact` and `/plugin` built-ins are not intercepted for a child's
  prompt.
- A symlinked project directory is project scope by its lexical path as well
  as by its canonical one.
- An empty effective tool set refuses the spawn.
- The child inherits the parent's selected model unless the definition names
  a configured one.
- The child metadata is attached before the state is published to the live
  map, and an id with the `sub_` prefix is treated as a child by the prompt
  and plan guards even before its bundle says so.
- Turn admission and deletion are decided twice on both sides: the turn
  rechecks the deleting mark after installing its cancel, the delete marks
  before it cancels. A turn that ignores its cancellation past the settle
  timeout aborts the deletion (`ErrTurnNotSettled`, HTTP 409) instead of
  losing its bundle underneath it.
- A failed or cancelled child creation rolls back the live entry and removes
  the bundle it created; the initial save is fatal.
- A forwarded permission request carries the child's effective permission
  mode (`acp.PermissionRequestParams.EffectivePermissionMode`, never
  serialised); every auto-allowing sender (ACP server, HTTP bridge, console,
  print, Telegram gateway) decides bypass from it, so neither a session-level
  nor a global bypass on the parent auto-allows a child narrowed to `ask`;
  senders that cannot prompt deny such a request.
- Turn admission is one path (`beginTurn` / `BeginTurn`): prompts, direct
  plan runs and the HTTP permission resume all register, lock, install the
  cancel and decide against deletion the same way.
- `DeleteSessionTree` rescans the tree after marking until it is stable, the
  tree scan includes live children not yet persisted, and a child creation
  refuses when its parent is not live or is being deleted, both at publish
  time and again before the first save.
