# Configuration Reference

This page is the narrative guide. Two companion artifacts cover the full key list:

- **[config-reference.md](config-reference.md)** - field-by-field tables: type, default, env-var fallback, required/optional, examples.
- **[config.schema.json](config.schema.json)** - JSON Schema (draft-07) for editor autocomplete and validation. Add this header line to your `config.yaml` and any editor with a YAML language server (VS Code YAML extension, IntelliJ, Zed) validates keys and values as you type:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/coddy-project/coddy-agent/main/docs/config.schema.json
```

The schema is kept in sync with the Go config structs by `TestDocsConfigSchemaMatchesStructs` (`internal/config/docs_schema_test.go`); CI fails when a config field is added or renamed without updating the schema.

## Config File Location and Paths

Resolved locations use environment variables and flags (see README). In short:

- **`CODDY_HOME`** - agent state directory. Default **`~/.coddy`**. Holds `config.yaml`, `sessions/`, `skills/`, and **`scheduler/`** when using the optional cron scheduler.
- **`CODDY_CWD`** - default filesystem cwd when `session/new` sends an empty `cwd`. Default is the process working directory at startup. Same meaning as the **`--cwd`** flag when set.
- **`CODDY_CONFIG`** - explicit path to `config.yaml`. Same as **`--config`**.

If no **`--config`** is given, the loader uses **`$CODDY_HOME/config.yaml`** (default home **`~/.coddy`**). If that file is missing, it tries **`config.yaml`** in the process current working directory (**`$CWD`** at startup). If neither file exists, built-in defaults apply (no error).

When the primary file exists but is invalid (YAML parse or validation error), the loader automatically recovers from **`config.yaml.bak`** in the same directory (see **`internal/config/recovery.go`**). After every successful load the server writes **`config.yaml.bak`**. The HTTP **`PUT /coddy/config`** route (see **`docs/http-api.md`**) also snapshots the current file to **`config.yaml.bak`** before overwriting, so a failed reload can be rolled back.

The `coddy acp` subcommand also accepts **`--home`** (override `CODDY_HOME`), **`--sessions-dir`**, and **`--session-id`**. Optional **`sessions.dir`** in the YAML overrides the sessions root when **`--sessions-dir`** is not set (default **`$CODDY_HOME/sessions`**).

## Full Configuration Schema

Agent name, title, and build version are not configurable here. They are fixed in the binary and reported during ACP `initialize` (`internal/acp` and `internal/version`).

```yaml
# LLM backends (Go: []config.ProviderConfig, internal/config/providers.go)
# Each providers[].name must match ^[a-zA-Z][a-zA-Z0-9_-]*$ (ASCII letter first, then letters, digits, hyphen, underscore).
# api_key may be a literal, "${ENV}" expanded when the file loads, or empty to read NAME_API_KEY at LLM call time
# (NAME is the provider name in uppercase with hyphens mapped to underscores, for example rpa -> RPA_API_KEY).
# api_key_command (optional): when api_key is empty, this command is run via the shell and its trimmed stdout is
# used as the key (credential helper, like git/docker helpers or AWS credential_process). It lets a provider fetch
# short-lived or login-issued keys without storing a static secret. On failure resolution falls back to NAME_API_KEY.
# Resolution order: literal api_key -> api_key_command stdout -> NAME_API_KEY env.
providers:
  - name: "openai"
    type: "openai"
    api_key: "${OPENAI_API_KEY}"
    # api_base: ""                    # optional override for OpenAI-compatible base URL
    # api_key_command: "my-cli print-token"  # optional credential helper; used when api_key is empty
    # proxy: "http://127.0.0.1:8888"   # optional per-provider HTTP(S) or SOCKS5/SOCKS5h proxy

  - name: "anthropic"
    type: "anthropic"
    api_key: "${ANTHROPIC_API_KEY}"

  - name: "neuraldeep"
    type: "neuraldeep"
    api_key: "${NEURALDEEP_API_KEY}"

  - name: "local"
    type: "openai"
    api_base: "http://localhost:11434/v1"
    api_key: "~"

  - name: "deepseek"
    type: "openai"
    api_base: "https://api.deepseek.com/v1"
    api_key: "${DEEPSEEK_API_KEY}"

# Logical models (Go: []config.ModelEntry, internal/config/models.go).
# Each model value is "provider_name/api_model_id". The first path segment must match providers[].name.
# The same string is the ACP model selector and agent.model default.
models:
  - model: "openai/gpt-4o"
    max_tokens: 8192
    temperature: 0.2
    multimodal: true              # accepts images/files; UI shows file attachment button

  - model: "anthropic/claude-3-5-sonnet-20241022"
    max_tokens: 8192
    temperature: 0.2
    multimodal: true

  - model: "openai/gpt-5"
    max_tokens: 8192
    reasoning_default: medium     # level pre-selected for new chats (composer reasoning selector)
    # reasoning_levels: [low, high]  # optional override of offered levels; [] hides the selector

  - model: "local/qwen2.5-coder:14b"
    max_tokens: 4096
    temperature: 0.1

  - model: "deepseek/deepseek-coder-v2"
    max_tokens: 8192
    temperature: 0.1

  - model: "neuraldeep/default"
    max_tokens: 8192
    temperature: 0.2

# ReAct loop settings (Go: config.Agent, internal/config/agent.go)
agent:
  model: "openai/gpt-4o"       # required when models is non-empty; default LLM until the client overrides per session
  max_turns: 30                # max LLM calls per prompt turn
  max_tokens_per_turn: 200000  # max tokens across all calls in one turn
  llm_retry_max: 3             # retries after HTTP 429 and similar errors (default 3)
  llm_retry_base_ms: 1000      # initial backoff between LLM retries
  llm_min_interval_ms: 0       # min gap between consecutive LLM calls; e.g. 12000 on strict free tiers

# System prompt templates
prompts:
  # Empty dir = use embedded defaults. Otherwise a directory containing the files named below.
  #
  # Go text/template data. Fields in internal/prompts/loader.go. YAML shape is config.Prompts in internal/config/prompts.go.
  #   {{.CWD}}      - session working directory
  #   {{.Tools}}    - markdown list of tool names and short descriptions for the current mode
  #   {{.Skills}}   - markdown block for active skills (omit section when empty via {{if .Skills}})
  #   {{.TodoList}} - current session todo checklist as markdown lines (empty until coddy todo tools update state)
  #   {{.Memory}}   - session agent memory plus optional long-term recall when memory.enabled is true
  #   {{.UTCNow}}   - date and time in UTC (RFC3339), refreshed whenever the system prompt is rendered
  #
  # Built-in templates order: Tools, Skills, optional TodoList block, Memory (session notes plus optional recall), trailing Current UTC time.
  # The checklist section is emitted only when the session plan is non-empty.
  dir: ""
  agent_prompt: "agent.md"     # optional; default agent.md
  plan_prompt: "plan.md"       # optional; default plan.md

# Session bundle storage (Go: config.Sessions, internal/config/sessions.go)
sessions:
  # Empty = default $CODDY_HOME/sessions. Supports ${CODDY_HOME} and ~ in path.
  dir: ""

# Optional long-term memory copilot (Go: config.MemoryConfig, internal/config/memory.go; logic in external/memory).
# Implementation is always linked; enable at runtime with memory.enabled.
memory:
  enabled: false
  # Exact id from models[]. Used only for recall and persist tool-calling passes, not for the main assistant model.
  # Example: "rpa/gpt-oss:120b". Empty means fall back to agent.model / session override.
  model: ""
  dir: "" # long-term memory root; empty = $CODDY_HOME/memory. Supports ${CODDY_HOME} and ~ when set.
  recall_max_turns: 6
  persist_max_turns: 12
  copilot_max_tokens: 4096
  max_search_hits: 8

# Skills directories (Go: config.Skills, internal/config/skills.go)
skills:
  # Directories to search for SKILL.md and optional root .md/.mdc skill files.
  # Later entries have HIGHER priority: if the same skill name appears in multiple
  # directories, the version from the last matching directory wins.
  # Default dirs (lowest → highest priority):
  #   ~/.agents/skills          - global skills, shared with npx skills / npx skillsbd
  #   ${CODDY_HOME}/skills      - coddy-specific; may contain symlinks to ~/.agents/skills
  #   ${CWD}/.coddy/skills      - project-local; overrides everything above
  # ${CODDY_HOME} and ${CWD} expand at runtime (per-session cwd for ${CWD}).
  dirs:
    - "~/.agents/skills"
    - "${CODDY_HOME}/skills"
    - "${CWD}/.coddy/skills"

# Project rules (Go: config.Rules, internal/config/rules.go)
# Discovered from .coddy/rules, .cursor/rules, .claude/rules, .codex/rules,
# and nested **/AGENTS.md under session CWD.
# Injected into {{.Rules}} in the system prompt (separate from skills). See docs/rules.md.
rules:
  auto_discover: true
  systems: []   # optional: coddy, cursor, claude, codex, agents

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
  # Controls when the agent asks for user approval before running tools.
  # ask          - always prompt for commands and file writes (default)
  # accept_edits - auto-approve file writes; prompt for shell commands
  # bypass       - never ask for permission (use only in trusted environments)
  # Overridable per session via ACP session/set_config_option with configId "permission_mode".
  permission_mode: ask

  # TCP dial timeout for SSH connections in seconds (default: 30).
  # ssh_connect_timeout: 30

# HTTP OpenAI gateway (only with go build -tags=http). Embedded SPA on / needs -tags=http,ui too. See docs/http-api.md
# httpserver:
#   host: "127.0.0.1"
#   port: 8080

# Cron scheduler (only with go build -tags=scheduler). UTC crontab; flat *.md jobs under scheduler.dir.
# scheduler:
#   enabled: false
#   dir: ""
#   max_queue: 10
#   timeout: "30m"
#   retain_sessions: 5  # max completed run session dirs kept per job_id (default 5)

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

## SSH remote execution

The built-in `ssh_run_command` tool lets the agent run commands on remote hosts over SSH — no external `ssh` binary required (pure-Go via `golang.org/x/crypto/ssh`). The only configurable knob is `tools.ssh_connect_timeout` (TCP dial timeout, default 30 s).

**Authentication order:**
1. **SSH agent** — if `SSH_AUTH_SOCK` is set and reachable, the agent is used first. This covers YubiKeys, 1Password SSH agent, gpg-agent, and standard `ssh-agent` setups.
2. **Key files** — Coddy always looks in the current OS user's `~/.ssh` directory. Key names tried in order: `id_ed25519`, `id_rsa`, `id_ecdsa`, `id_dsa`. Keys protected by a passphrase are silently skipped.

Both sources are active simultaneously — if the agent is available and has keys, files still act as a fallback if the agent declines.

**Host key verification** — derived automatically from `tools.permission_mode`:
- Any mode except `bypass` **(default)** — new hosts are added to `~/.ssh/known_hosts` automatically on first connect (TOFU); if a known host's key has changed, the old entry is replaced with the new one.
- `bypass` — host key verification is disabled (suitable for ephemeral VMs or CI environments).

**Tool schema** — `ssh_run_command` accepts:
| Field | Type | Required | Description |
|---|---|---|---|
| `host` | string | yes | `user@hostname` — user is required |
| `command` | string | yes | Shell command to run on the remote host |
| `port` | integer | no | SSH port (default: 22) |
| `timeout_seconds` | integer | no | Command timeout in seconds |
| `permission_rationale` | string | no | Text shown in the permission dialog |

The tool requires user permission (same as `run_command`) and returns combined stdout + stderr.

## HTTP gateway (optional build)

The **`httpserver`** key (`config.HTTPServerConfig` in `internal/config/http.go`) is ignored unless you use a binary built with **`-tags http`**. It sets default **`host`** and **`port`** when **`coddy http`** is still at the built-in flag defaults (`0.0.0.0` and `12345`). See **`docs/http-api.md`**.

## Scheduler (optional build)

The **`scheduler`** key (`config.SchedulerConfig` in `internal/config/scheduler.go`) is used only when you build with **`-tags scheduler`**. Set **`scheduler.enabled: true`** in YAML or pass **`coddy acp -scheduler-enabled`** / **`coddy http -scheduler-enabled`** to set **`scheduler.enabled`** for that process without editing the config file.

Jobs are flat **`*.md`** files under **`scheduler.dir`** (default **`${CODDY_HOME}/scheduler`** when **`dir`** is empty). Each file has YAML frontmatter with **`description`**, **`schedule`** (five cron fields, **UTC**), optional **`cwd`** (defaults to the directory where **`coddy`** was started), **`model`**, **`mode`** (`agent` or `plan`), optional **`paused`** (when true, cron and manual run are skipped until resume). The markdown body is the one-shot instruction for the sub-agent. Sidecars **`basename.state`** (last fired slot) and **`basename.lock`** (run in progress) sit next to **`basename.md`**.

**`retain_sessions`** (default **5**) caps how many **completed** scheduler-run session directories are kept per **`job_id`** under **`sessions.dir`**; older runs are pruned.

When the scheduler is effectively enabled, **`coddy_scheduler_*`** tools cover list or get, create or replace or patch, delete, pause or resume, manual run, cancel, and listing run metadata (**`coddy_scheduler_jobs_list`**, **`coddy_scheduler_job_get`**, **`coddy_scheduler_job_create`**, **`coddy_scheduler_job_replace`**, **`coddy_scheduler_job_patch`**, **`coddy_scheduler_job_delete`**, **`coddy_scheduler_job_pause`**, **`coddy_scheduler_job_resume`**, **`coddy_scheduler_job_run`**, **`coddy_scheduler_job_cancel`**, **`coddy_scheduler_job_runs`**). With **`-tags=http,scheduler`**, the same operations exist as REST under **`/coddy/scheduler`** (see **`docs/http-api.md`**).

## Messenger Gateway (`gateways`)

Requires a binary built with **`-tags gateway.telegram`** (Telegram only) or **`-tags gateway`** (all adapters). The `coddy gateway` subcommand reads this block.

```yaml
# Messenger gateways (external/gateway/; build with -tags gateway.telegram or -tags gateway).
# Full guide: docs/gateway.md
gateways:
  telegram:
    # Set to true to activate the Telegram adapter when coddy gateway starts.
    enabled: false

    # Bot token from @BotFather. Never hard-code; always use an env reference.
    token: "${TELEGRAM_BOT_TOKEN}"

    # Optional outbound proxy for Telegram API requests (http, https, socks5, socks5h).
    # proxy: "socks5h://127.0.0.1:1080"

    # Telegram user IDs with admin privileges.
    # Admins bypass every access check and can always interact with the bot.
    admins: []
    # Example:
    # admins: [98874093]

    # Default access level for chats without a per-chat override.
    #   "all"          - anyone who can write to the chat
    #   "admins"       - only user IDs listed in admins
    #   "group:<name>" - only users in the named user_groups entry (admins always pass)
    default_access: "all"

    # Default session isolation mode for group chats without a per-chat override.
    #   "individual"   - each group member gets their own session
    #   "shared"       - all members share one session
    #   "admin"        - only admins can interact; all admins share one session
    default_isolation: "individual"

    # Named sets of user IDs for group-based access control.
    user_groups: []
    # Example:
    # user_groups:
    #   - name: "devs"
    #     user_ids: [111222333, 444555666]

    # Per-chat overrides. chat_id is negative for groups and supergroups.
    chats: []
    # Example:
    # chats:
    #   - chat_id: -1001234567890
    #     isolation: "individual"
    #     access: "all"
    #   - chat_id: -1009876543210
    #     isolation: "admin"
    #     access: "admins"
```

`token` is validated at startup when `enabled: true`. `proxy` is optional (empty = direct connection). The other fields apply defaults if omitted: `default_access: "all"`, `default_isolation: "individual"`.

See **[docs/gateway.md](gateway.md)** for the full configuration guide, running instructions, and how to add adapters for other messengers.

## `.env` file

If **`$CODDY_HOME/.env`** exists, it is read at startup **before** `config.yaml` is parsed. This lets you keep all secrets in one place without touching shell profiles or Docker compose environment blocks.

```sh
# ~/.coddy/.env
OPENAI_API_KEY=sk-...
ANTHROPIC_API_KEY=sk-ant-...
TELEGRAM_BOT_TOKEN=8992982910:AAF...
```

Then in `config.yaml` reference them as usual:

```yaml
providers:
  - name: openai
    type: openai
    api_key: "${OPENAI_API_KEY}"
```

**Rules:**

- Variables that are **already set** in the process environment are **never overridden** — the process environment always wins. `.env` is a fallback only.
- A missing `.env` is silently ignored (not an error).
- The file is resolved relative to the effective `CODDY_HOME` (`~/.coddy` by default, or the path from `--home` / `CODDY_HOME` env var).

**Supported syntax:**

| Line form | Example |
|-----------|---------|
| `KEY=value` | `OPENAI_API_KEY=sk-abc` |
| `export KEY=value` | `export DEBUG=true` |
| Double-quoted value | `MSG="hello world"` |
| Single-quoted value | `PATH='no escape \n here'` |
| Escape sequences in `"…"` | `NOTE="line1\nline2"` → real newline |
| Inline comment (unquoted) | `KEY=val # this is ignored` |
| Comment line | `# full-line comment` |

Values already in the process environment (e.g. set by the shell, Docker, systemd) take priority and are never changed by `.env`.

## Environment Variable References

Any config value can reference environment variables using `${VAR_NAME}` syntax.
The agent resolves these at startup.

Special variables in YAML (before parse) and in path strings:

- **`${CODDY_HOME}`** - resolved `CODDY_HOME` directory
- **`${CWD}`** in **`skills.dirs`** is resolved at skill load time using the **session** working directory (ACP `session/new` cwd)

Inside the raw config file body, **`${CWD}`** and **`${CODDY_HOME}`** are expanded using the process **`CODDY_CWD`** and **`CODDY_HOME`** when the file is read. For paths that must follow the session cwd, leave **`${CWD}`** in **`skills.dirs`** so it is not baked in at parse time (defaults do this when **`dirs`** is empty).

## Model Provider Reference

Provider **`type`** values match **`internal/llm.NewProvider`**: **`openai`**, **`anthropic`**, **`neuraldeep`**.

YAML split:

- **`providers`**: **`name`** (unique), **`type`**, **`api_key`**, optional **`api_base`** (base URL override for the provider SDK: an OpenAI-compatible endpoint or Ollama host without **`/v1`** for **`type: openai`**, or an Anthropic-compatible gateway/relay for **`type: anthropic`**; ignored by **`type: neuraldeep`**, which always uses **`https://api.neuraldeep.ru/v1`**), optional **`proxy`** (per-provider outbound **`http://`**, **`https://`**, **`socks5://`**, or **`socks5h://`** URL; not a global default).
- **`models`**: **`model`** (string **`provider_name/api_model_id`**, session selector and **`agent.model`** value; first segment names **`providers[].name`**, remainder is the API model id), **`max_tokens`**, **`temperature`**, optional **`max_context_tokens`** (UI hint for context bar; 0 means derive from provider metadata), optional **`multimodal`** (boolean, default **`false`**; when **`true`** signals that the model accepts image/file inputs — the UI exposes a file attachment button in the composer for this model only), optional **`reasoning_levels`** (string list; overrides the reasoning levels offered for this model — when omitted they are auto-detected from the API model id: **`gpt-5*`** → **`minimal,low,medium,high`**, OpenAI **`o`**-series and Claude extended-thinking models → **`low,medium,high`**; an explicit empty list hides the composer reasoning selector), optional **`reasoning_default`** (the level pre-selected for new chats; must be one of the resolved levels). Reasoning levels map to OpenAI **`reasoning_effort`** and Anthropic extended-thinking **`budget_tokens`**.

### `openai`
Standard OpenAI API. Supports: `gpt-4o`, `gpt-4o-mini`, `gpt-4-turbo`, `o1`, `o3-mini`, etc.

Provider needs **`api_key`**. Optional **`proxy`** applies only to this provider row (HTTP, HTTPS, SOCKS5, or SOCKS5h). The **`models[].model`** string must start with this provider **`name`** and a slash, then the OpenAI API model id, for example **`openai/gpt-4o`**. Also set **`max_tokens`**, **`temperature`**.

### `anthropic`
Anthropic API. Supports: `claude-3-5-sonnet-*`, `claude-3-5-haiku-*`, `claude-3-opus-*`

Provider needs **`api_key`**. Optional **`api_base`** overrides the Anthropic API base URL (default **`https://api.anthropic.com`**), for example an Anthropic-compatible gateway or relay. Optional **`proxy`** applies only to this provider row. Use **`models[].model`** like **`anthropic/claude-3-5-sonnet-20241022`**, plus **`max_tokens`**, **`temperature`**.

### `neuraldeep`
NeuralDeep API via its OpenAI-compatible endpoint.

Provider needs **`api_key`**. **`api_base`** is not needed and is ignored; Coddy always sends requests to **`https://api.neuraldeep.ru/v1`**. Optional **`proxy`** applies only to this provider row. Use **`models[].model`** like **`neuraldeep/default`** or another NeuralDeep model id, plus **`max_tokens`**, **`temperature`**.

### Local OpenAI-compatible servers (Ollama, llama.cpp, LM Studio)
Use **`type: openai`** and set **`api_base`** to an OpenAI-compatible base URL that already includes **`/v1`**, for example **`http://localhost:11434/v1`** for Ollama.
