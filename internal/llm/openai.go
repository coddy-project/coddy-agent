package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// openAIProvider implements Provider using the OpenAI API (or compatible).
type openAIProvider struct {
	client    openai.Client
	model     string
	maxTokens int
	temp      float64
}

func newOpenAIProvider(model, apiKey, baseURL string, maxTokens int, temp float64) *openAIProvider {
	opts := []option.RequestOption{}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &openAIProvider{
		client:    openai.NewClient(opts...),
		model:     model,
		maxTokens: maxTokens,
		temp:      temp,
	}
}

func (p *openAIProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	params := p.buildParams(messages, tools)
	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai complete: %w", err)
	}
	return p.parseCompletion(resp)
}

func (p *openAIProvider) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	params := p.buildParams(messages, tools)
	stream := p.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()

	var fullContent string
	var toolCalls []ToolCall
	var stopReason string
	var inputTokens, outputTokens int

	// Accumulate tool call deltas by index.
	type tcBuilder struct {
		id   string
		name string
		args string
	}
	builders := make(map[int]*tcBuilder)

	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		if choice.FinishReason != "" {
			stopReason = mapOpenAIStopReason(string(choice.FinishReason))
		}

		delta := choice.Delta

		if delta.Content != "" {
			fullContent += delta.Content
			onChunk(StreamChunk{TextDelta: delta.Content})
		}

		for _, tc := range delta.ToolCalls {
			idx := int(tc.Index)
			if _, ok := builders[idx]; !ok {
				builders[idx] = &tcBuilder{}
			}
			b := builders[idx]
			if tc.ID != "" {
				b.id = tc.ID
			}
			if tc.Function.Name != "" {
				b.name = tc.Function.Name
			}
			b.args += tc.Function.Arguments
		}

		if chunk.Usage.TotalTokens > 0 {
			inputTokens = int(chunk.Usage.PromptTokens)
			outputTokens = int(chunk.Usage.CompletionTokens)
		}
	}

	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("openai stream: %w", err)
	}

	for i := 0; i < len(builders); i++ {
		b, ok := builders[i]
		if !ok {
			continue
		}
		tc := ToolCall{ID: b.id, Name: b.name, InputJSON: b.args}
		toolCalls = append(toolCalls, tc)
		onChunk(StreamChunk{ToolCall: &tc})
	}

	if stopReason == "" {
		if len(toolCalls) > 0 {
			stopReason = "tool_use"
		} else {
			stopReason = "end_turn"
		}
	}

	return &Response{
		Content:      fullContent,
		ToolCalls:    toolCalls,
		StopReason:   stopReason,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}, nil
}

func (p *openAIProvider) buildParams(messages []Message, tools []ToolDefinition) openai.ChatCompletionNewParams {
	oaiMessages := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			oaiMessages = append(oaiMessages, openai.SystemMessage(m.Content))
		case RoleUser:
			oaiMessages = append(oaiMessages, openai.UserMessage(m.Content))
		case RoleAssistant:
			if len(m.ToolCalls) > 0 {
				calls := make([]openai.ChatCompletionMessageToolCallParam, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					calls[i] = openai.ChatCompletionMessageToolCallParam{
						ID:   tc.ID,
						Type: "function",
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: tc.InputJSON,
						},
					}
				}
				asst := openai.ChatCompletionAssistantMessageParam{
					ToolCalls: calls,
				}
				if m.Content != "" {
					asst.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
						OfString: openai.String(m.Content),
					}
				}
				oaiMessages = append(oaiMessages, openai.ChatCompletionMessageParamUnion{OfAssistant: &asst})
			} else {
				oaiMessages = append(oaiMessages, openai.AssistantMessage(m.Content))
			}
		case RoleTool:
			oaiMessages = append(oaiMessages, openai.ToolMessage(m.Content, m.ToolCallID))
		}
	}

	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(p.model),
		Messages: oaiMessages,
	}

	if p.maxTokens > 0 {
		params.MaxTokens = openai.Int(int64(p.maxTokens))
	}
	if p.temp > 0 {
		params.Temperature = openai.Float(p.temp)
	}

	if len(tools) > 0 {
		oaiTools := make([]openai.ChatCompletionToolParam, len(tools))
		for i, t := range tools {
			schemaBytes, _ := json.Marshal(t.InputSchema)
			var schemaMap map[string]interface{}
			_ = json.Unmarshal(schemaBytes, &schemaMap)

			oaiTools[i] = openai.ChatCompletionToolParam{
				Type: "function",
				Function: openai.FunctionDefinitionParam{
					Name:        t.Name,
					Description: openai.String(t.Description),
					Parameters:  openai.FunctionParameters(schemaMap),
				},
			}
		}
		params.Tools = oaiTools
	}

	// Request usage statistics in the streaming response.
	// Without this the usage chunk is omitted and token counts stay at zero.
	params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
		IncludeUsage: openai.Bool(true),
	}

	return params
}

func (p *openAIProvider) parseCompletion(resp *openai.ChatCompletion) (*Response, error) {
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai: empty response")
	}
	choice := resp.Choices[0]
	msg := choice.Message

	r := &Response{
		Content:      msg.Content,
		StopReason:   mapOpenAIStopReason(string(choice.FinishReason)),
		InputTokens:  int(resp.Usage.PromptTokens),
		OutputTokens: int(resp.Usage.CompletionTokens),
	}

	for _, tc := range msg.ToolCalls {
		r.ToolCalls = append(r.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			InputJSON: tc.Function.Arguments,
		})
	}

	return r, nil
}

func mapOpenAIStopReason(reason string) string {
	switch reason {
	case "tool_calls":
		return "tool_use"
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	default:
		return reason
	}
}
