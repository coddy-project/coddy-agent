package acp

import "encoding/json"

// Protocol version supported by this agent.
const ProtocolVersion = 1

// AgentName is the agent's identifier.
const AgentName = "coddy-agent"

// AgentTitle is the human-readable agent name.
const AgentTitle = "Coddy Agent"

// ---- JSON-RPC 2.0 base types ----

// Request represents an incoming JSON-RPC request.
type Request struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      *RequestID  `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// RequestID identifies a JSON-RPC request on the wire as a number or string.
// The server decodes ids from raw JSON in Server.processLine.
type RequestID struct{}

// Response is a JSON-RPC response.
type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

// Notification is a JSON-RPC notification (no id, no response expected).
type Notification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// RPCError represents a JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC error codes.
const (
	ErrParseError     = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternalError  = -32603
)

// ---- ACP initialize ----

// InitializeParams are the parameters for the initialize method.
type InitializeParams struct {
	ProtocolVersion    int                 `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities  `json:"clientCapabilities"`
	ClientInfo         *ImplementationInfo `json:"clientInfo,omitempty"`
}

// ClientCapabilities describes what the client supports.
type ClientCapabilities struct {
	FS       *FSCapabilities `json:"fs,omitempty"`
	Terminal bool            `json:"terminal,omitempty"`
}

// FSCapabilities describes filesystem capabilities of the client.
type FSCapabilities struct {
	ReadTextFile  bool `json:"readTextFile,omitempty"`
	WriteTextFile bool `json:"writeTextFile,omitempty"`
}

// InitializeResult is returned in response to initialize.
type InitializeResult struct {
	ProtocolVersion   int                `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities  `json:"agentCapabilities"`
	AgentInfo         ImplementationInfo `json:"agentInfo"`
	AuthMethods       []string           `json:"authMethods"`
}

// SessionCaps is advertised during initialize (sessionCapabilities in ACP).
type SessionCaps struct{}

// MarshalJSON emits {"list":{}} when list support is enabled.
func (SessionCaps) MarshalJSON() ([]byte, error) {
	return []byte(`{"list":{}}`), nil
}

// AgentCapabilities describes what this agent supports.
type AgentCapabilities struct {
	LoadSession         bool                `json:"loadSession,omitempty"`
	SessionCapabilities *SessionCaps        `json:"sessionCapabilities,omitempty"`
	PromptCapabilities  *PromptCapabilities `json:"promptCapabilities,omitempty"`
	MCPCapabilities     *MCPCapabilities    `json:"mcpCapabilities,omitempty"`
}

// PromptCapabilities lists supported prompt content types.
type PromptCapabilities struct {
	Image           bool `json:"image,omitempty"`
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

// MCPCapabilities lists supported MCP transports.
type MCPCapabilities struct {
	HTTP bool `json:"http,omitempty"`
	SSE  bool `json:"sse,omitempty"`
}

// ImplementationInfo describes a client or agent implementation.
type ImplementationInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

// ---- ACP session/new ----

// SessionNewParams are the parameters for session/new.
type SessionNewParams struct {
	CWD        string      `json:"cwd"`
	MCPServers []MCPServer `json:"mcpServers,omitempty"`
}

// SessionNewResult is returned by session/new.
type SessionNewResult struct {
	SessionID     string         `json:"sessionId"`
	Modes         *ModeState     `json:"modes,omitempty"`
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`
}

// ConfigOption is a session-level configuration selector (Session Config Options in ACP).
type ConfigOption struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Description  string              `json:"description,omitempty"`
	Category     string              `json:"category,omitempty"`
	Type         string              `json:"type,omitempty"` // "select"
	CurrentValue string              `json:"currentValue"`
	Options      []ConfigOptionValue `json:"options"`
}

// ConfigOptionValue is one selectable value for a config option.
type ConfigOptionValue struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// MCPServer represents an MCP server configuration.
type MCPServer struct {
	// Common fields
	Type string `json:"type,omitempty"` // "stdio" (default), "http", "sse"
	Name string `json:"name"`

	// stdio transport
	Command string        `json:"command,omitempty"`
	Args    []string      `json:"args,omitempty"`
	Env     []EnvVariable `json:"env,omitempty"`

	// http/sse transport
	URL     string       `json:"url,omitempty"`
	Headers []HTTPHeader `json:"headers,omitempty"`
}

// EnvVariable is a name-value environment variable pair.
type EnvVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// HTTPHeader is a name-value HTTP header pair.
type HTTPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ModeState holds current and available session modes.
type ModeState struct {
	CurrentModeID  string        `json:"currentModeId"`
	AvailableModes []SessionMode `json:"availableModes"`
}

// SessionMode describes an available operating mode.
type SessionMode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ---- ACP session/load ----

// SessionLoadParams are the parameters for session/load.
type SessionLoadParams struct {
	SessionID  string      `json:"sessionId"`
	CWD        string      `json:"cwd"`
	MCPServers []MCPServer `json:"mcpServers,omitempty"`
}

// SessionLoadResult is returned by session/load after restoring state.
type SessionLoadResult struct {
	Modes         *ModeState     `json:"modes,omitempty"`
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`
}

// ---- ACP session/list ----

// SessionListParams are parameters for session/list.
type SessionListParams struct {
	Cursor *string `json:"cursor,omitempty"`
	CWD    *string `json:"cwd,omitempty"`
}

// SessionListInfo is one row returned from session/list.
type SessionListInfo struct {
	SessionID string  `json:"sessionId"`
	CWD       string  `json:"cwd"`
	Title     *string `json:"title,omitempty"`
	UpdatedAt *string `json:"updatedAt,omitempty"`
}

// SessionListResult is the response payload for session/list.
type SessionListResult struct {
	Sessions   []SessionListInfo `json:"sessions"`
	NextCursor *string           `json:"nextCursor,omitempty"`
}

// ---- ACP session/prompt ----

// SessionPromptParams are the parameters for session/prompt.
type SessionPromptParams struct {
	SessionID  string                 `json:"sessionId"`
	Prompt     []ContentBlock         `json:"prompt"`
	Meta       map[string]interface{} `json:"_meta,omitempty"`
	ImageParts []ImagePartRef         `json:"imageParts,omitempty"`
}

// ImagePartRef carries an inline image or file for a multimodal agent prompt.
type ImagePartRef struct {
	// DataURL is a data URI ("data:<mime>;base64,<bytes>") or an HTTPS image URL.
	DataURL string `json:"data_url"`
	// Name is the original file name (informational).
	Name string `json:"name,omitempty"`
}

// SessionPromptResult is the response to session/prompt.
type SessionPromptResult struct {
	StopReason StopReason `json:"stopReason"`
}

// StopReason describes why a prompt turn ended.
type StopReason string

const (
	StopReasonEndTurn   StopReason = "end_turn"
	StopReasonMaxTokens StopReason = "max_tokens"
	StopReasonMaxTurns  StopReason = "max_turns"
	StopReasonRefused   StopReason = "agent_refused"
	StopReasonCancelled StopReason = "cancelled"
)

// ---- ACP session/cancel ----

// SessionCancelParams are the parameters for the session/cancel notification.
type SessionCancelParams struct {
	SessionID string `json:"sessionId"`
}

// ---- ACP session/set_mode ----

// SessionSetModeParams are the parameters for session/set_mode.
type SessionSetModeParams struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

// SessionSetConfigOptionParams are the parameters for session/set_config_option.
type SessionSetConfigOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}

// SessionSetConfigOptionResult is returned by session/set_config_option.
type SessionSetConfigOptionResult struct {
	ConfigOptions []ConfigOption `json:"configOptions"`
}

// ---- ACP session/update ----

// SessionUpdateParams wraps a session update notification.
type SessionUpdateParams struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

// SessionUpdate is the discriminated union of all update types.
// The SessionUpdateType field selects the concrete type.
type SessionUpdate map[string]interface{}

// Update type constants for the "sessionUpdate" discriminator field.
const (
	UpdateTypePlan                    = "plan"
	UpdateTypeAgentMessageChunk       = "agent_message_chunk"
	UpdateTypeUserMessageChunk        = "user_message_chunk"
	UpdateTypeToolCall                = "tool_call"
	UpdateTypeToolCallUpdate          = "tool_call_update"
	UpdateTypeCurrentModeUpdate       = "current_mode_update"
	UpdateTypeConfigOptionUpdate      = "config_option_update"
	UpdateTypeTokenUsage              = "token_usage"
	UpdateTypeUsage                   = "usage_update"
	UpdateTypeProviderUsage           = "provider_usage"
	UpdateTypeMemoryPhase             = "memory_phase"
	UpdateTypeMemoryMessageChunk      = "memory_message_chunk"
	UpdateTypeAvailableCommandsUpdate = "available_commands_update"
)

// AvailableCommand is one slash command advertised to ACP clients.
type AvailableCommand struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Input       *AvailableCommandInput `json:"input,omitempty"`
}

// AvailableCommandInput is optional metadata for slash command text input.
type AvailableCommandInput struct {
	Hint string `json:"hint,omitempty"`
}

// AvailableCommandsUpdate publishes the current slash command catalog for a session.
type AvailableCommandsUpdate struct {
	SessionUpdate     string             `json:"sessionUpdate"` // "available_commands_update"
	AvailableCommands []AvailableCommand `json:"availableCommands"`
}

// PlanUpdate sends the agent's execution plan to the client.
type PlanUpdate struct {
	SessionUpdate string                 `json:"sessionUpdate"`
	Entries       []PlanEntry            `json:"entries"`
	Meta          map[string]interface{} `json:"_meta,omitempty"`
}

// PlanEntry is a single item in the agent's plan.
type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"` // "high", "medium", "low"
	Status   string `json:"status"`             // "pending", "in_progress", "completed", "failed"
}

// MessageChunkUpdate sends a text chunk from agent or user.
type MessageChunkUpdate struct {
	SessionUpdate string       `json:"sessionUpdate"` // "agent_message_chunk" or "user_message_chunk"
	Content       ContentBlock `json:"content"`
}

// ToolCallUpdate announces a new tool call.
type ToolCallUpdate struct {
	SessionUpdate string `json:"sessionUpdate"` // "tool_call"
	ToolCallID    string `json:"toolCallId"`
	Title         string `json:"title,omitempty"`
	Kind          string `json:"kind,omitempty"` // "read", "write", "run_command", "other", "switch_mode"
	Status        string `json:"status"`         // "pending"
}

// ToolCallStatusUpdate reports progress on an existing tool call.
type ToolCallStatusUpdate struct {
	SessionUpdate string                 `json:"sessionUpdate"` // "tool_call_update"
	ToolCallID    string                 `json:"toolCallId"`
	Status        string                 `json:"status"` // "in_progress", "completed", "failed", "cancelled"
	Content       []ToolCallResultItem   `json:"content,omitempty"`
	Meta          map[string]interface{} `json:"_meta,omitempty"` // ACP extensibility; Coddy uses coddy.toolResultPreview for truncated previews
}

// ToolCallResultItem wraps content in a tool call result.
type ToolCallResultItem struct {
	Type    string       `json:"type"` // "content"
	Content ContentBlock `json:"content"`
}

// ModeUpdate notifies the client that the current mode changed.
type ModeUpdate struct {
	SessionUpdate string `json:"sessionUpdate"` // "current_mode_update"
	CurrentModeID string `json:"currentModeId"`
}

// ConfigOptionUpdate sends the full session configuration options state to the client.
type ConfigOptionUpdate struct {
	SessionUpdate string         `json:"sessionUpdate"` // "config_option_update"
	ConfigOptions []ConfigOption `json:"configOptions"`
}

// TokenUsageUpdate reports token consumption for the current turn.
type TokenUsageUpdate struct {
	SessionUpdate string `json:"sessionUpdate"` // "token_usage"
	InputTokens   int    `json:"inputTokens"`
	OutputTokens  int    `json:"outputTokens"`
	TotalTokens   int    `json:"totalTokens"`
}

// UsageUpdate reports how much of the model context window is currently occupied.
type UsageUpdate struct {
	SessionUpdate string `json:"sessionUpdate"` // "usage_update"
	Used          int    `json:"used"`
	Size          int    `json:"size"`
}

// ProviderUsageUpdate reports the provider-side account quota behind the
// session's model: how much of each metered window is spent, when it resets,
// the wallet balance for wallet keys, and whether a request would be refused
// right now. Today only the neuraldeep provider type fills it (GET /v1/limits
// on the hub); consumers branch on ProviderType. The update never carries a
// credential, a hub URL, or a dollar amount.
//
// Relative durations (ResetInSec, RetryInSec, Rate.ResetInSec) are corrected
// for the snapshot's age when a cached snapshot is delivered, so a client can
// schedule its refresh from the value as received. Unsupported marks a
// provider type that has no usage source at all; Error marks a transport or
// credential failure (the windows, when present, are then Stale).
type ProviderUsageUpdate struct {
	SessionUpdate string `json:"sessionUpdate"` // "provider_usage"
	// Provider is the provider row name; ProviderType its wire type.
	Provider     string `json:"provider"`
	ProviderType string `json:"providerType"`
	// ObservedAt is the hub's own timestamp of the counters; FetchedAt is
	// the local time of the request that fetched them.
	ObservedAt string `json:"observedAt,omitempty"`
	FetchedAt  string `json:"fetchedAt,omitempty"`
	// Plan is the subscription tier (free, starter, pro); KeyName names the
	// key on the hub (never its value).
	Plan    string `json:"plan,omitempty"`
	KeyName string `json:"keyName,omitempty"`
	// Windows are the metered volumes: session, week, day.
	Windows []UsageWindow `json:"windows,omitempty"`
	// Rate is the live requests-per-minute window of the current minute.
	Rate *UsageRate `json:"rate,omitempty"`
	// CooldownSec is the pause the hub imposes after an exhausted session.
	CooldownSec int `json:"cooldownSec,omitempty"`
	// Wallet is the account's own balance in rubles, wallet keys only.
	Wallet *UsageWallet `json:"wallet,omitempty"`
	// Blocked reports that a chat request would be refused now; Blockers
	// lists why (session_exhausted, week_exhausted, rpm_exhausted,
	// session_cooldown, abuse_cooldown, daily_capacity_exhausted,
	// key_blocked, key_cap_blocked, wallet_empty, user_blocked).
	Blocked  bool     `json:"blocked"`
	Blockers []string `json:"blockers,omitempty"`
	// RetryAt is when the timed blockers lift (hub clock); RetryInSec the
	// same as a relative, age-corrected duration.
	RetryAt    string `json:"retryAt,omitempty"`
	RetryInSec int    `json:"retryInSec,omitempty"`
	// Unlimited marks a key without volume windows (wallet or bypass keys);
	// UnlimitedModels lists upstream model ids that bypass the windows on a
	// metered key. The snapshot is account-wide: a client compares the part
	// of its model selector after the first slash with this list.
	Unlimited       bool     `json:"unlimited,omitempty"`
	UnlimitedModels []string `json:"unlimitedModels,omitempty"`
	// Stale marks windows carried over from an earlier successful fetch
	// because the latest one failed (see Error).
	Stale bool `json:"stale,omitempty"`
	// Error is the failure kind of the latest fetch: "unauthorized",
	// "unavailable", or "invalid".
	Error string `json:"error,omitempty"`
	// Unsupported marks a provider type that has no usage source.
	Unsupported bool `json:"unsupported,omitempty"`
	// RefreshPending says the snapshot is older than the turn that asked for
	// it and a refresh is deferred by the hub's pacing floor; RefreshInSec is
	// when it fires, so a client can schedule one follow-up read.
	RefreshPending bool `json:"refreshPending,omitempty"`
	RefreshInSec   int  `json:"refreshInSec,omitempty"`
	// Resuming marks the update the agent sends while a turn waits for a hit
	// limit to lift (agent.wait_for_limit_reset): Blocked with RetryAt from
	// the provider's own pause, re-sent every 20 s so the countdown stays
	// visible. It comes from the turn, not from the usage source, and the
	// next turn-end read replaces it.
	Resuming bool `json:"resuming,omitempty"`
}

// UsageWindow is one metered volume window of a ProviderUsageUpdate. The
// counters are optional: a percent-only window (day) omits them.
type UsageWindow struct {
	// ID is "session", "week", or "day"; Label is the display label the
	// provider uses for it ("3h", "week", "day").
	ID          string  `json:"id"`
	Label       string  `json:"label"`
	Used        *int    `json:"used,omitempty"`
	Limit       *int    `json:"limit,omitempty"`
	Remaining   *int    `json:"remaining,omitempty"`
	UsedPercent float64 `json:"usedPercent"`
	Exhausted   bool    `json:"exhausted,omitempty"`
	// ResetsAt is the hub's absolute reset time (display); ResetInSec the
	// age-corrected relative one (local deadlines).
	ResetsAt   string `json:"resetsAt,omitempty"`
	ResetInSec int    `json:"resetInSec,omitempty"`
}

// UsageRate is the live per-minute request window of a ProviderUsageUpdate.
type UsageRate struct {
	Used       int `json:"used"`
	Limit      int `json:"limit"`
	Remaining  int `json:"remaining"`
	ResetInSec int `json:"resetInSec"`
}

// UsageWallet is the account's own money on the provider, in rubles. The
// balance may be negative on post-paid accounts.
type UsageWallet struct {
	BalanceRub  float64 `json:"balanceRub"`
	SpentRub30d float64 `json:"spentRub30d"`
}

// MemoryPhaseUpdate marks start or completion of a memory copilot sub-phase.
type MemoryPhaseUpdate struct {
	SessionUpdate string `json:"sessionUpdate"` // "memory_phase"
	MemoryRowID   string `json:"memoryRowId"`
	Phase         string `json:"phase"`  // "memory" (single pass) | "recall" | "persist" (legacy replay)
	Status        string `json:"status"` // "started" | "completed"
	UserTurnIndex int    `json:"userTurnIndex,omitempty"`
	DurationMs    int64  `json:"durationMs,omitempty"`
	// Recall-only populates when Phase is recall and Status is completed (coddy_memory_read paths).
	RecallReadPaths []string `json:"recallReadPaths,omitempty"`
	// Persist-only populates when Phase is persist and Status is completed.
	PersistSaved        bool   `json:"persistSaved,omitempty"`
	PersistSavedBody    string `json:"persistSavedBody,omitempty"` // markdown persisted when PersistSaved true (truncated for wire)
	PersistRelativePath string `json:"persistRelativePath,omitempty"`
	PersistTitle        string `json:"persistTitle,omitempty"`
}

// MemoryMessageChunkUpdate streams memory copilot model deltas to the client (not part of llm.Messages).
type MemoryMessageChunkUpdate struct {
	SessionUpdate string `json:"sessionUpdate"` // "memory_message_chunk"
	MemoryRowID   string `json:"memoryRowId"`
	Phase         string `json:"phase"` // "memory" | "recall" | "persist"
	Kind          string `json:"kind"`  // "text" | "reasoning"
	Delta         string `json:"delta"`
}

// ---- ACP session/request_permission ----

// PermissionRequestParams are the parameters for session/request_permission.
type PermissionRequestParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  PermissionToolCall `json:"toolCall"`
	Options   []PermissionOption `json:"options"`

	// EffectivePermissionMode is the permission mode of the agent that asks,
	// for in-process senders only (never serialised). A subagent's request is
	// forwarded under its parent's session id, so a sender that decides
	// "bypass, auto-allow" from the session would apply the parent's mode to a
	// child whose definition narrowed it; when this is set, the sender uses it
	// instead of looking the session up.
	EffectivePermissionMode string `json:"-"`
}

// PermissionToolCall describes the tool call needing permission.
type PermissionToolCall struct {
	ToolCallID string               `json:"toolCallId"`
	Title      string               `json:"title,omitempty"`
	Kind       string               `json:"kind,omitempty"`
	Status     string               `json:"status"`
	Content    []ToolCallResultItem `json:"content,omitempty"`
}

// PermissionOption is a choice presented to the user.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // "allow_once", "allow_always", "reject_once"
}

// PermissionResult is the client's response to a permission request.
type PermissionResult struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId"`
}

// UnmarshalJSON accepts both response shapes seen from ACP clients.
//
// The protocol nests the outcome in its own object, which is what Zed sends:
//
//	{"outcome": {"outcome": "selected", "optionId": "allow"}}
//	{"outcome": {"outcome": "cancelled"}}
//
// Coddy's own surfaces (console, web UI, remote client) and some editor
// extensions send the flat form instead:
//
//	{"outcome": "selected", "optionId": "allow"}
//
// Decoding the nested form into a plain string used to fail, and the caller
// read that failure as a cancellation - every approval from a spec-compliant
// client turned into "permission denied by user".
func (p *PermissionResult) UnmarshalJSON(data []byte) error {
	var wire struct {
		Outcome  json.RawMessage `json:"outcome"`
		OptionID string          `json:"optionId"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	p.Outcome = ""
	p.OptionID = wire.OptionID
	if len(wire.Outcome) == 0 {
		return nil
	}
	var flat string
	if err := json.Unmarshal(wire.Outcome, &flat); err == nil {
		p.Outcome = flat
		return nil
	}
	var nested struct {
		Outcome  string `json:"outcome"`
		OptionID string `json:"optionId"`
	}
	if err := json.Unmarshal(wire.Outcome, &nested); err != nil {
		return err
	}
	p.Outcome = nested.Outcome
	if nested.OptionID != "" {
		p.OptionID = nested.OptionID
	}
	return nil
}

// ---- ACP session/request_question ----

// QuestionOption is one selectable choice for QuestionPrompt.
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// QuestionPrompt is one interactive question with optional header and multiple choice flags.
type QuestionPrompt struct {
	Header   string           `json:"header,omitempty"`
	Question string           `json:"question"`
	Options  []QuestionOption `json:"options"`
	Multiple bool             `json:"multiple,omitempty"`
	Custom   bool             `json:"custom,omitempty"`
}

// QuestionRequestParams are the parameters for session/request_question.
type QuestionRequestParams struct {
	SessionID  string           `json:"sessionId"`
	RequestID  string           `json:"requestId"`
	ToolCallID string           `json:"toolCallId,omitempty"`
	Questions  []QuestionPrompt `json:"questions"`
}

// QuestionResult is the client's response to session/request_question.
type QuestionResult struct {
	Answers [][]string `json:"answers"`
}

// ---- Content blocks ----

// ContentBlock is a polymorphic content item used in prompts and messages.
type ContentBlock struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	Resource *Resource `json:"resource,omitempty"`
}

// Content block type values for agent_message_chunk (MessageChunkUpdate).
const (
	ContentTypeText      = "text"
	ContentTypeReasoning = "reasoning"
)

// Resource is a file or other resource referenced in a content block.
type Resource struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

// ---- fs methods (agent calls these on client) ----

// FSReadParams are the parameters for fs/read_text_file.
type FSReadParams struct {
	Path string `json:"path"`
}

// FSReadResult is the result of fs/read_text_file.
type FSReadResult struct {
	Content string `json:"content"`
}

// FSWriteParams are the parameters for fs/write_text_file.
type FSWriteParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
