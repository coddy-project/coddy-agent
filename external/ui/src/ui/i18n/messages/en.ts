/** English UI strings for the Coddy SPA. Keys use dot notation grouped by area.
 *  English values are kept byte-identical to the former hardcoded literals so
 *  existing English-asserting tests keep passing when strings move behind t(). */
export const messagesEn: Record<string, string> = {
  "appearance.themeLabel": "Theme",
  "appearance.themeGroupAria": "Theme",
  "appearance.languageLabel": "Language",
  "appearance.locale.auto": "Auto",
  "appearance.locale.en": "English",
  "appearance.locale.ru": "Русский",
  "appearance.toggleLabel": "Appearance",
  "appearance.theme.dark": "Dark",
  "appearance.theme.light": "Light",
  "appearance.theme.midnight": "Midnight",
  "appearance.theme.solarizedDark": "Solarized Dark",
  "appearance.theme.monokai": "Monokai",
  "appearance.theme.nord": "Nord",
  "appearance.theme.rosePine": "Rosé Pine",

  "common.cancel": "Cancel",
  "common.confirm": "Confirm",
  "common.confirmAction": "Confirm action",
  "common.delete": "Delete",

  "confirm.session.deleteDraft.title": "Delete draft?",
  "confirm.session.deleteDraft.message":
    "This draft conversation will be removed.",
  "confirm.session.deleteChat.title": "Delete chat?",
  "confirm.session.deleteChat.message":
    "This conversation will be permanently deleted.",
  "confirm.scheduler.deleteJob.title": 'Delete scheduler job "{id}"?',
  "confirm.scheduler.deleteJob.message":
    "This scheduled job will be permanently deleted.",

  "settings.title": "Settings",
  "settings.aria.panel": "Settings",
  "settings.aria.close": "Close settings",
  "settings.backToSections": "Back to sections",
  "settings.lead":
    "Edit configuration from the live JSON schema. Secrets (API keys) are shown in full - use only on trusted networks.",
  "settings.loading": "Loading…",
  "settings.toast.saved": "Saved all sections. In-process config reloaded.",
  "settings.reload.title": "Reload from server",
  "settings.reload.aria": "Reload configuration from server",
  "settings.save.title": "Save all sections",
  "settings.save.aria": "Save all configuration sections",
  "settings.error.schemaLoadFailed": "schema",
  "settings.error.configLoadFailed": "config",
  "settings.error.validationFailed": "validation failed",
  "settings.error.saveFailed": "save failed ({status})",
  "settings.error.requestFailed": "request failed",
  "settings.error.failedToLoad": "Failed to load: {error}",
  "settings.error.unsupportedSchemaRoot":
    "Unsupported schema root (expected object).",
  "settings.error.skillsSchemaUnavailable": "Skills schema unavailable.",
  "settings.error.sectionSchemaUnavailable": "Section schema unavailable.",
  "settings.error.noItemSchema": "This section has no item schema.",

  "settings.section.appearance.label": "Appearance",
  "settings.section.providers.label": "LLM providers",
  "settings.section.models.label": "Logical models",
  "settings.section.agent.label": "ReAct agent",
  "settings.section.tools.label": "Tools and permissions",
  "settings.section.mcp_servers.label": "MCP servers",
  "settings.section.skills.label": "Skills",
  "settings.section.memory.label": "Long-term memory",
  "settings.section.system.label": "System",
  "settings.section.compaction.label": "Context compaction",
  "settings.section.subagents.label": "Subagents",
  "settings.section.appearance.desc": "Theme & color mode",
  "settings.section.providers.desc": "LLM API connections",
  "settings.section.models.desc": "Named model configs",
  "settings.section.agent.desc": "ReAct agent defaults",
  "settings.section.tools.desc": "Tool permissions & limits",
  "settings.section.mcp_servers.desc": "External MCP tools",
  "settings.section.skills.desc": "Installed slash skills",
  "settings.section.memory.desc": "Long-term memory options",
  "settings.section.system.desc": "Scheduler, logs, prompts",
  "settings.section.compaction.desc": "Conversation history compaction",
  "settings.section.subagents.desc": "Delegation pool & trust",

  "settings.nav.aria.scrollLeft": "Scroll sections left",
  "settings.nav.aria.scrollRight": "Scroll sections right",
  "settings.nav.aria.sections": "Settings sections",
  "settings.tileGrid.aria": "Settings sections",

  "settings.array.add": "Add",
  "settings.array.removeTitle": "Remove",
  "settings.array.removeAria": "Remove",
  "settings.array.removeRowAria": "Remove {name}",
  "settings.array.unnamed": "(unnamed #{n})",
  "settings.array.back": "Back to list",
  "settings.array.backTitle": "Back to list",
  "settings.array.empty": "Nothing here yet. Use Add to create one.",

  "settings.field.apiBaseFallback": "API base URL",
  "settings.field.modelIdFallback": "Model id",
  "settings.field.defaultModelFallback": "Default model",
  "settings.field.providerAria": "Provider",
  "settings.field.providerPlaceholder": "provider",
  "settings.field.modelPlaceholder": "provider/model-id",
  "settings.field.fetching": "Fetching…",
  "settings.field.fetchModels": "Fetch models",
  "settings.field.fetchError":
    "Couldn't fetch models: {error}. Type the model id manually below.",
  "settings.field.noModels":
    "No models returned. Type the model id manually below.",
  "settings.reasoning.levelsFallback": "Reasoning levels",
  "settings.reasoning.fetch": "Fetch reasoning levels",
  "settings.reasoning.fetching": "Fetching…",
  "settings.reasoning.useAuto": "Use auto-detected",
  "settings.reasoning.autoDetected":
    "Auto-detected from the model id. Fetch the levels to review or override them.",
  "settings.reasoning.overridden":
    "These exact levels are offered for this model, instead of the auto-detected ones.",
  "settings.reasoning.hidden":
    "Empty list: the reasoning selector is hidden for this model. Use 'Use auto-detected' to go back.",
  "settings.reasoning.noneDetected":
    "This model id has no auto-detected reasoning levels. Add them by hand if the provider offers any.",
  "settings.reasoning.fetchError":
    "Couldn't fetch reasoning levels: {error}. Add them by hand below.",

  "settings.field.apiKeyPlaceholder":
    "If empty, reads from {env} at run time, or set a literal key (YAML may use {varToken} at load)",
  "settings.field.apiKeyPlaceholderInvalid":
    "Provider id must start with a letter. When the id is valid, leave empty to read the NAME_API_KEY variable (NAME is uppercase, hyphens become underscores).",

  // Schema-driven settings fields: labels and descriptions rendered from the
  // server JSON Schema (internal/config/ui_schema.go), keyed
  // settings.schema.<section>.<dotted.field.path>.label / .desc and resolved by
  // settings/schemaI18n.ts. English mirrors the schema text so the default
  // locale renders exactly what the server sends. System-group children live
  // under settings.schema.system.<child>.<path>.
  "settings.schema.providers.desc":
    "API credentials and transport selection for upstream LLM vendors.",
  "settings.schema.providers.name.label": "Provider name",
  "settings.schema.providers.name.desc":
    "Logical id used in model ids (provider/model-id). ASCII letters, digits, hyphen, and underscore only; must start with a letter. When api_key is empty, the runtime reads the key from the environment variable NAME_API_KEY (NAME is this field in uppercase with hyphens mapped to underscores).",
  "settings.schema.providers.type.label": "Provider type",
  "settings.schema.providers.type.desc":
    "Wire protocol for this provider entry.",
  "settings.schema.providers.api_base.label": "API base URL",
  "settings.schema.providers.api_base.desc":
    "Optional override of the default API base URL for this provider. For neuraldeep it selects the deployment - https://api.neuraldeep.ru/v1 (Russia) or https://api.neuraldeep.tech/v1 (the international mirror) - and any other value falls back to the first; ignored for codex, which uses a fixed official endpoint.",
  "settings.schema.providers.api_key.label": "API key",
  "settings.schema.providers.api_key.desc":
    "You may set a literal key, reference ${ENV} in YAML (expanded when the file is loaded), or leave empty so the process reads the conventional NAME_API_KEY variable derived from the provider name (see provider name description).",
  "settings.schema.providers.api_key_command.label": "API key command",
  "settings.schema.providers.api_key_command.desc":
    "Optional credential-helper command. When api_key is empty it is run via the detected host shell (pwsh, powershell, or cmd on Windows; bash or sh elsewhere) and its trimmed stdout is used as the key (like git/docker credential helpers or AWS credential_process). On failure resolution falls back to the conventional NAME_API_KEY variable.",
  "settings.schema.providers.proxy.label": "HTTP or SOCKS proxy",
  "settings.schema.providers.proxy.desc":
    "Optional per-provider outbound proxy. Use http:// or https:// for an HTTP proxy, or socks5:// / socks5h:// for SOCKS5 (socks5h resolves hostnames via the proxy). Leave empty for a direct connection.",
  "settings.schema.providers.timeout_ms.label": "Request timeout ms",
  "settings.schema.providers.timeout_ms.desc":
    "Optional bound on each LLM HTTP request to this provider, including the streamed body read. 0 (the default) sets no client timeout.",

  "settings.schema.models.desc":
    "Named model entries the agent and UI can select; ids reference provider prefixes.",
  "settings.schema.models.model.label": "Model id",
  "settings.schema.models.model.desc":
    "Logical id in the form provider/api-model-id; must match a provider name prefix.",
  "settings.schema.models.max_tokens.label": "Max tokens",
  "settings.schema.models.max_tokens.desc":
    "Upper bound on completion tokens the model may emit for one assistant message. Ignored by Codex because its backend does not accept max_output_tokens.",
  "settings.schema.models.temperature.label": "Temperature",
  "settings.schema.models.temperature.desc":
    "Sampling temperature for this logical model (0 = deterministic, higher = more random).",
  "settings.schema.models.max_context_tokens.label":
    "Max context tokens (UI hint)",
  "settings.schema.models.max_context_tokens.desc":
    "Optional UI hint for composer context bar; 0 means derive from provider metadata when available.",
  "settings.schema.models.multimodal.label": "Multimodal",
  "settings.schema.models.multimodal.desc":
    "When true, the model accepts image or file inputs in addition to text. The UI will offer file attachment for messages sent with this model.",
  "settings.schema.models.reasoning_levels.label": "Reasoning levels",
  "settings.schema.models.reasoning_levels.desc":
    "Optional override of the reasoning levels offered for this model (e.g. low, medium, high). Leave empty to auto-detect from the model id; an explicit empty list hides the reasoning selector.",
  "settings.schema.models.reasoning_default.label": "Default reasoning level",
  "settings.schema.models.reasoning_default.desc":
    "Reasoning level pre-selected for new chats with this model. Must be one of the resolved reasoning levels; ignored otherwise.",
  "settings.schema.models.stream.label": "Stream responses",
  "settings.schema.models.stream.desc":
    "Leave on to receive the answer token by token over SSE. Turn off to send one blocking request and wait for the whole answer, for servers or proxies that handle event streams badly; the transcript then fills in at once instead of typing out. Not available for codex models, whose backend is streaming-only.",

  "settings.schema.agent.model.label": "Default model",
  "settings.schema.agent.model.desc":
    "Logical model id from the models list used when the client omits a model.",
  "settings.schema.agent.max_turns.label": "Max turns",
  "settings.schema.agent.max_turns.desc":
    "Hard cap on ReAct iterations (LLM calls plus tool rounds) for one user request.",
  "settings.schema.agent.max_tokens_per_turn.label": "Max tokens per turn",
  "settings.schema.agent.max_tokens_per_turn.desc":
    "Upper bound on total tokens (prompt + completion) the model may use in one agent step.",
  "settings.schema.agent.llm_retry_max.label": "LLM retry max",
  "settings.schema.agent.llm_retry_max.desc":
    "Retries after retryable LLM errors such as HTTP 429 before failing the turn (an explicit 0 disables retries).",
  "settings.schema.agent.llm_retry_base_ms.label": "LLM retry base ms",
  "settings.schema.agent.llm_retry_base_ms.desc":
    "Initial backoff between LLM retries in milliseconds; a server-provided pause (Retry-After) overrides it.",
  "settings.schema.agent.llm_min_interval_ms.label": "LLM min interval ms",
  "settings.schema.agent.llm_min_interval_ms.desc":
    "Minimum gap between consecutive LLM calls in milliseconds, retries included (0 disables pacing).",
  "settings.schema.agent.llm_first_token_timeout_ms.label":
    "LLM first token timeout ms",
  "settings.schema.agent.llm_first_token_timeout_ms.desc":
    "How long a streamed LLM call may stay silent before the turn cancels it (an explicit 0 disables the guard).",
  "settings.schema.agent.loop_guard.label": "Loop guard",
  "settings.schema.agent.loop_guard.desc":
    "Stop a response that degenerates into repeating itself, and block a tool called over and over with identical arguments.",
  "settings.schema.agent.loop_tool_repeat_limit.label":
    "Loop tool repeat limit",
  "settings.schema.agent.loop_tool_repeat_limit.desc":
    "Consecutive identical tool calls before the loop guard steps in (0 disables the check).",
  "settings.schema.agent.loop_stream_repeat_cycles.label":
    "Loop stream repeat cycles",
  "settings.schema.agent.loop_stream_repeat_cycles.desc":
    "Identical back-to-back output cycles inside one streamed response before it is cut (0 disables the check).",
  "settings.schema.agent.loop_nudge_max.label": "Loop nudge max",
  "settings.schema.agent.loop_nudge_max.desc":
    "How many times one turn may be nudged back on track before the loop guard stops it.",

  "settings.schema.tools.permission_mode.label": "Permission mode",
  "settings.schema.tools.permission_mode.desc":
    'Controls when the agent asks for user approval before running tools. "ask": approve commands and writes. "accept_edits": auto-approve writes, approve commands. "bypass": skip all prompts.',
  "settings.schema.tools.command_allowlist.label": "Command allowlist",
  "settings.schema.tools.command_allowlist.desc":
    "If non-empty, only these shell command prefixes may run without extra policy.",
  "settings.schema.tools.output_limits.label": "Tool output limits",
  "settings.schema.tools.output_limits.desc":
    "Maximum lines each tool result or error may return into the LLM context. Enabled limits also apply a 64 KiB per-call byte safety ceiling. 0 disables both limits; unset uses the built-in default.",
  "settings.schema.tools.output_limits.read.desc":
    "Max lines for a read file page or directory listing (default 1000).",
  "settings.schema.tools.output_limits.grep.desc":
    "Max path:line:content records from grep (default 200).",
  "settings.schema.tools.output_limits.glob.desc":
    "Max paths from glob (default 300).",
  "settings.schema.tools.output_limits.print_tree.desc":
    "Max lines of a directory tree (default 400).",
  "settings.schema.tools.output_limits.run_command.desc":
    "Max stdout+stderr lines of a shell command (default 500).",
  "settings.schema.tools.output_limits.ssh_run_command.desc":
    "Max stdout+stderr lines of a remote SSH command (default 500).",
  "settings.schema.tools.output_limits.webfetch.desc":
    "Max lines of fetched page markdown (default 800).",
  "settings.schema.tools.output_limits.websearch.desc":
    "Max lines of search results (default 200).",
  "settings.schema.tools.output_limits.default.desc":
    "Applies to any unlisted tool, including MCP (default 1000; 0 = unlimited).",
  "settings.schema.tools.background.label": "Background tasks",
  "settings.schema.tools.background.desc":
    "Commands the agent runs detached in the session task pool instead of blocking a turn.",
  "settings.schema.tools.background.enabled.label": "Enabled",
  "settings.schema.tools.background.enabled.desc":
    "Offer the background option on run_command and the background task tools (default true).",
  "settings.schema.tools.background.max_concurrent.label": "Max concurrent",
  "settings.schema.tools.background.max_concurrent.desc":
    "How many background tasks one session may run at once (default 5).",
  "settings.schema.tools.background.default_timeout_seconds.label":
    "Default timeout (s)",
  "settings.schema.tools.background.default_timeout_seconds.desc":
    "Hard limit for a task started without a timeout or a duration estimate (default 900).",
  "settings.schema.tools.background.max_timeout_seconds.label":
    "Max timeout (s)",
  "settings.schema.tools.background.max_timeout_seconds.desc":
    "Ceiling applied to any requested or estimated timeout (default 3600).",
  "settings.schema.tools.background.output_buffer_bytes.label":
    "Output buffer (bytes)",
  "settings.schema.tools.background.output_buffer_bytes.desc":
    "How much of each task's output stays in memory for the ticker; the full log still goes to the session bundle (default 262144).",

  "settings.schema.subagents.desc":
    "User-defined child agents the model can delegate to with spawn_agent. Definitions are markdown files with YAML frontmatter; each run is a background task of the parent session with its own child session and transcript.",
  "settings.schema.subagents.enabled.label": "Enabled",
  "settings.schema.subagents.enabled.desc":
    "Register the spawn_agent tool and list the subagent catalog in the system prompt (default true).",
  "settings.schema.subagents.dirs.label": "Definition directories",
  "settings.schema.subagents.dirs.desc":
    "Lowest priority first; later entries override earlier ones by name. ${CODDY_HOME} and ${CWD} expand. Directories inside the workspace are project scope and follow the trust policy.",
  "settings.schema.subagents.project_trust.label": "Project definitions",
  "settings.schema.subagents.project_trust.desc":
    'Definitions found inside the workspace travel with the checkout. "ask": load them but refuse to spawn one until it is approved for this workspace (coddy agents trust). "allow": treat them like your own files. "deny": never read them.',
  "settings.schema.subagents.max_concurrent.label": "Max concurrent",
  "settings.schema.subagents.max_concurrent.desc":
    "How many subagent runs the whole process may have in flight at once (default 4). Extra spawns are refused, not queued.",
  "settings.schema.subagents.max_depth.label": "Max depth",
  "settings.schema.subagents.max_depth.desc":
    "How deep spawning may nest: 1 lets a session spawn subagents that cannot spawn further (default), 0 forbids spawning everywhere.",
  "settings.schema.subagents.default_timeout_seconds.label":
    "Default timeout (s)",
  "settings.schema.subagents.default_timeout_seconds.desc":
    "Hard limit for one run whose definition and call give no timeout (default 1800); capped by the background max timeout.",
  "settings.schema.subagents.max_turns.label": "Max turns",
  "settings.schema.subagents.max_turns.desc":
    "ReAct rounds a child may take; 0 follows agent.max_turns.",

  "settings.schema.skills.dirs.label": "Skill directories",
  "settings.schema.skills.dirs.desc":
    "Search paths for skills. Defaults: ~/.agents/skills (global, shared with npx skills / npx skillsbd), ${CODDY_HOME}/skills (coddy-specific), ${CWD}/.coddy/skills (project-local). ${CODDY_HOME} and ${CWD} expand at runtime.",
  "settings.schema.skills.auto_discovery.desc":
    "Let the agent load a matching skill's full instructions on its own (model-driven load_skill tool), instead of only when you type /name. Defaults to on.",

  "settings.schema.memory.enabled.label": "Enabled",
  "settings.schema.memory.enabled.desc":
    "Turns on the memory copilot for eligible builds.",
  "settings.schema.memory.model.label": "Memory model",
  "settings.schema.memory.model.desc":
    "Logical model override for memory LLM calls; empty uses agent model.",
  "settings.schema.memory.dir.label": "Memory root",
  "settings.schema.memory.dir.desc":
    "Filesystem root for memory markdown; empty uses ${CODDY_HOME}/memory.",
  "settings.schema.memory.recall_max_turns.label": "Recall max turns",
  "settings.schema.memory.recall_max_turns.desc":
    "Bounds recall-side LLM rounds in the memory loop.",
  "settings.schema.memory.persist_max_turns.label": "Persist max turns",
  "settings.schema.memory.persist_max_turns.desc":
    "Bounds persist-side LLM rounds in the memory loop.",
  "settings.schema.memory.copilot_max_tokens.label": "Copilot max tokens",
  "settings.schema.memory.copilot_max_tokens.desc":
    "Completion token cap for memory copilot calls.",
  "settings.schema.memory.max_search_hits.label": "Max search hits",
  "settings.schema.memory.max_search_hits.desc":
    "Maximum snippets returned by memory search tools.",

  "settings.schema.compaction.enabled.label": "Enabled",
  "settings.schema.compaction.enabled.desc":
    "Master switch for compaction (manual command and automatic trigger). Defaults to true.",
  "settings.schema.compaction.threshold_percent.label": "Auto threshold (%)",
  "settings.schema.compaction.threshold_percent.desc":
    "Auto-compact when the estimated context reaches this percent of the model's max_context_tokens (1..100, default 80). Models without max_context_tokens skip auto-compaction.",
  "settings.schema.compaction.keep_recent_turns.label": "Keep recent turns",
  "settings.schema.compaction.keep_recent_turns.desc":
    "How many most recent user turns stay verbatim after compaction (default 2; 0 summarizes everything).",
  "settings.schema.compaction.model.label": "Summarizer model",
  "settings.schema.compaction.model.desc":
    "Optional models[].model for the summarization call; empty uses the session model.",
  "settings.schema.compaction.result_eviction.label":
    "Read/grep result eviction",
  "settings.schema.compaction.result_eviction.desc":
    "Collapse superseded read/grep results to placeholders when building the LLM request; the persisted transcript is untouched. Only marked (keep_result / keep:true) or most-recent results survive.",
  "settings.schema.compaction.result_eviction.enabled.label": "Enabled",
  "settings.schema.compaction.result_eviction.enabled.desc":
    "Master switch for read/grep result eviction. Defaults to true.",
  "settings.schema.compaction.result_eviction.keep_recent.label":
    "Keep recent results",
  "settings.schema.compaction.result_eviction.keep_recent.desc":
    "How many most recent evictable results stay intact as a working window (default 2 — enough to hold a read and a grep at once; 0 keeps none).",
  "settings.schema.compaction.result_eviction.min_result_bytes.label":
    "Min result bytes",
  "settings.schema.compaction.result_eviction.min_result_bytes.desc":
    "Results at or below this size are never evicted (default 2000; 0 makes every result a candidate).",

  "settings.schema.system.scheduler.label": "Scheduler",
  "settings.schema.system.scheduler.enabled.label": "Enabled",
  "settings.schema.system.scheduler.enabled.desc":
    "When true, this process may run the scheduler daemon and REST.",
  "settings.schema.system.scheduler.dir.label": "Jobs directory",
  "settings.schema.system.scheduler.dir.desc":
    "Directory of job markdown definitions.",
  "settings.schema.system.scheduler.max_queue.label": "Max queue",
  "settings.schema.system.scheduler.max_queue.desc":
    "Maximum concurrent scheduled agent runs.",
  "settings.schema.system.scheduler.timeout.label": "Job timeout",
  "settings.schema.system.scheduler.timeout.desc":
    "Per-job wall-clock limit, e.g. 30m or 1h30m.",
  "settings.schema.system.scheduler.retain_sessions.label": "Retain sessions",
  "settings.schema.system.scheduler.retain_sessions.desc":
    "How many completed scheduler session folders to keep per job id.",
  "settings.schema.system.prompts.label": "Prompts",
  "settings.schema.system.prompts.dir.label": "Prompts directory",
  "settings.schema.system.prompts.dir.desc":
    "Optional override directory for prompt markdown files.",
  "settings.schema.system.prompts.agent_prompt.label": "Agent prompt file",
  "settings.schema.system.prompts.agent_prompt.desc":
    "Filename for the main agent system prompt.",
  "settings.schema.system.prompts.plan_prompt.label": "Plan prompt file",
  "settings.schema.system.prompts.plan_prompt.desc":
    "Filename for plan-mode system prompt.",
  "settings.schema.system.instructions.label": "Instructions",
  "settings.schema.system.instructions.files.label": "Instruction files",
  "settings.schema.system.instructions.files.desc":
    'Filenames relative to session CWD to read as instructions. Defaults to ["AGENTS.md"].',
  "settings.schema.system.logger.label": "Logger",
  "settings.schema.system.logger.level.label": "Level",
  "settings.schema.system.logger.level.desc":
    "Minimum severity written to configured outputs.",
  "settings.schema.system.logger.outputs.label": "Outputs",
  "settings.schema.system.logger.outputs.desc": "Where log lines are written.",
  "settings.schema.system.logger.file.label": "Log file path",
  "settings.schema.system.logger.file.desc":
    "Destination file when outputs include file.",
  "settings.schema.system.logger.format.label": "Format",
  "settings.schema.system.logger.format.desc":
    "text for human logs; json for structured logs.",
  "settings.schema.system.logger.rotation.label": "Rotation",
  "settings.schema.system.logger.rotation.desc":
    "Size-based rotation when logging to a file.",
  "settings.schema.system.logger.rotation.max_size_mb.label":
    "Max file size (MB)",
  "settings.schema.system.logger.rotation.max_size_mb.desc":
    "Rotate after the file reaches this size; 0 uses logger defaults.",
  "settings.schema.system.logger.rotation.max_files.label": "Max files",
  "settings.schema.system.logger.rotation.max_files.desc":
    "How many rotated segments to retain; 0 uses logger defaults.",
  "settings.schema.system.sessions.label": "Sessions",
  "settings.schema.system.sessions.dir.label": "Sessions directory",
  "settings.schema.system.sessions.dir.desc":
    "Override sessions root; empty resolves under CODDY_HOME.",
  "settings.schema.system.gateways.label": "Messenger gateways",
  "settings.schema.system.gateways.telegram.label": "Telegram",
  "settings.schema.system.gateways.telegram.desc":
    "Telegram bot adapter settings.",
  "settings.schema.system.gateways.telegram.enabled.label": "Enabled",
  "settings.schema.system.gateways.telegram.enabled.desc":
    "Run the Telegram bot (requires the gateway or gateway.telegram build tag).",
  "settings.schema.system.gateways.telegram.token.label": "Bot token",
  "settings.schema.system.gateways.telegram.token.desc":
    "BotFather token. Optional here — leave empty to read it from the TELEGRAM_BOT_TOKEN environment variable (e.g. via .env). Secret: when set it is stored in config.yaml and shown in full.",
  "settings.schema.system.gateways.telegram.rich_messages.label":
    "Rich messages",
  "settings.schema.system.gateways.telegram.rich_messages.desc":
    "Use Bot API 10.1 Rich Messages: the agent's native Markdown renders verbatim, tool activity streams as a Thinking placeholder, and executed tools show in a collapsible block. Falls back to legacy formatting if unsupported.",
  "settings.schema.system.gateways.telegram.proxy.label": "Proxy",
  "settings.schema.system.gateways.telegram.proxy.desc":
    "Optional outbound proxy for Telegram API requests. Use http, https, socks5, or socks5h.",
  "settings.schema.system.gateways.telegram.admins.label": "Admins",
  "settings.schema.system.gateways.telegram.admins.desc":
    "Telegram user IDs with elevated rights; admins always pass access checks.",
  "settings.schema.system.gateways.telegram.default_access.label":
    "Default access",
  "settings.schema.system.gateways.telegram.default_access.desc":
    "Fallback access level for chats without an override: all, admins, or group:<name>.",
  "settings.schema.system.gateways.telegram.default_isolation.label":
    "Default isolation",
  "settings.schema.system.gateways.telegram.default_isolation.desc":
    "Fallback session isolation for group chats.",
  "settings.schema.system.gateways.telegram.user_groups.label": "User groups",
  "settings.schema.system.gateways.telegram.user_groups.desc":
    "Named sets of user IDs referenced by access as group:<name>.",
  "settings.schema.system.gateways.telegram.user_groups.name.label":
    "Group name",
  "settings.schema.system.gateways.telegram.user_groups.name.desc":
    "Name referenced by access as group:<name>.",
  "settings.schema.system.gateways.telegram.user_groups.user_ids.label":
    "User IDs",
  "settings.schema.system.gateways.telegram.user_groups.user_ids.desc":
    "Telegram numeric user IDs that belong to this group.",
  "settings.schema.system.gateways.telegram.chats.label": "Per-chat overrides",
  "settings.schema.system.gateways.telegram.chats.desc":
    "Override isolation and access for specific chats.",
  "settings.schema.system.gateways.telegram.chats.chat_id.label": "Chat ID",
  "settings.schema.system.gateways.telegram.chats.chat_id.desc":
    "Telegram chat id; negative for groups and supergroups.",
  "settings.schema.system.gateways.telegram.chats.isolation.label": "Isolation",
  "settings.schema.system.gateways.telegram.chats.isolation.desc":
    "Per-chat session isolation override.",
  "settings.schema.system.gateways.telegram.chats.access.label": "Access",
  "settings.schema.system.gateways.telegram.chats.access.desc":
    "Per-chat access override: all, admins, or group:<name>.",

  "settings.combobox.toggleAria": "Toggle options",

  "codexAuth.error.signInFailed": "ChatGPT sign in failed.",
  "codexAuth.error.incompleteResponse":
    "The OAuth server returned an incomplete sign-in response.",
  "codexAuth.connected.viaCli":
    "Connected via the Codex CLI login on this server.",
  "codexAuth.connected.withChatGpt": "Connected with ChatGPT.",
  "codexAuth.fieldLabel": "ChatGPT account",
  "codexAuth.description":
    "Codex uses your ChatGPT subscription through OAuth. Credentials are stored on the Coddy server and are never added to config.yaml.",
  "codexAuth.enterCode": "Enter this one-time code in the ChatGPT page:",
  "codexAuth.openSignInPage": "Open sign-in page",
  "codexAuth.waiting": "Waiting for confirmation…",
  "codexAuth.signingOut": "Signing Out…",
  "codexAuth.signOut": "Sign Out",
  "codexAuth.waitingForChatGpt": "Waiting for ChatGPT…",
  "codexAuth.signInWithChatGpt": "Sign In with ChatGPT",
  "codexAuth.enterProviderName": "Enter a provider name before signing in.",

  "neuralDeepApiBase.description":
    "NeuralDeep runs the same API at two deployments: api.neuraldeep.ru serves Russia, api.neuraldeep.tech is the mirror for everywhere else. The choice also decides which hub the sign-in below talks to. Fetching the model list reads the saved config, so save before you fetch.",
  "neuralDeepApiBase.optionRu": "api.neuraldeep.ru — Russia",
  "neuralDeepApiBase.optionTech": "api.neuraldeep.tech — international mirror",
  "neuralDeepApiBase.unknown":
    "The saved api_base {value} is not a NeuralDeep endpoint, so requests go to {fallback}. Pick an endpoint to replace it.",

  "neuralDeepAuth.error.signInFailed": "NeuralDeep sign in failed.",
  "neuralDeepAuth.error.incompleteResponse":
    "The hub returned an incomplete sign-in response.",
  "neuralDeepAuth.fieldLabel": "NeuralDeep account",
  "neuralDeepAuth.description":
    "Sign in with your NeuralDeep hub account instead of pasting a key: the hub issues a personal key for Coddy. The key is stored on the Coddy server and is never added to config.yaml. To use your tier's models, add them under Logical models (the model picker fetches the catalog with this login).",
  "neuralDeepAuth.connected": "Signed in to NeuralDeep ({masked}).",
  "neuralDeepAuth.shadowedByKey":
    "An explicit API key is configured, so requests use it instead of this login. Clear the api_key field to use the login.",
  "neuralDeepAuth.hubMismatch":
    "This login was issued by {hub}, but {endpoint} is served by a different hub, so requests with it are rejected. Sign in again to get a key for this endpoint.",
  "neuralDeepAuth.enterCode":
    "Enter this one-time code on the NeuralDeep page:",
  "neuralDeepAuth.openSignInPage": "Open sign-in page",
  "neuralDeepAuth.waiting": "Waiting for confirmation…",
  "neuralDeepAuth.signingOut": "Signing Out…",
  "neuralDeepAuth.signOut": "Sign Out",
  "neuralDeepAuth.waitingForHub": "Waiting for NeuralDeep…",
  "neuralDeepAuth.signIn": "Sign In with NeuralDeep",
  "neuralDeepAuth.enterProviderName":
    "Enter a provider name before signing in.",

  "mcp.status.connectedOne": "Connected — {count} tool",
  "mcp.status.connectedMany": "Connected — {count} tools",
  "mcp.status.probeFailed": "Probe failed",
  "mcp.status.disabled": "Disabled",
  "mcp.status.needsApproval":
    "Waiting for your approval — not started, not contacted",
  "mcp.status.denied":
    "Project MCP servers are switched off by mcp.project_trust: deny",
  "mcp.status.unsupported": "Transport not supported",
  "mcp.error.toggleServer": "Failed to {action} {name}",
  "mcp.error.toggleTool": "Failed to {action} {tool}",
  "mcp.error.changeTrustPolicy": "Failed to change the project trust policy",
  "mcp.error.toggleTrust": "Failed to {action} {name}",
  "mcp.error.delete": "Failed to delete {name}",
  "mcp.error.invalidEntry": "Invalid entry.",
  "mcp.error.saveServer": "Failed to save server",
  "mcp.discovery.legend": "MCP discovery",
  "mcp.discovery.projectServersLabel": "Project servers",
  "mcp.servers.legend": "MCP servers",
  "mcp.addServer": "Add server",
  "mcp.refresh.title": "Re-probe all servers",
  "mcp.refresh.aria": "Refresh MCP servers",
  "mcp.loading": "Loading…",
  "mcp.expand.collapse": "Collapse tools",
  "mcp.expand.expand": "Expand tools",
  "mcp.expand.aria": "{action} {name} tools",
  "mcp.badge.definedIn": "Defined in {origin}",
  "mcp.trust.approvedTitle":
    "Approved for this workspace ({fingerprint}) — click to withdraw",
  "mcp.trust.approveTitle": 'Approve running "{target}" in this workspace',
  "mcp.trust.withdrawAria": "Withdraw approval of MCP server {name}",
  "mcp.trust.approveAria": "Approve MCP server {name}",
  "mcp.switch.enabledTitle": "Enabled — click to disable",
  "mcp.switch.disabledTitle": "Disabled — click to enable",
  "mcp.switch.disableAria": "Disable MCP server {name}",
  "mcp.switch.enableAria": "Enable MCP server {name}",
  "mcp.edit.title": "Edit entry ({origin})",
  "mcp.edit.readonlyTitle":
    "Defined in config.yaml — edit it in the config sections",
  "mcp.edit.aria": "Edit {name}",
  "mcp.delete.title": "Delete from {origin}",
  "mcp.delete.readonlyTitle": "Defined in config.yaml — cannot delete here",
  "mcp.delete.aria": "Delete {name}",
  "mcp.note.denied":
    "Project MCP servers are switched off by mcp.project_trust: deny. This entry is never started.",
  "mcp.fact.in": "in",
  "mcp.tools.emptyConnected": "This server advertises no tools.",
  "mcp.tools.notReachable": "No tool list — the server is not reachable.",
  "mcp.toolSwitch.serverDisabled": "Server is disabled",
  "mcp.toolSwitch.disableAria": "Disable tool {tool} of {server}",
  "mcp.toolSwitch.enableAria": "Enable tool {tool} of {server}",
  "mcp.editor.namePlaceholder": "server-name",
  "mcp.editor.nameAria": "Server name",
  "mcp.editor.scopeAria": "Server scope",
  "mcp.editor.scopeLocal": "Local — ./.coddy/mcp.json",
  "mcp.editor.scopeGlobal": "Global — ~/.coddy/mcp.json",
  "mcp.editor.jsonAria": "Server entry JSON",
  "mcp.editor.save": "Save",
  "mcp.editor.cancel": "Cancel",
  "mcp.discovery.description":
    "The project-local ./.coddy/mcp.json arrives with the checkout, so the repository — not you — picks the command a session would start. On Ask its servers are neither started nor contacted until you approve that exact declaration for this workspace (shield button in the list below); rewriting an approved entry asks again. Servers you add here are approved by the act of writing them. Entries from config.yaml and ~/.coddy/mcp.json are yours and are never gated.",
  "mcp.servers.description":
    "Model Context Protocol servers from three levels: config.yaml (mcp_servers) and the global ~/.coddy/mcp.json, merged with the local ./.coddy/mcp.json of the project (Cursor-compatible; later levels override by name). Switch off a whole server or individual tools — toggles persist into the file that defines the server and reach running sessions on their next turn.",
  "mcp.empty":
    "No MCP servers configured. Add one here (saved to the local ./.coddy/mcp.json or the global ~/.coddy/mcp.json) or declare it under mcp_servers in config.yaml.",
  "mcp.note.declaredBy":
    "Declared by {path}, which travels with the checkout, so it is neither started nor contacted yet. Approving covers exactly this declaration:",
  "mcp.note.namesOnly":
    "Environment and header names are listed, never their values. Editing the entry asks again.",
  "mcp.note.workspaceFallback": "the session workspace",
  "mcp.editor.formatDescription":
    "One mcpServers entry in Cursor format: command/args/env (object), optional disabled and disabledTools. Saved to {path}.",

  "mcp.trustOption.ask": "Ask — approve each project server once",
  "mcp.trustOption.allow": "Allow — start project servers automatically",
  "mcp.trustOption.deny": "Deny — never load project servers",
  "mcp.fact.transport": "transport",
  "mcp.fact.runs": "runs",
  "mcp.fact.contacts": "contacts",
  "mcp.fact.env": "env",
  "mcp.fact.headers": "headers",
  "mcp.origin.config": "config.yaml",
  "mcp.origin.home": "~/.coddy/mcp.json",
  "mcp.origin.project": "./.coddy/mcp.json",
  "mcp.validation.nameRequired": "Server name is required.",
  "mcp.validation.noDoubleUnderscore": 'Server name must not contain "__".',
  "mcp.validation.noSpacesOrSeparators":
    "Server name must not contain spaces or path separators.",
  "mcp.parse.invalidJson": "Invalid JSON.",
  "mcp.parse.mustBeObject": "Entry must be a JSON object.",
  "mcp.parse.typeString": '"type" must be a string.',
  "mcp.parse.commandString": '"command" must be a string.',
  "mcp.parse.urlString": '"url" must be a string.',
  "mcp.parse.commandOrUrlRequired": 'Either "command" or "url" is required.',
  "mcp.parse.argsStringArray": '"args" must be an array of strings.',
  "mcp.parse.envStringMap": '"env" must be an object of string values.',
  "mcp.parse.headersStringMap": '"headers" must be an object of string values.',
  "mcp.parse.disabledBoolean": '"disabled" must be a boolean.',
  "mcp.parse.disabledToolsStringArray":
    '"disabledTools" must be an array of strings.',

  "skills.state.enabled": "Enabled",
  "skills.state.disabled": "Disabled",
  "skills.badge.remote": "remote",
  "skills.loading": "Loading:",
  "skills.installed.legend": "Installed skills",
  "skills.autoDiscovery.legend": "Skill auto-discovery",
  "skills.autoDiscovery.aria": "Skill auto-discovery",
  "skills.autoDiscovery.fallbackDesc":
    "Let the agent load a matching skill's full instructions on its own (model-driven load_skill tool), instead of only when you type /name.",
  "skills.install.searchPlaceholder": "Search marketplace skills to install:",
  "skills.install.loadingMarketplaces": "Loading marketplaces:",
  "skills.install.noMatches": "No matching marketplace skills.",
  "skills.install.moreHint": "+{count} more - refine your search",
  "skills.install.installTitle": "Install {name}",
  "skills.install.installAria": "Install {name}",
  "skills.error.toggle": "Failed to {action}",
  "skills.error.delete": "Failed to delete",
  "skills.error.update": "Update failed",
  "skills.error.sync": "Sync failed",
  "skills.error.install": "Failed to install {name}",
  "skills.status.updated": "Updated {name}.",
  "skills.status.installed": "Installed {name}.",
  "skills.sources.legend": "Remote skill sources",
  "skills.sources.add": "Add",
  "skills.sources.syncAll": "Sync all",
  "skills.sources.syncAllTitle": "Fetch every configured marketplace",
  "skills.sources.completed": "Completed",
  "skills.sources.syncedTitle": "Synced",
  "skills.sources.syncTitle": "Sync {source}",
  "skills.sources.syncAria": "Sync this marketplace",
  "skills.sources.removeTitle": "Remove",
  "skills.sources.removeAria": "Remove marketplace",
  "skills.sources.description":
    "GitHub repos (owner/repo[@ref]), git URLs, or an agents-standard marketplace.json URL. Saved to skills.sources; fetched only when you sync.",
  "skills.sources.placeholder": "owner/repo  ·  https://…/marketplace.json",
  "skills.install.cliHint":
    "You can also install skills via npx skills or npx skillsbd - they land in ~/.agents/skills/ and are picked up automatically.",
  "skills.empty": "No skills found. Use npx skills or npx skillsbd to install.",
  "skills.switch.enabledTitle": "Enabled - click to disable",
  "skills.switch.disabledTitle": "Disabled - click to enable",
  "skills.switch.disableAria": "Disable {name}",
  "skills.switch.enableAria": "Enable {name}",
  "skills.delete.title": "Delete",
  "skills.delete.bundledTitle": "Bundled skill - cannot be deleted",
  "skills.delete.aria": "Delete {name}",
  "skills.update.title": "Download update: {name} v{from} → v{to}",
  "skills.update.aria": "Download update for {name} to version {version}",
  "skills.badge.syncedFrom": "Synced from {source}",
  "app.chatBusy": "This chat is busy in another client. Try again in a moment.",
  "app.emptyResponseBody": "Empty response body",
  "app.branchCreationNoSessionId": "Branch creation returned no session ID",

  "nav.ariaLabel": "Nav",
  "nav.brandTitle": "Coddy",
  "nav.brandSub": "agent",
  "nav.homeAriaLabel": "Coddy agent home",
  "nav.newChatTooltip": "New Chat",
  "nav.useWideSidebar": "Use wide sidebar",
  "nav.useNarrowSidebar": "Use narrow sidebar",
  "nav.wideSidebarTooltip": "Wide sidebar",
  "nav.history": "History",
  "nav.scheduler": "Scheduler",
  "nav.schedulerAriaLabel": "Scheduler jobs",
  "nav.settings": "Settings",

  "sessions.history": "History",
  "sessions.closeHistory": "Close history",
  "sessions.searchPlaceholder": "Search by title or first message",
  "sessions.searchAriaLabel": "Search history by title or first user message",
  "sessions.clearSearch": "Clear search",
  "sessions.empty": "No history yet",
  "sessions.permissionRequired": "Permission required",
  "sessions.questionPending": "Question pending",
  "sessions.unreadCompletion": "Unread completion",
  "sessions.newChatFallback": "New chat",
  "sessions.deleteConversation": "Delete conversation",
  "sessions.delete": "Delete",
  "sessions.loadingMore": "Loading...",

  "chat.newChat": "New chat",
  "chat.chatTitleAriaLabel": "Chat title",
  "chat.branchPrev": "Previous branch",
  "chat.branchNext": "Next branch",
  "chat.branchLabel": "Branch {current} of {total}",
  "chat.subagentReadOnly.notice":
    "Read-only transcript of subagent {name}. Prompts go to the parent chat.",
  "chat.subagentReadOnly.noticeUnnamed":
    "Read-only subagent transcript. Prompts go to the parent chat.",
  "chat.subagentReadOnly.openParent": "Open parent chat",
  "chat.subagentTitle": "Subagent {name}",
  "chat.subagentTitleUnnamed": "Subagent transcript",
  "chat.heroTitle": "What do you want to {verb}?",
  "chat.heroVerb.know": "know",
  "chat.heroVerb.build": "build",
  "chat.heroVerb.find": "find",
  "chat.heroVerb.research": "research",
  "chat.heroVerb.explore": "explore",
  "chat.heroVerb.debug": "debug",
  "chat.heroVerb.ship": "ship",
  "chat.heroVerb.design": "design",
  "chat.heroVerb.learn": "learn",
  "chat.heroVerb.automate": "automate",
  "chat.heroVerb.refactor": "refactor",
  "chat.heroVerb.plan": "plan",
  "chat.runPlanMessage": "Implement the plan.",
  "chat.contextTitle": "Context",
  "chat.contextClose": "Close",
  "chat.contextCloseBreakdown": "Close context breakdown",
  "chat.contextEmpty": "No context usage yet",
  "chat.contextPercentUsed": "{percent}% Used",
  "chat.contextTokensSummary": "~{used} / {max} Tokens",
  "chat.contextMeterAriaLabel": "Context {percent} percent used",
  "chat.contextSegmentTooltip": "{label}: {count}",
  "chat.contextSegment.systemPrompt": "System prompt",
  "chat.contextSegment.toolDefinitions": "Tool definitions",
  "chat.contextSegment.rules": "Rules",
  "chat.contextSegment.skills": "Skills",
  "chat.contextSegment.mcp": "MCP",
  "chat.contextSegment.subagents": "Subagents",
  "chat.contextSegment.conversation": "Conversation",

  "composer.messageLabel": "Message",
  "composer.placeholderEmpty": "Plan, Build, / for skills, @ for files",
  "composer.placeholderFollowUp": "Add a follow-up",
  "composer.send": "Send",
  "composer.stopGeneration": "Stop generation",
  "composer.enhance": "Improve prompt",
  "composer.enhanceNoModel":
    "Couldn't improve the prompt: no model is configured.",
  "composer.enhanceFailed":
    "Couldn't improve the prompt. Your draft is unchanged.",
  "composer.attachFile": "Attach file",
  "composer.attachUnsupportedModel": "Selected model cannot accept attachments",
  "composer.attachmentTooltip": "{fileName}\\n{label} · {size}",
  "composer.removeAttachment": "Remove {fileName}",
  "composer.attachedFilesAriaLabel": "Attached files",
  "composer.bytesB": "{n} B",
  "composer.bytesKB": "{n} KB",
  "composer.bytesMB": "{n} MB",
  "composer.mode": "Mode",
  "composer.modeAgent": "Agent",
  "composer.modePlan": "Plan",
  "composer.model": "Model",
  "composer.modelTitle": "YAML backend (metadata.model)",
  "composer.reasoning": "Reasoning",
  "composer.reasoningLevel": "Reasoning level",
  "composer.reasoningLevelTitle": "Reasoning level (metadata.reasoning)",
  "composer.contextUsage": "Context usage",
  "composer.contextTipIdle": "No context usage yet",
  "composer.contextTipUsed": "{percent}% context used",
  "composer.contextTipInput": "Input {count}",
  "composer.contextTipOutput": "Output {count}",
  "composer.contextTipTotal": "Total {count}",
  "composer.contextTipMaxContext": "Max context {count}",
  "composer.composerOptions": "Composer options",
  "composer.skillsTitle": "Skills",
  "composer.loading": "Loading…",
  "composer.more": "More",
  "composer.noMatchingSkills": "No matching skills",
  "composer.commandsTitle": "Commands",
  "composer.typeAfterAt": "Type after @ to search",
  "composer.noFiles": "No files",
  "composer.filterModels": "Filter models",
  "composer.filterModelsPlaceholder": "Filter models…",
  "composer.noModelsMatch": "No models match “{query}”",
  "composer.vendorOther": "Other",
  "composer.closePicker": "Close picker",
  "composer.slashCommandsAriaLabel": "Slash commands",
  "composer.workspaceFilesTitle": "Workspace files",
  "composer.workspaceFilesAriaLabel": "Workspace files",
  "composer.requestFailed": "request failed",
  "composer.env.ariaLabel": "Environment",
  "composer.env.title": "Environment (local or remote coddy http)",
  "composer.env.local": "Local",
  "composer.env.localThisOrigin": "Local (this origin)",
  "composer.env.groupEnvironment": "Environment",
  "composer.env.groupRemote": "Remote",
  "composer.env.addFormTitle": "Add a remote",
  "composer.env.addRemote": "+ Add remote…",
  "composer.env.namePlaceholder": "name",
  "composer.env.tokenPlaceholder": "bearer token (empty if none)",
  "composer.env.connect": "Connect",
  "composer.env.cancel": "Cancel",
  "composer.folderModal.title": "Open folder",
  "composer.folderModal.close": "Close folder browser",
  "composer.folderModal.pathLabel": "Folder path",
  "composer.folderModal.pathPlaceholder": "Path",
  "composer.folderModal.drivesPlaceholder": "This PC",
  "composer.folderModal.noSubfolders": "No subfolders",
  "composer.folderModal.noDrives": "No drives",
  "composer.folderModal.cannotList": "Cannot list {path}",
  "composer.folderModal.cancel": "Cancel",
  "composer.folderModal.open": "Open",
  "composer.folderModal.go": "Go",

  "env.banner.unreachable":
    "Remote {name} is unreachable or unauthorized — check that it is running, that {cors} allows this origin, and that the token is correct.",
  "env.banner.switchLocal": "Switch to Local",

  "prompts.questions": "Questions",
  "prompts.questionsCount": "{count} questions",
  "prompts.noAnswer": "(no answer)",
  "prompts.answered": "Answered",
  "prompts.skipped": "Skipped",
  "prompts.skip": "Skip",
  "prompts.continue": "Continue",
  "prompts.optionsAriaLabel": "Options {index}",
  "prompts.otherPlaceholder": "Other…",
  "prompts.otherAriaLabel": "Other, type your answer",
  "prompts.allow": "Allow",
  "prompts.allowAlways": "Allow always",
  "prompts.allowAlwaysProgram": "Always allow {program}",
  "prompts.reject": "Reject",
  "prompts.planRun": "Run plan",
  "prompts.planDiscard": "Discard",
  "prompts.planTogglePreview": "Toggle preview",
  "prompts.planBodyAriaLabel": "Plan body (markdown)",
  "prompts.planBodyPlaceholder": "Plan steps and notes…",
  "prompts.planSaving": "Saving…",
  "prompts.planPreviewEmpty": "Nothing to preview yet.",
  "prompts.planSaveFailed": "save failed ({status})",
  "prompts.planSaveFailedNoStatus": "save failed",

  "scheduler.title": "Scheduler",
  "scheduler.close": "Close scheduler",
  "scheduler.searchPlaceholder": "Search by description or job id",
  "scheduler.searchAriaLabel": "Search scheduler jobs by description or job id",
  "scheduler.clearSearch": "Clear scheduler search",
  "scheduler.empty": "No jobs yet",
  "scheduler.loading": "Loading…",
  "scheduler.noDescription": "—",
  "scheduler.paused": "paused",
  "scheduler.addJob": "Add job",
  "scheduler.runJobNow": "Run job now",
  "scheduler.stopJob": "Stop job",
  "scheduler.newJob": "New job",
  "scheduler.jobTitle": "Job {jobId}",
  "scheduler.editorNewAriaLabel": "New scheduler job",
  "scheduler.editorEditAriaLabel": "Edit scheduler job",
  "scheduler.closeEditor": "Close editor",
  "scheduler.field.jobId": "job_id",
  "scheduler.field.jobIdHelp":
    "Filename - letters, digits, hyphens (example: daily-report).",
  "scheduler.field.description": "description",
  "scheduler.field.schedule": "schedule (UTC, 5 fields)",
  "scheduler.field.schedulePlaceholder": "0 * * * *",
  "scheduler.field.cwd": "cwd (optional)",
  "scheduler.field.cwdHelp":
    "Defaults to the agent working directory for this instance.",
  "scheduler.field.mode": "mode",
  "scheduler.mode.agent": "agent",
  "scheduler.mode.plan": "plan",
  "scheduler.field.model": "model",
  "scheduler.field.body": "body (markdown)",
  "scheduler.bodyAriaLabel": "Job body markdown",
  "scheduler.bodyPlaceholder": "Instruction for the scheduled run…",
  "scheduler.pause": "Pause",
  "scheduler.resume": "Resume",
  "scheduler.delete": "Delete",
  "scheduler.apiNotAvailable":
    "Scheduler API is not available in this build (rebuild with http,scheduler).",
  "scheduler.disabled":
    "Scheduler is disabled (set scheduler.enabled or pass -scheduler-enabled).",
  "scheduler.validation.required": "Required",
  "scheduler.validation.tooLong": "Too long",
  "scheduler.validation.noSpaces":
    "No spaces - use hyphens (example: daily-report)",
  "scheduler.validation.invalidJobId":
    "Only letters, digits, and hyphens (example: daily-report)",

  "tasks.panelTitle": "Background tasks",
  "tasks.closePanel": "Close background tasks",
  "tasks.loading": "Loading…",
  "tasks.empty":
    "No background tasks in this chat yet. The agent starts one when a command is slow enough to be worth running detached.",
  "tasks.sectionRunning": "Running",
  "tasks.sectionFinished": "Finished {count}",
  "tasks.clearFinished": "Clear",
  "tasks.backToList": "← Back to tasks",
  "tasks.stopTitle": "Stop task",
  "tasks.stopAriaLabel": "Stop {label}",
  "tasks.outputHeading": "Output",
  "tasks.truncated": "truncated",
  "tasks.truncatedTitle":
    "Earlier output scrolled out of the in-memory window; the full log stays in the session bundle",
  "tasks.noOutput": "(no output yet)",
  "tasks.olderOnDisk":
    "{count} older tasks are kept on disk and not listed here",
  "tasks.progressAriaLabel": "Progress toward the estimate for {label}",
  "tasks.estimate": "est. {value}",
  "tasks.exitCode": "exit {code}",
  "tasks.overdue": "overdue",
  "tasks.status.queued": "Queued",
  "tasks.status.running": "Running",
  "tasks.status.succeeded": "Succeeded",
  "tasks.status.failed": "Failed",
  "tasks.status.timedOut": "Timed out",
  "tasks.status.stopped": "Stopped",
  "tasks.status.orphaned": "Orphaned",
  "tasks.badge.agent": "agent",
  "tasks.agentHeading": "Subagent",
  "tasks.openTranscript": "Open transcript",
  "tasks.openTranscriptUnavailable": "The child session is not known yet",

  "messages.preparingResponse": "Preparing response",
  "messages.copyCode": "Copy code",
  "messages.copy": "Copy",
  "messages.copied": "Copied",
  "messages.copyMessage": "Copy message",
  "messages.copyErrorMessage": "Copy error message",
  "messages.editMessage": "Edit message",
  "messages.attachedFiles": "Attached files",
  "messages.systemLabel": "System",
  "messages.refresh": "Refresh",
  "messages.retryLastMessage": "Retry the last message",
  "messages.thinkingInProgress": "thinking...",
  "messages.thinkingCompleted": "thinking",
  "messages.thinkingSummaryAriaLabel": "Thinking summary",
  "messages.thinkingContentAriaLabel": "Thinking content",
  "messages.compactionLabel": "context compacted",
  "messages.compactionSummaryAriaLabel": "Context compacted summary",
  "messages.compactionBodyAriaLabel": "Compacted context summary",
  "messages.memoryInProgress": "memory...",
  "messages.memoryCompleted": "memory",
  "messages.memoryInProgressAriaLabel": "Memory in progress",
  "messages.memorySummaryAriaLabel": "Memory copilot summary",
  "messages.memoryContentAriaLabel": "Memory copilot content",
  "messages.memoryMarkedSaved": "Marked saved ({title}).",
  "messages.memoryMarkedSavedDefaultTitle": "note",
  "messages.memoryEmpty": "No relevant notes matched this turn.",
  "messages.toolDefaultName": "tool",
  "messages.toolQuestionLabel": "question",
  "messages.toolPendingSuffix": "...",
  "messages.toolSummaryAriaLabel": "Tool summary",
  "messages.toolDetailsAriaLabel": "Tool call details",
  "messages.toolResultAriaLabel": "Tool result",
  "messages.toolResultSection": "Result",
  "messages.toolLoading": "Loading…",
  "messages.toolMore": "More…",
  "todo.preview.updatedItem": "Updated item",
  "todo.preview.plan": "Todo plan",
  "todo.preview.position": "{index} of {total}",
  "todo.preview.completed.one": "{count} completed",
  "todo.preview.completed.other": "{count} completed",
  "todo.preview.items.one": "{count} item",
  "todo.preview.items.other": "{count} items",
  "todo.status.pending": "Pending",
  "todo.status.inProgress": "In progress",
  "todo.status.completed": "Completed",
  "todo.status.failed": "Failed",
  "todo.status.cancelled": "Cancelled",
  "planExit.preview.header": "Agent mode",
  "planExit.preview.planMode": "Plan mode",
  "planExit.preview.agentMode": "Agent mode",
  "planExit.preview.inProgress": "Switching to Agent mode…",
  "planExit.preview.completed": "Switched to Agent mode",
  "messages.toolLess": "Less",
  "messages.toolQuestionTimelineAriaLabel": "Question tool timeline",
  "messages.toolAwaitingAnswer": "Awaiting answer",
  "messages.toolQuestionMirrorHint":
    "Answer using the Questions card in this chat. This row only mirrors the tool state.",
  "messages.toolBgTaskOpen": "Open in Tasks",
  "messages.toolBgTaskStop": "Stop",
  "messages.fileType.image": "Image",
  "messages.fileType.video": "Video",
  "messages.fileType.audio": "Audio",
  "messages.fileType.pdf": "PDF",
  "messages.fileType.text": "Text",
  "messages.fileType.archive": "Archive",
  "messages.fileType.file": "File",
  "messages.requestFailed": "Request failed",
  "messages.streamEnded": "Stream ended",

  "app.backendUnavailable": "Backend is unavailable ({status})",

  "sessions.draftPrefix": "Draft: {title}",
  "sessions.draftEmpty": "Draft: New chat",

  "workspace.detached": "detached",
  "workspace.worktree": "worktree",
  "workspace.worktreeActiveTitle": "This session works in a dedicated worktree",
  "workspace.worktreeInactiveTitle":
    "Open branch switches in a dedicated worktree",
  "workspace.recent": "Recent",
  "workspace.openFolder": "Open folder…",
  "workspace.noBranches": "No branches",

  "permission.preview.patch": "Patch preview",
  "permission.preview.edit": "Edit preview",
  "permission.preview.more": "More…",
  "permission.preview.less": "Less",
  "permission.question.runCommand": "Run this command?",
  "permission.question.writeFile": "Write this file?",
  "permission.question.editFile": "Edit this file?",
  "permission.question.applyPatch": "Apply this patch?",
  "permission.question.createDirectory": "Create this directory?",
  "permission.question.createOrUpdateFile": "Create or update this file?",
  "permission.question.movePath": "Move this path?",
  "permission.question.removeDirectoryTree": "Remove this directory tree?",
  "permission.question.removePath": "Remove this path?",
  "permission.question.removeEmptyDirectory": "Remove this empty directory?",
  "permission.question.allowAction": "Allow this action?",
  "permission.header.shell": "Shell",
  "permission.header.sshShell": "SSH shell",
  "permission.header.move": "Move",
  "permission.header.workspace": "Workspace",
  "permission.meta.timeout": "timeout {seconds}s",
  "permission.meta.replaceAll": "replace all",
  "permission.meta.chars.one": "{count} char",
  "permission.meta.chars.other": "{count} chars",
  "permission.meta.createParents": "create parents",
  "permission.meta.directParentOnly": "direct parent only",
  "permission.meta.existingParentsOnly": "existing parents only",
  "permission.meta.recursive": "recursive",
  "permission.meta.emptyDirectoryOnly": "empty directory only",
  "permission.meta.fromLine": "from line {line}",
  "permission.meta.lines.one": "{count} line",
  "permission.meta.lines.other": "{count} lines",
  "permission.meta.hiddenFiles": "hidden files",
  "permission.meta.caseSensitive": "case sensitive",
  "permission.meta.maxResults": "max {count}",
  "permission.meta.depth": "depth {depth}",

  "scheduler.cron.required": "Enter a cron expression (5 fields, UTC).",
  "scheduler.cron.invalid": "Invalid cron expression",

  "tasks.notReachable": "Coddy is not reachable",
  "tasks.chip.running.one": "{count} running task",
  "tasks.chip.running.other": "{count} running tasks",
  "tasks.chip.total.one": "{count} background task",
  "tasks.chip.total.other": "{count} background tasks",
  "tasks.chip.openAria": "Open background tasks: {label}",

  "env.error.remoteUnreachable":
    "Cannot reach remote {host} — it may be offline or the URL is wrong, or the response was blocked by CORS (enable httpserver.cors on the remote).",
  "env.error.localNetwork":
    "Network error sending the message — check that the server is running.",
  "env.error.remoteUnauthorized":
    "Unauthorized on remote {host} — check the bearer token for this environment.",
  "env.error.localUnauthorized": "Unauthorized ({status}).",
  "env.error.remoteRequestFailed":
    "Request to remote {host} failed ({status}).",
  "env.error.requestFailed": "Request failed ({status}).",

  "status.read": "Reading",
  "status.list": "Listing",
  "status.search": "Searching",
  "status.edit": "Editing",
  "status.write": "Writing",
  "status.run": "Running",
  "status.runRemote": "Running over SSH",
  "status.spawnAgent": "Running subagent",
  "status.createDir": "Creating directory",
  "status.touch": "Creating file",
  "status.move": "Moving",
  "status.delete": "Deleting",
  "status.webSearch": "Searching the web",
  "status.webFetch": "Fetching",
  "status.plan": "Updating the plan",
  "status.planRead": "Reading the plan",
  "status.skill": "Loading a skill",
  "status.schedule": "Updating the schedule",
  "status.config": "Updating the configuration",
  "status.backgroundWait": "Waiting for a background task",
  "status.backgroundList": "Checking background tasks",
  "status.backgroundOutput": "Reading background output",
  "status.backgroundStop": "Stopping a background task",
  "status.backgroundReap": "Cleaning up background tasks",
  "status.tool": "Running a tool",
  "status.thinking": "Thinking…",
  "status.memory": "Working with memory",
  "status.awaitingPermission": "Waiting for your approval",
  "status.awaitingAnswer": "Waiting for your answer",
  "status.waitingModel": "Waiting for the model",
  "status.waitingSlow": "The model is taking longer than usual",
  "status.waitingStuck": "Still no response from the server",
};
