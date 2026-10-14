package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
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
	// tempSet sends temp even at zero: the caller asked for that value.
	tempSet         bool
	reasoningEffort string
	// modelParams is what the provider's model listing says the model takes
	// (ProviderInput.SupportedParameters); nil when nothing is known.
	modelParams []string
}

func newOpenAIProvider(model, apiKey, baseURL string, httpClient *http.Client, maxTokens int, temp float64, reasoningEffort string) *openAIProvider {
	opts := []option.RequestOption{option.WithMaxRetries(0)}
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
		client:          openai.NewClient(opts...),
		model:           model,
		maxTokens:       maxTokens,
		temp:            temp,
		reasoningEffort: reasoningEffort,
	}
}

func (p *openAIProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	params := p.buildParams(messages, tools, false)
	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai complete: %w", err)
	}
	return p.parseCompletion(resp)
}

// buildParams assembles the chat completion request. streaming selects the
// stream-only fields: stream_options is rejected outright by OpenAI on a
// blocking request, so it is set for the streaming path only.
func (p *openAIProvider) buildParams(messages []Message, tools []ToolDefinition, streaming bool) openai.ChatCompletionNewParams {
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
					// Every picture type goes as one: a local server behind this
					// API may take more types than OpenAI does. An SVG is text.
					if IsPicture(ip) {
						parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
							URL:    ip.DataURL,
							Detail: "auto",
						}))
					} else {
						// Any other file: its text as a labelled block, or a
						// line naming a file that is not text.
						mime, _, _ := parseDataURL(ip.DataURL)
						parts = append(parts, openai.TextContentPart(fileText(ip, mime)))
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

	// An OpenRouter-style aggregator lists the unified reasoning object for a
	// model that does not take OpenAI's reasoning_effort: the level goes there,
	// and the request is otherwise an ordinary chat request (issue #258).
	if p.reasoningEffort != "" && p.takesReasoningObjectOnly() {
		reasoning := map[string]any{"effort": p.reasoningEffort}
		if p.reasoningEffort == reasoningOff {
			reasoning = map[string]any{"enabled": false}
		}
		extra := map[string]any{"reasoning": reasoning}
		// Here the listing is known, so the Qwen3 switch goes only where it
		// names it, and follows the level.
		if p.sendsQwenTemplateSwitch() {
			extra["chat_template_kwargs"] = map[string]any{"enable_thinking": p.reasoningEffort != reasoningOff}
		}
		params.SetExtraFields(extra)
		if p.maxTokens > 0 {
			params.MaxTokens = openai.Int(int64(p.maxTokens))
		}
		if p.temp > 0 || p.tempSet {
			params.Temperature = openai.Float(p.temp)
		}
		return p.finishParams(params, tools, streaming)
	}

	// "off" is the pseudo level that turns thinking off. Qwen3 has a real
	// switch in its chat template, and the request is then an ordinary chat
	// request; any other model it is offered for takes the "none" tier.
	qwenTemplate := p.sendsQwenTemplateSwitch()
	effort := p.reasoningEffort
	qwenOff := false
	if effort == reasoningOff {
		if qwenTemplate {
			effort, qwenOff = "", true
		} else {
			effort = "none"
		}
	}

	// reasoning_effort is only valid for reasoning models; callers pass an empty string for
	// non-reasoning models. Reasoning models also reject max_tokens (require
	// max_completion_tokens) and a custom temperature, so the reasoning path differs.
	if effort != "" {
		params.ReasoningEffort = openai.ReasoningEffort(effort)
		if p.maxTokens > 0 {
			params.MaxCompletionTokens = openai.Int(int64(p.maxTokens))
		}
		// Qwen3-family thinking is a chat-template switch (vLLM/SGLang convention for
		// open-weight Qwen), not an effort tier: pin it on so the selected effort is
		// honored even when the serving template defaults thinking off.
		if qwenTemplate {
			params.SetExtraFields(map[string]any{
				"chat_template_kwargs": map[string]any{"enable_thinking": true},
			})
		}
		// A configured temperature stays off, but one the caller asked for is
		// sent: an OpenAI-compatible server may well take it with reasoning
		// (Qwen3 thinking on vLLM does), and one that does not says so itself.
		if p.tempSet {
			params.Temperature = openai.Float(p.temp)
		}
	} else {
		if p.maxTokens > 0 {
			params.MaxTokens = openai.Int(int64(p.maxTokens))
		}
		if p.temp > 0 || p.tempSet {
			params.Temperature = openai.Float(p.temp)
		}
		if qwenOff {
			params.SetExtraFields(map[string]any{
				"chat_template_kwargs": map[string]any{"enable_thinking": false},
			})
		}
	}

	return p.finishParams(params, tools, streaming)
}

// finishParams adds what every request carries whatever its reasoning: the
// tools and, on a stream, the usage request.
func (p *openAIProvider) finishParams(params openai.ChatCompletionNewParams, tools []ToolDefinition, streaming bool) openai.ChatCompletionNewParams {
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

	if streaming {
		// Request usage statistics in the streaming response.
		// Without this the usage chunk is omitted and token counts stay at zero.
		params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		}
	}

	return params
}

// reasoningOff is config.ReasoningOff, the level that turns thinking off
// (the llm package does not import config).
const reasoningOff = "off"

// isQwenChatTemplateModel matches Qwen3-family models whose thinking mode is
// controlled by the chat template (chat_template_kwargs.enable_thinking) rather
// than reasoning_effort alone. Qwen2.5 has no thinking mode. The vendor an
// aggregator or a Hugging Face id puts in front (qwen/qwen3.8-flash,
// Qwen/Qwen3-32B) is not part of the model's name.
func isQwenChatTemplateModel(model string) bool {
	return strings.HasPrefix(modelIDTail(model), "qwen3")
}

// modelIDTail is a model id without the vendor path in front of it, lower case.
func modelIDTail(model string) string {
	id := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	return id
}

// listsParam reports whether the model listing names param. Only meaningful
// when the listing was read (modelParams != nil).
func (p *openAIProvider) listsParam(param string) bool {
	return slices.Contains(p.modelParams, param)
}

// takesReasoningObjectOnly reports whether the listing names the unified
// reasoning object and not reasoning_effort, as OpenRouter and RouterAI do for
// many models.
func (p *openAIProvider) takesReasoningObjectOnly() bool {
	return p.modelParams != nil && p.listsParam("reasoning") && !p.listsParam("reasoning_effort")
}

// sendsQwenTemplateSwitch reports whether a Qwen3 request carries the chat
// template switch: always when nothing is known about the server (vLLM and
// SGLang list nothing), and otherwise only where the listing names it.
func (p *openAIProvider) sendsQwenTemplateSwitch() bool {
	if !isQwenChatTemplateModel(p.model) {
		return false
	}
	return p.modelParams == nil || p.listsParam("chat_template_kwargs")
}

func (p *openAIProvider) parseCompletion(resp *openai.ChatCompletion) (*Response, error) {
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai: empty response")
	}
	choice := resp.Choices[0]
	msg := choice.Message

	r := &Response{
		Content:           msg.Content,
		Reasoning:         openAIReasoningText(msg.RawJSON()),
		StopReason:        mapOpenAIStopReason(string(choice.FinishReason)),
		InputTokens:       int(resp.Usage.PromptTokens),
		OutputTokens:      int(resp.Usage.CompletionTokens),
		CachedInputTokens: int(resp.Usage.PromptTokensDetails.CachedTokens),
		ReasoningTokens:   int(resp.Usage.CompletionTokensDetails.ReasoningTokens),
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

// openAIReasoningText pulls the thinking text out of a non-streamed assistant
// message or a streamed delta. None of the fields is part of the OpenAI
// schema, so they are read off the raw JSON: reasoning_content is what vLLM,
// SGLang and llama.cpp emit, reasoning is OpenRouter's and RouterAI's name (and
// vLLM's newer one, sent next to reasoning_content while it moved, so the first
// name that carries text is the only one read), thinking is the older spelling.
func openAIReasoningText(raw string) string {
	if raw == "" {
		return ""
	}
	for _, field := range []string{"reasoning_content", "reasoning", "thinking"} {
		// Text only: an aggregator may echo the request's reasoning object,
		// and its JSON is not reasoning.
		if r := gjson.Get(raw, field); r.Type == gjson.String && r.Str != "" {
			return r.Str
		}
	}
	return ""
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

// decodeDataURL extracts and base64-decodes the payload from a data URI.
// Returns the raw string on failure (best-effort).
func decodeDataURL(dataURL string) string {
	_, isBase64, payload := parseDataURL(dataURL)
	if isBase64 {
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err == nil {
			return string(decoded)
		}
	}
	return payload
}
