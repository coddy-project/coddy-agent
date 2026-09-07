# Long-term memory (Memory Copilot)

Implementation lives under **`external/memory`** and is linked only when you build with **`-tags memory`** (recommended together with **`http`** for REST routes under **`/coddy/sessions/{id}/memory/*`**). Turn behavior on or off at runtime with **`memory.enabled`** in **`config.yaml`** when the binary includes the **`memory`** tag.

## Build

```bash
make build TAGS="memory"
# full gateway + UI + scheduler + memory (matches default Docker BUILD_TAGS)
make build TAGS="http ui scheduler memory"
```

See root **README** and **[docs/build.md](../../docs/build.md)**.

## Behaviour

In the LLM sense, "memory" is whatever is injected into the context. Short-term memory is the chat history. **Long-term** memory here means markdown files on disk that are turned into a short block **before** the main model answers, merged into the same template slot as session notes (`{{.Memory}}` in **`agent.md`** / **`plan.md`** / **`ask.md`**). In **ask** mode the pass is recall-only: the copilot gets the search, list, and read tools and never saves, creates, or deletes a memory.

When **`memory.enabled`** is true and the binary was built with **`memory`**, Coddy runs **one** memory copilot pass per user message **before** the main ReAct agent. That pass chooses either **RECALL** (read-only tools only) or **PERSIST** (may call mkdir/save/delete after reading), never both in the same turn. The final plain text from that pass is merged into **`{{.Memory}}`**; the main agent then answers with that context.

The memory copilot system prompt is **`prompts/copilot.md`** in this directory, embedded at compile time (`go:embed`). Edit that file and rebuild to change instructions.

Coddy emits **ACP `session/update`** notifications (`memory_phase`, **`memory_message_chunk`**) and persists **`memory_trace.json`** alongside **`messages.json`**. The trace and the HTTP **`memoryTurns`** field on **`GET /coddy/sessions/{id}/messages`** are for UI observability only - they are **not** part of the Chat Completions transcript sent to the primary model.

The memory copilot wires **`PersistTools`** (all **`coddy_memory_*`** **`tooling.Tool`** values) and passes **`ToolDefinitions`** to the LLM. In **RECALL** mode it must restrict itself to search/list/read per the system prompt. In **PERSIST** mode it may write after deduplicating against existing notes.

The main ReAct loop **does not** receive these tool definitions and cannot call memory as a normal tool.

## Storage layout

- **Global** (shared across sessions): **`memory.dir`** in config. When **`dir`** is empty or unset, the root is **`$CODDY_HOME/memory`** (typically **`~/.coddy/memory`**). Values support **`${CODDY_HOME}`** and **`~`** expansion like other paths in config.
- **Project** (per workspace): always **`<session cwd>/memory`**. This path is not configurable.

Supported file extensions: **`.md`** and **`.txt`**. **`coddy_memory_search`** ranks nested files under each root by word overlap with the query. Subdirectories are encouraged for thematic grouping; use **`coddy_memory_mkdir`** before saving into a new folder branch.

When built with **`http`** and **`memory`**, REST endpoints under **`/coddy/sessions/{id}/memory/*`** expose the same tree for the SPA and mirror filesystem layout produced by copilot tools.

Cross-links inside stored bodies should use **`scope:relative/path.md`** (or Markdown targets with that form) so paths stay unambiguous across global vs project roots.

## Configuration (`memory`)

See **`config.example.yaml`** and **`docs/config.md`**. Fields:

- **`enabled`** - master switch at runtime (only effective when binary includes **`memory`**).
- **`model`** - optional exact **`models[].model`** id for the memory copilot only; does not change the main agent. Pin it when memory should stay on a fixed model regardless of **`agent.model`**. Empty uses the active session / **`agent.model`**.
- **`dir`**, **`recall_max_turns`**, **`persist_max_turns`**, **`copilot_max_tokens`**, **`max_search_hits`** - see the example config comments. **Effective tool-round cap** for the unified pass is **max(`recall_max_turns`, `persist_max_turns`)** (see **`copilot.go`**).

## Cost and latency

Each user turn with memory enabled adds **one** memory copilot run before the main agent. Latency is bounded by that pass plus the main ReAct loop.

## Code layout

- **`storage/`** - roots, search, read/write (flat slug or **`relative_path`**, nested dirs), mkdir, listing, delete file or directory tree (**package `memstorage`**).
- **`tools/`** - per-tool files **`mem_*.go`** each returning **`memory*Tool(...) *tooling.Tool`** (same shape as scheduler **`job_*.go`**), **`register.go`** with **`PersistTools`**, **`RecallTools`**, **`ToolDefinitions`**, **`Exec`**, and test helper **`ExecTool`** (**package `memtools`**).
- **`copilot.go`** - **`RunBeforeTurn`** unified loop with **`llm.Stream`** / **`Complete`** and tool callbacks (**package `memory`**, root of this module).

Runtime wiring: **`internal/agent/memory_hooks.go`** (built with **`//go:build memory`**).

## Related work

Prompt shape and the idea of routing context through a dedicated memory pass are **partly** informed by **[MemAgent](https://github.com/BytedTsinghua-SIA/MemAgent)** (Tsinghua-SIA / ByteDance). Coddy is not a fork of that repository - storage, tools, configs, and integrations (ACP, HTTP, UI) are separate.
