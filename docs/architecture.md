# Architecture: Coddy Agent

## Overview

Coddy is a **distroless-friendly ACP harness** written in Go. At its core it is protocol plumbing
(STDIO JSON-RPC server, sessions, configuration, MCP wiring) plus a **ReAct** execution loop backed
by pluggable LLM providers. Ship it as one binary suitable for scratch or distroless images,
sidecars, CI sandboxes, or local installs.

The default toolset and prompts are tuned so the harness presents as an **interactive coding agent**
(editors spawn `coddy acp`; users get filesystem, commands, MCP, Cursor rules/skills).
That coding-agent surface is **a productized profile on top of the harness**, not the only way to run Coddy.

## High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        ACP Client (editor)                       │
│                 (Cursor / Zed / CLI / other)                     │
└──────────────────────────┬──────────────────────────────────────┘
                           │  JSON-RPC 2.0 over stdio
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│                        ACP Server Layer                          │
│  - initialize / session/new / session/prompt / session/cancel   │
│  - session/update notifications                                  │
│  - session/request_permission                                    │
└──────────────────────────┬──────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Session Manager                             │
│  - maintains per-session state (history, mode, context)         │
│  - routes messages to the right ReAct loop                      │
└──────────────────────────┬──────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│                      ReAct Agent Loop                            │
│                                                                  │
│   User Prompt                                                    │
│       │                                                          │
│       ▼                                                          │
│   [THINK] LLM generates Thought + Action                        │
│       │                                                          │
│       ▼                                                          │
│   [ACT]  Execute tool / write file / call MCP                   │
│       │                                                          │
│       ▼                                                          │
│   [OBSERVE] Collect result, send session/update                 │
│       │                                                          │
│       └── loop back or [ANSWER] final response                  │
└──────────────────────────┬──────────────────────────────────────┘
                           │
              ┌────────────┼────────────┐
              ▼            ▼            ▼
         ┌─────────┐ ┌─────────┐ ┌──────────────┐
         │  LLM    │ │  Tools  │ │  MCP Clients │
         │Provider │ │Registry │ │  (external)  │
         └─────────┘ └─────────┘ └──────────────┘
```

## Component Descriptions

### ACP Server Layer (`internal/acp`)

Implements the JSON-RPC 2.0 server that speaks the ACP protocol over stdio.
Handles:
- `initialize` - version negotiation, capability exchange
- `session/new` - create session, connect MCP servers, return modes and Session Config Options (model + mode selectors)
- `session/load` - restore a persisted bundle from disk (`$HOME/coddy-agent/sessions` by default), replay history via `session/update`
- `session/list` - enumerate persisted sessions (ACP `sessionCapabilities.list`)
- `session/prompt` - receive user message, start ReAct loop
- `session/cancel` - cancel in-progress turn
- `session/set_mode` - switch between `agent` and `plan` modes (legacy, kept in sync with config options)
- `session/set_config_option` - change mode or model for the session (preferred ACP API)

### Session Manager (`internal/session`)

Maintains the state for each conversation session:
- Conversation history (messages, tool results)
- Current operating mode (`agent` / `plan`)
- Optional model override per session (when the user selects a model via ACP)
- Connected MCP server clients
- Working directory
- Active context (skills + cursor rules loaded)
- In-memory plan entries for todo tools (**`session.Plan`**), mirrored to **`todos/active.md`** when persistence is enabled (**`filesystem.go`**)

### ReAct Agent Loop (`internal/react`)

The core reasoning engine (**`agent.go`**):

1. Loads mode-appropriate tool definitions (built-ins plus MCP) and filters by **`ToolsForMode`**.
2. Builds the system prompt from **`internal/prompts.Render`**: embedded **`agent.md`** / **`plan.md`** or files under **`prompts.dir`**. Template data includes **`CWD`**, tools markdown, skills markdown (that order in stock templates), optional **`TodoList`** and **`Memory`**, plus **`UTCNow`** (RFC3339 UTC refreshed on every render).
3. Prepends that system message to the session message list and appends the newest user turn.
4. **Before every LLM invocation** inside one **`session/prompt`**, refreshes the **`system` message content** so **`TodoList`** and other template fields match state after prior tool calls in the same episode.
5. Streams the LLM response, executes tool calls, appends assistant and tool messages.
6. Loops until there are no tool calls, **`max_turns`** is exceeded, or cancellation.

### LLM Provider (`internal/llm`)

Abstracted interface for LLM backends. Configured via `config.yaml`.
Supported providers:
- OpenAI (GPT-4o, GPT-4-turbo, o1, o3)
- Anthropic (Claude 3.5, Claude 3)
- Ollama (local models)
- Any OpenAI-compatible API

### Tools Registry (`internal/tools`)

The **tool types and registry mechanics** live in **`internal/tooling`** (`Tool`, `Env`,
`Registry`, JSON `ParseArgs`, `ToolsForMode`). The **`internal/tools`** package is the
composition root (`NewRegistry` wires everything) and exposes the same APIs via type aliases so
call sites such as **`internal/react`** keep importing **`tools`** only.

Built-in implementations are grouped in subfolders under **`internal/tools/`**:

- **`internal/tools/fs`** - path helpers (`paths.go` with `ResolvePath`, `CheckInsideCWD`,
  `PathEscapesCWD`, `ToolPathsEscapeCWD`) and tools (`readfile.go`, **`writefile.go`** registers both
  **`write_file`** and **`write_text_file`**), **`ls.go`** (**`list_dir`**), **`find.go`** (**`search_files`**),
  **`patch.go`** (**`apply_diff`**), **`mkdir`**, **`rmdir`**, **`touch`**, **`rm`**, **`mv`**).
- **`internal/tools/shell`** - **`run_command`**
- **`internal/tools/todo`** - todo/plan list (**`create_todo_list`**, **`get_todo_list`**,
  **`update_todo_item`**, **`delete_todo_item`**, **`done_todo_item`**, **`undone_todo_item`**,
  **`clean_todo_list`**)

Agents see:

- **`read_file`**, **`list_dir`**, **`search_files`**, **`create_todo_list`**, **`get_todo_list`**,
  **`update_todo_item`**, **`delete_todo_item`**, **`done_todo_item`**, **`undone_todo_item`**,
  **`clean_todo_list`**, and **`write_text_file`** when in **`plan`**
  mode (**`write_text_file`** allows only `.txt` / `.md` / `.mdx` and is omitted from **`agent`**).
- **`write_file`** and the rest (including **`mkdir`**, **`rm`**, **`mv`**, etc.) plus
  **`run_command`** when in **`agent`** mode.

`run_command`, optional write paths, and out-of-tree paths still go through **`session/request_permission`** as before.

### MCP Client (`internal/mcp`)

Connects to external MCP servers specified in `session/new`. Supports:
- stdio transport (always available)
- HTTP transport (capability: `mcpCapabilities.http`)

Tools from MCP servers are merged into the tools registry for the session.

### Skills & Cursor Rules Loader (`internal/skills`)

Reads `.cursor/rules/` and skill files from:
1. Global skills directory (`~/.cursor/skills/`)
2. Project-level `.cursor/rules/` in the working directory
3. Custom skills paths from `config.yaml`

Each skill/rule file is parsed as Markdown and injected into the system prompt
when relevant (based on glob patterns in the skill's `glob` frontmatter field).

### Config (`internal/config`)

YAML-based configuration. Location (in priority order):
1. Path specified by `--config` flag
2. `~/.config/coddy-agent/config.yaml`
3. `./config.yaml` in current directory

## Session Modes

### `agent` mode (default)
- Full tool access (read, write, run commands)
- Executes tasks end-to-end
- Requests permission before destructive operations
- Suitable for: code generation, refactoring, debugging

### `plan` mode
- Limited tools: read files, write/edit text/markdown files only
- No code execution
- Focused on planning, documentation, writing specs
- Suitable for: design docs, specs, architecture planning

Mode switching:
- Client calls `session/set_config_option` with `configId` `mode` (preferred) or `session/set_mode` with `agent` or `plan`
- Agent sends `current_mode_update` and `config_option_update` when mode changes

## Directory Structure

```
coddy-agent/
├── cmd/coddy/
│   └── main.go                  # CLI entry (acp, sessions, skills)
├── internal/
│   ├── acp/                     # JSON-RPC ACP server (stdio)
│   ├── session/                 # lifecycle, persistence, state
│   ├── react/
│   │   └── agent.go             # ReAct loop (system prompt + tools + MCP)
│   ├── prompts/
│   │   ├── loader.go             # TemplateData, Render, embedded agent.md / plan.md
│   │   ├── agent.md
│   │   └── plan.md
│   ├── config/
│   ├── llm/
│   ├── tooling/                 # Tool, Registry, ToolsForMode, Env
│   ├── tools/                   # builtins (fs/, shell/, todo/), NewRegistry
│   ├── mcp/
│   └── skills/
├── examples/acp-jsonrpc-session/ # stdio JSON-RPC demos (manual + e2e)
├── docs/                        # this tree and guides
├── config.example.yaml
├── go.mod
├── go.sum
└── README.md
```
