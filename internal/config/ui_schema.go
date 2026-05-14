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
	providerProps := map[string]interface{}{
		"name": strProp("Provider name", "Logical id used in model ids (provider/model-id)."),
		"type": map[string]interface{}{
			"type":        "string",
			"title":       "Provider type",
			"description": "Wire protocol for this provider entry.",
			"enum":        []string{"openai", "anthropic"},
		},
		"api_base": strProp("API base URL", "Optional override of the default API base URL for this provider."),
		"api_key":  strProp("API key", "Secret API key. Prefer env-based injection outside untrusted networks."),
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
				[]string{"model", "max_tokens", "temperature", "max_context_tokens"},
				[]string{"model"}),
		},
		"agent": objectSchema("ReAct agent", "Defaults for the main agent loop (model id and safety caps).",
			map[string]interface{}{
				"model": strProp("Default model", "Logical model id from the models list used when the client omits a model."),
				"max_turns": intProp("Max turns",
					"Hard cap on ReAct iterations (LLM calls plus tool rounds) for one user request."),
				"max_tokens_per_turn": intProp("Max tokens per turn",
					"Upper bound on total tokens (prompt + completion) the model may use in one agent step."),
			},
			[]string{"model", "max_turns", "max_tokens_per_turn"},
			nil),
		"tools": objectSchema("Tools and permissions", "Filesystem and shell policy for built-in tools.",
			map[string]interface{}{
				"require_permission_for_commands": boolProp("Confirm shell commands",
					"When true, the user must approve each shell invocation."),
				"require_permission_for_writes": boolProp("Confirm writes",
					"When true, the user must approve mutating file operations."),
				"restrict_to_cwd": boolProp("Restrict to session cwd",
					"When true, file tools stay under the session working directory."),
				"command_allowlist": map[string]interface{}{
					"type":        "array",
					"title":       "Command allowlist",
					"description": "If non-empty, only these shell command prefixes may run without extra policy.",
					"items":       map[string]interface{}{"type": "string"},
				},
				"permission_master_key": strProp("Permission master key",
					"Optional shared secret to auto-approve permission prompts when the client supplies it."),
			},
			[]string{"require_permission_for_commands", "require_permission_for_writes", "restrict_to_cwd", "command_allowlist", "permission_master_key"},
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
					"description": "Search paths for skill folders; ${CODDY_HOME} and ${CWD} expand at runtime.",
					"items":       map[string]interface{}{"type": "string"},
				},
				"install_dir": strProp("Install directory", "Where `coddy skills install` stores downloaded packs."),
			},
			[]string{"dirs", "install_dir"},
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
	}

	rootOrder := []string{
		"providers", "models", "agent", "tools", "mcp_servers", "skills", "memory", "scheduler",
		"prompts", "logger", "sessions",
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
