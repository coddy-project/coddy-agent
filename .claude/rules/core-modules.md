---
description: Main internal packages at a glance
paths:
  - "internal/**/*.go"
---

# Core modules (sketch)

- **`internal/acp`** - ACP RPC server, session lifecycle from editors.
- **`internal/agent`** - tool loop and LLM turns.
- **`internal/session`** - session manager and mode (`agent` / `plan`).
- **`internal/config`** - YAML and flags.
- **`internal/tools`** - filesystem, shell, todo, MCP merge, etc.
- **`internal/skills`** - skill loading, enable/disable (`loader.go`, `disabled.go`), and remote install from repos / agents-standard marketplaces (`remote.go`, `manifest.go`: `Sync`, `AddSource`, `RemoveRemote`; git clone via `internal/gitws.Clone`/`Pull`, materialized into `${CODDY_HOME}/skills` with a `.remote.json` lockfile). Default dirs: `~/.agents/skills` (global, shared with `npx skills`/`npx skillsbd`), `~/.coddy/skills` (coddy-specific), `${CWD}/.coddy/skills` (project-local). Remote sources are listed in `skills.sources` and fetched only on demand (`coddy skills sync` / HTTP `POST /coddy/skills/sync` / the Settings → Skills UI). See `docs/skills.md`.

Prefer extending these over growing **`cmd/`** or duplicating logic in **`external/`**.

## References

@architecture.md
