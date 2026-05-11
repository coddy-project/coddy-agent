## Scheduler

### Overview

The scheduler is an optional cron-like runner. It scans a single job directory for flat `*.md` files (YAML frontmatter plus markdown body) and executes each job when due. It is compiled in only with the **`scheduler`** build tag.

Pieces:

- **`external/scheduler`** - daemon (**`Start`**) and job execution (**`run_job`**), wired into **`cmd/coddy`** so manual launches work in production binaries.
- **`external/scheduler/schedulerops`** - shared CRUD, run tracker, pruning of old run sessions, HTTP and tool payloads (no cycles with **`internal/tools`**).
- **`external/scheduler/tools`** - **`coddy_scheduler_*`** tools registered when **`scheduler.enabled`** is true.

The cron parser uses **five fields** (**minute hour day month weekday**) in **UTC**.

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

- **`basename.lock`** while a run holds the exclusive lock
- **`basename.state`** cron checkpoint (**`last_scheduled_utc`**)

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

Composer session list (**`GET /coddy/sessions`**) omits scheduler-only bundles unless **`include_scheduler=true`**.

Inspect runs - **`GET /coddy/scheduler/jobs/{job_id}/runs`** or tool **`coddy_scheduler_job_runs`**; transcripts - **`GET /coddy/sessions/{session_id}/messages`**.

Daemon process logging stays short (**`slog`**); full traces live in session storage.

### HTTP API

With **`-tags=http,scheduler`**, **`GET /coddy/scheduler/jobs`**, job CRUD, **`pause`** / **`resume`**, **`run`**, **`cancel`**, and **`…/runs`** mirror the **`schedulerops`** layer. **`503`** if **`scheduler.enabled`** is false. OpenAPI merges these paths only when **scheduler** is linked.

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

See also **`docs/http-api.md`** (scheduler table) and **`docs/config.md`** (**`scheduler`** key).
