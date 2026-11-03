//go:build http

package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// responsesDirectRequest is the Responses create shape a configured model
// answers: the supported subset plus the coddy extensions the profile shares.
type responsesDirectRequest struct {
	Model       string                         `json:"model"`
	Input       json.RawMessage                `json:"input"`
	Stream      bool                           `json:"stream"`
	Metadata    json.RawMessage                `json:"metadata,omitempty"`
	Attachments []session.PromptFileAttachment `json:"attachments,omitempty"`
	InlineFiles []inlineFileJSON               `json:"inline_files,omitempty"`

	Instructions    *string         `json:"instructions"`
	Tools           json.RawMessage `json:"tools"`
	ToolChoice      json.RawMessage `json:"tool_choice"`
	MaxOutputTokens *int            `json:"max_output_tokens"`
	Temperature     *float64        `json:"temperature"`
	TopP            *float64        `json:"top_p"`
	Reasoning       *struct {
		Effort string `json:"effort"`
	} `json:"reasoning"`
}

// responsesAllowedFields are the request keys a direct model answers; anything
// else - a field the subset does not implement, or one nobody knows - is
// refused rather than silently ignored.
var responsesAllowedFields = map[string]bool{
	"model": true, "input": true, "stream": true, "metadata": true,
	"attachments": true, "inline_files": true,
	"instructions": true, "tools": true, "tool_choice": true,
	"max_output_tokens": true, "temperature": true, "top_p": true,
	"reasoning": true, "store": true,
}

// validateResponsesDirectFields walks the raw request object and refuses every
// field the direct path does not answer. previous_response_id, store:true and
// the like are named explicitly so a client learns they are unsupported rather
// than being silently faked.
func validateResponsesDirectFields(raw json.RawMessage) *chatParameterError {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return &chatParameterError{Message: "request must be a JSON object"}
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !responsesAllowedFields[key] {
			return unsupportedChatParameter(key)
		}
		value := bytes.TrimSpace(fields[key])
		switch key {
		case "store":
			var v bool
			if err := json.Unmarshal(value, &v); err != nil {
				return &chatParameterError{Param: key, Message: "store must be a boolean or null"}
			} else if v {
				return &chatParameterError{Param: key, Message: "store: true is not supported: responses are stateless"}
			}
		case "reasoning":
			if len(value) == 0 || bytes.Equal(value, []byte("null")) {
				continue
			}
			var reasoning map[string]json.RawMessage
			if err := json.Unmarshal(value, &reasoning); err != nil || reasoning == nil {
				return &chatParameterError{Param: key, Message: "reasoning must be an object"}
			}
			for sub := range reasoning {
				if sub != "effort" {
					return unsupportedChatParameter("reasoning." + sub)
				}
			}
		}
	}
	return nil
}

// responsesItemToMessages converts the request's input - a string or the typed
// item array - into the provider's message list. A reasoning item's summary
// lands on the adjacent assistant message,
// contiguous reasoning, function_call and assistant message items merge into
// one assistant message, and function_call_output becomes a tool message.
func responsesInputToMessages(raw json.RawMessage) ([]llm.Message, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("input is required")
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("input must be a string or an array of items")
		}
		if strings.TrimSpace(s) == "" {
			return nil, errors.New("input must not be empty")
		}
		return []llm.Message{{Role: llm.RoleUser, Content: s}}, nil
	}
	if trimmed[0] != '[' {
		return nil, errors.New("input must be a string or an array of items")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, fmt.Errorf("invalid input items: %w", err)
	}
	msgs, err := responsesItemsToMessages(items)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, errors.New("input must contain at least one item")
	}
	if last := msgs[len(msgs)-1]; last.Role != llm.RoleUser && last.Role != llm.RoleTool {
		return nil, errors.New("input must end with a user message or a function_call_output")
	}
	return msgs, nil
}

// responsesInputItem is one entry of the input array, decoded across the
// fields the item kinds use.
type responsesInputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	ID        string          `json:"id"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Output    json.RawMessage `json:"output"`
	Summary   json.RawMessage `json:"summary"`
}

// responsesCheckKeys refuses a field the item, part or tool kind does not
// carry, so unsupported data is named rather than silently dropped.
func responsesCheckKeys(fields map[string]json.RawMessage, where string, allowed ...string) error {
	for key := range fields {
		found := false
		for _, a := range allowed {
			if key == a {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s: unsupported field %q", where, key)
		}
	}
	return nil
}

func responsesItemsToMessages(items []json.RawMessage) ([]llm.Message, error) {
	var msgs []llm.Message
	var pendingReasoning []string
	var pendingText []string
	var pendingCalls []llm.ToolCall
	// flushAssistant merges the contiguous reasoning, function_call and
	// assistant message items seen so far into one assistant message: that is
	// one model turn as the provider expects to read it back.
	flushAssistant := func() error {
		if len(pendingCalls) == 0 && len(pendingText) == 0 {
			if len(pendingReasoning) > 0 {
				return errors.New("reasoning summary requires an adjacent assistant message or function_call")
			}
			return nil
		}
		msg := llm.Message{
			Role:      llm.RoleAssistant,
			Content:   strings.Join(pendingText, ""),
			ToolCalls: pendingCalls,
		}
		if len(pendingReasoning) > 0 {
			msg.Reasoning = strings.Join(pendingReasoning, "\n\n")
			pendingReasoning = nil
		}
		msgs = append(msgs, msg)
		pendingText, pendingCalls = nil, nil
		return nil
	}
	for i, rawItem := range items {
		where := fmt.Sprintf("input[%d]", i)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(rawItem, &fields); err != nil || fields == nil {
			return nil, fmt.Errorf("%s: item must be an object", where)
		}
		var item responsesInputItem
		if err := json.Unmarshal(rawItem, &item); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		itemType := item.Type
		if itemType == "" {
			itemType = "message"
		}
		switch itemType {
		case "message":
			if err := responsesCheckKeys(fields, where, "type", "role", "content", "id", "status"); err != nil {
				return nil, err
			}
			switch role := strings.TrimSpace(item.Role); role {
			case "user", "system", "developer":
				if err := flushAssistant(); err != nil {
					return nil, fmt.Errorf("%s: %w", where, err)
				}
				text, err := responsesContentText(item.Content, where, map[string][]string{
					"input_text": {"type", "text"},
				})
				if err != nil {
					return nil, err
				}
				llmRole := llm.RoleUser
				if role != "user" {
					llmRole = llm.RoleSystem
				}
				msgs = append(msgs, llm.Message{Role: llmRole, Content: text})
			case "assistant":
				text, err := responsesContentText(item.Content, where, map[string][]string{
					"output_text": {"type", "text", "annotations"},
					"refusal":     {"type", "refusal"},
				})
				if err != nil {
					return nil, err
				}
				pendingText = append(pendingText, text)
			default:
				return nil, fmt.Errorf("%s: unsupported message role %q", where, item.Role)
			}
		case "reasoning":
			if err := responsesCheckKeys(fields, where, "type", "id", "summary", "status"); err != nil {
				return nil, err
			}
			texts, err := responsesSummaryText(item.Summary, where)
			if err != nil {
				return nil, err
			}
			pendingReasoning = append(pendingReasoning, texts...)
		case "function_call":
			if err := responsesCheckKeys(fields, where, "type", "id", "call_id", "name", "arguments", "status"); err != nil {
				return nil, err
			}
			callID := strings.TrimSpace(item.CallID)
			name := strings.TrimSpace(item.Name)
			if callID == "" || name == "" {
				return nil, fmt.Errorf("%s: function_call requires call_id and name", where)
			}
			args, err := responsesArgumentsText(item.Arguments)
			if err != nil {
				return nil, fmt.Errorf("%s: function_call %w", where, err)
			}
			pendingCalls = append(pendingCalls, llm.ToolCall{ID: callID, Name: name, InputJSON: args})
		case "function_call_output":
			if err := responsesCheckKeys(fields, where, "type", "id", "call_id", "output", "status"); err != nil {
				return nil, err
			}
			callID := strings.TrimSpace(item.CallID)
			if callID == "" {
				return nil, fmt.Errorf("%s: function_call_output requires call_id", where)
			}
			text, err := responsesContentText(item.Output, where, map[string][]string{
				"input_text": {"type", "text"},
			})
			if err != nil {
				return nil, err
			}
			if err := flushAssistant(); err != nil {
				return nil, fmt.Errorf("%s: %w", where, err)
			}
			msgs = append(msgs, llm.Message{Role: llm.RoleTool, ToolCallID: callID, Content: text})
		default:
			return nil, fmt.Errorf("%s: unsupported item type %q", where, item.Type)
		}
	}
	if err := flushAssistant(); err != nil {
		return nil, err
	}
	return msgs, nil
}

// responsesContentText reads a message item's content or a
// function_call_output's output: a string, or the part array. allowed maps
// each part type to the keys it may carry - a user or tool-output part admits
// input_text, an assistant part output_text and refusal.
func responsesContentText(raw json.RawMessage, where string, allowed map[string][]string) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return "", fmt.Errorf("%s: must be a string or an array of parts", where)
		}
		return s, nil
	}
	if trimmed[0] != '[' {
		return "", fmt.Errorf("%s: must be a string or an array of parts", where)
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &parts); err != nil {
		return "", fmt.Errorf("%s: invalid parts", where)
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		var typ string
		if err := json.Unmarshal(part["type"], &typ); err != nil || typ == "" {
			return "", fmt.Errorf("%s: part type must be a string", where)
		}
		keys, ok := allowed[typ]
		if !ok {
			return "", fmt.Errorf("%s: unsupported content part type %q", where, typ)
		}
		if err := responsesCheckKeys(part, where, keys...); err != nil {
			return "", err
		}
		// The text of a part lives in "text", for a refusal in "refusal".
		for _, field := range keys[1:] {
			rawText, ok := part[field]
			if !ok || field == "annotations" {
				continue
			}
			var s string
			if err := json.Unmarshal(rawText, &s); err != nil {
				return "", fmt.Errorf("%s: part %s must be a string", where, field)
			}
			if s != "" {
				texts = append(texts, s)
			}
		}
	}
	return strings.Join(texts, ""), nil
}

// responsesSummaryText reads a reasoning item's summary parts: every part must
// be a summary_text; any other kind is refused rather than dropped.
func responsesSummaryText(raw json.RawMessage, where string) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &parts); err != nil {
		return nil, fmt.Errorf("%s: invalid reasoning summary", where)
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if err := responsesCheckKeys(part, where, "type", "text"); err != nil {
			return nil, err
		}
		var typ, text string
		_ = json.Unmarshal(part["type"], &typ)
		if typ != "summary_text" {
			return nil, fmt.Errorf("%s: unsupported summary part type %q", where, typ)
		}
		if err := json.Unmarshal(part["text"], &text); err != nil {
			return nil, fmt.Errorf("%s: summary text must be a string", where)
		}
		if text != "" {
			texts = append(texts, text)
		}
	}
	return texts, nil
}

// responsesArgumentsText reads a function_call's arguments the way the chat
// shape does: a JSON string taken as is, an object kept as its own JSON.
func responsesArgumentsText(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "{}", nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return "", errors.New("arguments must be a JSON string or object")
		}
		if !json.Valid([]byte(strings.TrimSpace(s))) {
			return "", errors.New("arguments is not valid JSON")
		}
		return s, nil
	}
	if trimmed[0] != '{' || !json.Valid(trimmed) {
		return "", errors.New("arguments must be a JSON string or object")
	}
	return string(trimmed), nil
}

// responsesToolsToLLM reads the flat function tools of a Responses request.
// tool_choice "none" withholds them once read; other values but "auto" are
// refused, as is any tool type but "function".
func responsesToolsToLLM(rawTools, rawChoice json.RawMessage) ([]llm.ToolDefinition, error) {
	tools, err := parseResponsesTools(rawTools)
	if err != nil {
		return nil, err
	}
	if choice := bytes.TrimSpace(rawChoice); len(choice) > 0 && !bytes.Equal(choice, []byte("null")) {
		var s string
		if err := json.Unmarshal(choice, &s); err != nil {
			return nil, errors.New("tool_choice must be \"auto\" or \"none\"")
		}
		switch strings.TrimSpace(s) {
		case "none":
			return nil, nil
		case "auto":
		default:
			return nil, fmt.Errorf("tool_choice %q is not supported", s)
		}
	}
	return tools, nil
}

// parseResponsesTools reads the flat tool list: {"type":"function","name":...,
// "description":..., "parameters":{...}}. Any other type or key, and a strict
// flag the server cannot enforce (llm.ToolDefinition carries none), is
// refused.
func parseResponsesTools(rawTools json.RawMessage) ([]llm.ToolDefinition, error) {
	trimmed := bytes.TrimSpace(rawTools)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var rawList []json.RawMessage
	if err := json.Unmarshal(trimmed, &rawList); err != nil {
		return nil, fmt.Errorf("invalid tools: %w", err)
	}
	if len(rawList) > maxClientTools {
		return nil, fmt.Errorf("at most %d tools may be offered", maxClientTools)
	}
	out := make([]llm.ToolDefinition, 0, len(rawList))
	for i, rawTool := range rawList {
		where := fmt.Sprintf("tools[%d]", i)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(rawTool, &fields); err != nil || fields == nil {
			return nil, fmt.Errorf("%s: must be an object", where)
		}
		if err := responsesCheckKeys(fields, where, "type", "name", "description", "parameters", "strict"); err != nil {
			return nil, err
		}
		var t struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
			Strict      *bool           `json:"strict"`
		}
		if err := json.Unmarshal(rawTool, &t); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		if typ := strings.TrimSpace(t.Type); typ != "function" {
			return nil, fmt.Errorf("unsupported tool type %q", t.Type)
		}
		name := strings.TrimSpace(t.Name)
		if name == "" {
			return nil, fmt.Errorf("tool without a name")
		}
		if t.Strict != nil && *t.Strict {
			return nil, fmt.Errorf("tool %q: strict tools are not supported", name)
		}
		if len(t.Parameters) > maxClientToolSchemaBytes {
			return nil, fmt.Errorf("tool %q: parameters exceed %d bytes", name, maxClientToolSchemaBytes)
		}
		var schema map[string]interface{}
		if params := bytes.TrimSpace(t.Parameters); len(params) > 0 && !bytes.Equal(params, []byte("null")) {
			if err := json.Unmarshal(params, &schema); err != nil || schema == nil {
				return nil, fmt.Errorf("tool %q: invalid parameters: %w", name, err)
			}
		}
		if schema == nil {
			schema = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		out = append(out, llm.ToolDefinition{Name: name, Description: t.Description, InputSchema: schema})
	}
	return out, nil
}

// responsesGenerationOptions maps the request's generation fields onto the
// chat shape directRequestOptions reads: max_output_tokens is the newer name
// of the output cap, reasoning.effort the level name.
func responsesGenerationOptions(cfg *config.Config, model string, req *responsesDirectRequest) (llm.RequestOptions, error) {
	var effort *string
	if req.Reasoning != nil && strings.TrimSpace(req.Reasoning.Effort) != "" {
		level := strings.TrimSpace(req.Reasoning.Effort)
		effort = &level
	}
	chat := chatCompletionRequest{
		MaxCompletionTokens: req.MaxOutputTokens,
		Temperature:         req.Temperature,
		ReasoningEffort:     effort,
	}
	return directRequestOptions(cfg, model, chat, req.TopP)
}
