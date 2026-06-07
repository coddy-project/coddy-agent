package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/tidwall/gjson"
)

// openAIProvider implements Provider using the OpenAI API (or compatible).
type openAIProvider struct {
	client    openai.Client
	model     string
	maxTokens int
	temp      float64
}

func newOpenAIProvider(model, apiKey, baseURL string, httpClient *http.Client, maxTokens int, temp float64) *openAIProvider {
	opts := []option.RequestOption{}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	if httpClient != nil {
		opts = append(opts, option.WithHTTPClient(httpClient))
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
	defer func() { _ = stream.Close() }()

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

	finalizeOpenAIToolBuilders := func() []ToolCall {
		var out []ToolCall
		for i := 0; i < len(builders); i++ {
			b, ok := builders[i]
			if !ok {
				continue
			}
			out = append(out, ToolCall{ID: b.id, Name: b.name, InputJSON: b.args})
		}
		return out
	}

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

		raw := delta.RawJSON()
		if raw != "" {
			r := gjson.Get(raw, "reasoning_content").String()
			if r == "" {
				r = gjson.Get(raw, "thinking").String()
			}
			if r != "" {
				onChunk(StreamChunk{ReasoningDelta: r})
			}
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
		if errors.Is(err, context.Canceled) {
			toolCalls = finalizeOpenAIToolBuilders()
			if strings.TrimSpace(fullContent) != "" || len(toolCalls) > 0 {
				sr := stopReason
				if sr == "" {
					if len(toolCalls) > 0 {
						sr = "tool_use"
					} else {
						sr = "end_turn"
					}
				}
				return &Response{
					Content:      fullContent,
					ToolCalls:    toolCalls,
					StopReason:   sr,
					InputTokens:  inputTokens,
					OutputTokens: outputTokens,
				}, fmt.Errorf("openai stream: %w", err)
			}
		}
		return nil, fmt.Errorf("openai stream: %w", err)
	}

	toolCalls = finalizeOpenAIToolBuilders()
	for i := range toolCalls {
		tc := toolCalls[i]
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
			if len(m.ImageParts) > 0 {
				parts := make([]openai.ChatCompletionContentPartUnionParam, 0, 1+len(m.ImageParts))
				if m.Content != "" {
					parts = append(parts, openai.TextContentPart(m.Content))
				}
				for _, ip := range m.ImageParts {
					mime := dataURLMIME(ip.DataURL)
					if strings.HasPrefix(mime, "image/") || (!strings.HasPrefix(ip.DataURL, "data:") && strings.HasPrefix(ip.DataURL, "https://")) {
						parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
							URL:    ip.DataURL,
							Detail: "auto",
						}))
					} else {
						// Non-image data URL: decode and inject as labelled text block.
						decoded := decodeDataURL(ip.DataURL)
						label := ip.Name
						if label == "" {
							label = "file"
						}
						parts = append(parts, openai.TextContentPart(fmt.Sprintf("[File: %s]\n%s", label, decoded)))
					}
				}
				oaiMessages = append(oaiMessages, openai.UserMessage(parts))
			} else {
				oaiMessages = append(oaiMessages, openai.UserMessage(m.Content))
			}
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
				if m.Reasoning != "" {
					asst.SetExtraFields(map[string]any{
						"reasoning_content": m.Reasoning,
					})
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

// dataURLMIME extracts the MIME type from a data URI (e.g. "data:text/plain;base64,...").
// Returns empty string for non-data URIs.
func dataURLMIME(dataURL string) string {
	if !strings.HasPrefix(dataURL, "data:") {
		return ""
	}
	rest := dataURL[5:]
	semi := strings.IndexByte(rest, ';')
	comma := strings.IndexByte(rest, ',')
	if semi > 0 && (comma < 0 || semi < comma) {
		return rest[:semi]
	}
	if comma > 0 {
		return rest[:comma]
	}
	return ""
}

// decodeDataURL extracts and base64-decodes the payload from a data URI.
// Returns the raw string on failure (best-effort).
func decodeDataURL(dataURL string) string {
	comma := strings.IndexByte(dataURL, ',')
	if comma < 0 {
		return dataURL
	}
	payload := dataURL[comma+1:]
	if strings.Contains(dataURL[:comma], ";base64") {
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err == nil {
			return string(decoded)
		}
	}
	return payload
}
