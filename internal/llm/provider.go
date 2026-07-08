// Package llm provides an abstraction over LLM providers.
package llm

import (
	"context"
	"strings"
	"time"
)

// Role is the role of a conversation message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ImagePart is an image or file included in a multimodal user message.
type ImagePart struct {
	// DataURL is a data URI ("data:<mime>;base64,<bytes>") or an HTTPS image URL.
	DataURL string `json:"data_url"`
	// Name is the original file name (informational; not sent to the provider).
	Name string `json:"name,omitempty"`
	// FilePath is the absolute path where this file was saved on disk (session assets/).
	// When set the agent informs the model of this location so it can reference the file
	// directly without re-reading the base64 payload.
	FilePath string `json:"file_path,omitempty"`
}

// Message is a single turn in a conversation.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
	// ImageParts, when non-empty, make this a multimodal user message. Only
	// supported for RoleUser; ignored on other roles and by providers that do
	// not support vision.
	ImageParts []ImagePart `json:"image_parts,omitempty"`
	Reasoning  string      `json:"reasoning,omitempty"`
	// ReasoningSignature is the provider signature for the reasoning block (Anthropic extended
	// thinking). It is replayed unmodified with the exact Reasoning text on later requests when
	// thinking is enabled and the turn has tool calls; otherwise the Anthropic API rejects it.
	ReasoningSignature string `json:"reasoning_signature,omitempty"`
	// ReasoningDurationMs wall clock between first streamed reasoning delta and UI-equivalent finish
	// (first non-whitespace text delta or first tool-call chunk); omitted when unset or zero.
	ReasoningDurationMs int64      `json:"reasoning_duration_ms,omitempty"`
	ToolCalls           []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID          string     `json:"tool_call_id,omitempty"` // for RoleTool messages
	// Model is the YAML models[].model selector used to generate this assistant message (HTTP/Coddy), if set.
	Model string `json:"model,omitempty"`
	// CreatedAt is RFC3339 timestamp in UTC when the message was appended to history (UI and Coddy REST).
	CreatedAt string `json:"created_at,omitempty"`
	// PlanDocument holds a persisted design plan snapshot for the bundled UI (excluded from LLM prompts).
	PlanDocument *PlanDocumentSnapshot `json:"plan_document,omitempty"`
}

// PlanDocumentSnapshot is a persisted design plan row in the session transcript.
type PlanDocumentSnapshot struct {
	Slug      string `json:"slug"`
	Name      string `json:"name,omitempty"`
	Overview  string `json:"overview,omitempty"`
	Content   string `json:"content"`
	Body      string `json:"body,omitempty"`
	Path      string `json:"path,omitempty"`
	Discarded bool   `json:"discarded,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// ToolCall represents a tool invocation requested by the LLM.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
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
	// Reasoning is the exact (unmodified) reasoning text, when the provider returns one.
	Reasoning string
	// ReasoningSignature pairs with Reasoning for providers that sign reasoning blocks (Anthropic).
	ReasoningSignature string
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

// ProviderInput selects an LLM backend and connection parameters.
type ProviderInput struct {
	Type        string
	Model       string
	APIKey      string
	BaseURL     string
	ProxyURL    string
	MaxTokens   int
	Temperature float64
	// ReasoningEffort is the reasoning level name ("minimal"|"low"|"medium"|"high"), or empty.
	// OpenAI maps it to reasoning_effort; Anthropic maps it to an extended-thinking token budget.
	ReasoningEffort string
	// RetryMax is the number of retries after the first failed attempt (default 3).
	RetryMax int
	// RetryBase is the initial backoff between retries (default 1s).
	RetryBase time.Duration
	// RetryMaxDelay caps retry backoff (default 60s).
	RetryMaxDelay time.Duration
	// MinInterval enforces a minimum gap between consecutive LLM calls (default 0).
	MinInterval time.Duration
}

const neuralDeepBaseURL = "https://api.neuraldeep.ru/v1"

func providerBaseURL(providerType, configured string) string {
	if providerType == "neuraldeep" {
		return neuralDeepBaseURL
	}
	return strings.TrimSpace(configured)
}

// NewProvider creates the appropriate Provider from a model definition.
func NewProvider(p ProviderInput) (Provider, error) {
	hc, err := HTTPClientForOptionalProxy(p.ProxyURL)
	if err != nil {
		return nil, err
	}
	var inner Provider
	switch p.Type {
	case "openai":
		inner = newOpenAIProvider(p.Model, p.APIKey, providerBaseURL(p.Type, p.BaseURL), hc, p.MaxTokens, p.Temperature, p.ReasoningEffort)
	case "anthropic":
		inner = newAnthropicProvider(p.Model, p.APIKey, providerBaseURL(p.Type, p.BaseURL), hc, p.MaxTokens, p.Temperature, p.ReasoningEffort)
	case "neuraldeep":
		inner = newOpenAIProvider(p.Model, p.APIKey, providerBaseURL(p.Type, p.BaseURL), hc, p.MaxTokens, p.Temperature, p.ReasoningEffort)
	default:
		return nil, &UnsupportedProviderError{Provider: p.Type}
	}
	return applyResilientWrap(inner, p), nil
}

// UnsupportedProviderError is returned when the provider type is unknown.
type UnsupportedProviderError struct {
	Provider string
}

func (e *UnsupportedProviderError) Error() string {
	return "unsupported LLM provider: " + e.Provider
}
