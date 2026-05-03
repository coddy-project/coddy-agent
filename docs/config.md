# Configuration Reference

## Config File Location and Paths

Resolved locations use environment variables and flags (see README). In short:

- **`CODDY_HOME`** - agent state directory. Default **`~/.coddy`**. Holds `config.yaml`, `sessions/`, and `skills/`.
- **`CODDY_CWD`** - default filesystem cwd when `session/new` sends an empty `cwd`. Default is the process working directory at startup. Same meaning as the **`--cwd`** flag when set.
- **`CODDY_CONFIG`** - explicit path to `config.yaml`. Same as **`--config`**.

If no **`--config`** is given, the loader reads **`$CODDY_HOME/config.yaml`** when that file exists. Otherwise it falls back in order to **`~/.coddy/config.yaml`**, **`~/.config/coddy-agent/config.yaml`**, then **`./config.yaml`**. If none exist, built-in defaults apply (no error).

The `coddy acp` subcommand also accepts **`--home`** (override `CODDY_HOME`), **`--sessions-dir`**, **`--session-id`**, and **`--disable-session`**. Optional **`sessions.dir`** in the YAML overrides the sessions root when **`--sessions-dir`** is not set (default **`$CODDY_HOME/sessions`**).

## Full Configuration Schema

Agent name, title, and build version are not configurable here. They are fixed in the binary and reported during ACP `initialize` (`internal/acp` and `internal/version`).

```yaml
# LLM model configuration (Go: config.ModelsConfig, internal/config/models.go)
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

# ReAct loop settings (Go: config.React, internal/config/react.go)
react:
  max_turns: 30                # max LLM calls per prompt turn
  max_tokens_per_turn: 200000  # max tokens across all calls in one turn

# System prompt templates (agent.md and plan.md)
prompts:
  # Empty = use embedded defaults. Otherwise a directory containing those two files.
  #
  # Go text/template data. Fields in internal/prompts/loader.go. YAML shape is config.Prompts in internal/config/prompts.go.
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

# Session bundle storage (Go: config.Sessions, internal/config/sessions.go)
sessions:
  # Empty = default $CODDY_HOME/sessions. Supports ${CODDY_HOME} and ~ in path.
  dir: ""

# Skills directories (Go: config.Skills, internal/config/skills.go)
skills:
  # Directories to search for skill files (SKILL.md, rules as .md)
  # Searched in order. When omitted, defaults are
  # ${CODDY_HOME}/skills, ${CWD}/.skills, ~/.cursor/skills, ~/.claude/skills
  dirs:
    - "${CODDY_HOME}/skills"
    - "${CWD}/.skills"
    - "~/.cursor/skills"
    - "~/.claude/skills"

# MCP servers available to all sessions (Go: []config.MCPServerConfig, internal/config/mcp_servers.go)
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

# Tool configuration (Go: config.Tools, internal/config/tools.go)
tools:
  # Require explicit user permission before running shell commands
  require_permission_for_commands: true

  # Require permission before writing files
  require_permission_for_writes: false

  # Working directory restriction: only allow operations within session cwd
  restrict_to_cwd: true

# Logging (Go: config.Logger, internal/config/logger.go)
logger:
  level: "info"           # debug | info | warn | error
  # Where records go: any combination of stdout, stderr, file. Omitted or empty = stderr only.
  outputs: []
  # Path for the file sink; required when outputs includes file.
  file: ""
  # text (default) or json
  format: "text"
  rotation:
    max_size_mb: 0        # 0 = no size-based rotation
    max_files: 0          # rotated backups to keep when max_size_mb > 0
```

ACP flags override the same knobs when set: **`--log-level`**, **`--log-output`** (stdout, stderr, file, both), **`--log-file`**, **`--log-format`**. Empty flag values keep the YAML (or built-in) defaults.

If the older two-field style had **`file`** set under **`logger`** but no **`outputs`**, the loader expands to **`stderr`** plus **`file`** so file logging takes effect.

## Environment Variable References

Any config value can reference environment variables using `${VAR_NAME}` syntax.
The agent resolves these at startup.

Special variables in YAML (before parse) and in path strings:

- **`${CODDY_HOME}`** - resolved `CODDY_HOME` directory
- **`${CWD}`** in **`skills.dirs`** is resolved at skill load time using the **session** working directory (ACP `session/new` cwd)

Inside the raw config file body, **`${CWD}`** and **`${CODDY_HOME}`** are expanded using the process **`CODDY_CWD`** and **`CODDY_HOME`** when the file is read. For paths that must follow the session cwd, leave **`${CWD}`** in **`skills.dirs`** so it is not baked in at parse time (defaults do this when **`dirs`** is empty).

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
