---
description: Layers and dependency direction for Coddy Agent
paths:
  - "**/*.go"
---

# Architecture

**Coddy** is a Go CLI and ACP harness. Keep dependencies flowing **inward**: higher layers orchestrate lower ones; shared types stay shallow.

## Rough layers (low to high)

1. **`internal/version`**, **`internal/logger`**, small shared helpers - minimal inward imports.
2. **`internal/config`** - configuration structs and loading.
3. **`internal/session`**, **`internal/llm`**, **`internal/prompts`** - runtime state and model access.
4. **`internal/tools`**, **`internal/permission`**, **`internal/skills`** - capabilities and policies.
5. **`internal/agent`** - ReAct-style loop assembling the above.
6. **`internal/acp`** - ACP protocol server on top of the agent and session manager.
7. **`cmd/coddy`** - CLI entrypoints and wiring.

## Optional build tags

- **`memory`** - **`external/memory`** long-term memory copilot and HTTP session memory routes; **`//go:build memory`**. Recommended together with **`http`** for `/coddy/sessions/.../memory/*`. Default full binary in README and Docker includes **`memory`**.
- **`external/httpserver`** - OpenAI-shaped HTTP API; **`//go:build http`**. Embedded SPA uses **`//go:build http && ui`** with **`external/ui`**. Keep handler registration and **`openapi.go`** in sync.
- **`external/scheduler`** (**`daemon/`**, **`storage/`**, **`service/`** schedservice, **`tools/`** schedtools) - cron and related tools; scheduler tag.

Do not introduce import cycles. If a new package would cycle, split interfaces or move shared types down-layer.

## Optional module tools (`external/*/tools`)

Optional domains that ship their own LLM-callable tools (not the main **`internal/tools`** registry) use **`internal/tooling.Tool`**: **`Definition`** (**`llm.ToolDefinition`**) plus **`Execute func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error)`** in the same file, same pattern as **`external/scheduler/tools/job_get.go`**.

- **One constructor per file** - e.g. **`jobGetTool(cfg *config.Config) *tooling.Tool`**, **`memorySearchTool(store *memstorage.Store, mem *config.MemoryConfig) *tooling.Tool`**. **`InputSchema`** uses **`map[string]interface{}`** with **`[]interface{}`** for **`required`** and enums (match existing scheduler and memory files).
- **`register.go`** - aggregates constructors (scheduler **`RegisterTools`** into the session registry; memory **`PersistTools`**, **`RecallTools`**, **`ToolDefinitions`**, **`Exec`** for the memory copilot loop in **`external/memory`**).
- **File naming** - scheduler job tools use the **`job_*.go`** prefix; memory per-tool files use the **`mem_*.go`** prefix; shared **`env.go`**, **`names.go`**, **`register.go`** in **`external/memory/tools`** stay unprefixed.

## References

@README.md
@docs/architecture.md
@core-modules.md
