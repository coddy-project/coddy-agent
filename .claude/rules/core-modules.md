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
- **`internal/tools`** - filesystem, shell, todo, MCP merge, etc. Shell also owns the background task family (**`run_command`** **`background: true`** plus **`background_list`** / **`background_output`** / **`background_wait`** / **`background_stop`**), backed by **`internal/bgtask`**. See **`docs/background-tasks.md`**.
- **`internal/bgtask`** - process-wide, session-scoped pool for work that outlives a tool call. A task is whatever a **`Runner`** starts; **`CommandRunner`** is the only implementation today and **`KindAgent`** is reserved for a future nested-agent runner, so that lands as a second **`Runner`** rather than a second scheduling mechanism.
- **`internal/skills`** - skill loading, enable/disable (`loader.go`, `disabled.go`), and remote install from repos / agents-standard marketplaces (`remote.go`, `manifest.go`, `plugin.go`: `Sync`/`SyncSource` (all or one source), `AddSource`, `RemoveRemote`/`DeleteSkill` (delete any on-disk skill; bundled = read-only via `SkillReadonly`), `ListSources`, `RemoveSource`, `CheckUpdates`, `UpdateSkill`; git clone via `internal/gitws.Clone`/`Pull`, materialized into `${CODDY_HOME}/skills` with a `.remote.json` lockfile that records each skill's installed `version`). Marketplace plugin `version` (and `SKILL.md` frontmatter `version`) are surfaced by `InstalledVersion` and drive update detection (`compareVersions`). Default dirs: `~/.agents/skills` (global, shared with `npx skills`/`npx skillsbd`), `~/.coddy/skills` (coddy-specific), `${CWD}/.coddy/skills` (project-local). Remote sources are listed in `skills.sources` and fetched only on demand. Management parity across the CLI (`coddy skills add|sync|remove`, `coddy plugin marketplace list|add|remove|sync`, `coddy plugin install|remove|enable|disable`), the built-in `/plugin` chat command (`internal/agent/plugin_command.go`, deterministic like `/compact`; shared dispatcher `skills.RunPluginCommand` / `MarketplaceStatus` in `plugin.go`), HTTP (`/coddy/skills/*`), and the Settings → Skills UI. See `docs/skills.md`.

Prefer extending these over growing **`cmd/`** or duplicating logic in **`external/`**.

## References

@architecture.md
