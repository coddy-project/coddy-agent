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

The agent can propose switching to `agent` mode when ready to implement.

Best for: architecture planning, writing specs, design documents, code review.

Switch modes in your editor's mode selector, or the agent will offer to switch automatically.

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

# Test with a simple ACP client
echo '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}' | coddy acp
```

## License

MIT
