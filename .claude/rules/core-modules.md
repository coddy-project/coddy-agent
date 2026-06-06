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
- **`internal/skills`** - skill loading, enable/disable, and neuraldeep.ru registry client. Default dirs: `~/.agents/skills` (global, shared with `npx skills`/`npx skillsbd`), `~/.coddy/skills` (coddy-specific), `${CWD}/.coddy/skills` (project-local). No `install_dir` — installation is handled externally by `npx skills` / `npx skillsbd` or the HTTP UI (Settings → Skills). See `docs/skills.md`.

Prefer extending these over growing **`cmd/`** or duplicating logic in **`external/`**.

## References

@architecture.md
