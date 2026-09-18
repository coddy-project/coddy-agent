# Plan: long-term memory as a background subagent run

Status: implemented on branch `feat/memory-subagent-runs`, 2026-09-18; the
plan was cross-reviewed by the Cursor agent and Coddy on a local Qwen before
the build, and the implementation after it (section 8), with the deviations
recorded in section 9. Review deltas are marked inline as
`[rev]`. The user guide of the shipped behaviour will be `docs/features/memory.md`;
this file stays as the design record.

## 1. What

Long-term memory keeps its notes on disk and the tools that read and write
them (`external/memory/storage`, `external/memory/tools`). What changes is the
process that drives those tools. Today it is an in-process loop of its own,
`memory.RunBeforeTurn`, which runs synchronously before the main ReAct agent
and reports through three bespoke wire events, a trace file and a transcript
row. After this change a user turn starts a **memory subagent**: a child agent
run in the background task pool, built on the machinery `spawn_agent` uses
(the pool task, the child session inside the parent's bundle, the transcript,
the task log) through a launcher of its own, so not every spawn invariant
holds for it (3.1 lists what differs: no limiter, no `SubagentStart` /
`SubagentStop`, no `EffectiveTools`, no permission relay). The operator watches and revisits memory runs where
subagent runs already live: the Tasks drawer of the web UI (an `agent` task
with the `memory` name, its progress log, **Open transcript**), the pool tools
(`background_list`, `background_output`) and `GET /coddy/sessions/{id}/background-tasks`.

"Background" here means what it means for every task of the pool: the run
has a lifetime of its own, independent of the turn that started it, and is
watched, stopped and kept as a record like any task. It does not mean the turn
never waits. The turn waits at most `memory.wait_seconds` for the child's
report, then proceeds without it; a report that arrives later in the same turn
still reaches the model through the turn context block; a report that arrives
after the turn is history in the Tasks drawer and in the child transcript. With
the default wait the common case looks like today's synchronous pass (the
recall is in the first request), bounded where it was not; with `0` the start
is asynchronous too. The old logic goes: the copilot loop, the `memory_phase` and
`memory_message_chunk` updates, `memory_trace.json`, `memoryTurns`, the replay,
the SPA memory row and the console stream. It was asked to be thrown out, and
section 2 lists why keeping it would be the wrong call anyway.

## 2. Why the in-process pass goes

- **It is unbounded and synchronous.** Issue #221: the pass has no time bound
  of its own (`providers[].timeout_ms` has no default, the first-token timer of
  the main loop is not applied to it), and `react.go` calls it before the first
  model request, so a provider that accepts the connection and sends nothing
  hangs the whole turn. A task in the pool has a hard timeout by construction.
- **It duplicates the ReAct loop with fewer guards.** `copilot.go` re-implements
  streaming, tool dispatch, fallback across models and a round cap, and has
  none of the loop's protections: no lane replay for a stream that produced
  nothing, no loop detection, no hooks, no queue, and no transcript of its
  rounds (the trace kept the final text only). A child session runs the one loop that has all of that.
- **Its observability is bespoke and half-migrated.** Two wire events, a
  per-session `memory_trace.json` merged by turn index, a replay path in
  `manager_replay.go` that still emits the legacy `recall`/`persist` phases, a
  React component carrying both the unified and the legacy prop sets, a
  "freeze the wall clock when thinking starts" workaround in
  `consumeComposerSse.ts`. A subagent run has one record shape the pool
  already persists, one transcript the session store already serves, and a
  Tasks drawer that already renders both. (Issue #235, the codeword assertion
  of the ACP e2e harness, is a question about the persist contract, not about
  this observability; this plan does not settle it, see 3.6.)
- **Its input is unbounded.** Issue #266 reports the copilot solving the
  user's task instead of doing memory, and notes that the prompt text reaches
  it without a size bound. The pass already runs on a dedicated prompt
  (`copilot.md`), so a new launcher does not by itself change that behaviour:
  what this change adds is the bound (the child's task is cut at 32 KiB) and a
  system prompt template of its own, which makes the configurable addendum
  #266 asks for a one-field follow-up. The steering itself stays open.

## 3. Design

### 3.1 The run

**Trigger.** `Agent.Run` of an ordinary session, after the user message is
recorded and the `UserPromptSubmit` hooks accepted it, at the point where
`runMemoryBeforeTurn` is called today. Conditions: the binary carries the
`memory` tag, `memory.enable` is true, the session is not itself a subagent
run (a child never gets a memory child of its own, and the memory child
obviously not), and a `SubagentRuntime` is wired (every shipped surface runs
its turns through the manager's runner, which wires it: the console, print
mode, ACP, the HTTP server and its background waker, the gateway, the remote
client on the server side; a surface without one skips memory with a warning,
exactly as it cannot spawn, and the implementation confirms the scheduler's
path before the guide says otherwise). The built-in commands
(`/compact`, `/plugin`, `/export`) return before this point and start no run.

**The task.** One `bgtask.Spec` under the parent session:

| Field | Value |
|---|---|
| `Kind` | `KindAgent` |
| `Label` | `memory: <first line of the user message>`, capped at 60 runes like every task label |
| `CWD` | the parent's cwd (the project memory root is `<cwd>/memory`) |
| `ToolCallID` | empty: no tool call started it, so the transcript has no chip for it |
| `TimeoutSeconds` | `memory.timeout_seconds` (300), capped by `tools.background.max_timeout_seconds` as every task |
| `ExpectedSeconds` | 0 |
| `NotifyOnFinish` | false: the parent is never woken on memory's behalf |
| `Agent` | `{Name: "memory", SessionID: <child id>, System: true}` |

The run is **not** counted by `subagents.max_concurrent`: that cap bounds the
model's own delegations, and memory is a system errand the operator turned on
with `memory.enable`. It is a task of the pool, so drain applies and the
Tasks drawer shows it, but it is **not** counted by `tools.background.max_concurrent`
either, and it does not consume one of those slots: the per-session cap
(default 5) bounds the tasks the model starts, and one memory run per turn
holding a slot for up to `timeout_seconds` would refuse the model's own
`run_command` after a few quick turns. The pool admits a system task past the
per-session count and leaves it out of `runningForSession`. The bounds on
memory runs themselves are two constants: `memoryMaxInFlight` (2 per session)
and `memoryMaxInFlightProcess` (16 across the process, so a `coddy serve`
with many busy sessions cannot fan out without limit against one provider). A
run past either is skipped. Turns of a session are
serialised by the turn lock, so overlap only happens when turns end faster than
memory runs, and a skip there costs one turn its recall; the skip is logged at
warn level and sent as a `memory_run` with status `skipped`, so it is not
silent. `tools.background.enable` does not gate the run: that switch hides the
model-facing pool tools and nothing else (the pool itself never reads it, and a
`spawn_agent` background run launches without it). A refused launch (the
in-flight cap, draining, child creation failed) means no memory this turn: the
agent log says why, the `memory_run` update carries a reason that names the
cause (`memory runs in flight: 2 of 2`, `background task pool is shutting
down`, `create memory session: …`), and the turn goes on. Memory never fails a
turn; what can fail is the launch, and every launch failure is caught before
the turn continues.

**The one widening.** The subagent rules say a child's capabilities only ever
narrow the parent's, and a definition file can never add a tool. The memory
child has six tools the parent does not have. That is deliberate and bounded:
the set is fixed in code (`memtools.PersistTools`), it is granted only to the
system child the runtime itself creates, never through a definition, and it
contains nothing but the memory tools, which touch the two note roots and
nothing else. `EffectiveTools` and the definition path are untouched; the
memory launcher bypasses them, which is why it is a separate entry point
(section 4).

`bgtask.AgentInfo` gains `System bool` (JSON `system`), true for the memory
run: a client tells a system agent from a delegation by that flag, and the
pool's admission rule above keys on it. A definition file named `memory` stays
legal: the drawer tells the two apart by the flag, and `SubagentMeta` carries
a `Kind` (`memory`) that the tool registration keys on, so nothing in the code
keys on the name.

**The child session.** Built and run through the existing runtime
(`CreateSubagentSession`, `RunSubagentTurn`, `RetireSubagentSession`), so it
takes the turn lock, persists, honours cross-process cancel and is retired
like any child. For a system child (`Kind` set) the manager skips the skills
load and the rules discovery: the memory prompt renders neither, and that I/O
would otherwise be spent inside the wait. `session.SubagentSpec`:

| Field | Value |
|---|---|
| `Name` | `memory` |
| `Kind` | `memory`: the system child marker the tool registration, the `System` flag and the manager's shortcuts key on |
| `Mode` | `agent`, whatever the parent's mode: the memory tools are not in the plan or ask allowlists, and the child has no other tools anyway. Read-only-ness comes from the tool set below, not from the mode. A hook inside the child therefore sees mode `agent`; the `subagent` block of its payload carries the kind, which is the key an operator hook should match on |
| `PermissionMode` | the parent's effective mode (inherited; nothing in the set is gated) |
| `SelectedModelID` | `memory.model` when it names a configured model, else the parent's effective model |
| `Title` | the task label |
| `Tools` | the six `coddy_memory_*` names; the three recall names when the parent turn is in ask mode |
| `MaxTurns` | the larger of `recall_max_turns` and `persist_max_turns` (the round cap as documented today) |
| `Depth` | the parent's depth + 1 (so `canSpawn` is false inside it under the default `max_depth`) |
| `ConnectMCP` | false |
| `PromptTemplate` | the memory system prompt (below) |
| `MaxTokens` | `copilot_max_tokens` |
| `FallbackModels` | `memory.fallback_models` |

The last three are new `SubagentSpec` and `SubagentMeta` fields; like `Role`
and `Tools` they are not persisted (a restored child is a read-only
transcript).

**The child's tools.** `agent.NewAgent` builds the registry for a turn. A hook
in `memory_hooks.go` (build tag `memory`) registers the six memory tools into
that registry when the session's subagent meta names the system memory agent:
`memtools.PersistTools(memstorage.NewStore(&cfg.Memory, cfg.Paths, cwd), &cfg.Memory)`.
The `!memory` stub registers nothing. The main agent's registry never gets
them, so the contract stands: the main model never sees a memory tool. The
child's `Tools` allowlist is enforced twice as for every child (advertised set
filtered, every call checked before execution), so an ask-mode recall child
cannot save even if the model asks to.

**The child's prompt.** A subagent normally renders `agent.md` with its role in
`{{.SubagentRole}}`; that template is about coding, skills and rules, none of
which the memory child should read. `SubagentMeta.PromptTemplate` carries a
template source; when it is set, `buildSystemPromptParts` renders it with the
ordinary `TemplateData` instead of the mode template, and skips skills, rules,
project instructions, the subagents catalog and session memory. The
environment block, the hook context and the identity line are appended as for
every prompt. The memory template lives at `external/memory/prompts/memory_agent.md`
(the current `copilot.md` reworked: the two modes, the finishing text rules,
the secrets rule, `{{.CWD}}`, `{{.Tools}}`, and the read-only addendum when the
tool set is recall-only). Its task message is `User message for this turn:\n<text>`
with the text cut at `subagents.MaxPromptBytes` (32 KiB) with a marker, the
bound issue #266 noted was missing.

**The child's output.** The pool sink through the existing `subagentSender`:
`→ coddy_memory_search`, `✓ coddy_memory_search`, `[assistant] Already on disk…`,
then the `=== subagent report ===` block. That log is what the Tasks drawer
detail pane and `background_output` show. The child's final assistant message
is the report, and the report is what the main model gets.

**Hooks.** `SubagentStart` and `SubagentStop` do not fire for the memory child:
they describe delegations the model chose, and an operator hook that refuses
spawns must not be able to switch memory off by accident. Inside the child the
ordinary events fire for its own turn with a `subagent` block naming `memory`,
so a `PreToolUse` hook can see memory writes.

**Permission relay.** None. The child's sender gets a nil relay; `Request`
is nil-receiver safe (its first line answers a denial for a nil relay), so a
permission request, which no memory tool raises, is denied at once without a
transport.

**Lifecycle.** The run context is detached from the parent turn
(`context.WithoutCancel`): a Stop of the parent turn ends the wait but not the
run, because a persist in flight must be allowed to finish. The Tasks drawer,
`background_stop` and the hard timeout stop it. Retirement and the pool finish go through the same idempotent `finish`
shape as a spawned child; what the spawn path adds to it (the limiter slot and
the arbiter release) belongs to the spawn path alone, so a system run releases
nothing it never took. Deletion of the parent removes memory
children with the tree.

**Shutdown.** Drain stops tasks, and a memory run stopped mid-persist loses the
note. Print mode (`coddy -p`) today always finished the pass before answering;
to keep that, print mode waits for its session's memory run to finish before
exiting, for as long as the run's own timeout allows (the process has nothing
else to do), and after 15 s of that wait prints one line to stderr naming the
task it is waiting for, so a stuck persist is visible rather than a hang. That
is intentional and tested: a one-shot `remember X` must not lose X. The console on exit and `coddy serve` on shutdown or restart wait
up to `memoryDrainGrace` (15 s, a constant) for running memory runs across all
sessions and then stop them like any task; a persist longer than that is lost
there, which the guide says.

### 3.2 Delivery to the main agent

**What is delivered.** The text the main model gets is the child's last
non-empty assistant message, read from the child transcript after
`RunSubagentTurn` returned (`lastAssistantPlainText`, the same value the
foreground envelope of a spawn carries). Never the task log, which wraps it in
the `=== subagent report ===` block: the `{{.Memory}}` slot gets exactly the
bytes the copilot's final text gave it today (`Already on disk …`,
`(no memory hits)`, a persist summary).

**Who holds the run.** The launcher is the parent's `Agent`, which lives for
one turn: it keeps the run's bookkeeping in a per-turn field (`memoryRun`: the
task id, the child id, the `subagentRun` whose `report` the run goroutine
fills from the child transcript before the task settles, the delivered flag,
and whether the turn is over). The field is set at launch and marked over
when `Run` returns; the next turn is a new `Agent` with a new field, so
nothing has to be cleared. The report is read from that handle, never from the
child's state or the log.

**One store.** Whenever the report is in, it is written once with
`SetMemoryCopilotBlock`, the turn-scoped slot the pass fills today; every
render of the system prompt reads it through `{{.Memory}}`. The frozen build
remembers what it rendered (`systemPromptBuild.MemoryBlock`), and the turn
context block carries a `## Long-term memory` section only when the store
holds text the frozen prompt does not. That one rule covers every path:

1. **The bounded wait.** Right after the launch, before the system prompt is
   rendered, the turn waits for the task up to `memory.wait_seconds` (default
   20) with `Pool.Wait` on the turn context, so a Stop ends the wait. A report
   that is in by then is in the store before the first render, so the frozen
   prompt carries it and the turn context never does. The deadline is taken
   before `Pool.Launch`, so the child's creation (which runs on the run
   goroutine) is inside the wait, and the effective wait is never longer than
   the run's own timeout.
2. **A late report.** With the wait over and the task still running, the turn
   proceeds. The memory hook is asked on every step; once the task finished
   with a non-empty report, the store is filled and the turn context block
   carries the section for the rest of the turn. The block already changes per
   step (clock, checklist, activated rules), so the prefix cache is not
   disturbed further.
3. **A volatile template** under `prompts.dir` (one that prints the clock or
   the checklist itself) gets no turn context block at all; the loop
   re-renders its system prompt every step, and that render reads the store,
   so the late report reaches it there. Same rule, other side.
4. **A rebuild mid-turn.** An automatic compaction rebuilds the system prompt
   from the store, so a late report moves into the frozen prompt, and the
   section leaves the turn context on the next step because the two now
   match. The report is in one place at any time.
5. **Nothing to deliver.** An empty report, or a task that failed, timed out or
   was stopped, injects nothing; the agent log and the task record say why.
6. **`wait_seconds: 0`** never waits: the report can only arrive mid-turn.
7. **After the turn.** A report that lands after the turn ended is not carried
   into the next turn: a recall answers the message it was asked about, and the
   next message gets its own run. It stays readable in the drawer.

`delivered` on the wire (3.3) means a non-empty block reached the model in
this turn, by either path; `(no memory hits)` is non-empty and counts, exactly
as the copilot's text does today.

Where the report went is written into the task's own log, which the pool
persists: `report delivered to the turn (system prompt)`,
`report delivered to the turn (turn context)` or
`turn ended before the report`. The child transcript says what memory found;
that line says whether the model saw it.

The wait is the one place the new design costs a turn latency, and it is the
knob the operator has. `20` is a starting value from running the copilot on a
small local model (a recall with two or three tool calls), not a measurement;
a persist that runs longer finishes in the background.

### 3.3 Wire and surfaces

**Removed.** ACP `memory_phase` and `memory_message_chunk`; HTTP SSE
`memory_phase` and `memory_chunk`; `memoryTurns` on
`GET /coddy/sessions/{id}/messages`; `memory_trace.json`, `MemoryTurnTraceJSON`,
`AppendMemoryTurn`, `ReadMemoryTrace`; the memory replay in `session/load` and
`replayConversation`; the SPA `memory_copilot` transcript item,
`MemoryCopilotMessage`, `memoryStableId`, the wall-clock freeze; the console
`curMemory` stream and its `memory: <phase>...` line; the remote client
passthrough of the two events.

**Added.** One ACP update, `memory_run`, mirrored as the HTTP SSE event
`memory_run` and passed through by the remote client:

```json
{"sessionUpdate":"memory_run","status":"started","taskId":"bg_3","childSessionId":"sess_9f1c…"}
{"sessionUpdate":"memory_run","status":"finished","taskId":"bg_3","childSessionId":"sess_9f1c…","taskStatus":"succeeded","durationMs":3210,"delivered":true}
{"sessionUpdate":"memory_run","status":"skipped","reason":"background task pool is full for this session (limit 4)"}
```

`status` is `started`, `finished` or `skipped`; `taskStatus` is the pool's
verdict (`succeeded`, `failed`, `timed_out`, `stopped`); `delivered` says
whether the report reached the model in this turn (by the wait or mid-turn);
`reason` explains a skip or a failure. `finished` is sent from the turn's own
goroutine only, at the delivery or at the turn's end when the run had already
settled: a run that outlives the turn sends nothing more, because the turn's
sender (the HTTP bridge's response) does not outlive the turn. The first
live stand found exactly that: a watcher goroutine sending `finished` after
the handler returned crashed `coddy serve` on a nil response writer. No text travels on it: the report is in
the child transcript and the log. The update is not persisted and not replayed;
the Tasks drawer is the record, and a client that reconnects mid-run learns
about the run from `GET .../background-tasks`, not from the stream (the SPA
already reads that route on session open and while the drawer is open). That
is a deliberate limitation of the wire. Consumers:

- the **SPA** live status line says `Working with memory` between `started`
  and `finished` while no newer row exists: the phrase (`status.memory`) and
  the `memory` kind exist in `liveStatus.ts` for the removed transcript item
  and for the memory tool names; what changes is the source, a per-session
  "memory run in flight" flag the two updates toggle. Nothing is added to the
  transcript;
- the **console** sets its status to `Working with memory` on `started` and
  prints one dim line on `finished` (`memory: recalled in 3.2s`,
  `memory: still running as task bg_3`, `memory: skipped - <reason>`);
- the **Tasks drawer** renders the run as an agent task: the badge reads
  `memory` when `agent.system` is true, the detail pane shows the role name
  `memory`, **Open transcript** opens the child read-only, the finished list
  shows past runs with their duration and outcome. No new panel, no new route.
- the **model-facing pool tools** do not see it: `background_list` omits
  system tasks, and `background_output`, `background_wait` and
  `background_stop` answer a system task id with `task bg_3 is a system task
  (memory) and is not yours to read or control`. A model that waited on a
  memory run would stall its own turn on work that was never meant to wake
  it; the drawer and the HTTP routes are the operator's, and they show it.

**HTTP.** `GET .../background-tasks` rows gain `agent.system`. `GET
/coddy/sessions?include_subagents=true` includes memory children like any
child. The messages route loses `memoryTurns`. `openapi.go` follows.

### 3.4 Retention

One run per user turn adds one task record and one child bundle per message.
`memory.keep_runs` (default 20, `0` keeps everything) bounds that: when a
memory run finishes, the finished memory runs of that session beyond the
newest N are removed, task record (in memory and under
`<session>/background/<task>/`) and child bundle (`<session>/subagents/<child>/`,
index entry forgotten) alike. Running runs and every other task are untouched.
Two small additions carry it: `Pool.Remove(sessionID, taskID)` for one finished
task, and `Manager.RemoveRetiredChild(childID)` for a bundle whose session is
no longer live and has no running task; both refuse anything else with an
error and remove nothing. A transcript the SPA has open when its bundle goes
answers `404` on the next read, the state the SPA already has for a deleted
session; a harness that holds a bundle path re-reads it and finds it gone.

### 3.5 Configuration

```yaml
memory:
  enable: false
  model: ""                # models[].model for the memory subagent; empty = the session's model
  fallback_models: []      # tried in order when the model above them fails before answering
  dir: ""                  # global root; empty = ${CODDY_HOME}/memory
  wait_seconds: 20         # new: how long a turn waits for the memory report before the first model call
  timeout_seconds: 300     # new: hard limit of one memory run (capped by tools.background.max_timeout_seconds)
  keep_runs: 20            # new: finished memory runs kept per session; 0 keeps all
  recall_max_turns: 6      # the child's round cap is the larger of the two
  persist_max_turns: 12
  copilot_max_tokens: 4096 # completion cap of the memory model's calls
  max_search_hits: 8
```

- Additive: no key is renamed or removed, so the schema can be published to
  the site at once (workflow step 8). `recall_max_turns` and `persist_max_turns`
  survive as two keys for one number on purpose: the schema sets
  `additionalProperties: false`, so a removed key turns every config already on
  disk into a validation error, and a renamed key holds the site publication
  until a release. The generated field table says the cap is the larger of
  the two; folding them into one `max_turns` with the old keys as deprecated
  aliases is a follow-up for a release that renames keys anyway.
- Validation (`-t`): `wait_seconds`, `timeout_seconds` and `keep_runs`
  non-negative. A wait longer than the run's timeout is not an error: the
  effective wait is the smaller of the two, because the run settles by its
  timeout and the wait ends with it, so a misconfigured pair blocks a turn for
  `timeout_seconds` at most, never longer.
- `fallback_models` keeps its contract through a provider chain in the child:
  `getProvider` wraps the model's provider in `llm.FallbackChain` when the
  session's subagent meta carries fallback models; a call that fails before
  producing any output moves to the next model (`memory.model`, then the list,
  then the parent's model as the last resort, unresolvable entries skipped and
  logged), a stream that broke after output follows the lane's own retry. That
  is narrower than the copilot's rule, which fell back on any round error and
  could stream one round twice; the change is intentional, a #247-style outage
  (the model is down, unauthorised, out of quota) fails before the first byte
  and still falls back, and a scenario asserts that a stream which broke after
  output does not jump models mid-answer.
- `copilot_max_tokens` clamps the child provider's `MaxTokens` as before.
- The web UI Settings section, `config.example.yaml`, the schema descriptions,
  `UISchemaMap()` and `configure-coddy/SKILL.md` follow (workflow step 7).

### 3.6 Out of scope

Deliberate follow-ups: memory for subagent children; a transcript row for
memory in the SPA (the drawer is the interface asked for); the configurable
addendum of #266 (the template makes it a one-field change); a distinct memory
role for plan mode; a per-run "what changed on disk" summary beyond what the
child reports; rewriting the three memory e2e harnesses beyond what the new
timing needs (they poll the disk for the note and read the recalled text from
the reply; the ACP harness's codeword assertion of issue #235 keeps failing
exactly as before, because the persist contract it asks about is not decided
here).

## 4. Alternatives considered

- **Keep the copilot loop, put it on the pool as a task.** Gives the history
  and the timeout, keeps the second loop with its missing guards and its own
  wire format. Half the benefit for most of the cost.
- **Give the main agent the memory tools.** Simplest of all, and it changes
  the product: the main model would spend turns on memory housekeeping, the
  recall would no longer be a block in the prompt, and every read-only mode
  would need its own exclusions. The documented contract says the main model
  never sees them.
- **Fully asynchronous, no wait.** A one-step answer would never see its
  recall. The wait is a knob, and `0` is that design.
- **Spawn through `spawn_agent` with a hidden built-in definition.** The
  effective tool set of a child is the intersection with the parent's set, and
  the parent has no memory tools; the definition path would need an exception
  in the one place that must not have one. The launcher shares the pool and
  session machinery instead, through a `launchChildRun` extracted from
  `spawnSubagentInMode`.
- **Keep the transcript row, feed it from the task.** Possible (the SPA could
  place a row by the task's `started_at`), but it is a second rendering of the
  same record and the request was explicit about the drawer.

## 5. Files to change

Layered order, lowest first (`implementation-order.md`):

1. `internal/config`: `memory.go` (`WaitSeconds`, `TimeoutSeconds`, `KeepRuns`,
   defaults, validation), `config.schema.json`, `ui_schema.go`,
   `config.example.yaml`, `docs/reference/config.md` (generated),
   `internal/skills/bundled/configure-coddy/SKILL.md`.
2. `internal/llm`: `FallbackChain` provider (first-output rule), tests.
3. `internal/bgtask`: `AgentInfo.System`, the admission rule for system
   tasks, `Pool.Remove`, tests.
4. `internal/session`: `SubagentSpec` / `SubagentMeta` gain `Kind`,
   `PromptTemplate`, `MaxTokens`, `FallbackModels`; `RemoveRetiredChild`;
   delete `memory_trace.go` and the replay; tests.
5. `internal/acp/types.go`: `MemoryRunUpdate`, the two old types removed.
6. `internal/agent`: `subagent.go` (extract `launchChildRun`, nil relay, the
   `System` flag), `memory_hooks.go` (launch, wait, turn-context section,
   retention, the in-flight cap, the delivery log line, drain grace) and its
   stub, `system_prompt.go` (`PromptTemplate` rendering), `turn_context.go`
   (memory section hook), `react.go` (`getProvider` chain and clamp,
   `NewAgent` tool registration hook), tests and the godog harness.
7. `external/memory`: delete `copilot.go` and `sequential_chain_test.go`,
   add `agent.go` (template, task framing, tool names, label), rename
   `prompts/copilot.md` to `prompts/memory_agent.md`, README.
8. Surfaces: `internal/remote/prompt.go`, `external/cli/updates.go` and
   `status.go`, `external/httpserver/bridge.go`, `coddy_coddy.go`
   (`memoryTurns`), `background_http.go` (`system`), `openapi.go`; drain grace
   in `cmd/coddy` print mode, `external/cli/run.go`, `internal/serve`.
9. `external/ui`: remove `MemoryCopilotMessage`, `memoryStableId`, the
    `memory_copilot` item, the SSE handlers and the freeze; `liveStatus.ts`
    reads `memory_run`; `tasks/types.ts`, `taskStatus.ts`,
    `BackgroundTasksPanel.tsx` (badge); i18n (remove `messages.memory*`, add
    `tasks.badge.memory`); tests; rebuilt embedded assets.
10. Docs: `docs/features/memory.md` (rewrite), `docs/reference/acp-protocol.md`,
    `http-api.md`, `tools.md`, `docs/surfaces/web-ui.md`, `console.md`,
    `docs/getting-started/configuration.md`, `docs/contributing/architecture.md`,
    `react-agent.md`, `docs/features/subagents.md` (the system agent and the
    one widening), `docs/features/background-tasks.md` (a system task and the
    admission rule),
    `AGENTS.md` table row, screenshots of the drawer with a memory run under
    `docs/assets/memory/`, `make docs`, `make site-schema`, `make site-docs`.
11. Examples: the three memory harnesses keep their assertions (a note on
    disk, the recalled text in the reply); none of them reads
    `memory_trace.json`. Each gains one check of the run record where its
    surface exposes it: the HTTP harness reads the `memory` row from
    `GET .../background-tasks`, the ACP and console harnesses look for the
    child bundle under `<session>/subagents/`.

## 6. Delivery flow

BDD as `.claude/rules/workflow.md` asks. Executable specification first:

- `features/memory_subagent.feature`, harness `internal/agent/bdd_memory_test.go`
  (`-tags memory`; a real `session.Manager` over a temporary home, scripted
  providers for the parent and for the memory child, a recording parent
  client, the process-wide pool), scenarios:
  1. a user turn starts a memory run: the pool has an agent task named
     `memory` under the parent session, a child session bundle inside the
     parent's records the parent id and the name `memory`, and the child was
     offered the six memory tools and nothing else;
  2. the report of a run that finishes within the wait is in the parent's
     first system prompt (`Already on disk`), and the parent's client received
     `memory_run` started and finished with `delivered: true`;
  3. a run that finishes after the wait but before the second step is in the
     turn context of that step and not in the system prompt;
  4. an ask-mode turn offers the child the three recall tools only, and a save
     the child asks for is refused;
  5. a persist run writes the note under the global root and reports it;
  6. the parent's client receives no memory text and no `memory_message_chunk`
     (the child never writes to the parent's stream);
  7. a memory model that fails before answering falls back to the next model
     of the chain and the turn still gets its report, and a model that fails
     after streaming text does not;
  7a. the parent model's `background_list` does not show the memory run and
     `background_wait` on its id is refused;
  8. a Stop of the parent turn during the wait leaves the memory run running;
  9. a session's finished memory runs beyond `keep_runs` are removed, the
     newest kept.
- `features/memory_http.feature`, harness in `external/httpserver`
  (`-tags http,memory`): the task row of a memory run carries `agent.name`
  `memory` and `agent.system` true, the child transcript is readable, the
  messages payload has no `memoryTurns`.
- Unit tests next to the code: config defaults and validation; the label and
  the prompt cut; `FallbackChain` (first-output rule, unresolvable entry
  skipped, last-resort model); `Pool.Remove` and the admission of a system
  task past the per-session cap; the in-flight cap; the delivery log line in
  each of its three forms; the prompt template rendering (no skills, rules,
  catalog); print mode waiting for the run and the drain grace elsewhere; the
  console lines; the SPA badge and status.

Then: red on the narrowest scope, green, `make test`, docs and screenshots
(the drawer with a running memory run and a finished one, Dark 1280, from a
`coddy serve` on a stub provider), OpenAPI, schema, site checks, `make lint`.

## 7. Risks

- **Latency.** A turn now waits up to `wait_seconds` with nothing on screen
  but the status phrase. Same as today's synchronous pass in the common case,
  bounded where it was not, and the knob is documented.
- **One-step answers on a slow memory model** see no recall. Documented, and
  the operator can raise the wait or pin a faster model.
- **Tokens.** A memory run is a second model conversation per turn, as the
  copilot pass was: its own system prompt (the template, the environment
  block, six tool definitions) and its rounds. The order of cost is the same;
  the guide's cost section keeps saying so.
- **The wire break.** `memory_phase`, `memory_message_chunk` and
  `memoryTurns` go without a compatibility window. Their consumers are
  Coddy's own surfaces, all updated in this change; ACP editors ignore session
  updates they do not know. What an operator loses is the streamed recall text
  in the transcript: the phrase stays, the text moves to the drawer. The guide
  and the OpenAPI notes say so.
- **Drawer noise.** A memory run per turn would dominate the finished list
  without retention; `keep_runs` bounds it and the badge sets the rows apart.
- **Persist lost at exit** without the drain grace; the grace is part of the
  change, and print mode is covered by a test.
- **A child per turn costs a session bundle.** Small (a few KB), bounded by
  retention, removed with the parent.
- **The template path in `buildSystemPromptParts`** is a new branch in a
  function that is careful about the prefix cache; the branch renders once per
  turn like the mode template and adds nothing that moves.
- **A system task past the pool cap** is a new admission rule in
  `internal/bgtask`; it is keyed on one flag and covered by a unit test, and
  the in-flight cap keeps it from becoming an unbounded lane.
- **The widening** (a child with tools its parent lacks) is the one exception
  to the subagent invariant; it is confined to the memory launcher and stated
  in the subagents guide, and the review rule in `AGENTS.md` keeps forbidding
  it for definitions.

## 8. Review log

### Cursor agent (auto), plan iteration 1, 2026-09-18

Verdict: approve with changes. What changed in the text above:

- nil relay: verified against the code, `permissionRelay.Request` guards a
  nil receiver, so the design stands and the paragraph says why (raised as a
  blocker; not a defect);
- late delivery under a volatile `prompts.dir` template and across an
  automatic compaction: one turn-scoped store (`SetMemoryCopilotBlock`) for
  every path, the frozen build remembers what it rendered, and the turn context
  section appears only when the store holds something the frozen prompt does
  not (3.2, rewritten);
- `tools.background.enable` dropped from the refusal list: the pool never
  reads it and a `spawn_agent` background run launches without it;
- print mode keeps waiting for the run up to its timeout, now with a stderr
  line after 15 s, and the guide calls it intentional;
- the delivered text is pinned to the child's last assistant message, never
  the task log envelope;
- the model-facing pool tools hide system tasks and refuse their ids;
- the manager skips skills and rules for a system child, so the wait is not
  spent on I/O the prompt does not use;
- the fallback rule is stated as an intentional narrowing with the
  "streamed then failed" scenario;
- the in-flight cap is 2, skips are logged and sent, the skip reasons are
  distinct and name their cause;
- `delivered` is defined as a non-empty block reaching the model;
- retention refuses live or running children and says what an open
  transcript sees;
- the claim that a memory run is "exactly" a `spawn_agent` child is softened,
  and the wire break is a named risk.

### Coddy (neuraldeep/qwen3.8-27b, ask mode), plan iteration 1, 2026-09-18

Verdict: approve with changes. Twelve findings, three of them the same as
Cursor's (the nil relay, which the code guards; the report source; the
per-turn state of the late-delivery hook). What changed in the text above:

- the parent's per-turn `memoryRun` field is named, with its lifecycle, and
  the report is read from the run handle the launcher holds;
- a process-wide cap on memory runs (16) joins the per-session one (2);
- the `finish` of a system run releases no limiter slot and no arbiter;
- the wait deadline is taken before `Pool.Launch` (the child is created on
  the run goroutine, inside the wait), and the effective wait is the smaller
  of `wait_seconds` and the run timeout, stated in 3.5 instead of a warning;
- the live-status phrase is described as an existing phrase with a new
  source;
- the wire's disconnect limitation and the token cost are stated.

Declined: reserving the definition name `memory` (the drawer tells the two
apart by the flag, and the subagents guide documents the collision);
`Pool.Remove` returning anything but errors (`ErrTaskRunning`, `ErrNotFound`,
as implemented). Verified false: the nil relay panic (`Request` guards a nil
receiver), and the claim that `Pool.Launch` creates the child synchronously
(the callback starts the run goroutine and returns; creation happens there).

Declined: a compatibility window for the removed wire events (the consumers
are all in this repository and the operator asked for the old logic to go);
a config key for the in-flight cap (one constant, a knob when somebody needs
one); a child mode that follows the parent (the ask allowlist would leave the
child without tools; the hook payload carries the kind instead).

### Cursor agent (auto), implementation review, 2026-09-18

Verdict: approve with changes. Confirmed and fixed:

- **blocker** - a late report never reached a volatile `prompts.dir`
  template: delivery lived in the turn context section alone, and a volatile
  template gets no turn context. `buildSystemPromptParts` now delivers into
  the store before every render, so the per-step re-render and a rebuild
  after a compaction carry it through `{{.Memory}}`; the frozen-versus-store
  rule of the turn context is unchanged. Unit test
  `TestVolatileTemplateReceivesALateMemoryReport`;
- the "streamed then failed" fallback rule was tested on the provider only:
  scenario "A memory model that fails after streaming text does not jump
  models" runs it through the memory child;
- the print-mode wait had no test: `waitForMemoryRun` reads the agent's
  accessors through swappable variables and `TestWaitForMemoryRunWaitsOutTheRun`
  asserts the grace, the stderr line and the timeout;
- the pool tools hiding the run was a unit test only: scenario "The parent
  model does not see the memory run among its background tasks" joins the
  feature suite;
- the delivery line no longer promises a step index; the docs say
  `(turn context)`;
- the import grouping of `external/cli/print.go`;
- the ACP reference tells a client not to wait for `finished`.

### Coddy (neuraldeep/qwen3.6-35b-a3b, ask mode), implementation review, 2026-09-18

The first attempt on qwen3.8-27b stalled for 17 minutes without a byte and
was cut by the new stream guard; the 3.6 model answered in a few minutes.
Verdict: approve with changes, ten findings. Each was checked against the
code and none is a defect:

- "data race on `subagentRun.report`": `reportText` takes the run's own
  mutex, the one the run goroutine writes the report under; the report is
  written before the handle's `done` closes, which is before `Pool.Wait`
  returns;
- "double delivery": after a late delivery through the turn context, a
  rebuild of the system prompt carries the report and the section drops out
  on the next step because the store now equals `MemoryRecall`; the two never
  coincide in one request;
- "slot leak in `acquireMemorySlot`": the release is under `sync.Once`, and a
  delete of a missing key is a no-op;
- "`OutputSink.Write` races `Close`": both hold the sink's mutex for their
  whole body;
- "skip with an empty reason when there is no runtime": the call passes a
  reason;
- "report not visible after `Pool.Wait`": the ordering above;
- "retention could remove a run before its delivery": the pruned runs are the
  oldest beyond the kept tail, and the run of the turn in flight is always the
  newest of its session; a comment on `pruneMemoryRuns` now says so.

Taken: the comment; the note that a system child loads no skills and no
rules is covered by `TestRemoveRetiredChildRefusesLiveAndRemovesRetired` and
the prompt scenario.

## 9. Implementation notes (deviations from the text above)

- `finished` on the wire is sent from the turn's goroutine only (3.3): the
  first live stand crashed `coddy serve` when the watcher goroutine sent it
  after the HTTP response was gone. A run that outlives the turn sends nothing
  more; the drawer is the record.
- `Pool.Remove` answers `ErrTaskRunning` for a running task and `ErrNotFound`
  for an unknown id, and removes a record left by an earlier process too.
  `Manager.RemoveRetiredChild` refuses a live child or one with a running
  task with `ErrChildLive`, refuses a session that is not a child, and is a
  no-op for a bundle already gone.
- The sink of a task reopens its mirror file for a write after `Close`, so
  the delivery line lands in the persisted log too (3.2).
- `SubagentMeta` carries `Kind`, `PromptTemplate`, `MaxTokens` and
  `FallbackModels`; the memory child's tools are registered in `NewAgent`
  through a hook of the memory build tag, and the fallback chain is applied
  in `getProvider` for any child whose meta names fallback models.
- The child's tool announcement in the task log is deduplicated by call id:
  a provider that streams a call's name before its arguments announced the
  same call twice, for every subagent, not only the memory one.
- The hook payload's `subagent` block carries `kind` for a system child.
- `wait_seconds` and `keep_runs` are pointers in the config struct, the
  repository's convention for an explicit zero that means something; the
  schema types them `[integer, null]`.
- The SPA keeps an invisible `memory_run` transcript item per turn (nothing
  renders it) as the source of the `Working with memory` status, instead of
  a per-session flag: the live status derivation is a pure function over the
  transcript, and the item follows the turn the way every other row does.

## 10. Issues folded into the pull request

- **#235** (the ACP harness): the turn asked the assistant to invent a fact in its reply, but the memory subagent works from the user message alone; the harness now asks to remember a fact stated in the message, the codeword included, and asks for it again in a fresh session of the same process, where the answer can only come from the note on disk. `examples/config.demo.yaml` waits 90 s for the report.
- **#221** (a hang on an exhausted memory account): the design already bounds the turn (the wait) and the run (the task timeout), moves the chain on a call that failed before output, and keeps the child out of `agent.wait_for_limit_reset` (a child fails fast). What was missing was the diagnostic: `subagentHandle.Wait` answered `(exit, nil)`, so a failed run's task record had no error and the `finished` update no `reason`. The handle now hands a failed run's error to the pool; the console line, the drawer row and `memory_run` carry it. Scenario: *A memory model whose account is exhausted fails the run inside the wait and the turn goes on*.
- **#266** (an operator addendum with a cap): `memory.additional_prompt` and `memory.additional_prompt_max_chars`. The text travels as the child's `Role` and the template renders it under **Operator instructions**, after the memory role and before the tool list; a templated child gets its role text raw in `{{.SubagentRole}}` because the template frames the child itself. The cap counts characters; a cut is logged at every launch that reads it and reported by `coddy -t`, so it is never silent. Scenario: *The operator's additional prompt reaches the memory child and stays out of the parent's prompt*; the cap has a unit test in `internal/config`.

## 11. The report left the system message (2026-09-18)

Section 3.2 put a report that was in by the end of the wait into the `{{.Memory}}` slot of the system prompt and only a late one into the turn context. That cost the provider's prompt cache on every turn: a recall differs for nearly every message, so `messages[0]` was a new one each turn and the whole conversation behind it was read at full price, the defect issue #253 had fixed for the clock and the checklist. A live four-turn run against `rpa/qwen3.6-35b-a3b` with memory on showed `cached_input_tokens` at 0 on every turn; with the report after the history it is 87 to 90 percent from the second turn on.

The rule now: the report never enters the system message. It rides in the `<turn_context>` block on every step of the turn that has one, on the first request when the run settled inside the wait and from a later step otherwise. `systemPromptBuild.MemoryRecall` and the frozen-versus-store comparison are gone; `{{.Memory}}` holds the session notes alone. A volatile template under `prompts.dir` gets a block that carries the memory section alone. The task log says `report delivered to the turn (first request)` or `(a later step)`. The store is per turn (`ClearMemoryCopilotBlock` when a turn starts), which a scenario now pins.

Scenarios: the system message does not move between turns that recalled different things; every step of a turn carries the report after the history; a turn whose run delivers nothing does not inherit the previous turn's report.

The same live run showed the memory child reading the user's "do not use tools" as addressed to itself and skipping recall. The template's opt-out now takes only an explicit request not to consult the notes; what the user says about tools in general is for the main assistant.
