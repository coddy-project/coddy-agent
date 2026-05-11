# Optional cron scheduler (`scheduler` build tag)

This tree implements **`daemon/`** (UTC minute-aligned cron loop and **`RunJobFile`**), **`storage/`** (flat `*.md` jobs, cron, `.state` / `.lock`), **`service/`** (**`schedservice`**, CRUD and run tracking for HTTP and tools), and **`tools/`** (**`schedtools`**, flat Go files per **`coddy_scheduler_*`** tool).

Within **`service/`**, logic is split across small files (**`errors`**, **`types`**, **`patch_decode`**, **`service`** core, **`jobs_read`** / **`jobs_write`**, **`manual_run`**, **`runs`**, **`tracker`**, **`prune`**).

- Human-oriented guide - **`docs/scheduler.md`**
- YAML and retention - **`docs/config.md`** (**`scheduler`** key)
- HTTP routes - **`docs/http-api.md`** (scheduler section requires **`-tags=http,scheduler`**)

Build - **`go build -tags=scheduler ./cmd/coddy`**, optionally with **`http`** (and **`ui`** for the SPA gateway).
