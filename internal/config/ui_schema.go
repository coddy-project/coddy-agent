package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// UISchemaJSON returns a JSON Schema (draft 2020-12) document for ConfigJSON (UI editor).
// HTTPServer is omitted; listen bind is controlled via CLI, not this form.
func UISchemaJSON() ([]byte, error) {
	doc := UISchemaMap()
	return json.Marshal(doc)
}

func strProp(title, description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "string",
		"title":       title,
		"description": description,
	}
}

func intProp(title, description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "integer",
		"title":       title,
		"description": description,
	}
}

func numProp(title, description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "number",
		"title":       title,
		"description": description,
	}
}

func boolProp(title, description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "boolean",
		"title":       title,
		"description": description,
	}
}

func objectSchema(title, description string, props map[string]interface{}, order []string, required []string) map[string]interface{} {
	out := map[string]interface{}{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if title != "" {
		out["title"] = title
	}
	if description != "" {
		out["description"] = description
	}
	if len(order) > 0 {
		ord := make([]interface{}, len(order))
		for i, k := range order {
			ord[i] = k
		}
		out["x-coddy-property-order"] = ord
	}
	if len(required) > 0 {
		req := make([]interface{}, len(required))
		for i, k := range required {
			req[i] = k
		}
		out["required"] = req
	}
	return out
}

func attachNodeDefaults(node map[string]interface{}, def interface{}) {
	if node == nil || def == nil {
		return
	}
	t, _ := node["type"].(string)
	switch t {
	case "object":
		props, ok := node["properties"].(map[string]interface{})
		if !ok {
			return
		}
		defObj, ok := def.(map[string]interface{})
		if !ok {
			return
		}
		for k, sub := range props {
			sm, ok := sub.(map[string]interface{})
			if !ok {
				continue
			}
			if dv, ok := defObj[k]; ok {
				attachNodeDefaults(sm, dv)
			}
		}
	case "array":
		node["default"] = def
		items, ok := node["items"].(map[string]interface{})
		if !ok {
			return
		}
		arr, ok := def.([]interface{})
		if !ok || len(arr) == 0 {
			return
		}
		first, ok := arr[0].(map[string]interface{})
		if !ok {
			return
		}
		attachNodeDefaults(items, first)
	default:
		if def != nil {
			node["default"] = def
		}
	}
}

func attachSchemaDefaultsFromExample(root map[string]interface{}, example *ConfigJSON) {
	raw, err := json.Marshal(example)
	if err != nil {
		return
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return
	}
	props, ok := root["properties"].(map[string]interface{})
	if !ok {
		return
	}
	ensureObjectMatchesSchema(m, props)
	for k, pv := range props {
		v, ok := m[k]
		if !ok {
			continue
		}
		sub, ok := pv.(map[string]interface{})
		if !ok {
			continue
		}
		attachNodeDefaults(sub, v)
	}
}

// ensureObjectMatchesSchema fills omitted JSON keys (e.g. omitempty empty slices) so array defaults apply.
func ensureObjectMatchesSchema(obj map[string]interface{}, schemaProps map[string]interface{}) {
	for k, pv := range schemaProps {
		sub, ok := pv.(map[string]interface{})
		if !ok {
			continue
		}
		typ, _ := sub["type"].(string)
		switch typ {
		case "array":
			if _, ok := obj[k]; !ok {
				obj[k] = []interface{}{}
			}
		case "object":
			childProps, ok := sub["properties"].(map[string]interface{})
			if !ok {
				continue
			}
			childObj, ok := obj[k].(map[string]interface{})
			if !ok || childObj == nil {
				childObj = map[string]interface{}{}
				obj[k] = childObj
			}
			ensureObjectMatchesSchema(childObj, childProps)
		}
	}
}

// UISchemaMap builds the JSON Schema as a generic map (for tests and handlers).
func UISchemaMap() map[string]interface{} {
	providerName := strProp("Provider name",
		"Logical id used in model ids (provider/model-id). ASCII letters, digits, hyphen, and underscore only; must start with a letter. When api_key is empty, the runtime reads the key from the environment variable NAME_API_KEY (NAME is this field in uppercase with hyphens mapped to underscores).")
	providerName["pattern"] = `^[a-zA-Z][a-zA-Z0-9_-]*$`
	providerAPIKey := strProp("API key",
		"You may set a literal key, reference ${ENV} in YAML (expanded when the file is loaded), or leave empty so the process reads the conventional NAME_API_KEY variable derived from the provider name (see provider name description).")
	providerAPIKey["x-coddy-provider-api-key-env-placeholder"] = true
	providerProps := map[string]interface{}{
		"name": providerName,
		"type": map[string]interface{}{
			"type":        "string",
			"title":       "Provider type",
			"description": "Wire protocol for this provider entry.",
			"enum":        []string{"openai", "anthropic", "neuraldeep"},
		},
		"api_base": strProp("API base URL", "Optional override of the default API base URL for this provider. Ignored for neuraldeep, which always uses https://api.neuraldeep.ru/v1."),
		"api_key":  providerAPIKey,
		"api_key_command": strProp("API key command",
			"Optional credential-helper command. When api_key is empty it is run via the detected host shell (pwsh, powershell, or cmd on Windows; bash or sh elsewhere) and its trimmed stdout is used as the key (like git/docker credential helpers or AWS credential_process). On failure resolution falls back to the conventional NAME_API_KEY variable."),
		"proxy": strProp("HTTP or SOCKS proxy",
			"Optional per-provider outbound proxy. Use http:// or https:// for an HTTP proxy, or socks5:// / socks5h:// for SOCKS5 (socks5h resolves hostnames via the proxy). Leave empty for a direct connection."),
	}
	modelProps := map[string]interface{}{
		"model": strProp("Model id", "Logical id in the form provider/api-model-id; must match a provider name prefix."),
		"max_tokens": intProp("Max tokens",
			"Upper bound on completion tokens the model may emit for one assistant message."),
		"temperature": numProp("Temperature",
			"Sampling temperature for this logical model (0 = deterministic, higher = more random)."),
		"max_context_tokens": intProp("Max context tokens (UI hint)",
			"Optional UI hint for composer context bar; 0 means derive from provider metadata when available."),
		"multimodal": boolProp("Multimodal",
			"When true, the model accepts image or file inputs in addition to text. The UI will offer file attachment for messages sent with this model."),
		"reasoning_levels": map[string]interface{}{
			"type":        "array",
			"title":       "Reasoning levels",
			"description": "Optional override of the reasoning levels offered for this model (e.g. low, medium, high). Leave empty to auto-detect from the model id; an explicit empty list hides the reasoning selector.",
			"items":       map[string]interface{}{"type": "string"},
		},
		"reasoning_default": strProp("Default reasoning level",
			"Reasoning level pre-selected for new chats with this model. Must be one of the resolved reasoning levels; ignored otherwise."),
	}
	envProps := map[string]interface{}{
		"name":  strProp("Variable name", "Environment variable name passed to the MCP process."),
		"value": strProp("Value", "Variable value."),
	}
	headerProps := map[string]interface{}{
		"name":  strProp("Header name", "HTTP header name for MCP HTTP transports."),
		"value": strProp("Header value", "HTTP header value."),
	}
	mcpProps := map[string]interface{}{
		"type":    strProp("Server type", "stdio runs a local command; http connects to a remote MCP endpoint."),
		"name":    strProp("Server name", "Stable id referenced by the agent; must be unique in this list."),
		"command": strProp("Command", "Executable for stdio transport (leave empty when using http url)."),
		"args": map[string]interface{}{
			"type":        "array",
			"title":       "Arguments",
			"description": "Argv passed after command for stdio MCP servers.",
			"items":       map[string]interface{}{"type": "string"},
		},
		"env": map[string]interface{}{
			"type":        "array",
			"title":       "Environment",
			"description": "Extra environment variables for the stdio child process.",
			"items": map[string]interface{}{
				"type":                 "object",
				"properties":           envProps,
				"required":             []interface{}{"name", "value"},
				"additionalProperties": false,
			},
		},
		"url": strProp("MCP URL", "HTTP(S) endpoint when type selects an HTTP-based MCP server."),
		"headers": map[string]interface{}{
			"type":        "array",
			"title":       "HTTP headers",
			"description": "Optional headers sent with MCP HTTP requests.",
			"items": map[string]interface{}{
				"type":                 "object",
				"properties":           headerProps,
				"required":             []interface{}{"name", "value"},
				"additionalProperties": false,
			},
		},
	}

	isolationEnum := []string{string(IsolationIndividual), string(IsolationShared), string(IsolationAdmin)}
	telegramUserGroupProps := map[string]interface{}{
		"name": strProp("Group name", "Name referenced by access as group:<name>."),
		"user_ids": map[string]interface{}{
			"type":        "array",
			"title":       "User IDs",
			"description": "Telegram numeric user IDs that belong to this group.",
			"items":       map[string]interface{}{"type": "integer"},
		},
	}
	telegramChatProps := map[string]interface{}{
		"chat_id": intProp("Chat ID", "Telegram chat id; negative for groups and supergroups."),
		"isolation": map[string]interface{}{
			"type": "string", "title": "Isolation",
			"description": "Per-chat session isolation override.",
			"enum":        isolationEnum,
		},
		"access": strProp("Access", "Per-chat access override: all, admins, or group:<name>."),
	}
	telegramProps := map[string]interface{}{
		"enabled": boolProp("Enabled", "Run the Telegram bot (requires the gateway or gateway.telegram build tag)."),
		"token": strProp("Bot token",
			"BotFather token. Optional here — leave empty to read it from the TELEGRAM_BOT_TOKEN environment variable (e.g. via .env). Secret: when set it is stored in config.yaml and shown in full."),
		"rich_messages": boolProp("Rich messages",
			"Use Bot API 10.1 Rich Messages: the agent's native Markdown renders verbatim, tool activity streams as a Thinking placeholder, and executed tools show in a collapsible block. Falls back to legacy formatting if unsupported."),
		"proxy": strProp("Proxy",
			"Optional outbound proxy for Telegram API requests. Use http, https, socks5, or socks5h."),
		"admins": map[string]interface{}{
			"type":        "array",
			"title":       "Admins",
			"description": "Telegram user IDs with elevated rights; admins always pass access checks.",
			"items":       map[string]interface{}{"type": "integer"},
		},
		"default_access": strProp("Default access",
			"Fallback access level for chats without an override: all, admins, or group:<name>."),
		"default_isolation": map[string]interface{}{
			"type": "string", "title": "Default isolation",
			"description": "Fallback session isolation for group chats.",
			"enum":        isolationEnum,
		},
		"user_groups": map[string]interface{}{
			"type":        "array",
			"title":       "User groups",
			"description": "Named sets of user IDs referenced by access as group:<name>.",
			"items": objectSchema("", "", telegramUserGroupProps,
				[]string{"name", "user_ids"}, []string{"name"}),
		},
		"chats": map[string]interface{}{
			"type":        "array",
			"title":       "Per-chat overrides",
			"description": "Override isolation and access for specific chats.",
			"items": objectSchema("", "", telegramChatProps,
				[]string{"chat_id", "isolation", "access"}, []string{"chat_id"}),
		},
	}

	props := map[string]interface{}{
		"providers": map[string]interface{}{
			"type":        "array",
			"title":       "LLM providers",
			"description": "API credentials and transport selection for upstream LLM vendors.",
			"items": objectSchema("", "", providerProps,
				[]string{"name", "type", "api_base", "api_key", "proxy"},
				[]string{"name", "type"}),
		},
		"models": map[string]interface{}{
			"type":        "array",
			"title":       "Logical models",
			"description": "Named model entries the agent and UI can select; ids reference provider prefixes.",
			"items": objectSchema("", "", modelProps,
				[]string{"model", "max_tokens", "temperature", "max_context_tokens", "multimodal", "reasoning_levels", "reasoning_default"},
				[]string{"model"}),
		},
		"agent": objectSchema("ReAct agent", "Defaults for the main agent loop (model id and safety caps).",
			map[string]interface{}{
				"model": strProp("Default model", "Logical model id from the models list used when the client omits a model."),
				"max_turns": intProp("Max turns",
					"Hard cap on ReAct iterations (LLM calls plus tool rounds) for one user request."),
				"max_tokens_per_turn": intProp("Max tokens per turn",
					"Upper bound on total tokens (prompt + completion) the model may use in one agent step."),
				"llm_retry_max": intProp("LLM retry max",
					"Retries after retryable LLM errors such as HTTP 429 before failing the turn."),
				"llm_retry_base_ms": intProp("LLM retry base ms",
					"Initial backoff between LLM retries in milliseconds."),
				"llm_min_interval_ms": intProp("LLM min interval ms",
					"Minimum gap between consecutive LLM calls in milliseconds (0 disables pacing)."),
			},
			[]string{"model", "max_turns", "max_tokens_per_turn", "llm_retry_max", "llm_retry_base_ms", "llm_min_interval_ms"},
			nil),
		"tools": objectSchema("Tools and permissions", "Filesystem and shell policy for built-in tools.",
			map[string]interface{}{
				"permission_mode": map[string]interface{}{
					"type":        "string",
					"title":       "Permission mode",
					"description": "Controls when the agent asks for user approval before running tools. \"ask\": approve commands and writes. \"accept_edits\": auto-approve writes, approve commands. \"bypass\": skip all prompts.",
					"enum":        []string{PermModeAsk, PermModeAcceptEdits, PermModeBypass},
				},
				"command_allowlist": map[string]interface{}{
					"type":        "array",
					"title":       "Command allowlist",
					"description": "If non-empty, only these shell command prefixes may run without extra policy.",
					"items":       map[string]interface{}{"type": "string"},
				},
			},
			[]string{"permission_mode", "command_allowlist"},
			nil),
		"mcp_servers": map[string]interface{}{
			"type":        "array",
			"title":       "MCP servers",
			"description": "Model Context Protocol servers started or contacted for new sessions.",
			"items": objectSchema("", "", mcpProps,
				[]string{"type", "name", "command", "args", "env", "url", "headers"},
				[]string{"name"}),
		},
		"skills": objectSchema("Skills", "Slash commands and skill packs discovered from these directories.",
			map[string]interface{}{
				"dirs": map[string]interface{}{
					"type":        "array",
					"title":       "Skill directories",
					"description": "Search paths for skills. Defaults: ~/.agents/skills (global, shared with npx skills / npx skillsbd), ${CODDY_HOME}/skills (coddy-specific), ${CWD}/.coddy/skills (project-local). ${CODDY_HOME} and ${CWD} expand at runtime.",
					"items":       map[string]interface{}{"type": "string"},
				},
				"sources": map[string]interface{}{
					"type":        "array",
					"title":       "Remote skill sources",
					"description": "GitHub repos (owner/repo[@ref]), git URLs, or an http(s) URL to an agents-standard marketplace.json. Installed on demand via `coddy skills sync` or the Sync button; never fetched automatically.",
					"items":       map[string]interface{}{"type": "string"},
				},
			},
			[]string{"dirs", "sources"},
			nil),
		"memory": objectSchema("Long-term memory", "Optional memory copilot (requires memory build tag and provider).",
			map[string]interface{}{
				"enabled":            boolProp("Enabled", "Turns on the memory copilot for eligible builds."),
				"model":              strProp("Memory model", "Logical model override for memory LLM calls; empty uses agent model."),
				"dir":                strProp("Memory root", "Filesystem root for memory markdown; empty uses ${CODDY_HOME}/memory."),
				"recall_max_turns":   intProp("Recall max turns", "Bounds recall-side LLM rounds in the memory loop."),
				"persist_max_turns":  intProp("Persist max turns", "Bounds persist-side LLM rounds in the memory loop."),
				"copilot_max_tokens": intProp("Copilot max tokens", "Completion token cap for memory copilot calls."),
				"max_search_hits":    intProp("Max search hits", "Maximum snippets returned by memory search tools."),
			},
			[]string{"enabled", "model", "dir", "recall_max_turns", "persist_max_turns", "copilot_max_tokens", "max_search_hits"},
			nil),
		"scheduler": objectSchema("Scheduler", "Cron-style scheduled jobs (requires scheduler build tag).",
			map[string]interface{}{
				"enabled":         boolProp("Enabled", "When true, this process may run the scheduler daemon and REST."),
				"dir":             strProp("Jobs directory", "Directory of job markdown definitions."),
				"max_queue":       intProp("Max queue", "Maximum concurrent scheduled agent runs."),
				"timeout":         strProp("Job timeout", "Per-job wall-clock limit, e.g. 30m or 1h30m."),
				"retain_sessions": intProp("Retain sessions", "How many completed scheduler session folders to keep per job id."),
			},
			[]string{"enabled", "dir", "max_queue", "timeout", "retain_sessions"},
			nil),
		"prompts": objectSchema("Prompts", "Built-in system prompt files relative to dir.",
			map[string]interface{}{
				"dir":          strProp("Prompts directory", "Optional override directory for prompt markdown files."),
				"agent_prompt": strProp("Agent prompt file", "Filename for the main agent system prompt."),
				"plan_prompt":  strProp("Plan prompt file", "Filename for plan-mode system prompt."),
			},
			[]string{"dir", "agent_prompt", "plan_prompt"},
			nil),
		"instructions": objectSchema("Instructions", "Files read from the session working directory and appended to the system prompt as project instructions (AGENTS.md-compatible).",
			map[string]interface{}{
				"files": map[string]interface{}{
					"type":        "array",
					"title":       "Instruction files",
					"description": "Filenames relative to session CWD to read as instructions. Defaults to [\"AGENTS.md\"].",
					"items":       map[string]interface{}{"type": "string"},
				},
			},
			[]string{"files"},
			nil),
		"logger": objectSchema("Logger", "Process log level, outputs, and rotation.",
			map[string]interface{}{
				"level": map[string]interface{}{
					"type":        "string",
					"title":       "Level",
					"description": "Minimum severity written to configured outputs.",
					"enum":        []string{"debug", "info", "warn", "error", "warning"},
				},
				"outputs": map[string]interface{}{
					"type":        "array",
					"title":       "Outputs",
					"description": "Where log lines are written.",
					"items": map[string]interface{}{
						"type": "string",
						"enum": []string{"stdout", "stderr", "file"},
					},
				},
				"file": strProp("Log file path", "Destination file when outputs include file."),
				"format": map[string]interface{}{
					"type":        "string",
					"title":       "Format",
					"description": "text for human logs; json for structured logs.",
					"enum":        []string{"text", "json"},
				},
				"rotation": objectSchema("Rotation", "Size-based rotation when logging to a file.",
					map[string]interface{}{
						"max_size_mb": intProp("Max file size (MB)", "Rotate after the file reaches this size; 0 uses logger defaults."),
						"max_files":   intProp("Max files", "How many rotated segments to retain; 0 uses logger defaults."),
					},
					[]string{"max_size_mb", "max_files"},
					nil),
			},
			[]string{"level", "outputs", "file", "format", "rotation"},
			nil),
		"sessions": objectSchema("Sessions", "Where persisted chat bundles are stored.",
			map[string]interface{}{
				"dir": strProp("Sessions directory", "Override sessions root; empty resolves under CODDY_HOME."),
			},
			[]string{"dir"},
			nil),
		"compaction": objectSchema("Context compaction", "Summarize older conversation history so long sessions keep fitting the model context window.",
			map[string]interface{}{
				"enabled":           boolProp("Enabled", "Master switch for compaction (manual command and automatic trigger). Defaults to true."),
				"threshold_percent": intProp("Auto threshold (%)", "Auto-compact when the estimated context reaches this percent of the model's max_context_tokens (1..100, default 80). Models without max_context_tokens skip auto-compaction."),
				"keep_recent_turns": intProp("Keep recent turns", "How many most recent user turns stay verbatim after compaction (default 2; 0 summarizes everything)."),
				"model":             strProp("Summarizer model", "Optional models[].model for the summarization call; empty uses the session model."),
			},
			[]string{"enabled", "threshold_percent", "keep_recent_turns", "model"},
			nil),
		"gateways": objectSchema("Messenger gateways", "Telegram bot gateway (requires the gateway or gateway.telegram build tag).",
			map[string]interface{}{
				"telegram": objectSchema("Telegram", "Telegram bot adapter settings.", telegramProps,
					[]string{"enabled", "token", "rich_messages", "proxy", "admins", "default_access", "default_isolation", "user_groups", "chats"},
					nil),
			},
			[]string{"telegram"},
			nil),
	}

	rootOrder := []string{
		"providers", "models", "agent", "tools", "mcp_servers", "skills", "memory", "scheduler",
		"prompts", "instructions", "logger", "sessions", "compaction", "gateways",
	}

	doc := map[string]interface{}{
		"$schema":                "https://json-schema.org/draft/2020-12/schema",
		"title":                  "Coddy config",
		"description":            "Runtime configuration edited via the Settings UI. Secrets are included in GET responses.",
		"type":                   "object",
		"properties":             props,
		"additionalProperties":   false,
		"x-coddy-property-order": toIfaceOrder(rootOrder),
	}

	attachSchemaDefaultsFromExample(doc, SchemaExampleConfigJSON())
	return doc
}

func toIfaceOrder(keys []string) []interface{} {
	out := make([]interface{}, len(keys))
	for i, k := range keys {
		out[i] = k
	}
	return out
}

// UISchemaCoversConfigJSONFields checks that UI schema properties match ConfigJSON except httpserver (hidden from UI).
func UISchemaCoversConfigJSONFields() error {
	doc := UISchemaMap()
	props, ok := doc["properties"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("schema missing properties")
	}
	t := reflect.TypeOf(ConfigJSON{})
	want := make(map[string]struct{}, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		name := tag
		if c := strings.IndexByte(tag, ','); c >= 0 {
			name = tag[:c]
		}
		if name == "" || name == "-" || name == "httpserver" {
			continue
		}
		want[name] = struct{}{}
	}
	for k := range want {
		if _, ok := props[k]; !ok {
			return fmt.Errorf("schema missing property %q", k)
		}
	}
	for k := range props {
		if k == "httpserver" {
			return fmt.Errorf("schema must not expose httpserver in UI")
		}
		if _, ok := want[k]; !ok {
			return fmt.Errorf("schema has unknown property %q", k)
		}
	}
	return nil
}
