package llm

// The client side of the Agent Client Protocol wire, as far as the acp
// provider speaks it: JSON-RPC 2.0 over newline-delimited JSON on the agent's
// stdin and stdout. internal/acp holds the agent side of the same protocol,
// but it imports the session and agent packages, so the few client shapes are
// declared here. Field names are the protocol's camelCase.

import "encoding/json"

// acpProtocolVersion is the ACP major version the provider speaks.
const acpProtocolVersion = 1

// JSON-RPC error codes the provider tells apart.
const (
	rpcMethodNotFound = -32601
	// acpAuthRequired is the code an agent answers session/new with until the
	// client called authenticate.
	acpAuthRequired = -32000
)

// acpMessage is any JSON-RPC message, decoded far enough to route it.
type acpMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *acpRPCError    `json:"error,omitempty"`
}

// acpRPCError is a JSON-RPC error object.
type acpRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type acpInitializeParams struct {
	ProtocolVersion    int                   `json:"protocolVersion"`
	ClientCapabilities acpClientCapabilities `json:"clientCapabilities"`
	ClientInfo         acpImplementation     `json:"clientInfo"`
}

// acpClientCapabilities says what the agent may ask of Coddy: nothing. The
// agent reads and writes files and runs commands with its own tools.
type acpClientCapabilities struct {
	FS       acpFSCapabilities `json:"fs"`
	Terminal bool              `json:"terminal"`
}

type acpFSCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type acpImplementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

type acpInitializeResult struct {
	ProtocolVersion   int                  `json:"protocolVersion"`
	AgentCapabilities acpAgentCapabilities `json:"agentCapabilities"`
	AgentInfo         *acpImplementation   `json:"agentInfo,omitempty"`
	AuthMethods       []acpAuthMethod      `json:"authMethods,omitempty"`
}

type acpAgentCapabilities struct {
	PromptCapabilities  acpPromptCapabilities  `json:"promptCapabilities"`
	SessionCapabilities acpSessionCapabilities `json:"sessionCapabilities"`
}

type acpPromptCapabilities struct {
	Image bool `json:"image,omitempty"`
}

// acpSessionCapabilities holds the optional session methods; a present
// (non-null) member means the agent supports the method.
type acpSessionCapabilities struct {
	Close json.RawMessage `json:"close,omitempty"`
}

func (c acpSessionCapabilities) canClose() bool {
	return len(c.Close) > 0 && string(c.Close) != "null"
}

type acpAuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

type acpNewSessionParams struct {
	CWD        string        `json:"cwd"`
	MCPServers []interface{} `json:"mcpServers"`
}

// acpSessionState is what session/new returns and the agent's later
// notifications keep current: the modes, the models and the config options.
type acpSessionState struct {
	SessionID     string            `json:"sessionId"`
	Modes         *acpModeState     `json:"modes,omitempty"`
	Models        *acpModelState    `json:"models,omitempty"`
	ConfigOptions []acpConfigOption `json:"configOptions,omitempty"`
}

type acpModeState struct {
	CurrentModeID  string    `json:"currentModeId"`
	AvailableModes []acpMode `json:"availableModes"`
}

type acpMode struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type acpModelState struct {
	CurrentModelID  string     `json:"currentModelId"`
	AvailableModels []acpModel `json:"availableModels"`
}

type acpModel struct {
	ModelID string `json:"modelId"`
	Name    string `json:"name,omitempty"`
}

// acpConfigOption is one session config option. Only select options are
// read; a boolean one is never offered to Coddy, which does not advertise
// support for them.
type acpConfigOption struct {
	ID           string          `json:"id"`
	Name         string          `json:"name,omitempty"`
	Category     string          `json:"category,omitempty"`
	Type         string          `json:"type,omitempty"`
	CurrentValue json.RawMessage `json:"currentValue,omitempty"`
	Options      json.RawMessage `json:"options,omitempty"`
}

// acpConfigValue is one value of a select option.
type acpConfigValue struct {
	Value string `json:"value"`
	Name  string `json:"name,omitempty"`
}

// values returns the select values of the option, groups flattened.
func (o acpConfigOption) values() []acpConfigValue {
	if len(o.Options) == 0 {
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(o.Options, &raw); err != nil {
		return nil
	}
	var out []acpConfigValue
	for _, r := range raw {
		var group struct {
			Group   string           `json:"group"`
			Options []acpConfigValue `json:"options"`
		}
		if err := json.Unmarshal(r, &group); err == nil && group.Group != "" {
			out = append(out, group.Options...)
			continue
		}
		var v acpConfigValue
		if err := json.Unmarshal(r, &v); err == nil && v.Value != "" {
			out = append(out, v)
		}
	}
	return out
}

// current returns the option's current value when it is a string.
func (o acpConfigOption) current() string {
	var s string
	if err := json.Unmarshal(o.CurrentValue, &s); err != nil {
		return ""
	}
	return s
}

type acpSetConfigOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}

type acpSetConfigOptionResult struct {
	ConfigOptions []acpConfigOption `json:"configOptions"`
}

type acpSetModeParams struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

type acpSetModelParams struct {
	SessionID string `json:"sessionId"`
	ModelID   string `json:"modelId"`
}

type acpSessionIDParams struct {
	SessionID string `json:"sessionId"`
}

type acpPromptParams struct {
	SessionID string            `json:"sessionId"`
	Prompt    []acpContentBlock `json:"prompt"`
}

// acpContentBlock is a prompt or message content block: text or an image.
type acpContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
	URI      string `json:"uri,omitempty"`
	Name     string `json:"name,omitempty"`
}

type acpPromptResult struct {
	StopReason string    `json:"stopReason"`
	Usage      *acpUsage `json:"usage,omitempty"`
}

// acpUsage is the token usage a prompt result reports.
type acpUsage struct {
	InputTokens       int `json:"inputTokens"`
	OutputTokens      int `json:"outputTokens"`
	ThoughtTokens     int `json:"thoughtTokens"`
	CachedReadTokens  int `json:"cachedReadTokens"`
	CachedWriteTokens int `json:"cachedWriteTokens"`
}

type acpSessionNotification struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

// acpSessionUpdate is a session/update payload, decoded as far as the
// provider reads it.
type acpSessionUpdate struct {
	SessionUpdate string            `json:"sessionUpdate"`
	Content       json.RawMessage   `json:"content,omitempty"`
	ToolCallID    string            `json:"toolCallId,omitempty"`
	Title         string            `json:"title,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	Status        string            `json:"status,omitempty"`
	CurrentModeID string            `json:"currentModeId,omitempty"`
	ConfigOptions []acpConfigOption `json:"configOptions,omitempty"`
	RawInput      json.RawMessage   `json:"rawInput,omitempty"`
}

// toolContent returns the content of a tool_call or tool_call_update: an
// array of tool call content items (a message chunk's content is one
// content block instead).
func (u acpSessionUpdate) toolContent() []json.RawMessage {
	var items []json.RawMessage
	if len(u.Content) == 0 || u.Content[0] != '[' {
		return nil
	}
	if err := json.Unmarshal(u.Content, &items); err != nil {
		return nil
	}
	return items
}

// acpPermissionRequest is the agent's session/request_permission.
type acpPermissionRequest struct {
	SessionID string                `json:"sessionId"`
	ToolCall  acpPermissionToolCall `json:"toolCall"`
	Options   []acpPermissionOption `json:"options"`
}

type acpPermissionToolCall struct {
	ToolCallID string            `json:"toolCallId"`
	Title      string            `json:"title,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Content    []json.RawMessage `json:"content,omitempty"`
	RawInput   json.RawMessage   `json:"rawInput,omitempty"`
	Locations  []struct {
		Path string `json:"path"`
	} `json:"locations,omitempty"`
}

type acpPermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

// acpPermissionOutcome is the client's answer to a permission request.
type acpPermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

type acpPermissionResult struct {
	Outcome acpPermissionOutcome `json:"outcome"`
}
