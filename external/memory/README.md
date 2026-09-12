# Long-term memory (Memory Copilot)

Implementation lives under **`external/memory`** and is linked only when you build with **`-tags memory`** (recommended together with **`http`** for REST routes under **`/coddy/sessions/{id}/memory/*`**). Turn behavior on or off at runtime with **`memory.enable`** in **`config.yaml`** when the binary includes the **`memory`** tag. What the copilot does, when it runs, the storage layout, the configuration keys, the HTTP routes and the cost are described in the user guide, [docs/features/memory.md](../../docs/features/memory.md).

## Build

```bash
make build TAGS="memory"
# full gateway + UI + scheduler + memory (matches default Docker BUILD_TAGS)
make build TAGS="http ui scheduler memory"
```

See root **README** and **[docs/contributing/build.md](../../docs/contributing/build.md)**.

## Code layout

- **`storage/`** - roots, search, read/write (flat slug or **`relative_path`**, nested dirs), mkdir, listing, delete file or directory tree (**package `memstorage`**).
- **`tools/`** - per-tool files **`mem_*.go`** each returning **`memory*Tool(...) *tooling.Tool`** (same shape as scheduler **`job_*.go`**), **`register.go`** with **`PersistTools`**, **`RecallTools`**, **`ToolDefinitions`**, **`Exec`**, and test helper **`ExecTool`** (**package `memtools`**).
- **`copilot.go`** - **`RunBeforeTurn`** unified loop with **`llm.Stream`** / **`Complete`** and tool callbacks (**package `memory`**, root of this module).

Runtime wiring: **`internal/agent/memory_hooks.go`** (built with **`//go:build memory`**).

## Related work

Prompt shape and the idea of routing context through a dedicated memory pass are **partly** informed by **[MemAgent](https://github.com/BytedTsinghua-SIA/MemAgent)** (Tsinghua-SIA / ByteDance). Coddy is not a fork of that repository - storage, tools, configs, and integrations (ACP, HTTP, UI) are separate.
