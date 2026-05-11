## Scheduler

### Overview

The scheduler is an optional cron-like runner. It scans a single job directory for flat `*.md` files (YAML frontmatter plus markdown body) and executes each job when due. It is compiled in only with the **`scheduler`** build tag.

Pieces:

- **`external/scheduler`** - thin **`Start`** entry wired into **`cmd/coddy`**.
- **`external/scheduler/daemon`** - poll loop, **`RunJobFile`**, and **`LaunchManualJob`** hook registration.
- **`external/scheduler/storage`** - flat job discovery, YAML frontmatter, UTC cron, `.state` / `.lock` paths.
- **`external/scheduler/service`** (**`schedservice`**) - shared CRUD, run tracker, pruning of old run sessions, HTTP and tool payloads (no cycles with **`internal/tools`**).
- **`external/scheduler/tools`** - **`schedtools`** registers **`coddy_scheduler_*`** tools (one `*.go` file per tool under **`tools/`**) when **`scheduler.enabled`** is true.

The cron parser uses **five fields** (**minute hour day month weekday**) in **UTC**. Fires are evaluated on **UTC minute boundaries** (second **0**, nanoseconds **0**), like **crond**: the daemon wakes once per UTC minute, scans **`scheduler.dir`**, and starts a job only when that minute matches the expression and the **`.state`** checkpoint is strictly before that minute. **`* * * * *`** therefore runs **at most once per UTC minute**. Step fields such as **`*/2 * * * *`** use the same minute grid as vixie cron (minutes **0,2,4,…** UTC); **`*/3 * * * *`** uses **0,3,6,…** UTC.

Changes to the **`schedule`** field in a job file are read on the next directory scan (no restart). When the schedule **string** changes, in-memory duplicate-launch bookkeeping for that job is cleared so the new expression is not skewed by the old cron stepping.

### Build

- Scheduler only - `go build -tags=scheduler ./cmd/coddy`
- HTTP and scheduler - `go build -tags=http,scheduler ./cmd/coddy` (add `,ui` with `http` for the embedded SPA)

### Enabling

The scheduler daemon and tools are active when **`scheduler.enabled: true`** in config, or when you pass **`coddy acp -scheduler-enabled`** or **`coddy http -scheduler-enabled`**.

REST routes under **`/coddy/scheduler`** require **`-tags=http,scheduler`**; see **`docs/http-api.md`**.

### Job directory

Jobs are **`*.md`** files **directly** under **`scheduler.dir`**. Nested subdirectories are not used for discovery.

When **`scheduler.dir`** is empty, it defaults to **`${CODDY_HOME}/scheduler`**.

Sidecars next to **`basename.md`**:

- **`basename.lock`** while a run holds the exclusive lock. The first line is the committed cron fire instant in UTC (RFC3339), same value as the checkpoint written to **`.state`**, so poll ticks that fire before the atomic **`.state`** rename still skip a duplicate launch for that slot. (API **`running`** follows the in-process run tracker, not the lock file alone; stale locks are cleaned after a timeout-based grace window.)
- **`basename.state`** cron checkpoint (**`last_scheduled_utc`**)

The daemon writes **`.state`** as soon as a cron run is committed (after the scheduler session is first persisted on disk), using the **UTC minute start** that fired, so the next UTC minute tick does not re-trigger the same minute while the agent turn is still running. Checkpoints are written **atomically** (temp file plus rename in the same directory) so ticks never observe a half-written JSON file as a missing checkpoint. On Unix the final rename **replaces** an existing file in one step (no interval where the path is absent). On Windows the implementation removes the previous file first because **`os.Rename`** cannot replace an existing destination there. With no checkpoint yet (or a stale pre-1980 timestamp left by older builds), the first run follows **vixie-style** timing from wall clock and the five-field expression, not a backlog from the Unix epoch. Only **one** long-lived process should enable the scheduler against a given **`scheduler.dir`** - two daemons on the same directory can still double-fire regardless of checkpointing.

Optional YAML frontmatter **`paused: true`** skips both cron ticks and **`POST …/run`** until resumed.

### Job file format

Frontmatter fields:

- **`description`** (string) - short human summary
- **`schedule`** (string) - five-field crontab, UTC
- **`cwd`** (string, optional) - empty means the Coddy process cwd; relative paths resolve against process cwd at run time
- **`model`** (string, optional) - session model override for the run
- **`mode`** (string, optional) - **`agent`** or **`plan`** (default **`agent`**)
- **`paused`** (bool, optional) - when true, the job does not execute

Body - markdown used as the one-shot user instruction for that scheduler run.

### Runs and sessions

Each execution persists a normal session directory under **`sessions.dir`** with **`schedulerRun`** metadata in **`session.json`** (job id, start or end timestamps, **`status`**). Completed runs older than **`scheduler.retain_sessions`** per **`job_id`** are pruned (default **5** when unset).

Step expressions such as **`*/2 * * * *`** follow normal vixie semantics (minutes **0,2,4,…** UTC); **`*/5 * * * *`** fires at minutes **0,5,10,…** UTC. While a run is in progress, the exclusive **`basename.lock`** prevents starting another execution for the same job until the prior run releases it.

Composer session list (**`GET /coddy/sessions`**) omits scheduler-only bundles unless **`include_scheduler=true`**.

Inspect runs - **`GET /coddy/scheduler/jobs/{job_id}/runs`** or tool **`coddy_scheduler_job_runs`**; transcripts - **`GET /coddy/sessions/{session_id}/messages`**.

Daemon process logging stays short (**`slog`**); full traces live in session storage.

### HTTP API

With **`-tags=http,scheduler`**, **`GET /coddy/scheduler/jobs`**, job CRUD, **`pause`** / **`resume`**, **`run`**, **`cancel`**, and **`…/runs`** mirror the **`schedservice`** layer. **`503`** if **`scheduler.enabled`** is false. OpenAPI merges these paths only when **scheduler** is linked (see **`external/httpserver/scheduler_http.go`** vs **`scheduler_http_stub.go`**).

### Tools (when scheduler is enabled)

- **`coddy_scheduler_jobs_list`** - list jobs (**`include_body`** optional)
- **`coddy_scheduler_job_get`** - one job JSON including **`body`**
- **`coddy_scheduler_job_create`** / **`coddy_scheduler_job_replace`** / **`coddy_scheduler_job_patch`**
- **`coddy_scheduler_job_delete`**
- **`coddy_scheduler_job_pause`** / **`coddy_scheduler_job_resume`**
- **`coddy_scheduler_job_run`** - manual run (does not advance cron **`.state`**)
- **`coddy_scheduler_job_cancel`**
- **`coddy_scheduler_job_runs`** - metadata list for persisted runs

Legacy names **`coddy_scheduler_list`**, **`read`**, **`write`**, **`delete`**, **`validate`** are removed.

### Example job (minute tick)

```md
---
description: "Minute tick"
schedule: "* * * * *"
cwd: ""
mode: agent
---

In the session working directory run

bash -lc 'date -u +%FT%TZ > tick.txt'
```

See also **`docs/http-api.md`** (scheduler table), **`docs/config.md`** (**`scheduler`** key), and **`examples/README.md`** (Python harnesses **`http_e2e_scheduler_api`**, **`http_e2e_scheduler_agent`**, **`acp_e2e_scheduler_agent`**).
