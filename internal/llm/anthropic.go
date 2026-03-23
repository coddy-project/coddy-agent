package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// anthropicProvider implements Provider using the Anthropic API.
type anthropicProvider struct {
	client    anthropic.Client
	model     string
	maxTokens int
	temp      float64
}

func newAnthropicProvider(model, apiKey string, maxTokens int, temp float64) *anthropicProvider {
	opts := []option.RequestOption{}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	if maxTokens == 0 {
		maxTokens = 8192
	}
	return &anthropicProvider{
		client:    anthropic.NewClient(opts...),
		model:     model,
		maxTokens: maxTokens,
		temp:      temp,
	}
}

func (p *anthropicProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	system, msgs := p.splitMessages(messages)
	params := p.buildParams(system, msgs, tools)
	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anthropic complete: %w", err)
	}
	return p.parseResponse(*resp)
}

func (p *anthropicProvider) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	system, msgs := p.splitMessages(messages)
	params := p.buildParams(system, msgs, tools)

	stream := p.client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	var fullContent string
	var toolCalls []ToolCall
	var stopReason string
	var inputTokens, outputTokens int

	// Accumulate tool use blocks by index.
	type toolUseAccum struct {
		id    string
		name  string
		input string
	}
	toolUseMap := make(map[int64]*toolUseAccum)

	for stream.Next() {
		event := stream.Current()
		switch e := event.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			switch d := e.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				fullContent += d.Text
				onChunk(StreamChunk{TextDelta: d.Text})
			case anthropic.InputJSONDelta:
				if acc, ok := toolUseMap[e.Index]; ok {
					acc.input += d.PartialJSON
				}
			}

		case anthropic.ContentBlockStartEvent:
			cb := e.ContentBlock
			if cb.Type == "tool_use" {
				toolUseMap[e.Index] = &toolUseAccum{
					id:   cb.ID,
					name: cb.Name,
				}
			}

		case anthropic.MessageDeltaEvent:
			stopReason = mapAnthropicStopReason(string(e.Delta.StopReason))
			outputTokens = int(e.Usage.OutputTokens)

		case anthropic.MessageStartEvent:
			inputTokens = int(e.Message.Usage.InputTokens)
		}
	}

	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("anthropic stream: %w", err)
	}

	for i := int64(0); i < int64(len(toolUseMap)); i++ {
		acc, ok := toolUseMap[i]
		if !ok {
			continue
		}
		tc := ToolCall{ID: acc.id, Name: acc.name, InputJSON: acc.input}
		toolCalls = append(toolCalls, tc)
		onChunk(StreamChunk{ToolCall: &tc})
	}

	return &Response{
		Content:      fullContent,
		ToolCalls:    toolCalls,
		StopReason:   stopReason,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}, nil
}

// splitMessages extracts the system message and converts messages to Anthropic format.
func (p *anthropicProvider) splitMessages(messages []Message) (string, []anthropic.MessageParam) {
	var system string
	var result []anthropic.MessageParam

	for _, m := range messages {
		if m.Role == RoleSystem {
			system = m.Content
			continue
		}

		switch m.Role {
		case RoleUser:
			result = append(result, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))

		case RoleAssistant:
			if len(m.ToolCalls) > 0 {
				var blocks []anthropic.ContentBlockParamUnion
				if m.Content != "" {
					blocks = append(blocks, anthropic.NewTextBlock(m.Content))
				}
				for _, tc := range m.ToolCalls {
					var inputMap map[string]interface{}
					_ = json.Unmarshal([]byte(tc.InputJSON), &inputMap)
					blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, inputMap, tc.Name))
				}
				result = append(result, anthropic.NewAssistantMessage(blocks...))
			} else {
				result = append(result, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
			}

		case RoleTool:
			result = append(result, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(m.ToolCallID, m.Content, false),
			))
		}
	}

	return system, result
}

func (p *anthropicProvider) buildParams(system string, messages []anthropic.MessageParam, tools []ToolDefinition) anthropic.MessageNewParams {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: int64(p.maxTokens),
		Messages:  messages,
	}

	if system != "" {
		params.System = []anthropic.TextBlockParam{{Type: "text", Text: system}}
	}
	if p.temp > 0 {
		params.Temperature = anthropic.Float(p.temp)
	}

	if len(tools) > 0 {
		anthTools := make([]anthropic.ToolUnionParam, len(tools))
		for i, t := range tools {
			schemaBytes, _ := json.Marshal(t.InputSchema)
			var schemaMap map[string]interface{}
			_ = json.Unmarshal(schemaBytes, &schemaMap)

			var propsRaw interface{}
			if schemaMap != nil {
				propsRaw = schemaMap["properties"]
			}

			tp := anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Description),
				InputSchema: anthropic.ToolInputSchemaParam{
					Type:       "object",
					Properties: propsRaw,
				},
			}
			anthTools[i] = anthropic.ToolUnionParam{OfTool: &tp}
		}
		params.Tools = anthTools
	}

	return params
}

func (p *anthropicProvider) parseResponse(resp anthropic.Message) (*Response, error) {
	r := &Response{
		StopReason:   mapAnthropicStopReason(string(resp.StopReason)),
		InputTokens:  int(resp.Usage.InputTokens),
		OutputTokens: int(resp.Usage.OutputTokens),
	}

	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			r.Content += b.Text
		case anthropic.ToolUseBlock:
			r.ToolCalls = append(r.ToolCalls, ToolCall{
				ID:        b.ID,
				Name:      b.Name,
				InputJSON: string(b.Input),
			})
		}
	}

	return r, nil
}

func mapAnthropicStopReason(reason string) string {
	switch reason {
	case "tool_use":
		return "tool_use"
	case "end_turn":
		return "end_turn"
	case "max_tokens":
		return "max_tokens"
	default:
		return reason
	}
}
