# Long-term memory (Memory Copilot)

This package is linked **only** when you build with the Go tag `memory`. A plain `make build` omits it so the default binary stays smaller and never runs extra LLM passes for memory.

## Build

- Default agent: `make build` → `build/coddy`
- With memory: `make build-memory` → `build/coddy-memory` (same as `go build -tags memory ...`)

If `memory.enabled: true` in `config.yaml` but the binary was built **without** `-tags memory`, configuration loading **fails** with a clear error telling you to rebuild with the tag.

## Behaviour

In the LLM sense, “memory” is whatever is injected into the context. Short-term memory is the chat history. **Long-term** memory here means markdown files on disk that are turned into a short block **before** the main model answers, merged into the same template slot as session notes (`{{.Memory}}` in `agent.md` / `plan.md`).

A separate **memory copilot** (extra `llm.Complete` passes) may call only `coddy_memory_*` tools:

- **Recall** (before the main reply) - search/read (and optional save/delete inside that sub-loop). Output is stored in a per-turn session field and merged with user session notes when rendering the system prompt.
- **Persist** (after the final assistant message in a user turn, when there are no pending tool calls) - a judge returns JSON; on approval, a new `.md` file is written.

The main ReAct loop **does not** receive these tool definitions and cannot call memory as a normal tool.

## Storage layout

- **Global** (shared across sessions): `memory.dir` in config. When `dir` is empty or unset, the root is **`$CODDY_HOME/memory`** (typically `~/.coddy/memory`). Values support `${CODDY_HOME}` and `~` expansion like other paths in config.
- **Project** (per workspace): always **`<session cwd>/memory`**. This path is not configurable.

Supported file extensions: `.md` and `.txt`. `coddy_memory_search` ranks files by word overlap with the latest user message text.

## Configuration (`memory`)

See `config.example.yaml` and `docs/config.md`. Fields:

- `enabled` - master switch (only meaningful in a `-tags memory` binary).
- `model` - optional selector from `models[]` for copilot calls; empty uses the active session / `agent.model`.
- `dir`, `recall_max_turns`, `persist_max_turns`, `copilot_max_tokens`, `max_search_hits` - see the example config comments.

## Cost and latency

Each user turn with memory enabled adds at least one recall LLM call when any memory files exist, plus a judge call when the turn ends cleanly. Both use English system prompts defined in this package.

## Code layout

- `store.go` - roots, search, read, write, delete.
- `tools.go` - tool schemas and execution.
- `copilot.go` - recall loop with `llm.Complete` and persist after the judge.

Runtime wiring: `internal/agent/memory_turn_memory.go` (tag `memory`) and `internal/agent/memory_turn_stub.go` (default build).

Whether the feature is compiled in is reported by `config.MemoryFeatureCompiled()` (`internal/config/memory_compiled_*.go`).


