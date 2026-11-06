# Long-term memory (the memory subagent)

Implementation lives under **`external/memory`** and is linked only when you build with **`-tags memory`** (recommended together with **`http`** for REST routes under **`/coddy/sessions/{id}/memory/*`**). Turn behavior on or off at runtime with **`memory.enable`** in **`config.yaml`** when the binary includes the **`memory`** tag. With it on, the model delegates to the **memory subagent** on demand: a `spawn_agent` call with `agent: "memory"` (the bundled definition of that name) starts a child agent run in the background task pool, with its own session bundle inside the parent's, its own transcript and its own task log, the way any `spawn_agent` child runs. What the child does, how the model calls it, the storage layout, the configuration keys, the HTTP routes and the cost are described in the user guide, [docs/features/memory.md](../../docs/features/memory.md).

## Build

```bash
make build TAGS="memory"
# full gateway + UI + scheduler + memory (matches default Docker BUILD_TAGS)
make build TAGS="http ui scheduler memory"
```

See root **README** and **[docs/contributing/build.md](../../docs/contributing/build.md)**.

## Code layout

- **`storage/`** - roots, search (BM25F: the **`docs.Ranker`** of the documentation search over a note's path words and body, **`pathWeight`** 3 and **`bodyWeight`** 1), read/write (flat slug or **`relative_path`**, nested dirs), mkdir, listing, delete file or directory tree (**package `memstorage`**). The project root is reached through **`os.Root`** (**`roots.go`**), so a link in it never leads out of it; the global root follows links. The HTTP notes routes use the same store.
- **`tools/`** - per-tool files **`mem_*.go`** each returning **`memory*Tool(...) *tooling.Tool`** (same shape as scheduler **`job_*.go`**), **`register.go`** with **`PersistTools`**, **`RecallTools`**, **`ToolDefinitions`**, **`Exec`**, and test helper **`ExecTool`** (**package `memtools`**).
- **`agent.go`** - what the child runs on: **`PromptTemplate`** (the embedded **`prompts/memory_agent.md`**, plus the read-only addendum for an ask-mode turn), **`Guides`** (the **`AGENTS.md`** at the root of each memory root, read through the store like any note) and **`TaskMessage`** (the guides, global first, each under a **`Memory guide (scope:AGENTS.md)`** heading, quoted line by line (so an **`@`** reference in it stays text) and read to 8 KiB at most, with a marker when the file goes on, then the user message behind a preamble, cut at 32 KiB), **`ToolNames`** (the six names, or the three recall names) and **`Tools`** (the tool set over the two note roots of a cwd) (**package `memory`**, root of this module).

Runtime wiring: **`internal/agent/memory_hooks.go`** (built with **`//go:build memory`**) holds the spawn-path helpers: **`registerMemoryChildTools`** adds the six tools to the child's own registry (an ordinary session never sees a memory tool), **`memorySpawnCheck`** gates a `spawn_agent` call made under the bundled definition, **`memorySpawnVisible`** hides that definition from the model-facing catalog while the feature is off, **`memorySpawnSpec`** turns such a spawn into a run of the memory subagent (its tools, prompt template, model and fallback chain, the operator's addendum and the guides ahead of the task), and **`memoryFallbackModels`** builds the model chain. The run itself is launched through the generic subagent machinery of **`internal/agent/subagent.go`**, like any other delegation. Without the tag the stub in **`memory_hooks_stub.go`** answers with an error that names it.

## Related work

Prompt shape and the idea of routing context through a dedicated memory pass are **partly** informed by **[MemAgent](https://github.com/BytedTsinghua-SIA/MemAgent)** (Tsinghua-SIA / ByteDance). Coddy is not a fork of that repository - storage, tools, configs, and integrations (ACP, HTTP, UI) are separate.
