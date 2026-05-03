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
# LLM backends (Go: []config.ProviderConfig, internal/config/providers.go)
providers:
  - name: "openai"
    type: "openai"
    api_key: "${OPENAI_API_KEY}"
    # api_base: ""                    # optional override for OpenAI-compatible base URL

  - name: "anthropic"
    type: "anthropic"
    api_key: "${ANTHROPIC_API_KEY}"

  - name: "local"
    type: "ollama"
    api_base: "http://localhost:11434"
    api_key: ""

  - name: "deepseek"
    type: "openai_compatible"
    api_base: "https://api.deepseek.com/v1"
    api_key: "${DEEPSEEK_API_KEY}"

# Logical models (Go: []config.ModelEntry, internal/config/models.go).
# Each id appears as a selectable model in ACP clients. provider must match providers[].name.
models:
  - id: "openai/gpt-4o"
    provider: "openai"
    model: "gpt-4o"
    max_tokens: 8192
    temperature: 0.2

  - id: "anthropic/claude-3-5-sonnet"
    provider: "anthropic"
    model: "claude-3-5-sonnet-20241022"
    max_tokens: 8192
    temperature: 0.2

  - id: "local/qwen"
    provider: "local"
    model: "qwen2.5-coder:14b"
    max_tokens: 4096
    temperature: 0.1

  - id: "custom/deepseek"
    provider: "deepseek"
    model: "deepseek-coder-v2"
    max_tokens: 8192
    temperature: 0.1

# ReAct loop settings (Go: config.Agent, internal/config/agent.go)
agent:
  model: "openai/gpt-4o"       # required when models is non-empty; default LLM until the client overrides per session
  max_turns: 30                # max LLM calls per prompt turn
  max_tokens_per_turn: 200000  # max tokens across all calls in one turn

# System prompt templates
prompts:
  # Empty dir = use embedded defaults. Otherwise a directory containing the files named below.
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
  agent_prompt: "agent.md"     # optional; default agent.md
  plan_prompt: "plan.md"       # optional; default plan.md

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

Provider **`type`** values match **`internal/llm.NewProvider`**: **`openai`**, **`openai_compatible`**, **`anthropic`**, **`ollama`**.

YAML split:

- **`providers`**: **`name`** (unique), **`type`**, **`api_key`**, optional **`api_base`** (OpenAI-compatible base URL, Ollama host without **`/v1`**, etc.).
- **`models`**: **`id`** (session selector and **`agent.model`** value), **`provider`** (references **`providers[].name`**), **`model`** (API model id; omit to default to **`id`**), **`max_tokens`**, **`temperature`**.

### `openai`
Standard OpenAI API. Supports: `gpt-4o`, `gpt-4o-mini`, `gpt-4-turbo`, `o1`, `o3-mini`, etc.

Provider needs **`api_key`**. Model entry sets **`model`**, **`max_tokens`**, **`temperature`**.

### `anthropic`
Anthropic API. Supports: `claude-3-5-sonnet-*`, `claude-3-5-haiku-*`, `claude-3-opus-*`

Provider needs **`api_key`**. Model entry sets **`model`**, **`max_tokens`**, **`temperature`**.

### `ollama`
Local Ollama instance. Supports any model installed via `ollama pull`.

Provider **`api_base`**: host root, for example **`http://localhost:11434`** (default inside the client if empty). Model entry sets the Ollama model name in **`model`**.

### `openai_compatible`
Any API with OpenAI-compatible chat endpoints (DeepSeek, Together, Groq, LM Studio, etc.)

Provider needs **`api_base`** (for example **`https://api.deepseek.com/v1`**) and **`api_key`**. Model entry sets **`model`**, **`max_tokens`**, **`temperature`**.
