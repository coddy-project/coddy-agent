package acp

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

// RequestID can be either a number or a string.
type RequestID struct {
	value interface{}
}

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
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
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
	UpdateTypePlan               = "plan"
	UpdateTypeAgentMessageChunk  = "agent_message_chunk"
	UpdateTypeUserMessageChunk   = "user_message_chunk"
	UpdateTypeToolCall           = "tool_call"
	UpdateTypeToolCallUpdate     = "tool_call_update"
	UpdateTypeCurrentModeUpdate  = "current_mode_update"
	UpdateTypeConfigOptionUpdate = "config_option_update"
	UpdateTypeTokenUsage         = "token_usage"
)

// PlanUpdate sends the agent's execution plan to the client.
type PlanUpdate struct {
	SessionUpdate string      `json:"sessionUpdate"`
	Entries       []PlanEntry `json:"entries"`
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
	SessionUpdate string               `json:"sessionUpdate"` // "tool_call_update"
	ToolCallID    string               `json:"toolCallId"`
	Status        string               `json:"status"` // "in_progress", "completed", "failed", "cancelled"
	Content       []ToolCallResultItem `json:"content,omitempty"`
}

// ToolCallResultItem wraps content in a tool call result.
type ToolCallResultItem struct {
	Type    string       `json:"type"` // "content"
	Content ContentBlock `json:"content"`
}

// ModeUpdate notifies the client that the current mode changed.
type ModeUpdate struct {
	SessionUpdate string `json:"sessionUpdate"` // "current_mode_update"
	ModeID        string `json:"modeId"`
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

// ---- ACP session/request_permission ----

// PermissionRequestParams are the parameters for session/request_permission.
type PermissionRequestParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  PermissionToolCall `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
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
