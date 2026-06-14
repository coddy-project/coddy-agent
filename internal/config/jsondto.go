package config

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// ConfigJSON is the JSON shape for GET/PUT /coddy/config (snake_case keys match YAML).
type ConfigJSON struct {
	Providers    []ProviderJSON   `json:"providers,omitempty"`
	Models       []ModelJSON      `json:"models,omitempty"`
	Agent        AgentJSON        `json:"agent,omitempty"`
	Prompts      PromptsJSON      `json:"prompts,omitempty"`
	Instructions InstructionsJSON `json:"instructions,omitempty"`
	Skills       SkillsJSON       `json:"skills,omitempty"`
	MCPServers   []MCPServerJSON  `json:"mcp_servers,omitempty"`
	Tools        ToolsJSON        `json:"tools,omitempty"`
	Logger       LoggerJSON       `json:"logger,omitempty"`
	Sessions     SessionsJSON     `json:"sessions,omitempty"`
	Memory       MemoryJSON       `json:"memory,omitempty"`
	HTTPServer   HTTPServerJSON   `json:"httpserver,omitempty"`
	Scheduler    SchedulerJSON    `json:"scheduler,omitempty"`
}

// InstructionsJSON mirrors Instructions for JSON APIs.
type InstructionsJSON struct {
	Files []string `json:"files,omitempty"`
}

// ProviderJSON mirrors ProviderConfig for JSON APIs.
type ProviderJSON struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	APIBase string `json:"api_base,omitempty"`
	APIKey  string `json:"api_key,omitempty"`
	Proxy   string `json:"proxy,omitempty"`
}

// ModelJSON mirrors ModelEntry for JSON APIs.
// Field order and types must match ModelEntry (direct struct conversion is used below).
type ModelJSON struct {
	Model            string   `json:"model"`
	MaxTokens        int      `json:"max_tokens"`
	Temperature      float64  `json:"temperature"`
	MaxContextTokens int      `json:"max_context_tokens,omitempty"`
	Multimodal       bool     `json:"multimodal,omitempty"`
	ReasoningLevels  []string `json:"reasoning_levels,omitempty"`
	ReasoningDefault string   `json:"reasoning_default,omitempty"`
}

// AgentJSON mirrors Agent for JSON APIs.
type AgentJSON struct {
	Model            string `json:"model"`
	MaxTurns         int    `json:"max_turns,omitempty"`
	MaxTokensPerTurn int    `json:"max_tokens_per_turn,omitempty"`
	LLMRetryMax      int    `json:"llm_retry_max,omitempty"`
	LLMRetryBaseMS   int    `json:"llm_retry_base_ms,omitempty"`
	LLMMinIntervalMS int    `json:"llm_min_interval_ms,omitempty"`
}

// PromptsJSON mirrors Prompts for JSON APIs.
type PromptsJSON struct {
	Dir         string `json:"dir,omitempty"`
	AgentPrompt string `json:"agent_prompt,omitempty"`
	PlanPrompt  string `json:"plan_prompt,omitempty"`
}

// SkillsJSON mirrors Skills for JSON APIs.
type SkillsJSON struct {
	Dirs []string `json:"dirs,omitempty"`
}

// MCPServerJSON mirrors MCPServerConfig for JSON APIs.
type MCPServerJSON struct {
	Type    string           `json:"type,omitempty"`
	Name    string           `json:"name"`
	Command string           `json:"command,omitempty"`
	Args    []string         `json:"args,omitempty"`
	Env     []EnvVarJSON     `json:"env,omitempty"`
	URL     string           `json:"url,omitempty"`
	Headers []HTTPHeaderJSON `json:"headers,omitempty"`
}

// EnvVarJSON mirrors EnvVarConfig.
type EnvVarJSON struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// HTTPHeaderJSON mirrors HTTPHeaderConfig.
type HTTPHeaderJSON struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ToolsJSON mirrors Tools for JSON APIs.
type ToolsJSON struct {
	PermissionMode   string   `json:"permission_mode,omitempty"`
	CommandAllowlist []string `json:"command_allowlist,omitempty"`
}

// LoggerJSON mirrors Logger for JSON APIs.
type LoggerJSON struct {
	Level    string             `json:"level,omitempty"`
	Outputs  []string           `json:"outputs,omitempty"`
	File     string             `json:"file,omitempty"`
	Format   string             `json:"format,omitempty"`
	Rotation LoggerRotationJSON `json:"rotation,omitempty"`
}

// LoggerRotationJSON mirrors LoggerRotation.
type LoggerRotationJSON struct {
	MaxSizeMB int `json:"max_size_mb,omitempty"`
	MaxFiles  int `json:"max_files,omitempty"`
}

// SessionsJSON mirrors Sessions.
type SessionsJSON struct {
	Dir string `json:"dir,omitempty"`
}

// MemoryJSON mirrors MemoryConfig.
type MemoryJSON struct {
	Enabled          bool   `json:"enabled,omitempty"`
	Model            string `json:"model,omitempty"`
	Dir              string `json:"dir,omitempty"`
	RecallMaxTurns   int    `json:"recall_max_turns,omitempty"`
	PersistMaxTurns  int    `json:"persist_max_turns,omitempty"`
	CopilotMaxTokens int    `json:"copilot_max_tokens,omitempty"`
	MaxSearchHits    int    `json:"max_search_hits,omitempty"`
}

// HTTPServerJSON mirrors HTTPServerConfig.
type HTTPServerJSON struct {
	Host string `json:"host,omitempty"`
	Port int    `json:"port,omitempty"`
}

// SchedulerJSON mirrors SchedulerConfig.
type SchedulerJSON struct {
	Enabled        bool   `json:"enabled,omitempty"`
	Dir            string `json:"dir,omitempty"`
	MaxQueue       int    `json:"max_queue,omitempty"`
	Timeout        string `json:"timeout,omitempty"`
	RetainSessions int    `json:"retain_sessions,omitempty"`
}

// ConfigToJSONDTO copies a loaded Config into ConfigJSON (for GET /coddy/config).
func ConfigToJSONDTO(c *Config) *ConfigJSON {
	if c == nil {
		return &ConfigJSON{}
	}
	out := &ConfigJSON{}
	for _, p := range c.Providers {
		out.Providers = append(out.Providers, ProviderJSON(p))
	}
	for _, m := range c.Models {
		out.Models = append(out.Models, ModelJSON(m))
	}
	out.Agent = AgentJSON{
		Model:            c.Agent.Model,
		MaxTurns:         c.Agent.MaxTurns,
		MaxTokensPerTurn: c.Agent.MaxTokensPerTurn,
		LLMRetryMax:      c.Agent.LLMRetryMax,
		LLMRetryBaseMS:   c.Agent.LLMRetryBaseMS,
		LLMMinIntervalMS: c.Agent.LLMMinIntervalMS,
	}
	out.Prompts = PromptsJSON{
		Dir: c.Prompts.Dir, AgentPrompt: c.Prompts.AgentPrompt, PlanPrompt: c.Prompts.PlanPrompt,
	}
	out.Instructions = InstructionsJSON{Files: append([]string(nil), c.Instructions.Files...)}
	out.Skills = SkillsJSON{Dirs: append([]string(nil), c.Skills.Dirs...)}
	for _, s := range c.MCPServers {
		mj := MCPServerJSON{Type: s.Type, Name: s.Name, Command: s.Command, Args: append([]string(nil), s.Args...), URL: s.URL}
		for _, e := range s.Env {
			mj.Env = append(mj.Env, EnvVarJSON(e))
		}
		for _, h := range s.Headers {
			mj.Headers = append(mj.Headers, HTTPHeaderJSON(h))
		}
		out.MCPServers = append(out.MCPServers, mj)
	}
	out.Tools = ToolsJSON{
		PermissionMode:   c.Tools.ResolvedPermMode(),
		CommandAllowlist: append([]string(nil), c.Tools.CommandAllowlist...),
	}
	out.Logger = LoggerJSON{
		Level: c.Logger.Level, Outputs: append([]string(nil), c.Logger.Outputs...),
		File: c.Logger.File, Format: c.Logger.Format,
		Rotation: LoggerRotationJSON{MaxSizeMB: c.Logger.Rotation.MaxSizeMB, MaxFiles: c.Logger.Rotation.MaxFiles},
	}
	out.Sessions = SessionsJSON{Dir: c.Sessions.Dir}
	out.Memory = MemoryJSON{
		Enabled: c.Memory.Enabled, Model: c.Memory.Model, Dir: c.Memory.Dir,
		RecallMaxTurns: c.Memory.RecallMaxTurns, PersistMaxTurns: c.Memory.PersistMaxTurns,
		CopilotMaxTokens: c.Memory.CopilotMaxTokens, MaxSearchHits: c.Memory.MaxSearchHits,
	}
	out.HTTPServer = HTTPServerJSON{Host: c.HTTPServer.Host, Port: c.HTTPServer.Port}
	out.Scheduler = SchedulerJSON{
		Enabled: c.Scheduler.Enabled, Dir: c.Scheduler.Dir, MaxQueue: c.Scheduler.MaxQueue,
		Timeout: c.Scheduler.Timeout, RetainSessions: c.Scheduler.RetainSessions,
	}
	return out
}

// JSONDTOToConfig maps JSON DTO into a new Config (Paths must be set by caller before validate).
func JSONDTOToConfig(j *ConfigJSON, paths Paths) *Config {
	cfg := &Config{Paths: paths}
	if j == nil {
		return cfg
	}
	for _, p := range j.Providers {
		cfg.Providers = append(cfg.Providers, ProviderConfig(p))
	}
	for _, m := range j.Models {
		cfg.Models = append(cfg.Models, ModelEntry(m))
	}
	cfg.Agent = Agent{
		Model:            j.Agent.Model,
		MaxTurns:         j.Agent.MaxTurns,
		MaxTokensPerTurn: j.Agent.MaxTokensPerTurn,
		LLMRetryMax:      j.Agent.LLMRetryMax,
		LLMRetryBaseMS:   j.Agent.LLMRetryBaseMS,
		LLMMinIntervalMS: j.Agent.LLMMinIntervalMS,
	}
	cfg.Prompts = Prompts{
		Dir: j.Prompts.Dir, AgentPrompt: j.Prompts.AgentPrompt, PlanPrompt: j.Prompts.PlanPrompt,
	}
	cfg.Instructions = Instructions{Files: append([]string(nil), j.Instructions.Files...)}
	cfg.Skills = Skills{
		Dirs: append([]string(nil), j.Skills.Dirs...),
	}
	for _, s := range j.MCPServers {
		mc := MCPServerConfig{Type: s.Type, Name: s.Name, Command: s.Command, Args: append([]string(nil), s.Args...), URL: s.URL}
		for _, e := range s.Env {
			mc.Env = append(mc.Env, EnvVarConfig(e))
		}
		for _, h := range s.Headers {
			mc.Headers = append(mc.Headers, HTTPHeaderConfig(h))
		}
		cfg.MCPServers = append(cfg.MCPServers, mc)
	}
	cfg.Tools = Tools{
		PermissionMode:   j.Tools.PermissionMode,
		CommandAllowlist: append([]string(nil), j.Tools.CommandAllowlist...),
	}
	cfg.Logger = Logger{
		Level: j.Logger.Level, Outputs: append([]string(nil), j.Logger.Outputs...),
		File: j.Logger.File, Format: j.Logger.Format,
		Rotation: LoggerRotation{
			MaxSizeMB: j.Logger.Rotation.MaxSizeMB, MaxFiles: j.Logger.Rotation.MaxFiles,
		},
	}
	cfg.Sessions = Sessions{Dir: j.Sessions.Dir}
	cfg.Memory = MemoryConfig{
		Enabled: j.Memory.Enabled, Model: j.Memory.Model, Dir: j.Memory.Dir,
		RecallMaxTurns: j.Memory.RecallMaxTurns, PersistMaxTurns: j.Memory.PersistMaxTurns,
		CopilotMaxTokens: j.Memory.CopilotMaxTokens, MaxSearchHits: j.Memory.MaxSearchHits,
	}
	cfg.HTTPServer = HTTPServerConfig{Host: j.HTTPServer.Host, Port: j.HTTPServer.Port}
	cfg.Scheduler = SchedulerConfig{
		Enabled: j.Scheduler.Enabled, Dir: j.Scheduler.Dir, MaxQueue: j.Scheduler.MaxQueue,
		Timeout: j.Scheduler.Timeout, RetainSessions: j.Scheduler.RetainSessions,
	}
	return cfg
}

// ParseAndValidateConfigJSON unmarshals JSON into ConfigJSON, maps to Config, applies defaults and validates.
func ParseAndValidateConfigJSON(data []byte, paths Paths) (*Config, error) {
	var j ConfigJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	cfg := JSONDTOToConfig(&j, paths)
	applyDefaults(cfg)
	if err := validateSubconfigs(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// MarshalConfigYAML serializes cfg to YAML bytes for disk (Paths is omitted via yaml:"-" on field).
func MarshalConfigYAML(cfg *Config) ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	return yaml.Marshal(cfg)
}
