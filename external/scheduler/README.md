# Optional cron scheduler (`scheduler` build tag)

This tree implements **`daemon/`** (UTC minute-aligned cron loop and the **`Runtime`** that starts, stops and keeps the runs), **`storage/`** (flat `*.md` jobs in the two fixed roots - `${CODDY_HOME}/scheduler` and `<workspace>/.coddy/scheduler` - the containment rules of project jobs, cron, the **`.state`** sidecar with the checkpoint and the job session id, the approvals of project jobs and the registry of scanned workspaces), **`service/`** (**`schedservice`**, CRUD, run rows and the **`Runtime`** interface the HTTP handlers and tools reach the daemon through), and **`tools/`** (**`schedtools`**, flat Go files per **`coddy_scheduler_*`** tool).

A run is a background task of kind `agent` under the job's session, made by **`internal/agent.RunScheduledJob`** on the same child-run executor `spawn_agent` uses; the daemon runs nothing on a state of its own.

- Human-oriented guide - **`docs/operate/scheduler.md`**
- Design records - **`docs/plans/scheduler-runs.md`**, **`docs/plans/scheduler-project-jobs.md`** (project jobs and their trust)
- YAML and retention - **`docs/getting-started/configuration.md`** (**`scheduler`** key)
- HTTP routes - **`docs/reference/http-api.md`** (scheduler section requires **`-tags=http,scheduler`**)

Build - **`go build -tags=scheduler ./cmd/coddy`**, optionally with **`http`** (and **`ui`** for the SPA gateway).
