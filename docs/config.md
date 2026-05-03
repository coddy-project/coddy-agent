# Configuration Reference

## Config File Location

The agent searches for `config.yaml` in the following order:

1. Path provided by `--config /path/to/config.yaml` flag
2. `$XDG_CONFIG_HOME/coddy-agent/config.yaml` (default: `~/.config/coddy-agent/config.yaml`)
3. `./config.yaml` in the current working directory

The `coddy acp` subcommand additionally accepts `--cwd` for the filesystem directory used when the ACP client omits `cwd` in `session/new`. Omitting `--cwd` uses the process working directory when the binary starts. See also `--sessions-dir`, `--session-id`, and `--disable-session` (no disk session bundles, e.g. cron) in the README.

## Full Configuration Schema

Agent name, title, and build version are not configurable here. They are fixed in the binary and reported during ACP `initialize` (`internal/acp` and `internal/version`).

```yaml
# LLM model configuration
models:
  # Default model used when no mode-specific override is set
  default: "openai/gpt-4o"

  # Per-mode model overrides
  agent_mode: "openai/gpt-4o"
  plan_mode: "anthropic/claude-3-5-sonnet"

  # Each entry in definitions appears as a selectable model in ACP clients (Session Config Options).
  # The session uses agent_mode / plan_mode / default until the user picks another model in the client.
  definitions:
    - id: "openai/gpt-4o"
      provider: "openai"
      model: "gpt-4o"
      api_key: "${OPENAI_API_KEY}"      # env var reference
      max_tokens: 8192
      temperature: 0.2
      base_url: ""                      # optional, for OpenAI-compatible APIs

    - id: "anthropic/claude-3-5-sonnet"
      provider: "anthropic"
      model: "claude-3-5-sonnet-20241022"
      api_key: "${ANTHROPIC_API_KEY}"
      max_tokens: 8192
      temperature: 0.2

    - id: "local/qwen"
      provider: "ollama"
      model: "qwen2.5-coder:14b"
      base_url: "http://localhost:11434"
      max_tokens: 4096
      temperature: 0.1

    - id: "custom/deepseek"
      provider: "openai_compatible"
      model: "deepseek-coder-v2"
      api_key: "${DEEPSEEK_API_KEY}"
      base_url: "https://api.deepseek.com/v1"
      max_tokens: 8192
      temperature: 0.1

# ReAct loop settings
react:
  max_turns: 30                # max LLM calls per prompt turn
  max_tokens_per_turn: 200000  # max tokens across all calls in one turn

# System prompt templates (agent.md and plan.md)
prompts:
  # Empty = use embedded defaults. Otherwise a directory containing those two files.
  #
  # Go text/template data (see internal/prompts/loader.go):
  #   {{.CWD}}      - session working directory
  #   {{.Tools}}    - markdown list of tool names and short descriptions for the current mode
  #   {{.Skills}}   - markdown block for active skills and rules (omit section when empty via {{if .Skills}})
  #   {{.TodoList}} - current session todo checklist as markdown lines (empty until create_todo_list / updates)
  #   {{.Memory}}   - session agent memory string
  #   {{.UTCNow}}   - date and time in UTC (RFC3339), refreshed whenever the system prompt is rendered
  #
  # Built-in templates order: Tools, Skills, optional TodoList block, Memory, trailing Current UTC time. The checklist section is emitted
  # only when the session plan is non-empty.
  dir: ""

# Skills and Cursor rules directories
skills:
  # Directories to search for skill files (SKILL.md)
  # Searched in order - first match wins for glob patterns
  dirs:
    - "~/.cursor/skills"
    - "~/.cursor/skills-cursor"
    - "${WORKSPACE}/.cursor/rules"   # WORKSPACE = session cwd
    - "${WORKSPACE}/.cursor/skills"

  # Explicitly include specific skill files regardless of path
  extra_files: []

# MCP servers available to all sessions (merged with per-session servers from client)
mcp_servers:
  - name: "filesystem"
    command: "npx"
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/user"]
    env: []

  # HTTP MCP server example
  # - type: "http"
  #   name: "my-api"
  #   url: "https://my-mcp-server.example.com/mcp"
  #   headers:
  #     - name: "Authorization"
  #       value: "Bearer ${MY_API_TOKEN}"

# Tool configuration
tools:
  # Require explicit user permission before running shell commands
  require_permission_for_commands: true

  # Require permission before writing files
  require_permission_for_writes: false

  # Working directory restriction: only allow operations within session cwd
  restrict_to_cwd: true

# Logging
log:
  level: "info"           # debug | info | warn | error
  file: ""                # empty = stderr only
```

## Environment Variable References

Any config value can reference environment variables using `${VAR_NAME}` syntax.
The agent resolves these at startup.

Special variables:
- `${WORKSPACE}` - replaced with the session working directory at runtime

## Model Provider Reference

### `openai`
Standard OpenAI API. Supports: `gpt-4o`, `gpt-4o-mini`, `gpt-4-turbo`, `o1`, `o3-mini`, etc.

Required fields: `api_key`, `model`

### `anthropic`
Anthropic API. Supports: `claude-3-5-sonnet-*`, `claude-3-5-haiku-*`, `claude-3-opus-*`

Required fields: `api_key`, `model`

### `ollama`
Local Ollama instance. Supports any model installed via `ollama pull`.

Required fields: `model`, `base_url` (default: `http://localhost:11434`)

### `openai_compatible`
Any API with OpenAI-compatible endpoints (DeepSeek, Together, Groq, LM Studio, etc.)

Required fields: `api_key`, `model`, `base_url`
