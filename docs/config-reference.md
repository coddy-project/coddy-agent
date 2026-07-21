# `config.yaml` Field Reference

Field-by-field reference for `~/.coddy/config.yaml`. For narrative documentation (file discovery, `.env`, provider guides) see [config.md](config.md).

A machine-readable [JSON Schema](config.schema.json) accompanies this reference. Point your editor's YAML language server at it to get autocomplete and typo checking:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/coddy-project/coddy-agent/main/docs/config.schema.json
```

VS Code (with the YAML extension), IntelliJ, and Zed pick this comment up automatically. The schema is kept in sync with the Go config structs by `TestDocsConfigSchemaMatchesStructs` in `internal/config/docs_schema_test.go`.

Every field is optional unless marked **required**; an empty `config.yaml` (or none at all) is valid and uses built-in defaults. Any string value may reference environment variables with `${VAR_NAME}` (expanded when the file is loaded). To keep a **literal `$`** in a value (e.g. a secret like `$2y$10$…`), double it as `$$` — the UI does this automatically for the `proxy` fields. `${CODDY_HOME}` and `${CWD}` are expanded by the loader (see [config.md](config.md#environment-variable-references)).

## Top-level keys

| Key | Type | Purpose | Build tag |
|---|---|---|---|
| [`providers`](#providers) | list | LLM API credentials and endpoints | — |
| [`models`](#models) | list | Logical model entries selectable per session | — |
| [`agent`](#agent) | object | ReAct loop model and safety caps | — |
| [`prompts`](#prompts) | object | System prompt template overrides | — |
| [`instructions`](#instructions) | object | Project instruction files (AGENTS.md) | — |
| [`skills`](#skills) | object | Skill discovery directories | — |
| [`rules`](#rules) | object | Project rules discovery | — |
| [`mcp_servers`](#mcp_servers) | list | MCP servers connected per session | — |
| [`tools`](#tools) | object | Permission policy for built-in tools | — |
| [`logger`](#logger) | object | Log level, outputs, rotation | — |
| [`sessions`](#sessions) | object | Session bundle storage | — |
| [`compaction`](#compaction) | object | Context compaction (history summarization) | — |
| [`memory`](#memory) | object | Long-term memory copilot | `memory` |
| [`httpserver`](#httpserver) | object | OpenAI-compatible HTTP API defaults | `http` |
| [`scheduler`](#scheduler) | object | Cron scheduler | `scheduler` |
| [`gateways`](#gateways) | object | Messenger bot adapters | `gateway` / `gateway.telegram` |

"Build tag" means the block only takes effect in binaries built with that `-tags` value; it is parsed and ignored otherwise.

## `providers`

List of LLM backends (`[]config.ProviderConfig`, `internal/config/providers.go`).

| Field | Type | Required | Default | Env fallback | Description |
|---|---|---|---|---|---|
| `name` | string | **yes** | — | — | Logical id used as the first segment of `models[].model`. Must match `^[a-zA-Z][a-zA-Z0-9_-]*$`. |
| `type` | string | **yes** | — | — | Wire protocol: `openai`, `anthropic`, or `neuraldeep`. Use `openai` for configurable OpenAI-compatible endpoints (DeepSeek, Groq, Ollama, llama.cpp, LM Studio); `neuraldeep` uses NeuralDeep's fixed OpenAI-compatible endpoint. |
| `api_base` | string | no | provider SDK default | — | Base URL override. For `type: openai` include `/v1` (e.g. `http://localhost:11434/v1`); for `type: anthropic` an Anthropic-compatible gateway. Ignored for `type: neuraldeep`, which always uses `https://api.neuraldeep.ru/v1`. |
| `api_key` | string | no | `""` | `NAME_API_KEY` | Literal secret or `"${ENV}"` reference. Empty reads `NAME_API_KEY` at LLM call time (NAME = provider name uppercased, hyphens → underscores; e.g. `deepseek` → `DEEPSEEK_API_KEY`). |
| `api_key_command` | string | no | `""` | — | Credential-helper command run via the detected host shell when `api_key` is empty (`pwsh` → `powershell` → `cmd` on Windows; `bash` → `sh` elsewhere); trimmed stdout becomes the key. Falls back to `NAME_API_KEY` on failure. |
| `proxy` | string | no | direct | — | Per-provider outbound proxy: `http://`, `https://`, `socks5://`, or `socks5h://` URL. Treated as a literal URL (no `${VAR}` references); a `$` in the userinfo is auto-escaped to `$$` when saved via the UI. |

Key resolution order: `api_key` → `api_key_command` stdout → `NAME_API_KEY` env var.

```yaml
providers:
  - name: openai
    type: openai
    api_key: "${OPENAI_API_KEY}"
  - name: local
    type: openai
    api_base: "http://localhost:11434/v1"
    api_key: "~"
  - name: neuraldeep
    type: neuraldeep
    api_key: "${NEURALDEEP_API_KEY}"
```

## `models`

List of logical models (`[]config.ModelEntry`, `internal/config/models.go`).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `model` | string | **yes** | — | `"provider_name/api_model_id"`. First segment must match a `providers[].name`; the remainder is sent to the API verbatim (may contain slashes). |
| `max_tokens` | int | no | `0` | Completion-token cap per assistant message. |
| `temperature` | float | no | `0` | Sampling temperature. |
| `max_context_tokens` | int | no | `0` | UI hint for the context bar; `0` derives from provider metadata. |
| `multimodal` | bool | no | `false` | Model accepts image/file inputs; UI shows an attachment button. |
| `reasoning_levels` | string list | no | auto-detected | Override the offered reasoning levels. Omitted: auto-detect from the model id (`gpt-5*` → `minimal,low,medium,high`; o-series and Claude thinking models → `low,medium,high`). Explicit `[]` hides the selector. |
| `reasoning_default` | string | no | — | Level pre-selected for new chats; must be one of the resolved levels. |

```yaml
models:
  - model: "openai/gpt-4o"
    max_tokens: 8192
    temperature: 0.2
    multimodal: true
  - model: "openai/gpt-5"
    max_tokens: 8192
    reasoning_default: medium
```

## `agent`

ReAct loop settings (`config.Agent`, `internal/config/agent.go`).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `model` | string | required when `models` is non-empty | — | Default `models[].model` id until the client overrides per session. |
| `max_turns` | int | no | `30` | Max LLM calls per prompt turn. |
| `max_tokens_per_turn` | int | no | `200000` | Max tokens across all calls in one turn. |
| `llm_retry_max` | int | no | `3` | Retries after retryable LLM errors (e.g. HTTP 429). |
| `llm_retry_base_ms` | int | no | `1000` | Initial backoff between retries, ms. |
| `llm_min_interval_ms` | int | no | `0` | Minimum gap between consecutive LLM calls, ms (e.g. `12000` on strict free tiers). |

## `prompts`

System prompt template overrides (`config.Prompts`, `internal/config/prompts.go`). Template fields are documented in [config.md](config.md#full-configuration-schema).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `dir` | string | no | `""` (embedded templates) | Directory with Go text/template files. Supports `~` and `${CWD}` (session cwd at render time). |
| `agent_prompt` | string | no | `agent.md` | Template file name for agent mode, inside `dir`. |
| `plan_prompt` | string | no | `plan.md` | Template file name for plan mode, inside `dir`. |

## `instructions`

Project instruction files (`config.Instructions`, `internal/config/instructions.go`).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `files` | string list | no | `["AGENTS.md"]` | Filenames relative to the session CWD, read and appended to the system prompt. |

## `skills`

Skill discovery (`config.Skills`, `internal/config/skills.go`).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `dirs` | string list | no | `["~/.agents/skills", "${CODDY_HOME}/skills", "${CWD}/.coddy/skills"]` | Directories scanned for skills. Later entries have **higher** priority on name conflicts. `${CODDY_HOME}` and `${CWD}` expand at runtime (per-session cwd for `${CWD}`). |

## `rules`

Project rules discovery (`config.Rules`, `internal/config/rules.go`). See [rules.md](rules.md).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `auto_discover` | bool | no | `true` | Scan `.coddy/rules`, `.cursor/rules`, `.claude/rules`, `.codex/rules` under the session CWD. |
| `systems` | string list | no | `[]` (all) | Restrict which rule systems are loaded: `coddy`, `cursor`, `claude`, `codex`. |

## `mcp_servers`

MCP servers connected for every new session (`[]config.MCPServerConfig`, `internal/config/mcp_servers.go`).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `type` | string | no | `stdio` | Transport: `stdio` (local command) or `http` (remote endpoint). |
| `name` | string | **yes** | — | Stable unique id. |
| `command` | string | stdio only | — | Executable for stdio transport. |
| `args` | string list | no | `[]` | Argv after `command`. `${CWD}` expands to the session cwd. |
| `env` | list of `{name, value}` | no | `[]` | Extra environment variables for the stdio child process. |
| `url` | string | http only | — | HTTP(S) endpoint for `type: http`. |
| `headers` | list of `{name, value}` | no | `[]` | Headers sent with MCP HTTP requests (e.g. `Authorization`). |

```yaml
mcp_servers:
  - name: filesystem
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/user"]
```

## `tools`

Permission policy (`config.Tools`, `internal/config/tools.go`).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `permission_mode` | string | no | `ask` | `ask` — prompt for commands and file writes; `accept_edits` — auto-approve writes, prompt for commands; `bypass` — never ask (trusted environments only). Overridable per session via ACP `session/set_config_option`. |
| `command_allowlist` | string list | no | `[]` | Commands that never require permission. Exact or prefix match (prefix + space + args). `"*"` allows everything. |
| `ssh_connect_timeout` | int | no | `30` | TCP dial timeout in seconds for the `ssh_run_command` tool. |

## `logger`

Logging (`config.Logger`, `internal/config/logger.go`). ACP flags `--log-level`, `--log-output`, `--log-file`, `--log-format` override these when set.

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `level` | string | no | `info` | `debug`, `info`, `warn`, `error` (`warning` accepted as alias of `warn`). |
| `outputs` | string list | no | `["stderr"]` | Any combination of `stdout`, `stderr`, `file`. |
| `file` | string | required when `outputs` includes `file` | `""` | Path for the file sink. Supports `${CODDY_HOME}`. |
| `format` | string | no | `text` | `text` or `json`. |
| `rotation.max_size_mb` | int | no | `0` | Rotate after this size in MB; `0` disables size-based rotation. |
| `rotation.max_files` | int | no | `0` | Rotated backups to keep when `max_size_mb > 0`. |

## `sessions`

Session bundle storage (`config.Sessions`, `internal/config/sessions.go`).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `dir` | string | no | `""` → `${CODDY_HOME}/sessions` | Sessions root. Supports `${CODDY_HOME}` and `~`. Overridden by the `--sessions-dir` flag. |

## `compaction`

Context compaction (`config.Compaction`, `internal/config/compaction.go`): summarizing older conversation history so long sessions keep fitting the model context window. Applies to the manual compact command and the automatic threshold trigger.

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `enabled` | bool | no | `true` | Master switch for compaction (manual command and automation). |
| `threshold_percent` | int | no | `80` | Auto-compaction fires when the estimated context usage reaches this percent of the effective model's `max_context_tokens` (valid `1..100`). Models without `max_context_tokens` skip auto-compaction; the manual command still works. |
| `keep_recent_turns` | int | no | `2` | How many most recent user turns (each with the agent replies and tool activity after it) stay verbatim; older history is folded into the summary. `0` summarizes the whole window. |
| `model` | string | no | `""` (session model) | Exact `models[].model` id used for the summarization call. |

## `memory`

Long-term memory copilot (`config.MemoryConfig`, `internal/config/memory.go`; implementation in `external/memory`, `memory` build tag).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `enabled` | bool | no | `false` | Turn on the memory copilot. |
| `model` | string | no | `""` (agent model) | Exact `models[].model` id used only for recall/persist LLM calls. |
| `dir` | string | no | `""` → `${CODDY_HOME}/memory` | Long-term memory root. Supports `${CODDY_HOME}` and `~`. |
| `recall_max_turns` | int | no | `6` | Bounds recall-side LLM rounds. |
| `persist_max_turns` | int | no | `12` | Bounds persist-side LLM rounds. |
| `copilot_max_tokens` | int | no | `4096` | Completion cap for memory LLM calls. |
| `max_search_hits` | int | no | `8` | Max snippets returned by `memory_search`. |

## `httpserver`

OpenAI-compatible HTTP API defaults (`config.HTTPServerConfig`, `internal/config/http.go`; `http` build tag). See [http-api.md](http-api.md).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `host` | string | no | `""` → `0.0.0.0` | Default bind address when `coddy http` does not pass `-H/--host`. |
| `port` | int | no | `0` → `12345` | Default listen port when `coddy http` does not pass `-P/--port`. Range 0–65535. |
| `auth_token` | string | no | `""` | Optional bearer credential. Empty means no auth (historical default). Enables auth on `/v1/*` and `/coddy/*`. Supports `${ENV}`. Never returned by `GET /coddy/config`. Prefer `--auth-token` / `CODDY_HTTP_TOKEN`. |
| `public_docs` | bool | no | `false` | When auth is enabled, keep `/docs` and `/openapi.*` reachable without a token. |
| `allow_insecure` | bool | no | `false` | Silence the startup warning about a non-loopback bind without authentication. |
| `cors.enabled` | bool | no | `false` | Handle CORS preflight and emit `Access-Control-*` headers so a browser UI on another origin can call this API. |
| `cors.allowed_origins` | []string | no | `[]` | Exact origins allowed to call the API (e.g. `http://localhost:12345`). A single `*` allows any origin; bearer auth still applies. |
| `remotes[].name` | string | yes* | - | Display label for a remote server offered in the UI environment selector (*required per entry). |
| `remotes[].url` | string | yes* | - | Base URL of a remote `coddy http` server (*required per entry). Tokens are kept client-side, not here. |

## `ui`

Embedded web UI (`config.UIConfig`, `internal/config/ui.go`; only meaningful with `-tags http,ui`).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `enabled` | bool | no | `true` | Serve the embedded SPA at `GET /`. Set `false` to run an API-only server (the API still requires `httpserver.auth_token` when configured). Unset means enabled. |

## `scheduler`

Cron scheduler (`config.SchedulerConfig`, `internal/config/scheduler.go`; `scheduler` build tag). Job file format is described in [config.md](config.md#scheduler-optional-build).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `enabled` | bool | no | `false` | Run the scheduler daemon and expose `coddy_scheduler_*` tools. `coddy acp\|http -scheduler-enabled` forces it per process. |
| `dir` | string | no | `""` → `${CODDY_HOME}/scheduler` | Directory with flat `*.md` job definitions. |
| `max_queue` | int | no | `10` | Concurrent scheduled runs; extra firings are skipped when saturated. |
| `timeout` | string | no | `"30m"` | Per-run wall-clock limit (Go duration, e.g. `1h30m`). |
| `retain_sessions` | int | no | `5` | Completed run session dirs kept per `job_id` under `sessions.dir`. |

## `gateways`

Messenger gateways (`config.GatewayConfig`, `internal/config/gateway.go`; `gateway` or `gateway.telegram` build tag; run with `coddy gateway`). See [gateway.md](gateway.md).

### `gateways.telegram`

| Field | Type | Required | Default | Env fallback | Description |
|---|---|---|---|---|---|
| `enabled` | bool | no | `false` | — | Activate the Telegram adapter. |
| `token` | string | no | `""` | `TELEGRAM_BOT_TOKEN` | Bot token from @BotFather. Empty reads the env var (e.g. via `~/.coddy/.env`). |
| `proxy` | string | no | direct | — | Outbound proxy: `http`, `https`, `socks5`, `socks5h`. Treated as a literal URL (no `${VAR}` references); a `$` in the userinfo is auto-escaped to `$$` when saved via the UI. |
| `rich_messages` | bool | no | `false` | — | Bot API 10.1 Rich Messages; falls back to legacy formatting when unsupported. |
| `admins` | int list | no | `[]` | — | Telegram user IDs with elevated rights; always pass access checks. |
| `default_access` | string | no | `all` | — | `all`, `admins`, or `group:<name>`. |
| `default_isolation` | string | no | `individual` | — | `individual`, `shared`, or `admin`. |
| `user_groups` | list | no | `[]` | — | Named sets: `{name, user_ids}`. Referenced as `group:<name>`. |
| `chats` | list | no | `[]` | — | Per-chat overrides: `{chat_id, isolation, access}`. `chat_id` is negative for groups/supergroups. |

## Related environment variables

These control config discovery itself, not individual fields (see [config.md](config.md#config-file-location-and-paths)):

| Variable | Flag equivalent | Meaning |
|---|---|---|
| `CODDY_HOME` | `--home` | Agent state directory (default `~/.coddy`). |
| `CODDY_CWD` | `--cwd` | Default session working directory. |
| `CODDY_CONFIG` | `--config` | Explicit path to `config.yaml`. |
| `NAME_API_KEY` | — | Per-provider API key fallback (see [`providers`](#providers)). |
| `TELEGRAM_BOT_TOKEN` | — | Telegram bot token fallback (see [`gateways.telegram`](#gatewaystelegram)). |
