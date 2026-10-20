package llm

// The client side of the Agent Client Protocol as the acp provider reads it.
// The protocol itself - JSON-RPC over the agent's stdio, the requests Coddy
// sends, the initialize and prompt results - is github.com/coder/acp-go-sdk's.
// What is declared here is what Coddy decodes on its own, leniently, because
// the agents it runs send more than the SDK's schema holds:
//
//   - a session's state (session/new, set_config_option): an option of a
//     type the schema does not know would fail the whole response there, and
//     the models of an agent that still offers them the older way
//     (models, session/set_model) are not in it;
//   - session/update: Coddy as the agent streams its reasoning as a message
//     chunk of type "reasoning", and a tool call's content and raw input are
//     only shown, never interpreted;
//   - session/request_permission, for the same tool call fields.

import (
	"encoding/json"

	acpsdk "github.com/coder/acp-go-sdk"
)

// acpAuthRequired is the code an agent answers session/new with until the
// client called authenticate.
const acpAuthRequired = -32000

// acpMessage is the answer to a request of Coddy's: its result or its error.
type acpMessage struct {
	Result json.RawMessage
	Error  *acpRPCError
}

// acpRPCError is a JSON-RPC error object.
type acpRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
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

type acpSetConfigOptionResult struct {
	ConfigOptions []acpConfigOption `json:"configOptions"`
}

// acpSetModelParams is session/set_model, the older way of selecting a
// session's model, which the SDK's schema no longer has.
type acpSetModelParams struct {
	SessionID string `json:"sessionId"`
	ModelID   string `json:"modelId"`
}

// acpContentBlock is a block of a prompt as the provider builds it: text or
// an image. acpSDKBlocks turns it into the protocol's.
type acpContentBlock struct {
	Type     string
	Text     string
	MIMEType string
	Data     string
}

// acpSDKBlocks is the prompt as the protocol carries it.
func acpSDKBlocks(blocks []acpContentBlock) []acpsdk.ContentBlock {
	out := make([]acpsdk.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "image" {
			out = append(out, acpsdk.ImageBlock(b.Data, b.MIMEType))
			continue
		}
		out = append(out, acpsdk.TextBlock(b.Text))
	}
	return out
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

// acpPermissionOutcome is Coddy's answer to a permission request: selected
// with the agent's option id, or cancelled.
type acpPermissionOutcome struct {
	Outcome  string
	OptionID string
}
