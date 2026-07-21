package config

import (
	"encoding/json"
	"fmt"
	"strings"

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
	Compaction   CompactionJSON   `json:"compaction,omitempty"`
	Memory       MemoryJSON       `json:"memory,omitempty"`
	HTTPServer   HTTPServerJSON   `json:"httpserver,omitempty"`
	Scheduler    SchedulerJSON    `json:"scheduler,omitempty"`
	Gateways     GatewaysJSON     `json:"gateways,omitempty"`
}

// GatewaysJSON mirrors GatewayConfig for JSON APIs.
type GatewaysJSON struct {
	Telegram TelegramGatewayJSON `json:"telegram,omitempty"`
}

// TelegramGatewayJSON mirrors TelegramGatewayConfig.
type TelegramGatewayJSON struct {
	Enabled          bool                    `json:"enabled,omitempty"`
	Token            string                  `json:"token,omitempty"`
	Proxy            string                  `json:"proxy,omitempty"`
	RichMessages     bool                    `json:"rich_messages,omitempty"`
	Admins           []int64                 `json:"admins,omitempty"`
	DefaultAccess    string                  `json:"default_access,omitempty"`
	DefaultIsolation string                  `json:"default_isolation,omitempty"`
	UserGroups       []TelegramUserGroupJSON `json:"user_groups,omitempty"`
	Chats            []TelegramChatJSON      `json:"chats,omitempty"`
}

// TelegramUserGroupJSON mirrors TelegramUserGroup.
type TelegramUserGroupJSON struct {
	Name    string  `json:"name"`
	UserIDs []int64 `json:"user_ids,omitempty"`
}

// TelegramChatJSON mirrors TelegramChatConfig.
type TelegramChatJSON struct {
	ChatID    int64  `json:"chat_id"`
	Isolation string `json:"isolation,omitempty"`
	Access    string `json:"access,omitempty"`
}

// InstructionsJSON mirrors Instructions for JSON APIs.
type InstructionsJSON struct {
	Files []string `json:"files,omitempty"`
}

// ProviderJSON mirrors ProviderConfig for JSON APIs.
// Field order and types must match ProviderConfig (direct struct conversion is used below).
type ProviderJSON struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	APIBase       string `json:"api_base,omitempty"`
	APIKey        string `json:"api_key,omitempty"`
	APIKeyCommand string `json:"api_key_command,omitempty"`
	Proxy         string `json:"proxy,omitempty"`
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

// CompactionJSON mirrors Compaction. Pointer fields keep the unset/explicit
// distinction (enabled defaults to true, keep_recent_turns to 2).
type CompactionJSON struct {
	Enabled          *bool  `json:"enabled,omitempty"`
	ThresholdPercent int    `json:"threshold_percent,omitempty"`
	KeepRecentTurns  *int   `json:"keep_recent_turns,omitempty"`
	Model            string `json:"model,omitempty"`
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

// HTTPServerJSON mirrors HTTPServerConfig. AuthToken is write-only: ConfigToJSONDTO never
// populates it (redacted), reporting only whether one is set via AuthConfigured.
type HTTPServerJSON struct {
	Host           string           `json:"host,omitempty"`
	Port           int              `json:"port,omitempty"`
	AuthToken      string           `json:"auth_token,omitempty"`
	AuthConfigured bool             `json:"auth_configured,omitempty"`
	PublicDocs     bool             `json:"public_docs,omitempty"`
	AllowInsecure  bool             `json:"allow_insecure,omitempty"`
	CORS           HTTPCORSJSON     `json:"cors,omitempty"`
	Remotes        []HTTPRemoteJSON `json:"remotes,omitempty"`
}

// HTTPCORSJSON mirrors HTTPCORSConfig.
type HTTPCORSJSON struct {
	Enabled        bool     `json:"enabled,omitempty"`
	AllowedOrigins []string `json:"allowed_origins,omitempty"`
}

// HTTPRemoteJSON mirrors HTTPRemote.
type HTTPRemoteJSON struct {
	Name string `json:"name"`
	URL  string `json:"url"`
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
	out.Compaction = CompactionJSON{
		Enabled:          cloneBoolPtr(c.Compaction.Enabled),
		ThresholdPercent: c.Compaction.ThresholdPercent,
		KeepRecentTurns:  cloneIntPtr(c.Compaction.KeepRecentTurns),
		Model:            c.Compaction.Model,
	}
	out.Memory = MemoryJSON{
		Enabled: c.Memory.Enabled, Model: c.Memory.Model, Dir: c.Memory.Dir,
		RecallMaxTurns: c.Memory.RecallMaxTurns, PersistMaxTurns: c.Memory.PersistMaxTurns,
		CopilotMaxTokens: c.Memory.CopilotMaxTokens, MaxSearchHits: c.Memory.MaxSearchHits,
	}
	out.HTTPServer = HTTPServerJSON{
		Host:          c.HTTPServer.Host,
		Port:          c.HTTPServer.Port,
		PublicDocs:    c.HTTPServer.PublicDocs,
		AllowInsecure: c.HTTPServer.AllowInsecure,
		// AuthToken is intentionally redacted; report only whether one is configured.
		AuthConfigured: strings.TrimSpace(c.HTTPServer.AuthToken) != "",
		CORS: HTTPCORSJSON{
			Enabled:        c.HTTPServer.CORS.Enabled,
			AllowedOrigins: append([]string(nil), c.HTTPServer.CORS.AllowedOrigins...),
		},
	}
	for _, rm := range c.HTTPServer.Remotes {
		out.HTTPServer.Remotes = append(out.HTTPServer.Remotes, HTTPRemoteJSON(rm))
	}
	out.Scheduler = SchedulerJSON{
		Enabled: c.Scheduler.Enabled, Dir: c.Scheduler.Dir, MaxQueue: c.Scheduler.MaxQueue,
		Timeout: c.Scheduler.Timeout, RetainSessions: c.Scheduler.RetainSessions,
	}
	tg := c.Gateways.Telegram
	tgJSON := TelegramGatewayJSON{
		Enabled: tg.Enabled, Token: tg.Token, Proxy: tg.Proxy, RichMessages: tg.RichMessages,
		Admins:           append([]int64(nil), tg.Admins...),
		DefaultAccess:    string(tg.DefaultAccess),
		DefaultIsolation: string(tg.DefaultIsolation),
	}
	for _, g := range tg.UserGroups {
		tgJSON.UserGroups = append(tgJSON.UserGroups, TelegramUserGroupJSON{
			Name: g.Name, UserIDs: append([]int64(nil), g.UserIDs...),
		})
	}
	for _, ch := range tg.Chats {
		tgJSON.Chats = append(tgJSON.Chats, TelegramChatJSON{
			ChatID: ch.ChatID, Isolation: string(ch.Isolation), Access: string(ch.Access),
		})
	}
	out.Gateways = GatewaysJSON{Telegram: tgJSON}
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
	cfg.Compaction = Compaction{
		Enabled:          cloneBoolPtr(j.Compaction.Enabled),
		ThresholdPercent: j.Compaction.ThresholdPercent,
		KeepRecentTurns:  cloneIntPtr(j.Compaction.KeepRecentTurns),
		Model:            j.Compaction.Model,
	}
	cfg.Memory = MemoryConfig{
		Enabled: j.Memory.Enabled, Model: j.Memory.Model, Dir: j.Memory.Dir,
		RecallMaxTurns: j.Memory.RecallMaxTurns, PersistMaxTurns: j.Memory.PersistMaxTurns,
		CopilotMaxTokens: j.Memory.CopilotMaxTokens, MaxSearchHits: j.Memory.MaxSearchHits,
	}
	cfg.HTTPServer = HTTPServerConfig{
		Host:          j.HTTPServer.Host,
		Port:          j.HTTPServer.Port,
		AuthToken:     j.HTTPServer.AuthToken,
		PublicDocs:    j.HTTPServer.PublicDocs,
		AllowInsecure: j.HTTPServer.AllowInsecure,
		CORS: HTTPCORSConfig{
			Enabled:        j.HTTPServer.CORS.Enabled,
			AllowedOrigins: append([]string(nil), j.HTTPServer.CORS.AllowedOrigins...),
		},
	}
	for _, rm := range j.HTTPServer.Remotes {
		cfg.HTTPServer.Remotes = append(cfg.HTTPServer.Remotes, HTTPRemote(rm))
	}
	cfg.Scheduler = SchedulerConfig{
		Enabled: j.Scheduler.Enabled, Dir: j.Scheduler.Dir, MaxQueue: j.Scheduler.MaxQueue,
		Timeout: j.Scheduler.Timeout, RetainSessions: j.Scheduler.RetainSessions,
	}
	jt := j.Gateways.Telegram
	tg := TelegramGatewayConfig{
		Enabled: jt.Enabled, Token: jt.Token, Proxy: jt.Proxy, RichMessages: jt.RichMessages,
		Admins:           append([]int64(nil), jt.Admins...),
		DefaultAccess:    AccessLevel(jt.DefaultAccess),
		DefaultIsolation: IsolationMode(jt.DefaultIsolation),
	}
	for _, g := range jt.UserGroups {
		tg.UserGroups = append(tg.UserGroups, TelegramUserGroup{
			Name: g.Name, UserIDs: append([]int64(nil), g.UserIDs...),
		})
	}
	for _, ch := range jt.Chats {
		tg.Chats = append(tg.Chats, TelegramChatConfig{
			ChatID: ch.ChatID, Isolation: IsolationMode(ch.Isolation), Access: AccessLevel(ch.Access),
		})
	}
	cfg.Gateways = GatewayConfig{Telegram: tg}
	return cfg
}

func cloneBoolPtr(p *bool) *bool {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// ParseAndValidateConfigJSON unmarshals JSON into ConfigJSON, maps to Config, applies defaults and validates.
func ParseAndValidateConfigJSON(data []byte, paths Paths) (*Config, error) {
	return ParseConfigJSONPreservingSecrets(data, paths, nil)
}

// ParseConfigJSONPreservingSecrets is like ParseAndValidateConfigJSON but, when current is
// non-nil, carries write-only secrets that GET /coddy/config redacts (currently the
// httpserver auth tokens) from current into the incoming config when the payload omitted them.
// This lets the UI save an edited, redacted config without wiping tokens it never received.
func ParseConfigJSONPreservingSecrets(data []byte, paths Paths, current *Config) (*Config, error) {
	var j ConfigJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	cfg := JSONDTOToConfig(&j, paths)
	preserveRedactedSecrets(cfg, current)
	applyDefaults(cfg)
	if err := validateSubconfigs(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// preserveRedactedSecrets copies redacted, write-only secrets from current into next when next
// left them empty. GET /coddy/config never returns these, so a plain round-trip would drop them.
func preserveRedactedSecrets(next, current *Config) {
	if next == nil || current == nil {
		return
	}
	if strings.TrimSpace(next.HTTPServer.AuthToken) == "" && strings.TrimSpace(current.HTTPServer.AuthToken) != "" {
		next.HTTPServer.AuthToken = current.HTTPServer.AuthToken
	}
}

// MarshalConfigYAML serializes cfg to YAML bytes for disk (Paths is omitted via yaml:"-" on field).
// Always-literal secret fields (proxy URLs) are "$"-escaped so the load-time expansion pass restores
// them verbatim instead of resolving "$WORD"/"$N" fragments to empty environment variables.
func MarshalConfigYAML(cfg *Config) ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	return yaml.Marshal(escapeYAMLSecrets(cfg))
}

// escapeYAMLSecrets returns a copy of cfg with always-literal proxy URLs "$"-escaped for disk.
// It copies only what it mutates (the Providers slice and the gateway proxy string), leaving the
// caller's in-memory *Config untouched — the live config keeps the real, unescaped values.
func escapeYAMLSecrets(cfg *Config) *Config {
	out := *cfg
	if len(cfg.Providers) > 0 {
		out.Providers = make([]ProviderConfig, len(cfg.Providers))
		copy(out.Providers, cfg.Providers)
		for i := range out.Providers {
			out.Providers[i].Proxy = escapeYAMLDollar(out.Providers[i].Proxy)
		}
	}
	out.Gateways.Telegram.Proxy = escapeYAMLDollar(cfg.Gateways.Telegram.Proxy)
	return &out
}
