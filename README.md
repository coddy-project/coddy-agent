# Coddy Agent

**Coddy is a distroless-friendly harness** for running an [Agent Client Protocol (ACP)](https://agentclientprotocol.com/)
agent over stdio. It ships as a single static-friendly Go binary, so you can drop it into
minimal container images (`scratch`, `distroless`, read-only workspaces) without a full OS shell
inside the image. The bundled default is a **ReAct** loop with filesystem, shell (when exposed), todo,
and MCP tools, **which makes Coddy behave as a coding agent** inside Cursor, Zed, or any other ACP client.
The harness layer (ACP RPC, sessions, prompts, providers) stays the same if you tighten the toolset or
drive it from automation instead of an IDE.

## Contents

- [Features](#features)
- [Quick start](#quick-start)
  - [Installation](#installation)
  - [Build tags](#build-tags)
  - [Docker](#docker)
  - [Paths (`CODDY_HOME`, `CODDY_CWD`)](#paths-coddy_home-coddy_cwd)
  - [Configuration](#configuration)
- [Operating modes](#operating-modes)
- [Cursor rules and skills](#cursor-rules-and-skills)
- [MCP server integration](#mcp-server-integration)
- [Configuration (reference)](#configuration-1)
- [Architecture](#architecture)
- [Documentation](#documentation)
- [Examples (ACP over stdio)](#examples-acp-over-stdio)
- [Persistent sessions](#persistent-sessions)
- [Development](#development)
- [License](#license)

## Features

- **Harness-first** - ACP server, session lifecycle, prompts, LLM backends, MCP merge, distroless-ready binary
- **ReAct loop** - LLM alternates between reasoning, acting (tool calls), and observing results (coding-agent persona out of the box)
- **Two operating modes** - `agent` (full tool access) and `plan` (planning + text files only)
- **Cursor rules support** - reads `.cursor/rules/` and skills just like Cursor IDE
- **MCP server integration** - connect any MCP server for additional tools
- **Multi-provider LLM** - OpenAI, Anthropic, Ollama, any OpenAI-compatible API
- **ACP protocol** - works with Cursor, Zed, and other ACP-compatible editors

## Quick Start

### Installation

**Prerequisites**

- **Go** - same minor version as [`go.mod`](go.mod) (currently **1.25**).
- **Git** - used by the Makefile for the embedded version string.
- **Node.js / npm** - only if you build with **`http`** and **`ui`** (the Makefile runs **`ui-build`** for embedded assets).

**Install with Go (quick, default upstream tags)**

```bash
go install github.com/EvilFreelancer/coddy-agent/cmd/coddy@latest
```

That builds whatever the module ships **without** custom `-tags`. For **`coddy http`**, the bundled SPA, and scheduler tools together, **build from source** with the tags below (same defaults as [`Dockerfile`](Dockerfile) / [`docker-compose.yml`](docker-compose.yml)).

**Recommended full binary from source (HTTP + UI + scheduler + memory)**

Long-term memory is **always linked**; optional HTTP, SPA, and scheduler use build tags (see [Build tags](#build-tags)).

```bash
git clone https://github.com/EvilFreelancer/coddy-agent
cd coddy-agent
make build TAGS="http ui scheduler"
```

The CLI is written to **`build/coddy`** (not the repo root).

**Install `build/coddy` onto your PATH**

```bash
make install TAGS="http ui scheduler"
```

- **root** - **`/usr/local/bin/coddy`**
- **regular user** - **`~/.local/bin/coddy`** (put that directory on **`PATH`** if needed)

**Build without installing**

```bash
make build TAGS="http ui scheduler"
```

**Manual `go build` (same as Makefile)**

When **`TAGS`** includes **`http`** and **`ui`**, run **`make ui-build`** first (or rely on **`make build`**, which triggers it).

```bash
make ui-build   # required before go build when using -tags=...,ui,... with http
VERSION="$(make -s print-version)"
go build -tags=http,ui,scheduler \
  -ldflags "-X github.com/EvilFreelancer/coddy-agent/internal/version.Version=${VERSION}" \
  -o build/coddy \
  ./cmd/coddy/
```

Lean **ACP-only** binary (no **`coddy http`**, no embedded UI, no scheduler packages):

```bash
make build
```

After any local build, prefer **`./build/coddy`** or **`make install`** so you do not accidentally run another **`coddy`** already on **`PATH`**. Check with **`which coddy`** and **`coddy -v`**.

Full detail, **`LDFLAGS`**, and **`make print-version`** - **[docs/build.md](docs/build.md)**.

The agent speaks ACP over stdio. Editors launch **`coddy`** once it is configured. **`coddy -v`** or **`coddy --version`** prints the embedded build version (**`dev`** if not set at link time). Flags for ACP live on the subcommand, for example **`coddy acp --help`** (**`--log-level`**, **`--home`**, **`--cwd`**, **`--config`**, etc.).

### Build tags

Use **`Makefile`** variable **`TAGS`** with **spaces** (**`make build TAGS="http ui scheduler"`**). **`go build`** uses **commas** (**`-tags=http,ui,scheduler`**).

| Tag | Enables | Docs |
|-----|---------|------|
| *(always linked)* | Memory copilot (**`memory.enabled`** in YAML) | [`external/memory/README.md`](external/memory/README.md) |
| **`http`** | **`coddy http`**, REST gateway, **`/docs`**, **`/openapi.yaml`** | [`docs/http-api.md`](docs/http-api.md) |
| **`ui`** | Embedded SPA on **`/`** (needs **`http`**) | [`docs/ui/README.md`](docs/ui/README.md), [`DESIGN.md`](DESIGN.md) |
| **`scheduler`** | Scheduler daemon and **`coddy_scheduler_*`** tools; with **`http`**, **`/coddy/scheduler`** REST | [`docs/scheduler.md`](docs/scheduler.md), [`external/scheduler/README.md`](external/scheduler/README.md) |

Extended narrative and Docker alignment - **[docs/build.md](docs/build.md)**.

### Docker

**[docs/docker.md](docs/docker.md)** describes **`docker compose`** (image build args, volumes, smoke script **`examples/httpserver/docker.sh`**). Default image tags match the recommended full binary (**`http`**, **`scheduler`**, **`ui`**).

### Paths (`CODDY_HOME`, `CODDY_CWD`)

- **`CODDY_HOME`** (or **`coddy acp --home`**) is the agent state directory. Default **`~/.coddy`**. The process creates **`sessions/`** and **`skills/`** under it. Config defaults to **`$CODDY_HOME/config.yaml`**.
- **`CODDY_CWD`** (or **`coddy acp --cwd`**) is the default session working directory when `session/new` sends an empty **`cwd`**. Default is the process current directory at startup. Editors that pass a path in **`session/new`** use that path instead.

### Configuration

**`CODDY_HOME`** defaults to **`~/.coddy`**. Unless you set **`CODDY_CONFIG`** or pass **`--config`**, the primary config file is **`config.yaml`** at **`$CODDY_HOME/config.yaml`**.

Copy the example and edit it:

```bash
mkdir -p ~/.coddy && cp config.example.yaml ~/.coddy/config.yaml
```

If **`$CODDY_HOME/config.yaml`** is absent, the loader may use **`config.yaml`** in the process working directory (useful when running from a repository clone). See **`docs/config.md`**.

Set your API keys:

```bash
export OPENAI_API_KEY="sk-..."
# or
export ANTHROPIC_API_KEY="sk-ant-..."
```

## Operating Modes

### Agent Mode (default)

Full task execution mode. The agent has access to all tools:
- Read and write files
- Execute shell commands (with permission prompt)
- Search codebase
- Call MCP server tools

Best for: code generation, refactoring, debugging, feature implementation.

### Plan Mode

Planning and documentation mode. Restricted tools:
- Read files (no write to code files)
- Write/edit text and markdown files
- Search codebase

When the plan is ready, switch to **agent** mode yourself for full tools and implementation.

Best for: architecture planning, writing specs, design documents, code review.

Use your editor session mode selector (or **`session/set_config_option`**).

## Cursor Rules and Skills

By default the agent reads skill files and rules from (see **`skills`** in **`docs/config.md`**):

1. **`$CODDY_HOME/skills/`** (installed skills)

Rules support the standard Cursor frontmatter format:

```markdown
---
description: "Go coding standards"
globs: ["**/*.go"]
alwaysApply: false
---

Write all comments in English.
Use fmt.Errorf("context: %w", err) for error wrapping.
```

See [Skills Guide](docs/skills.md) for details.

## MCP Server Integration

Connect external tools via MCP servers. Configured globally in `config.yaml` or
passed per-session by the ACP client.

Example adding a GitHub MCP server in config:

```yaml
mcp_servers:
  - name: "github"
    command: "npx"
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      - name: "GITHUB_PERSONAL_ACCESS_TOKEN"
        value: "${GITHUB_TOKEN}"
```

See [MCP Integration Guide](docs/mcp-integration.md) for details.

## Configuration

Full configuration reference in [docs/config.md](docs/config.md).

Key settings:

```yaml
providers:
  - name: local
    type: openai
    api_key: "${OPENAI_API_KEY}"
    api_base: "${OPENAI_API_BASE}"

models:
  - model: "local/gpt-4o"
    max_tokens: 8192
    temperature: 0.2

agent:
  model: "local/gpt-4o"
  max_turns: 30

tools:
  require_permission_for_commands: true
```

## Architecture

```
ACP Client (Cursor/Zed)
        |
    JSON-RPC 2.0 over stdio
        |
    ACP Server Layer
        |
    Session Manager
        |
    ReAct Agent Loop
 /      |       |      \
LLM   Tools    Skills    MCP
```

See [Architecture docs](docs/architecture.md) for full details.

## Documentation

- [Build from source](docs/build.md) - prerequisites, **`make build`**, **`TAGS`** vs **`go build -tags`**, **`build/coddy`**
- [Docker](docs/docker.md) - **`Dockerfile`** and **`docker compose`**
- [Architecture](docs/architecture.md) - system design and component overview
- [ACP Protocol](docs/acp-protocol.md) - protocol reference and message formats
- [ReAct Agent](docs/react-agent.md) - ReAct loop design and tool specifications
- [Configuration](docs/config.md) - full config file reference
- [HTTP API](docs/http-api.md) - REST gateway (**`-tags=http`**) and embedded UI (**`-tags=http,ui`**)
- [Embedded UI](docs/ui/README.md) - Vite SPA, dev workflow, build tags
- [DESIGN.md](DESIGN.md) - UI tokens and layout (English)
- [AGENTS.md](AGENTS.md) - repo map and contributor notes for automation
- [Skills & Rules](docs/skills.md) - cursor rules and skills guide
- [MCP Integration](docs/mcp-integration.md) - MCP server integration guide

## Examples (ACP over stdio)

[**`examples/acp/acp_e2e_todo.py`**](examples/acp/acp_e2e_todo.py) is a newline-delimited JSON-RPC harness against **`coddy acp`** ( **`stdbuf -oL`**, permission auto-reply, nil-result responses). Use it as reference when building your own minimal client rather than chaining naive **`echo`** lines into a pipe.

[**`examples/acp/acp_e2e_memory.py`**](examples/acp/acp_e2e_memory.py) drives **`build/coddy`**, an isolated **`CODDY_HOME`**, and **`RPA_API_KEY`** to verify recall, persist, and optional prune of markdown under **`$CODDY_HOME/memory`**. See the script docstring for flags. Overview of all harnesses - [**`examples/README.md`**](examples/README.md).

## Persistent sessions

By default, `coddy acp` and `coddy http` store each session bundle under **`$CODDY_HOME/sessions/<sessionId>/`** (default **`~/.coddy/sessions/`**) with `session.json`, `messages.json`, an `assets/` directory, and `todos/active.md` (plus `todos/archive/` when completed lists are replaced). Override the root with **`coddy acp --sessions-dir`**, **`coddy http --sessions-dir`**, or **`sessions.dir`** in **`config.yaml`**. If the sessions directory cannot be created, startup fails with an error.

- **`coddy sessions list`** prints stored sessions (`--sessions-dir` and `--cwd` filters supported).
- **`coddy acp --session-id <id>`** makes the **next** `session/new` either reopen snapshots for that folder (if present) or create a fresh bundle whose directory name matches that id.
- **`session/load`** restores history and notifies the client; **`session/list`** lists bundles for ACP-aware clients.

The coddy todo tools keep the active checklist mirrored to `todos/active.md`. A wholesale **`coddy_todo_plan_replace`** while items are incomplete is rejected until you finish rows or run **`coddy_todo_plan_archive`**; replacing when every row is **`completed`** moves the prior `active.md` into **`todos/archive/`** (`todo-<nanos>.md`). **`coddy_todo_plan_archive`** finishes open rows to **`completed`**, writes **`todos/archive/plan_<unix_seconds>.md`**, then clears the session plan when persistence is on.

When the persisted plan is **non-empty**, the agent injects **`### Current todo checklist`** plus rendered markdown checklist lines into the system prompt template (embedded defaults, or files under **`prompts.dir`** using **`prompts.agent_prompt`** and **`prompts.plan_prompt`**, which default to **`agent.md`** and **`plan.md`**) via `{{if .TodoList}}` … `{{end}}`. That block is omitted when there is nothing to track. Before **each** LLM call inside one **`session/prompt`** turn, Coddy refreshes that system message so a todo list created or updated earlier in the same ReAct episode stays visible immediately.

## Development

```bash
# Run tests
go test ./...
make test

# Example harnesses (see examples/README.md): ./examples/build_coddy.sh && ./examples/test_acp.sh && ./examples/test_httpserver.sh

# Full-featured local binary (HTTP + UI + scheduler), same defaults as Docker
make build TAGS="http ui scheduler"

./build/coddy -v    # same as --version

# Run with debug logging (ACP mode); optional --log-output, --log-file, --log-format
coddy acp --log-level debug

# Single-line sanity check only (responses may omit JSON-RPC "result" for nil payloads; prefer examples/acp/acp_e2e_todo.py)
echo '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}' | coddy acp
```

## License

This project is licensed under the MIT License, see the [LICENSE](LICENSE) file in the repository root for details.
