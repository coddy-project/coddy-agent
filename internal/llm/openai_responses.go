package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/responses"
	"net/http"
)

// The API-key Responses adapter shares the configured OpenAI client's proxy
// and headers, but not Codex authentication or generation restrictions.
type openAIResponsesProvider struct {
	*openAIProvider
	identity string
}

func (p *openAIResponsesProvider) Complete(ctx context.Context, m []Message, t []ToolDefinition) (*Response, error) {
	params := p.responsesParams(m, t)
	response, err := p.client.Responses.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai responses: %w", err)
	}
	out := &Response{ActualServiceTier: string(response.ServiceTier), InputTokens: int(response.Usage.InputTokens), OutputTokens: int(response.Usage.OutputTokens), CachedInputTokens: int(response.Usage.InputTokensDetails.CachedTokens), ReasoningTokens: int(response.Usage.OutputTokensDetails.ReasoningTokens), UsageKnown: true}
	var items []json.RawMessage
	for _, item := range response.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" {
					out.Content += part.Text
				}
			}
		case "function_call":
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: item.CallID, Name: item.Name, InputJSON: item.Arguments})
		case "reasoning":
			items = append(items, json.RawMessage(item.RawJSON()))
			for _, part := range item.Summary {
				out.Reasoning += part.Text
			}
		}
	}
	out.ReasoningSignature = p.encodeReasoningItems(items)
	out.StopReason = codexStopReason(out.ToolCalls)
	if response.Status == "incomplete" && response.IncompleteDetails.Reason == "max_output_tokens" {
		out.StopReason = "max_tokens"
	} else if response.Status == "incomplete" && response.IncompleteDetails.Reason == "content_filter" {
		out.StopReason = "content_filter"
	} else if response.Status != "completed" {
		return out, fmt.Errorf("openai responses: response status %s", response.Status)
	}
	return out, nil
}
func (p *openAIResponsesProvider) responsesParams(m []Message, t []ToolDefinition) responses.ResponseNewParams {
	params := buildResponsesParams(p.model, opencodeResponsesEffort(p.reasoningEffort), p.decodeReasoningItems, m, t)
	if p.maxTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(p.maxTokens))
	}
	if p.tempSet || p.temp > 0 && p.reasoningEffort == "" {
		params.Temperature = openai.Float(p.temp)
	}
	if p.topP != nil {
		params.TopP = openai.Float(*p.topP)
	}
	return params
}
func (p *openAIResponsesProvider) Stream(ctx context.Context, m []Message, t []ToolDefinition, emit func(StreamChunk)) (*Response, error) {
	params := p.responsesParams(m, t)
	var raw *http.Response
	err := p.client.Post(ctx, "responses", params, &raw, option.WithJSONSet("stream", true), option.WithHeader("Accept", "text/event-stream"))
	stream := ssestream.NewStream[responses.ResponseStreamEventUnion](newCodexEventDecoder(raw), err)
	defer func() { _ = stream.Close() }()
	return consumeResponsesStream(stream, emit, "openai", p.encodeReasoningItems)
}

type openAIReasoningCarrier struct {
	Provider string            `json:"openai_responses"`
	Model    string            `json:"model"`
	Items    []json.RawMessage `json:"items"`
}

func (p *openAIResponsesProvider) encodeReasoningItems(items []json.RawMessage) string {
	if len(items) == 0 {
		return ""
	}
	raw, err := json.Marshal(openAIReasoningCarrier{p.identity, p.model, items})
	if err != nil {
		return ""
	}
	return string(raw)
}
func (p *openAIResponsesProvider) decodeReasoningItems(signature string) []json.RawMessage {
	var c openAIReasoningCarrier
	if json.Unmarshal([]byte(signature), &c) != nil || c.Provider != p.identity || c.Model != p.model {
		return nil
	}
	return c.Items
}
