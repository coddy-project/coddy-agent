# `config.yaml` Field Reference

Field-by-field reference for `~/.coddy/config.yaml`. For narrative documentation (file discovery, `.env`, provider guides) see [config.md](config.md).

A machine-readable [JSON Schema](config.schema.json) accompanies this reference. Point your editor's YAML language server at it to get autocomplete and typo checking:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/coddy-project/coddy-agent/refs/heads/main/docs/config.schema.json
```

VS Code (with the YAML extension), IntelliJ, and Zed pick this comment up automatically. The schema is kept in sync with the Go config structs by `TestDocsConfigSchemaMatchesStructs` in `internal/config/docs_schema_test.go`.

Every field is optional unless marked **required**; an empty `config.yaml` (or none at all) is valid and uses built-in defaults. Any string value may reference environment variables with `${VAR_NAME}` (expanded when the file is loaded). To keep a **literal `$`** in a value (e.g. a secret like `$2y$10$…`), double it as `$$` — the UI does this automatically for the `proxy` fields. `${CODDY_HOME}` and `${CWD}` are expanded by the loader (see [config.md](config.md#environment-variable-references)).

## Agent self-configuration

Agent sessions expose a typed configuration tool family with staged, uci-like semantics:

- `config_get` reads a dotted path from the active YAML file. Secret-shaped fields (including `api_key_command`), MCP environment values, and HTTP header values are returned as `<redacted>`.
- `config_set` **stages** UCI-style commands (`set`, `add_list`, `del_list`, `delete`) without touching the file. Unknown schema paths and commands that would make the config invalid are rejected at staging time. Echoed command lists mask secret-shaped values as `<redacted>`; the staged store keeps the original values.
- `config_changes` lists the staged commands that a commit would apply (secrets redacted).
- `config_commit` applies the staged batch: validates, snapshots the previous file to `config.yaml.prev` (an empty document when the config file did not exist yet, so the first commit stays reversible), writes atomically, and hot-reloads skills, rules, built-in tools, and configured MCP servers. Because a commit can start MCP processes and change the permission policy itself, it prompts for tool permission in both `ask` and `accept_edits` modes - only `tools.permission_mode: bypass` skips the dialog - and the prompt lists the staged commands with secrets redacted. The agent is additionally instructed to ask the user to confirm saving first. If runtime reload fails, the file is restored and the staged commands are kept; if even that restore fails, the staged list stays consumed so a blind retry cannot replay it.
- `config_revert` discards staged commands (all of them, or those under one path).
- `config_rollback` restores the pre-commit snapshot over the active file (swapping the two, so a second rollback undoes the first) and hot-reloads. It carries the same permission policy as `config_commit`, and the agent warns the user before calling it.

Commands and paths are dotted like OpenWrt's `uci` CLI, with a selector for named sequence entries:

| Command | Meaning |
|---|---|
| `set agent.max_turns=40` | Set a mapping field |
| `set mcp_servers[name=context7]={"command":"npx"}` | Select a sequence object by scalar field; append it when setting if absent |
| `add_list skills.dirs=/opt/skills` | Append a sequence entry |
| `del_list skills.dirs=/opt/skills` | Remove a matching sequence entry |
| `delete mcp_servers[name=context7]` | Delete a field or entry |
| `skills.dirs.0` (path form) | Sequence index |

The root path (`.` or `/`) is read-only. Values are JSON for objects and arrays; string-typed fields take the literal text. Staged commands persist in the session bundle, so they survive restarts and HTTP permission resumes.

The bundled `/configure-coddy` skill teaches the agent this syntax, the confirm-then-commit workflow, and the safe discovery/install workflow for MCP servers and skills; it also carries the agent-facing catalog of configuration areas and must be updated together with this reference on any schema change. Process-level listener changes may still require restarting the relevant command; the hot reload is specifically guaranteed for the current session's agent configuration, skills, rules, built-in tools, and global MCP clients.

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
| [`mcp`](#mcp) | object | Trust policy for project-local MCP declarations | — |
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
| `type` | string | **yes** | — | — | Wire protocol: `openai`, `anthropic`, `neuraldeep`, or `codex`. Use `openai` for configurable OpenAI-compatible endpoints (DeepSeek, Groq, Ollama, llama.cpp, LM Studio); `neuraldeep` uses NeuralDeep's OpenAI-compatible endpoint, selected from its two official deployments with `api_base`; `codex` uses ChatGPT OAuth against the official Codex backend (Responses API). |
| `api_base` | string | no | provider SDK default | — | Base URL override. For `type: openai` include `/v1` (e.g. `http://localhost:11434/v1`); for `type: anthropic` an Anthropic-compatible gateway. For `type: neuraldeep` it selects the deployment - `https://api.neuraldeep.ru/v1` (Russia, the default) or `https://api.neuraldeep.tech/v1` (the international mirror) - and any other value falls back to the default; the choice also decides which hub `coddy providers login` and the SPA sign-in talk to. Ignored for `type: codex`, which always uses a fixed official endpoint. |
| `api_key` | string | no | `""` | `NAME_API_KEY` | Literal secret or `"${ENV}"` reference. Empty reads `NAME_API_KEY` at LLM call time (NAME = provider name uppercased, hyphens → underscores; e.g. `deepseek` → `DEEPSEEK_API_KEY`). For `type: neuraldeep`, when the key is empty from all three sources the key stored by `coddy providers login <name>` (`$CODDY_HOME/providers/<name>/neuraldeep-auth.json`) is used - an explicit key always wins over the stored login. |
| `api_key_command` | string | no | `""` | — | Credential-helper command run via the detected host shell when `api_key` is empty (`pwsh` → `powershell` → `cmd` on Windows; `bash` → `sh` elsewhere); trimmed stdout becomes the key. Falls back to `NAME_API_KEY` on failure. |
| `proxy` | string | no | direct | — | Per-provider outbound proxy: `http://`, `https://`, `socks5://`, or `socks5h://` URL. Treated as a literal URL (no `${VAR}` references); a `$` in the userinfo is auto-escaped to `$$` when saved via the UI. |
| `timeout_ms` | int | no | `0` | — | Bound on each LLM HTTP request to this provider, including the streamed body read. `0` sets no client timeout, so slow prompt processing on large contexts is never cut short; the turn context stays the only bound. A client timeout is not retried. |

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
  - name: codex
    type: codex # use Sign In with ChatGPT in the bundled web UI; no api_key needed
```

### llama.cpp as an OpenAI-compatible provider

`llama-server` works as a `type: openai` provider (`api_base: "http://host:8080/v1"`). Recommended launch flags:

- `--jinja` — enables the model's chat template on the server, which is required for **tool calling**. Without it llama.cpp silently ignores the `tools` parameter and the agent loop degrades to plain text answers.
- `-c <n>` — set the context window large enough for an agent prompt (system prompt plus tool schemas plus history; 16k is a practical minimum, more is better). When a request exceeds the server context, llama.cpp reports `the request exceeds the available context size` — raise `-c` or trim `max_context_tokens`.

llama.cpp builds through 2025 report mid-stream failures with a non-standard SSE `error:` field; Coddy understands both that dialect and the current `data: {"error": ...}` shape and surfaces the server's message in the error.

For `type: codex`, open **Settings → LLM Providers** in the bundled web UI and select **Sign In with ChatGPT**, or run the terminal equivalent for ACP and headless setups:

```bash
coddy codex login    # device flow: prints a URL and a one-time code, then waits
coddy codex status   # shows whether a credential is available and where it came from
coddy codex logout   # removes the Coddy-managed credential (leaves the Codex CLI login alone)
```

Both paths use the same storage; `--provider NAME` targets a specific codex provider when `config.yaml` defines several. Coddy uses the official device authorization flow and stores refreshable credentials at `$CODDY_HOME/providers/<provider-name>/codex-auth.json` with restrictive file permissions; tokens never enter `config.yaml`. `api_key`, `api_key_command`, and `api_base` are ignored for Codex, while `proxy` applies to OAuth and provider requests. The model picker reads the catalog from the official Codex backend with the saved token. If no Coddy-managed credential exists, Coddy remains compatible with a Codex CLI login in `~/.codex/auth.json` (or `$CODEX_HOME/auth.json`). Codex requests always target the official backend; the process-level `CODDY_CODEX_BASE_URL` is the only override (tests and self-hosted gateways), so a settings document can never redirect an OAuth token on its own.

Codex is only a model backend: the agent keeps Coddy's own system prompt, tool catalog, permissions, and ReAct loop, and the ChatGPT credential is used solely to authenticate the Responses calls (`features/codex_auth.feature` pins this on both the HTTP and ACP surfaces).

**Token lifetime.** The access token is refreshed transparently shortly before it expires, and the refreshed tokens are written back to the file they came from. When the credential is the Codex CLI login, that file is `~/.codex/auth.json` itself - the same file the `codex` CLI reads, so both tools keep working off one login, and a refresh performed by Coddy is visible to the CLI (and vice versa). A Coddy-managed credential is refreshed in place under `$CODDY_HOME/providers/<name>/` and never touches the CLI login. Signing out (`coddy codex logout`, or **Sign Out** in Settings) removes only the Coddy-managed file.

**Startup report.** When at least one `type: codex` provider is configured, `coddy acp` and `coddy http` log one `codex credential` line per provider at startup: where the credential came from and how long the access token is still valid. A missing credential, an unusable `auth_mode`, or an expired token with no refresh token left is logged as a **warning** naming `coddy codex login`; an expired but refreshable token is only an informational line, since the next request renews it. Setups without a codex provider log nothing.

**Reasoning.** The Codex backend serves `gpt-5*` model ids but accepts only `none`, `low`, `medium`, `high`, and `xhigh`, so codex-backed models offer **`none`** where other providers offer `minimal` (an explicit `reasoning_levels: [minimal]` is remapped as well). Reasoning turns request summaries (`summary: auto`) so thinking streams into the UI, and encrypted reasoning (`include: reasoning.encrypted_content`) so the model's own chain of thought is replayed verbatim on the next request of the same turn - the same flow the Codex CLI uses. Replayed reasoning is tagged with the model that produced it and is skipped when the session switches models. The items are stored opaquely in `messages.json` (`reasoning_signature`, ~1 KB per assistant turn) and are not exposed by `GET /coddy/sessions/{id}/messages`.

## `models`

List of logical models (`[]config.ModelEntry`, `internal/config/models.go`).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `model` | string | **yes** | — | `"provider_name/api_model_id"`. First segment must match a `providers[].name`; the remainder is sent to the API verbatim (may contain slashes). |
| `max_tokens` | int | no | `0` | Completion-token cap per assistant message. Ignored by `codex`, whose backend rejects `max_output_tokens`. |
| `temperature` | float | no | `0` | Sampling temperature. |
| `max_context_tokens` | int | no | `0` | UI hint for the context bar; `0` derives from provider metadata. |
| `multimodal` | bool | no | `false` | Model accepts image/file inputs; UI shows an attachment button. |
| `stream` | bool | no | `true` | Transport. Omitted or `true` streams the answer over SSE. `false` sends one blocking completion request and delivers the whole answer at once. Rejected for `type: codex` providers, whose backend is streaming-only. |
| `reasoning_levels` | string list | no | auto-detected | Override the offered reasoning levels. Omitted: auto-detect from the model id (`gpt-5*` → `minimal,low,medium,high`; OpenAI o-series, `gpt-oss*`, `qwen3*`, and Claude extended-thinking models → `low,medium,high`). Explicit `[]` hides the selector. Both states survive a Settings save: the key is omitted from the written YAML when unset rather than serialized as `[]`. Settings → Logical models → **Fetch reasoning levels** fills this list from `GET /coddy/config/reasoning-levels`. |
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
  - model: "local/qwen3-30b"
    max_tokens: 8192
    stream: false
```

**Non-streaming models.** `stream: false` changes one thing: coddy sends a single blocking `POST /chat/completions` instead of asking for SSE, and hands the finished answer to the rest of the runtime in one piece. Everything downstream is unchanged, so the transcript, tool calls, and session bundle look the same; what differs is that nothing appears until the model is done, and the thinking row shows no live duration. Two consequences are worth knowing before turning it on. Pressing **Stop** during a blocking call loses the whole answer, because the server has sent nothing yet, whereas a streamed turn keeps the tokens that already arrived. And a client asking for an SSE stream still gets one - the switch governs the connection to the LLM, not the connection to the client - which is why a streaming HTTP response now carries a keepalive comment every 15 s so proxies do not drop a turn that stays silent for minutes.

The switch is rejected for `type: codex` providers. The Codex Responses backend has no blocking mode, so honoring the key there would mean streaming anyway and only pretending not to.

## `agent`

ReAct loop settings (`config.Agent`, `internal/config/agent.go`).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `model` | string | required when `models` is non-empty | — | Default `models[].model` id until the client overrides per session. |
| `max_turns` | int | no | `30` | Max LLM calls per prompt turn. |
| `max_tokens_per_turn` | int | no | `200000` | Max tokens across all calls in one turn. |
| `llm_retry_max` | int | no | `3` | Retries after retryable LLM errors (e.g. HTTP 429). An explicit `0` disables retries. |
| `llm_retry_base_ms` | int | no | `1000` | Initial backoff between retries, ms. A server-provided pause (`Retry-After-Ms` / `Retry-After` headers, `Limit resets at` / `retry in Ns` body phrases) overrides the exponential backoff, capped at 60s. |
| `llm_min_interval_ms` | int | no | `0` | Minimum gap between consecutive LLM calls, ms, retry attempts included (e.g. `12000` on strict free tiers). |
| `llm_first_token_timeout_ms` | int | no | `90000` | How long a streamed LLM call may stay silent before the turn cancels it (the API hang guard). An explicit `0` disables the guard; blocking (`stream: false`) transports are never guarded. |
| `loop_guard` | bool | no | `true` | Runaway-loop protection: cut a response that degenerates into repeating itself, block a tool called over and over with identical arguments. |
| `loop_tool_repeat_limit` | int | no | `3` | Consecutive identical tool calls before the guard steps in; `0` disables the check. |
| `loop_stream_repeat_cycles` | int | no | `5` | Identical back-to-back output cycles in one streamed response before it is cut; `0` disables the check. |
| `loop_nudge_max` | int | no | `2` | Nudges the guard sends before it stops the turn with a notice. |

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
| `sources` | string list | no | `[]` | Remote skill sources installed on demand with `coddy skills sync` (never fetched automatically). Each entry is a GitHub repo (`owner/repo[@ref]`), a git URL, or an http(s) URL to an agents-standard `marketplace.json`. Cloned/copied into `${CODDY_HOME}/skills/`, then picked up like any local skill. See [skills.md](skills.md). |
| `auto_discovery` | bool | no | `true` | Offer the model-driven `load_skill` tool so the agent pulls a catalogued skill's full instructions into a turn on its own when the request matches, instead of requiring an explicit `/name`. Set `false` to keep skills manual-only. |

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
| `type` | string | no | `stdio` | Transport: `stdio` (local command), `http` (streamable HTTP to `url`, with automatic legacy-SSE fallback), or `sse` (legacy HTTP+SSE). Url-only entries default to `http`. |
| `name` | string | **yes** | — | Stable unique id. |
| `command` | string | stdio only | — | Executable for stdio transport. |
| `args` | string list | no | `[]` | Argv after `command`. `${CWD}` expands to the session cwd. |
| `env` | list of `{name, value}` | no | `[]` | Extra environment variables for the stdio child process. |
| `url` | string | http/sse only | — | HTTP(S) endpoint for `type: http` or `type: sse`. `${CWD}` expands to the session cwd. |
| `headers` | list of `{name, value}` | no | `[]` | Headers sent with MCP HTTP requests (e.g. `Authorization`). |
| `disabled` | bool | no | `false` | Skip connecting this server without removing its definition. |
| `disabled_tools` | string list | no | `[]` | Tool names of this server hidden from the agent. |

```yaml
mcp_servers:
  - name: filesystem
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/user"]
    disabled_tools: ["write_file"]
```

Servers can also be declared in Cursor-compatible mcp.json files: the user-global
`~/.coddy/mcp.json` (like Cursor's `~/.cursor/mcp.json`; together with this
`mcp_servers` list it forms the "global" scope) and the project-local
`<workspace>/.coddy/mcp.json` ("local" scope). Each file holds a single
`mcpServers` object keyed by server name (`env` and `headers` are JSON objects;
per-tool switches use `disabledTools`). Later levels override earlier ones by
name: `mcp_servers` < `~/.coddy/mcp.json` < `./.coddy/mcp.json`. Entries from the
project-local file need a workspace approval before they are started — see
[`mcp`](#mcp) and `docs/mcp-integration.md`.

## `mcp`

MCP settings that are not tied to a single server entry (`config.MCP`, `internal/config/mcp.go`).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `project_trust` | string | no | `ask` | Trust policy for the project-local `<workspace>/.coddy/mcp.json`, which travels with the checkout and therefore picks the command a session would start. `ask` — its servers stay cold until the operator approves that exact declaration for that workspace; `allow` — start them automatically (workspaces you already trust); `deny` — never load them, with no approval path. Overridable per process with `coddy acp --mcp-project-trust <value>` / `coddy http --mcp-project-trust <value>`. |

Added for [issue #80](https://github.com/coddy-project/coddy-agent/issues/80).
Approvals are recorded in `~/.coddy/mcp-trust.json`, keyed by the canonical workspace path
and a digest of the command-bearing declaration (transport, command, args, env, url,
headers), so rewriting an approved entry asks again. Approve with `coddy mcp trust <name>`,
`POST /coddy/mcp/{name}/trust`, or the shield button in **Settings → MCP servers**. That tab
also edits this policy itself (`POST /coddy/mcp/project-trust`), so it is not rendered as a
separate settings section. See `docs/mcp-integration.md`.

## `tools`

Permission policy (`config.Tools`, `internal/config/tools.go`).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `permission_mode` | string | no | `ask` | `ask` — prompt for commands and file writes; `accept_edits` — auto-approve writes, prompt for commands; `bypass` — never ask (trusted environments only). Overridable per session via ACP `session/set_config_option`. |
| `command_allowlist` | string list | no | `[]` | Commands that never require permission. Exact or prefix match (prefix + space + args). `"*"` allows everything. |
| `ssh_connect_timeout` | int | no | `30` | TCP dial timeout in seconds for the `ssh_run_command` tool. |
| `output_limits` | object | no | — | Per-tool ceilings on how many lines a result or error may contribute to the LLM context, plus a byte safety ceiling while enabled. See below. |
| `background` | object | no | — | Bounds for commands the agent runs detached in the session background task pool. See below. |

### `tools.output_limits`

Maximum lines a tool result or error may return into the LLM context (`config.ToolOutputLimits`). Every enabled line limit also applies a hard **64 KiB per-call byte ceiling**, preventing a minified file, base64 payload, or one-line MCP response from bypassing the guard. `0` disables both limits for that tool; an unset field falls back to the built-in default. Truncated output ends with a marker telling the model how to fetch the rest (`offset`/`limit` for `read`, a narrower pattern for `grep`, `page` for `websearch`).

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `read` | int | no | `1000` | Lines for a `read` file page or directory listing. |
| `grep` | int | no | `200` | `path:line:content` records from `grep`. |
| `glob` | int | no | `300` | Paths from `glob`. |
| `print_tree` | int | no | `400` | Lines of a directory tree. |
| `run_command` | int | no | `500` | Combined stdout+stderr lines of a shell command. |
| `ssh_run_command` | int | no | `500` | Combined stdout+stderr lines of a remote SSH command. |
| `webfetch` | int | no | `800` | Lines of fetched page markdown. |
| `websearch` | int | no | `200` | Lines of search results. |
| `default` | int | no | `1000` | Applies to any tool not named above, including MCP tools. `0` means unlimited. |

### `tools.background`

Bounds for background execution (`config.ToolBackground`). A backgrounded `run_command` returns a task id instead of output; `background_list`, `background_output`, `background_wait`, and `background_stop` collect the result later. The pool lives inside the running `coddy` process: each task mirrors its metadata and captured output into the session bundle under `background/<task_id>/`, and a task interrupted by a restart is reported as `orphaned` rather than as still running. `0` on any integer field means "use the default". See `docs/background-tasks.md`.

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `enabled` | bool | no | `true` | Offer the `background` option on `run_command` and expose the background task tools. |
| `max_concurrent` | int | no | `5` | Background tasks one session may run at once. Starting past the limit is refused, not queued. |
| `default_timeout_seconds` | int | no | `900` | Hard limit for a task started without an explicit `timeout_seconds` and without `expected_seconds`. |
| `max_timeout_seconds` | int | no | `3600` | Ceiling applied to any requested or estimate-derived timeout. |
| `output_buffer_bytes` | int | no | `262144` | In-memory output window per task, used by the status ticker and `background_output`. The full log still goes to the session bundle. |

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
| `result_eviction` | object | no | — | Prunes superseded `read`/`grep` results from the LLM projection (the persisted transcript is never rewritten). See below. |

### `compaction.result_eviction`

Collapses unmarked `read`/`grep` tool results to short placeholders when building the LLM request (`config.ResultEviction`), so paging a large file or running a wide search cannot pin dead lines in every later turn. A result survives when the model marks it (the `keep_result` tool, or `keep: true` on the call) or when it is inside the most recent working window; a filesystem write to a file invalidates earlier reads/greps that covered it. The persisted session bundle keeps every result in full.

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `enabled` | bool | no | `true` | Master switch for read/grep result eviction. |
| `keep_recent` | int | no | `2` | How many most recent evictable results (read pages, grep dumps) stay intact as a working window. `0` keeps none. The default of `2` keeps a read *and* a grep live at once; with `1`, a model comparing two results keeps re-fetching whichever the other evicted. |
| `min_result_bytes` | int | no | `2000` | Results at or below this size are never evicted. `0` makes every result a candidate. |

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
