# Optional cron scheduler (`scheduler` build tag)

This tree implements the Coddy cron scheduler daemon, **`schedulerops`** (CRUD and run tracking shared by HTTP and tools), and **`tools/`** package **`schedtools`** (**`coddy_scheduler_*`**).

- Human-oriented guide - **`docs/scheduler.md`**
- YAML and retention - **`docs/config.md`** (**`scheduler`** key)
- HTTP routes - **`docs/http-api.md`** (scheduler section requires **`-tags=http,scheduler`**)

Build - **`go build -tags=scheduler ./cmd/coddy`**, optionally with **`http`** (and **`ui`** for the SPA gateway).
