# Coddy Agent

A **ReAct (Reasoning + Acting)** AI coding agent written in Go, compatible with any
[Agent Client Protocol (ACP)](https://agentclientprotocol.com/) editor such as Cursor, Zed,
or any other ACP client.

## Features

- **ReAct loop** - LLM alternates between thinking, acting (tool calls), and observing results
- **Two operating modes** - `agent` (full tool access) and `plan` (planning + text files only)
- **Cursor rules support** - reads `.cursor/rules/` and skills just like Cursor IDE
- **MCP server integration** - connect any MCP server for additional tools
- **Multi-provider LLM** - OpenAI, Anthropic, Ollama, any OpenAI-compatible API
- **ACP protocol** - works with Cursor, Zed, and other ACP-compatible editors

## Quick Start

### Installation

```bash
go install github.com/EvilFreelancer/coddy-agent/cmd/coddy@latest
```

Or build and install manually from source:

```bash
git clone https://github.com/EvilFreelancer/coddy-agent
cd coddy-agent
make install
```

`make install` builds the binary and copies it to the appropriate location:
- root - `/usr/local/bin/coddy`
- regular user - `~/.local/bin/coddy`

To only build without installing:

```bash
make build
# or manually:
go build -ldflags "-X github.com/EvilFreelancer/coddy-agent/internal/version.Version=$(make -s print-version)" -o coddy ./cmd/coddy/
```

After `make build` the binary is `build/coddy`. If another `coddy` is already on your `PATH`, a plain `coddy acp` runs that older install. Use `./build/coddy acp`, run `make install`, or compare with `which coddy` and `coddy -v`.

The agent speaks ACP over stdio. Editors launch `coddy` for you once it is configured. `coddy -v` or `coddy --version` prints the embedded build version (`dev` if not set at link time - see `-ldflags` in the build command above). Flags for ACP itself live on the subcommand, for example `coddy acp --help` for `--log-level`, `--cwd`, and `--config`.

`--cwd` sets the filesystem working directory used when `session/new` sends an empty `cwd` field. If you omit `--cwd`, the agent uses the process current directory (`os.Getwd()` at startup). Editors that pass a workspace path in `session/new` continue to use that path.

### Configuration

Copy the example config and edit it:

```bash
mkdir -p ~/.config/coddy-agent
cp config.example.yaml ~/.config/coddy-agent/config.yaml
```

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

The agent reads skill files and cursor rules from:

1. `{project}/.cursor/rules/` - project-specific rules
2. `{project}/.cursor/skills/` - project-specific skills
3. `~/.cursor/skills/` - global user skills
4. `~/.cursor/skills-cursor/` - cursor-specific skills

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
models:
  default: "openai/gpt-4o"
  agent_mode: "openai/gpt-4o"
  plan_mode: "anthropic/claude-3-5-sonnet"

  definitions:
    - id: "openai/gpt-4o"
      provider: "openai"
      model: "gpt-4o"
      api_key: "${OPENAI_API_KEY}"

react:
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

- [Architecture](docs/architecture.md) - system design and component overview
- [ACP Protocol](docs/acp-protocol.md) - protocol reference and message formats
- [ReAct Agent](docs/react-agent.md) - ReAct loop design and tool specifications
- [Configuration](docs/config.md) - full config file reference
- [Skills & Rules](docs/skills.md) - cursor rules and skills guide
- [MCP Integration](docs/mcp-integration.md) - MCP server integration guide

## Examples (ACP over stdio)

Python harnesses under [**`examples/acp-jsonrpc-session/`**](examples/acp-jsonrpc-session/) show newline-delimited JSON-RPC against **`coddy acp`** ( **`stdbuf -oL`**, permission auto-reply, nil-result responses). Use them as reference when building your own minimal client rather than chaining naive **`echo`** lines into a pipe.

## Persistent sessions

By default, `coddy acp` stores each session bundle under **`$HOME/coddy-agent/sessions/<sessionId>/`** with `session.json`, `messages.json`, an `assets/` directory, and `todos/active.md` (plus `todos/archive/` when completed lists are replaced). Override the root with **`coddy acp --sessions-dir /path/to/sessions`**. If `$HOME` is unavailable, persistence is skipped and logged.

Use **`coddy acp --disable-session`** to avoid writing any bundle (in-memory only, e.g. cron or one-shot). The agent does not advertise **`session/load`** or **`session/list`** in that mode.

- **`coddy sessions list`** prints stored sessions (`--sessions-dir` and `--cwd` filters supported).
- **`coddy acp --session-id <id>`** makes the **next** `session/new` either reopen snapshots for that folder (if present) or create a fresh bundle whose directory name matches that id.
- **`session/load`** restores history and notifies the client; **`session/list`** lists bundles for ACP-aware clients.

The todo tools keep the active checklist mirrored to `todos/active.md`. Creating a **new** list while items are incomplete is rejected until you finish items or run **`clean_todo_list`**; replacing an **all-completed** list archives the prior `active.md` into **`todos/archive/`**.

When the persisted plan is **non-empty**, the agent injects **`### Current todo checklist`** plus rendered markdown checklist lines into the embedded (or **`prompts.dir`**) **`agent.md` / `plan.md`** templates (`{{if .TodoList}}` … `{{end}}`). That block is omitted when there is nothing to track. Before **each** LLM call inside one **`session/prompt`** turn, Coddy refreshes that system message so a todo list created or updated earlier in the same ReAct episode stays visible immediately.

## Development

```bash
# Run tests
go test ./...
make test

# Build binary (with git version embedded)
make build

coddy -v    # same as --version

# Run with debug logging (ACP mode)
coddy acp --log-level debug

# Single-line sanity check only (responses may omit JSON-RPC "result" for nil payloads; prefer examples/acp-jsonrpc-session/)
echo '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}' | coddy acp
```

## License

MIT
