// Package llm provides an abstraction over LLM providers.
package llm

import "context"

// Role is the role of a conversation message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is a single turn in a conversation.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // for RoleTool messages
}

// ToolCall represents a tool invocation requested by the LLM.
type ToolCall struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	InputJSON string `json:"input"` // raw JSON arguments
}

// ToolDefinition describes a tool available to the LLM.
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"` // JSON Schema object
}

// Response is the LLM's reply to a completion request.
type Response struct {
	Content   string
	ToolCalls []ToolCall
	// StopReason explains why generation stopped.
	// "end_turn" | "tool_use" | "max_tokens"
	StopReason string
	// InputTokens and OutputTokens are for usage tracking.
	InputTokens  int
	OutputTokens int
}

// StreamChunk is a single chunk streamed from the LLM.
type StreamChunk struct {
	TextDelta      string
	ReasoningDelta string
	ToolCall       *ToolCall
	StopReason     string
	InputTokens    int
	OutputTokens   int
}

// Provider is the interface all LLM backends must implement.
type Provider interface {
	// Complete sends a non-streaming completion request.
	Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error)

	// Stream sends a streaming completion request, calling onChunk for each chunk.
	Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error)
}

// NewProvider creates the appropriate Provider from a model definition.
func NewProvider(providerType, model, apiKey, baseURL string, maxTokens int, temp float64) (Provider, error) {
	switch providerType {
	case "openai", "openai_compatible":
		return newOpenAIProvider(model, apiKey, baseURL, maxTokens, temp), nil
	case "anthropic":
		return newAnthropicProvider(model, apiKey, maxTokens, temp), nil
	case "ollama":
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		return newOpenAIProvider(model, "", baseURL+"/v1", maxTokens, temp), nil
	default:
		return nil, &UnsupportedProviderError{Provider: providerType}
	}
}

// UnsupportedProviderError is returned when the provider type is unknown.
type UnsupportedProviderError struct {
	Provider string
}

func (e *UnsupportedProviderError) Error() string {
	return "unsupported LLM provider: " + e.Provider
}
