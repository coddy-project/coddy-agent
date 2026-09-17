# Long-term memory (the memory subagent)

Implementation lives under **`external/memory`** and is linked only when you build with **`-tags memory`** (recommended together with **`http`** for REST routes under **`/coddy/sessions/{id}/memory/*`**). Turn behavior on or off at runtime with **`memory.enable`** in **`config.yaml`** when the binary includes the **`memory`** tag. With it on, every user turn starts a **memory subagent**: a child agent run in the background task pool, with its own session bundle inside the parent's, its own transcript and its own task log, the way a `spawn_agent` child runs. What the child does, how the turn waits for its report, the storage layout, the configuration keys, the HTTP routes and the cost are described in the user guide, [docs/features/memory.md](../../docs/features/memory.md).

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
- **`agent.go`** - what the child runs on: **`PromptTemplate`** (the embedded **`prompts/memory_agent.md`**, plus the read-only addendum for an ask-mode turn), **`TaskMessage`** (the user message behind a preamble, cut at 32 KiB), **`ToolNames`** (the six names, or the three recall names) and **`Tools`** (the tool set over the two note roots of a cwd) (**package `memory`**, root of this module).

Runtime wiring: **`internal/agent/memory_hooks.go`** (built with **`//go:build memory`**) launches the child through the subagent machinery of **`internal/agent/subagent.go`** and registers the tools into the child's registry; **`internal/agent/memory_run.go`** (untagged) holds the parent-side bookkeeping: the in-flight bounds, the wait for the report, its delivery through the system prompt or the turn context, the `memory_run` updates, retention of finished runs and the drain grace.

## Related work

Prompt shape and the idea of routing context through a dedicated memory pass are **partly** informed by **[MemAgent](https://github.com/BytedTsinghua-SIA/MemAgent)** (Tsinghua-SIA / ByteDance). Coddy is not a fork of that repository - storage, tools, configs, and integrations (ACP, HTTP, UI) are separate.
