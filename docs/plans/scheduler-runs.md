# Scheduler runs as background subagent tasks

Design record, 2026-09-18. The scheduler keeps its job files, its cron grid and
its REST and tool surface for jobs; what changes is how a job **runs** and how
its runs are **kept and shown**. A run becomes the same thing a `spawn_agent`
call starts: a task of kind `agent` in the background task pool, backed by a
child session, with the Tasks panel as its history. The previous execution
path (`daemon.RunJobFile`, the `.lock` sidecar, the in-process run tracker, the
stale-lock sweeps, the per-job session pruning over the whole sessions root) is
removed rather than adapted.

## 1. Why

The old path built a session outside the session manager: `RunJobFile` made a
`session.State` by hand, ran `agent.RunScheduledTurn` on it with a sender that
allowed every permission and dropped every update, and saved the bundle three
times along the way. Nothing owned that state after the run:

- every run registered its bundle in the process-wide task pool
  (`Agent.backgroundPool` calls `Pool.SetSessionDir` for the run's session id)
  and nothing ever released it, so the pool's session index grew by one entry
  per run for the life of the daemon;
- a `run_command` the run backgrounded stayed in the pool's task map as a
  finished task with its output window (`tools.background.output_buffer_bytes`,
  256 KiB by default) for ever, because the only thing that drops finished
  tasks is the operator's Clear on a session nobody opens;
- the state carried no context-window manager (`contextWindows` was nil), so
  a long run never compacted and its transcript grew until the timeout;
- there was no retirement path: MCP clients were never dialed, but nothing
  closed anything either, and the `.lock` / tracker / dedupe bookkeeping had to
  reconstruct "is this job running" from three sources that could disagree.

The subagent runtime already has all of that: a child session built and
registered by the manager, one turn through the normal prompt path (turn lock,
activity edges, cross-process cancel, compaction, persistence), a pool task
with a hard timeout and a Stop that cancels the child, retirement that stops
the child's own work and closes its clients, and a drawer that shows the
progress log and opens the transcript. Scheduled runs move onto it.

## 2. What the operator gets

- **A job has a run history.** In the scheduler drawer every job row shows its
  last run (status dot and time) and opens **Runs**: the background tasks
  panel of that job, docked where the job editor docks. Running runs are cards
  with a Stop control, finished runs are one line each, a run opens its
  progress log, and **Open transcript** opens the run's read-only session.
  Route: `#/scheduler/jobs/<job_id>/runs` and `#/scheduler/jobs/<job_id>/runs/<task_id>`.
- **Runs are bounded.** `scheduler.retain_sessions` keeps the newest N finished
  runs of a job (their task records and their transcripts); older ones are
  removed when a run finishes. Clear in the panel drops every finished run of
  the job.
- **Runs are stoppable and honest.** Cancel stops the pool task; the run is
  recorded as `stopped`, a run that hit `scheduler.timeout` as `timed_out`, a
  run whose turn failed as `failed` with the error on the row.
- **A job may name a subagent definition.** Frontmatter `agent: <name>` runs
  the job under that definition's role, tool allowlist, model and permission
  narrowing (`subagents.dirs`, project trust included). Without it the run is a
  general agent with the full tool set of its mode.
- **Unattended by design.** Frontmatter `permission_mode` (`ask`,
  `accept_edits`, `bypass`) narrows what the run may do without asking. The
  default stays what the old scheduler did, `bypass`, because a job file is
  the operator's own instruction and nobody is there to answer a prompt; under
  `ask` or `accept_edits` a gated call is denied, never waited on.
- **Runs have MCP.** Configured MCP servers are dialed for the run's cwd
  through the workspace trust gate, exactly as for a new session, so a job can
  use the operator's servers. The old path never dialed any.

## 3. Approach

### 3.1 The job session

Every job owns one ordinary session, the **job session**. It is minted on the
job's first run (`session.NewSessionID()`, a plain `sess_` id) and recorded in
the job's `.state` sidecar as `session_id`, next to the cron checkpoint; the
rename path already moves `.state` with the job, so the history follows a
rename. `[rev1]` The sidecar becomes one record with two fields, and both
writers preserve the other's field: `WriteJobState` (the cron checkpoint) reads
the file first and keeps `session_id`, `WriteJobSessionID` keeps
`last_scheduled_utc`; today's checkpoint write replaces the whole file and
would erase the pointer on the first tick after the run that minted it. A
pointer that names a bundle no longer on disk is kept and the bundle is
recreated under the same id, so deleting the job session is the same as
clearing the history. `[rev2]` A sidecar with no pointer (a `.state` deleted
by hand, or one written by the old scheduler) does not orphan an existing
history: before minting an id the daemon looks for a bundle in the sessions
root whose `session.json` says `schedulerRun` with this `schedulerJobId` and
re-attaches it, so the only way to lose a job's runs is to delete them. Its
`session.json` carries `schedulerRun: true` and
`schedulerJobId` (the two fields kept from the old metadata;
`schedulerStartedAt`, `schedulerEndedAt` and `schedulerStopStatus` go, the task
record has them), its title is the job id, its cwd the job's resolved cwd.

The job session never runs a turn of its own. It is the **parent** every run is
a child of: run bundles live at `<sessions root>/<job session>/subagents/<run>/`
and run task records at `<job session>/background/<task>/`, which is the layout
the Tasks panel, `GET /coddy/sessions/{id}/background-tasks` and
`DELETE /coddy/sessions/{id}` (tree deletion) already understand. It stays
hidden from History and every default listing through the existing
`schedulerRun` exclusion (`include_scheduler=true` lists it), and a prompt
against it is refused the way a child session refuses one. `[rev1]` The
refusal lives in the **manager**, not only at the HTTP edge: the read-only
predicate the manager already applies to children on every path that starts a
turn (`HandleSessionPromptWithSender`, `BeginTurn`, the permission resume, the
mode and config-option handlers, the run-plan path) becomes
`State.IsReadOnlyTranscript()`, true for a child and for a job session, with
`ErrSchedulerSessionReadOnly` naming the job so the HTTP mapping (`409`) and the
console can say which job it is. `rejectSubagentTurn` in `external/httpserver`
follows the same predicate, so no surface (ACP `session/prompt`, the wake turn,
a load followed by a prompt) can start a turn on a job session.

Tree deletion is not refused while a run is in flight: `DeleteSessionTree`
cancels the run's turn, stops its task through the pool (the run bundle carries
`parentSessionId` and `subagentTaskId`, which is how the tree walk finds the
task under the job session) and removes the bundles, and the run's own `finish`
is what the pool's Stop reaches, so the `max_queue` slot and the runtime's
running-jobs entry are released on that path exactly as on every other one. A
run whose creation lands on a parent under deletion is recorded as a `failed`
task, released the same way. Deleting the job session through the sessions API
is therefore the same as clearing the job's history; the daemon recreates the
bundle under the same id on the next run. Deleting the job **file** keeps its
old rule: `409` while a run is in flight, and the job session tree goes with
the file otherwise.

The daemon loads or creates the job session right before a run
(`Manager.EnsureSchedulerJobSession(ctx, jobID, cwd)`: the live entry when one
exists, the bundle when one is on disk, a fresh bundle otherwise, with no MCP
dial and no `SessionStart` hooks because nothing will talk to it) and retires it
(`ForgetLiveSession`) when the job's last run finishes, so the daemon holds no
live state between runs. An HTTP read that loaded the session meanwhile keeps
working: the panel reads task rows from the pool and the bundle, not from the
live entry.

### 3.2 A run

A run is `Pool.Launch` under the job session with `Kind: agent`,
`Agent: {name: <job_id>, session_id: <run session>}` (`[rev1]` the name is
the definition name when the job sets `agent:`, the job id otherwise; the
panel docked in the scheduler labels that line "Job" instead of "Subagent"),
`TimeoutSeconds` from `scheduler.timeout` (the pool still caps it at
`tools.background.max_timeout_seconds`, like any task, and the daemon warns at
start when the scheduler limit is above the cap), `CWD` the job's cwd and a
label that names the job and says how the run started:
`nightly-report · cron 2026-09-18 10:00 UTC` or
`nightly-report · manual 2026-09-18 10:07 UTC` (capped at the pool's 60 runes).
`[rev1]` Before the launch the daemon calls `Pool.SetSessionDir(job session,
its bundle dir)` and `Pool.SetConfig` from `tools.background`, exactly what
`Agent.backgroundPool` does for a chat; without the directory the pool
persists nothing and the history would vanish at the next restart. Inside the
launch callback the run session is created with
`Manager.CreateSubagentSession` and the job body is its one prompt, through
`Manager.RunSubagentTurn`, with a sender that writes the child's progress to
the task's output sink (the `→ tool` / `✓ tool` / `[assistant]` lines the
subagent runtime already renders) and ends with the same
`=== subagent report ===` block. Every exit path (create failure, timeout, stop,
panic, completion) goes through one `finish` that stops the run's own tasks,
retires the run session, drops the job from the runtime's running map and
releases the `max_queue` slot. `[rev2]` **The run session's own pool entries
go with it.** Work the run started (a backgrounded `run_command`, a
grandchild) is registered under the run session; `finish` stops what still
runs and then releases the run session from the pool through a new
`Pool.ReleaseSession(sessionID)`: its finished tasks leave the pool's memory
and its `sessionDirs` entry goes, while the records stay in the run bundle
and are read back from there like the records of any earlier process. The
same call is added to the `spawn_agent` finish, which had the same one-level
growth. Between runs the daemon releases the job session the same way, so a
daemon that ran for a month holds no task of a finished run in memory; the
records the panel shows come from the bundles.

`[rev1]` **The `max_queue` slot and the running mark are taken before
`Pool.Launch` and released only by `finish`.** `RunScheduledJob` returns as
soon as the task is registered, like a background spawn, so a tick that held
the slot with a `defer` around that call would release it while the run is
still going and the cap would bound nothing. The daemon reserves the job
(running mark plus slot) under one mutex, so a cron tick and a manual run
cannot both start the same job, and a `Launch` refusal or a creation failure
releases the reservation through the same `finish`. Manual runs take a slot
too: a manual run while the cap is saturated is refused with `409` naming
`scheduler.max_queue`, where the old path started it outside the cap.

`[rev1]` **Project trust for `agent:`.** A job that names a definition resolves
it for the job's cwd with the same loader `spawn_agent` uses and decides trust
on that value (`subagents.Decide` against `subagents.project_trust` and the
receipt store) **before** anything is reserved or launched. A project-scope
definition without a receipt does not run: the run is not started, the tick
logs the refusal with the approval hint, and a manual run answers `409` with
that hint. User-scope files and built-ins need no receipt, as everywhere else.

The spec differs from a `spawn_agent` child in five places, and each is a field
on `session.SubagentSpec` / `session.SubagentMeta` rather than a second kind of
session:

- **Origin.** `Scheduler: &SchedulerRunMeta{JobID, Trigger, FireSlot}` marks the
  run in memory and in `session.json` (`schedulerJobId` on the child bundle too),
  so the read-only notice in the SPA, the hooks payload and the run rows can say
  "scheduled run of job X" instead of "spawned by a parent". The system prompt
  preamble for such a run says it is a scheduled job started unattended, that
  nobody answers questions, and that its final message is kept as the run's
  record; the definition's role body, when there is one, follows.
- **Depth 0.** The run is a depth-0 run (`[rev2]` structurally a child of the
  job session, by spawn depth a root): it may itself spawn subagents within
  `subagents.max_depth` (its children sit at depth 1), which the old path could
  never do. The mandatory exclusions still apply
  (`question`, the `config_*` family, `plan_exit`), and `spawn_agent` is
  withheld at the depth limit as for any session. `[rev1]` Its children count
  against `subagents.max_concurrent` like any spawn, while the run itself is
  bounded by `scheduler.max_queue` only; `[rev2]` a job that fans out many
  children therefore competes with interactive spawns for that cap through its
  children, which is the operator's knob to set, not a reason to give
  scheduled runs a cap of their own. `[rev1]` Nothing wakes a run: the
  `notify_on_finish` of a backgrounded `run_command` and of a background spawn
  is decided by a new `tooling.Env.WakeableSession` (true only for a session
  without child metadata) instead of `SubagentDepth == 0`, so a run never
  registers a wake that the read-only guard would only refuse.
- **Tool set resolved after the MCP dial.** A `spawn_agent` child inherits the
  parent's names, MCP names included, before it is created. A scheduled run has
  no parent to copy from, so `SubagentSpec.ResolveTools func(mcpTools []string)
  []string` is called by the manager once the run's MCP clients are up and the
  names are known: without a definition it returns every registry name of the
  run's mode plus every MCP name, minus the exclusions; with `agent: <name>` it
  returns `subagents.EffectiveTools(all names, mode set, definition, exclusions)`.
  `SubagentSpec.Tools` keeps its meaning for callers that know the set up front.
  `[rev1]` This is a change to `CreateSubagentSession`'s order, made explicit:
  publish the live entry with the child metadata (tool set still empty, the run
  has not started), lay out the bundle, dial MCP when `ConnectMCP` is set,
  call the resolver with the dialed tool names, store the result on the child
  metadata (`State.SetSubagentTools`), then the first save. `[rev2]` The
  decision stays the runtime's: the resolver is the runtime's own function,
  the manager only calls it at the moment the names exist. `ConnectMCP` is
  decided by the runtime the way a spawn decides it, from the tool policy: a
  run without a definition dials the servers the manager's trust gate admits
  for the job's cwd; a run under a definition dials them only when the
  definition's allowlist can admit a tool of one of them (`Allows` probed with
  each configured server's name), so a job under a read-only definition never
  starts an MCP process. The turn starts
  only after the call returns, so the empty set is never what the model sees; a
  transcript read in that window sees a child with no tools, which is what a
  restored child shows anyway. A resolver that returns nothing fails the
  creation the way an empty allowlist fails a spawn.
- **Permission mode from the job.** `permission_mode` in the frontmatter,
  default `bypass`; with `agent:` the definition narrows it further
  (`subagents.NarrowPermissionMode`). The run's sender denies every forwarded
  permission request and refuses questions, which the gate only reaches under
  `ask` / `accept_edits`.
- **No limiter slot, no parent hooks.** `subagents.max_concurrent` counts
  children a model spawned; scheduled runs are bounded by `scheduler.max_queue`
  instead, a semaphore the daemon holds for the run's lifetime (the tick skips
  a due job while it is saturated and says so, as now). `SubagentStart` /
  `SubagentStop` fire in a spawning parent's turn; there is none here. The run's
  own hooks fire inside its turn as in any session, with the `subagent` block
  naming the job and the job session.

The runner lives in `internal/agent/scheduled_run.go` (replacing
`scheduled.go`): `RunScheduledJob(ctx, cfg, rt SubagentRuntime, pool, log,
ScheduledRunSpec) (bgtask.Snapshot, error)`, built on the pieces
`spawnSubagentInMode` uses (`subagentHandle`, `subagentSender`,
`formatSubagentReport`, the run bookkeeping) refactored into a shared child-run
executor so the two entry points cannot drift. The log lines the e2e harnesses
wait on keep their names: `scheduler_run_spawn` and `scheduler_run_finish`,
with `job_id`, `session_id` (the run session), `task_id` and `status`.

### 3.3 The daemon

`external/scheduler` keeps the UTC minute tick and the `.state` checkpoint
(`storage.CronJobEligibleForMinute`, the atomic `.state` write before the run
starts, vixie timing for a job with no checkpoint), and drops the rest of the
run bookkeeping:

- no `.lock` sidecar, no stale-lock grace, no lock fire slot, no orphan-lock
  removal on cancel;
- no in-memory spawn dedupe and no `lastSeenScheduleByPath`: the checkpoint is
  written synchronously before `Pool.Launch` returns, and the next tick is a
  minute away;
- no run tracker keyed by job path: a `Runtime` (`external/scheduler/runtime.go`,
  built by `Serve`, reachable process-wide the way `bgtask.Default()` is) holds
  the manager, the pool, the live config accessor, the `max_queue` semaphore
  and the map of running jobs (job path to task and run session ids). "Is this
  job running" has one answer.

`scheduler.Options` gains `Mgr *session.Manager` and `Pool *bgtask.Pool`;
`cmd/coddy/serve.go` passes `rt.Mgr`, and `coddy acp -scheduler` and the console
start the daemon after their manager exists instead of before. A process whose
configuration enables the scheduler but has no manager (a bare swarm relay)
never enables it today and does not start to.

Two daemons on one `scheduler.dir` could double-fire before this change and
still can; the `.lock` never prevented it across processes and is not replaced
by anything that would.

### 3.4 Retention

`scheduler.retain_sessions` keeps that many **finished** runs per job (whatever
their status: the old rule kept terminal runs too), newest by start time
(`[rev1]` the old sweep ordered by end time; a run that started later but
ended earlier now survives its neighbour, which is what "the last N runs"
means to a person reading the panel). The knob's floor stays 1: `ApplyDefaults`
turns 0 into the default, as before. After a run finishes the daemon lists the
job session's tasks (pool and bundle), and for every finished run past the
limit removes the run bundle (`<job session>/subagents/<run>`), the task record
(`<job session>/background/<task>`) and the pool's task record, in memory and on disk, through a new
`Pool.Forget(sessionID, taskID)` (finished tasks only, otherwise
`ErrTaskRunning`; `[rev2]` the pool owns the record as `ClearFinished` does,
the run bundle is the sweep's own business). Clearing the history
(`DELETE /coddy/scheduler/jobs/{job_id}/runs`, the panel's Clear) is the same
sweep with a limit of zero. `[rev1]` The panel's Clear must go through that
route and not through `DELETE .../background-tasks`: the pool's `ClearFinished`
drops task records only and would leave the run transcripts orphaned inside
the job session. Deleting a job deletes its job session tree, so its history
goes with it; the old scheduler left run bundles behind.

### 3.5 REST and tools

| Method | Path | Change |
|---|---|---|
| GET | `/coddy/scheduler/jobs` | rows gain `session_id` (the job session, empty until the first run), `last_run` `{task_id, session_id, status, started_at, finished_at}` (`[rev2]` `session_id` there is the **run** session, the transcript, as in the `runs` rows), and the two new frontmatter fields `agent` and `permission_mode` (`[rev1]` on the create, replace and patch bodies too, and in the job editor of the SPA); `running` comes from the runtime; the envelope keeps `runs_active`. |
| POST | `.../{job_id}/run` | `202` with `task_id`, `session_id` (job session) and `run_session_id` (`[rev1]` both ids exist before the answer: the run session id is minted before `Pool.Launch`, and `Launch` returns the registered task); `409` while the job is running, paused, refused by project trust or over `scheduler.max_queue`; does not advance `.state`. |
| POST | `.../{job_id}/cancel` | stops the running task through the pool; `{cancelled: bool}`; no lock cleanup any more. |
| GET | `.../{job_id}/runs` | rows are the job's runs newest first: `task_id`, `session_id` (the **run** session, what a transcript read wants), `job_session_id`, `trigger` (`cron` / `manual`), `status`, `started_at`, `ended_at`, `elapsed_seconds`, `error`, `label`; `limit` as before. |
| DELETE | `.../{job_id}/runs` | new: clears finished runs, `{cleared: n}`. |
| GET | `/coddy/sessions/{id}/messages` | the `subagent` block of a run session gains `scheduler: {jobId, trigger}` so the SPA can route back to the job's runs; `readOnly` stays true. A job session answers `409` to a prompt. |
| GET | `/coddy/sessions/{id}/background-tasks` | unchanged; this is what the runs panel polls, with `{id}` the job session. |

`coddy_scheduler_job_run`, `coddy_scheduler_job_cancel` and
`coddy_scheduler_job_runs` keep their names and take the new shapes; their
descriptions stop talking about locks. The OpenAPI fragment in
`scheduler_http.go` follows. `[rev1]` The status vocabulary of a run changes
with the shapes: the pool's `succeeded` / `failed` / `timed_out` / `stopped` /
`orphaned` replace the old `completed` / `failed` / `cancelled` (a timeout used
to be reported as `failed`, a cancel as `cancelled`). A client of the old
`/runs` rows has to follow; the SPA's scheduler tool card renders the status
string as it comes. Panel Stop (`POST .../background-tasks/{task}/stop` on the
job session) and `POST .../cancel` are the same `Pool.Stop`, and both end in
the run's `finish`, which is the only writer of the runtime's running map.

### 3.6 SPA

- `SchedulerJob` gains `session_id` and `last_run`; the row shows the last run
  as a status dot with the local time and a **Runs** control; the job editor
  footer gets the same control.
- `#/scheduler/jobs/<job_id>/runs[/<task_id>]` docks `BackgroundTasksPanel`
  (unchanged component) in the scheduler cluster in place of the editor, fed
  by `listBackgroundTasks(job.session_id)` / `getBackgroundTask` on the same
  cadence as the chat panel; Stop goes through `stopBackgroundTask`, Clear
  through the scheduler's `DELETE .../runs`, Open transcript to `#/s/<run>`.
  A job with no `session_id` yet shows the empty state.
- `SubagentReadOnlyNotice` reads the `scheduler` block and says "scheduled run
  of job X" with a link to `#/scheduler/jobs/X/runs/<task>` instead of "open
  parent chat".
- `hashRoute.ts` gains the runs routes; `en` and `ru` dictionaries gain the
  copy; screenshots of the drawer, the panel (running and finished) and the
  transcript go to `docs/assets/scheduler/`.

### 3.7 Out of scope

Resuming or messaging a run, a per-job concurrency above one (a job never runs
twice at once), cross-process locking of `scheduler.dir`, a live SSE relay for
a run (the panel polls, like the chat panel), and per-job retention overrides.

## 4. Alternatives considered

- **Runs as top-level sessions with a task record inside each.** Keeps the
  old bundle layout but gives the history no single place: listing a job's runs
  means scanning the whole sessions root as `ListJobRuns` did, and the drawer
  would need a new list component instead of the Tasks panel. The job session
  gives every run one parent and reuses tree deletion, task persistence and the
  panel as they are.
- **A deterministic job session id (`sched_<job id>`).** No pointer to store,
  but a job id may contain characters a session folder refuses, and a rename
  would have to move the bundle and rewrite every `parentSessionId` inside it.
  The `.state` pointer is one field the rename path already carries.
- **Counting scheduled runs against `subagents.max_concurrent`.** They are
  child LLM loops, but the knob is documented as the cap on what a model
  delegates, and a nightly batch of jobs would silently starve interactive
  spawns. `scheduler.max_queue` keeps its meaning.
- **Keeping the `.lock` for crash recovery.** A lock file whose owner died is
  exactly what the stale-lock grace existed to clean up; with the pool as the
  only source of "running" there is nothing to recover, and a run that was in
  flight when the process died is listed as `orphaned` by the panel like any
  task.

## 5. Files

Go: `external/scheduler/{serve.go, serve_scheduler.go, serve_stub.go, start.go,
start_stub.go, runtime.go}`, `external/scheduler/daemon/{start.go, tick.go,
run.go}` (delete `hook.go`, `spawn_dedupe.go`),
`external/scheduler/service/{service.go, types.go, errors.go, runs.go,
manual_run.go, retain.go, jobs_read.go, jobs_write.go}` (delete `tracker.go`,
`stale_locks.go`, `prune.go`), `external/scheduler/storage/storage.go`,
`external/scheduler/tools/{job_run.go, job_cancel.go, job_runs.go}`,
`internal/agent/{scheduled_run.go, subagent.go, system_prompt.go}` (delete
`scheduled.go`), `internal/session/{subagent.go, scheduler_session.go, state.go,
filesystem.go, manager.go}`, `internal/bgtask/pool.go`,
`external/httpserver/{scheduler_http.go, coddy_coddy.go, subagents_http.go,
openapi.go}`, `cmd/coddy/{serve.go, main.go}`, `external/cli/run.go`,
`internal/config/{config.schema.json, ui_schema.go}`.

SPA: `external/ui/src/ui/scheduler/{types.ts, api.ts, hashRoute.ts,
SchedulerJobsDrawer.tsx, SchedulerJobEditorSheet.tsx}`,
`external/ui/src/ui/chat/{SubagentReadOnlyNotice.tsx, subagentTranscript.ts}`,
`external/ui/src/ui/App.tsx`, `external/ui/src/ui/i18n/messages/{en,ru}.ts`,
`external/ui/src/styles.css`.

Specs and tests: `features/scheduler_runs.feature`
(`external/scheduler/bdd_scheduler_test.go`, tag `scheduler`, a real manager
with scripted providers), `features/scheduler_runs_http.feature`
(`external/httpserver/bdd_scheduler_test.go`, tags `http,scheduler`); unit
tests for the `.state` pointer and rename, retention, `Pool.Forget`, the tick
skipping a running job, the read-only job session, tool resolution; vitest for
the routes, the panel wiring and the notice.

Docs: `docs/operate/scheduler.md`, `docs/reference/http-api.md`,
`docs/reference/tools.md`, `docs/features/{sessions,subagents,background-tasks}.md`,
`DESIGN.md`, `internal/skills/bundled/configure-coddy/SKILL.md`, the schema
descriptions and `make docs`, `examples/*scheduler*` harnesses, this record.

## 6. Addressed concerns

### Iteration 1 (Cursor `auto`, 2026-09-18)

Verified against the code and folded into the text above, marked `[rev1]`:

- the cron checkpoint write replaced the whole `.state` file and would have
  erased `session_id` on the first tick (3.1);
- the job session's read-only rule is enforced in the manager on every
  turn-starting path, not only in the HTTP handlers (3.1);
- tree deletion of the job session during a run reaches the run through the
  pool's Stop and the run's `finish`, which is also what releases the
  `max_queue` slot and the running mark (3.1);
- the tool resolver is a change to `CreateSubagentSession`'s order, spelled out
  step by step (3.2);
- the `max_queue` slot is reserved before `Pool.Launch` and released only by
  `finish`, never by the return of the launching call; manual runs take a slot
  (3.2);
- project trust for `agent:` is decided before anything is reserved (3.2);
- `Pool.SetSessionDir` for the job session before the launch, or nothing is
  persisted (3.2);
- a run never registers a wake: `tooling.Env.WakeableSession` replaces the
  depth test (3.2);
- the task's `agent.name` is the definition name when there is one (3.2);
- Clear must remove transcripts too, so it is the scheduler's own route;
  retention orders by start time on purpose (3.4);
- `POST .../run` can answer with both ids because both exist before the
  answer; the status vocabulary change is called out; `agent` and
  `permission_mode` reach the REST bodies and the editor (3.5).

Answered, no change: a run's own backgrounded commands are registered under
the **run** session, so the job session never holds more than the one run
task and `tools.background.max_concurrent` cannot block the next run; legacy
`sched_*` bundles are hidden and not served by `GET .../runs`; the manager's
own trust gate decides which configured servers a run dials.

### Iteration 2 (Coddy on `neuraldeep/qwen3.8-27b`, 2026-09-18)

Verified and folded in, marked `[rev2]`:

- a run's own pool entries were the same growth one level down: `finish`
  releases the run session from the pool (`Pool.ReleaseSession`), the spawn
  path gets the same call, and the daemon releases the job session between
  runs (3.2);
- `ConnectMCP` follows the tool policy instead of being always on, so a job
  under a read-only definition starts no MCP process; the resolver is the
  runtime's decision executed by the manager when the names exist (3.2);
- a job whose `.state` lost its pointer re-attaches its history from the
  bundles instead of minting a second job session (3.1);
- `last_run.session_id` is the run session, like the `runs` rows (3.5);
  `Pool.Forget` owns the task record in memory and on disk, the sweep owns the
  bundle (3.4); "top-level agent" became "depth-0 run" (3.2);
- a run's children compete for `subagents.max_concurrent` with interactive
  spawns; noted as the operator's knob rather than capped again (3.2).

Already covered by iteration 1 and confirmed: the run session id is minted
before `Pool.Launch`, so the `202` of a manual run is honest (3.5); the running
mark is claimed under the runtime mutex before `Pool.Launch`, which is what
keeps a manual run and a tick from starting the same job twice without the
`.lock` (3.2). Answered, no change: the runtime's running map starts empty at
daemon start and a run a dead process left in flight shows as `orphaned` in
the panel like any task, the `.state` checkpoint keeping the slot from firing
again; `schedulerRun: true` marks the job session only, a run bundle carries
`subagentRun` with `schedulerJobId`, so the two are told apart by the flags,
not by the layout; the child-creation rollback removes the parent's folder
only when it is empty, and a job session's bundle holds its `session.json`
before any run is launched.

## 7. Risks

- **Behaviour change under `permission_mode`.** The default keeps the old
  auto-allow; a job that sets `ask` gets denials, not prompts. Documented on the
  scheduler page next to the field.
- **Timeout cap.** `scheduler.timeout` above `tools.background.max_timeout_seconds`
  is capped by the pool. The daemon logs it at start and the page says so.
- **A run that spawns.** Depth 0 lets a job delegate; `subagents.max_depth: 0`
  forbids it everywhere as before.
- **Legacy bundles.** Run bundles of the old scheduler (`sched_` ids) stay on
  disk, hidden by the existing prefix rule, and are never pruned; the page tells
  the operator they can be deleted by hand.
