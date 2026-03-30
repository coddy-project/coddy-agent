# Architecture: Coddy Agent

## Overview

This project implements a **ReAct (Reasoning + Acting)** AI agent in Go that exposes itself
via the **Agent Client Protocol (ACP)**. The agent can be integrated into any ACP-compatible
editor (Cursor, Zed, VS Code via extension, etc.).

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
- `session/load` - restore previous session (optional)
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

### ReAct Agent Loop (`internal/prompts/react`)

The core reasoning engine:
1. Builds system prompt from: base instructions + current mode + active skills
2. Sends prompt to LLM provider with available tools
3. Parses LLM response: extracts thoughts + tool calls
4. Executes tools (with permission checks via ACP)
5. Feeds results back to LLM
6. Repeats until LLM produces a final answer

### LLM Provider (`internal/llm`)

Abstracted interface for LLM backends. Configured via `config.yaml`.
Supported providers:
- OpenAI (GPT-4o, GPT-4-turbo, o1, o3)
- Anthropic (Claude 3.5, Claude 3)
- Ollama (local models)
- Any OpenAI-compatible API

### Tools Registry (`internal/tools`)

Built-in tools available to the agent:
- `read_file` - read a file from the working directory
- `write_file` - write/create a file
- `list_dir` - list directory contents
- `search_files` - search file content (ripgrep-based)
- `run_command` - execute shell command (requires permission)
- `apply_diff` - apply unified diff to a file

In `plan` mode, only file-read and text-write tools are available.
In `agent` mode, all tools including `run_command` are available.

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
- Agent can self-switch from `plan` to `agent` after creating a plan (with permission)

## Directory Structure

```
coddy-agent/
├── cmd/
│   └── agent/
│       └── main.go              # entry point, CLI flags
├── internal/
│   ├── acp/
│   │   ├── server.go            # JSON-RPC server loop (stdio)
│   │   ├── types.go             # all ACP protocol types
│   │   └── handlers.go          # method handlers
│   ├── session/
│   │   ├── manager.go           # session lifecycle
│   │   └── state.go             # session state struct
│   ├── react/
│   │   ├── agent.go             # ReAct agent (main loop)
│   │   ├── mode.go              # mode-specific behavior
│   │   └── prompt_builder.go    # system prompt construction
│   ├── config/
│   │   ├── config.go            # config loading + validation
│   │   └── types.go             # config structs
│   ├── llm/
│   │   ├── provider.go          # Provider interface
│   │   ├── openai.go            # OpenAI implementation
│   │   ├── anthropic.go         # Anthropic implementation
│   │   └── ollama.go            # Ollama implementation
│   ├── tools/
│   │   ├── registry.go          # tool registration + dispatch
│   │   ├── fs.go                # file system tools
│   │   ├── search.go            # search tools
│   │   └── terminal.go          # command execution tool
│   ├── mcp/
│   │   ├── client.go            # MCP stdio/http client
│   │   └── types.go             # MCP types
│   └── skills/
│       ├── loader.go            # skill/rule file loader
│       └── types.go             # skill types
├── docs/
│   ├── architecture.md          # this file
│   ├── acp-protocol.md          # ACP protocol reference
│   ├── config.md                # config file reference
│   ├── skills.md                # skills and cursor rules guide
│   └── mcp-integration.md       # MCP server integration guide
├── config.example.yaml          # example configuration
├── go.mod
├── go.sum
└── README.md
```
