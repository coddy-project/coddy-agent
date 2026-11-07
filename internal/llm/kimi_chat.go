package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/openai/openai-go"
	"github.com/tidwall/gjson"
)

// kimiChatProvider implements only Kimi's Chat Completions protocol.
type kimiChatProvider struct {
	transport *KimiInferenceTransport
	input     ProviderInput
}

var _ Provider = (*kimiChatProvider)(nil)

// NewKimiChatProvider creates a Kimi Chat Completions provider bound to a fixed route.
// It does not implement the Anthropic or Responses protocols.
func NewKimiChatProvider(input ProviderInput, source KimiCredentialSource) (Provider, error) {
	client, err := providerHTTPClient(input.ProxyURL, input.Timeout, input.StreamIdleTimeout)
	if err != nil {
		return nil, err
	}
	transport, err := newKimiInferenceTransport(input.Mode, input.Region, "kimi", source, client)
	if err != nil {
		return nil, err
	}
	return newKimiChatProvider(transport, input), nil
}

func newKimiChatProvider(transport *KimiInferenceTransport, input ProviderInput) *kimiChatProvider {
	return &kimiChatProvider{transport: transport, input: input}
}

func (p *kimiChatProvider) request(ctx context.Context, messages []Message, tools []ToolDefinition, stream bool) (*http.Response, error) {
	if p.transport == nil {
		return nil, errors.New("kimi chat: missing transport")
	}
	// Kimi Code takes only temperature 1 and refuses any other value with HTTP
	// 400, whatever the model, so a temperature a models[] row merely
	// configures stays out of a subscription request, as it does on this
	// provider's Anthropic and Responses protocols. One the caller asked for
	// on the request still travels: the endpoint answers for itself. Platform
	// is the Moonshot API, which takes the parameter, and keeps it either way.
	temperature := p.input.Temperature
	if p.input.Mode != "platform" && !p.input.TemperatureSet {
		temperature = 0
	}
	op := newOpenAIProvider(p.input.Model, "", "", nil, 0, temperature, "", "")
	op.tempSet = p.input.TemperatureSet
	op.topP = p.input.TopP
	normalized := make([]ToolDefinition, len(tools))
	for i, tool := range tools {
		schema, err := kimiChatNormalizeSchema(tool.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("kimi chat tool %q: %w", tool.Name, err)
		}
		normalized[i] = tool
		normalized[i].InputSchema = schema
	}
	params := op.buildParams(messages, normalized, stream)
	data, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("kimi chat encode: %w", err)
	}
	var body map[string]any
	if err = json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("kimi chat encode: %w", err)
	}
	delete(body, "max_tokens")
	if p.input.MaxTokens > 0 {
		body["max_completion_tokens"] = p.input.MaxTokens
	}
	if effort := p.input.ReasoningEffort; effort != "" {
		thinking := map[string]any{"type": "enabled"}
		if effort == "off" {
			thinking["type"] = "disabled"
		} else if effort != "on" {
			thinking["effort"] = effort
		}
		body["thinking"] = thinking
	}
	if stream {
		body["stream"] = true
	}
	data, err = json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("kimi chat encode: %w", err)
	}
	resp, err := p.transport.Do(ctx, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, withToolSchemaRequest(kimiStatusError("kimi chat", resp), body, false)
	}
	return resp, nil
}

func kimiChatUsage(raw []byte, r *Response) {
	u := gjson.GetBytes(raw, "usage")
	if !u.IsObject() {
		u = gjson.GetBytes(raw, "choices.0.usage")
	}
	if u.Get("prompt_tokens").Type != gjson.Number || u.Get("completion_tokens").Type != gjson.Number {
		return
	}
	r.UsageKnown = true
	r.UsageIncomplete = false
	r.InputTokens = int(u.Get("prompt_tokens").Int())
	r.OutputTokens = int(u.Get("completion_tokens").Int())
	r.CachedInputTokens = int(u.Get("prompt_tokens_details.cached_tokens").Int())
	r.ReasoningTokens = int(u.Get("completion_tokens_details.reasoning_tokens").Int())
}

func (p *kimiChatProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	resp, err := p.request(ctx, messages, tools, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("kimi chat read: %w", err)
	}
	var completion openai.ChatCompletion
	if err = json.Unmarshal(data, &completion); err != nil {
		return nil, fmt.Errorf("kimi chat decode: %w", err)
	}
	if len(completion.Choices) == 0 {
		return nil, errors.New("kimi chat: empty response")
	}
	choice := completion.Choices[0]
	result := &Response{Content: choice.Message.Content, Reasoning: openAIReasoningText(choice.Message.RawJSON()), StopReason: mapOpenAIStopReason(string(choice.FinishReason)), UsageIncomplete: true}
	for _, tc := range choice.Message.ToolCalls {
		call := ToolCall{ID: tc.ID, Name: tc.Function.Name, InputJSON: tc.Function.Arguments}
		if !kimiChatValidToolCall(call) {
			return nil, errors.New("kimi chat: invalid tool call")
		}
		result.ToolCalls = append(result.ToolCalls, call)
	}
	kimiChatUsage(data, result)
	return result, nil
}

func (p *kimiChatProvider) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	resp, err := p.request(ctx, messages, tools, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	result := &Response{UsageIncomplete: true}
	builders := map[int]*ToolCall{}
	named := map[int]bool{}
	progress := map[int]int{}
	emitted := false
	done := false
	emit := func(c StreamChunk) {
		if onChunk != nil {
			emitted = true
			onChunk(c)
		}
	}
	scanner := newSSEScanner(resp.Body)
	for scanner.Next() {
		frame := scanner.Frame()
		payload := bytes.TrimSpace(frame.data)
		if len(frame.errData) > 0 {
			err = newStreamServerError(bytes.TrimSpace(frame.errData), emitted)
			break
		}
		if bytes.Equal(payload, []byte("[DONE]")) {
			done = true
			break
		}
		if len(payload) == 0 {
			continue
		}
		var parsed any
		if decodeErr := json.Unmarshal(payload, &parsed); decodeErr != nil {
			und := &streamUndecodableError{snippet: streamErrorSnippet(payload), cause: decodeErr}
			if truncated := streamDecodeTruncation(und, emitted, len(payload)); truncated != nil {
				err = truncated
			} else {
				err = und
			}
			break
		}
		if e := gjson.GetBytes(payload, "error"); e.Exists() && e.Type != gjson.Null {
			err = newStreamServerError([]byte(e.Raw), emitted)
			break
		}
		kimiChatUsage(payload, result)
		var chunk openai.ChatCompletionChunk
		if e := json.Unmarshal(payload, &chunk); e != nil {
			err = &streamUndecodableError{snippet: streamErrorSnippet(payload), cause: e}
			break
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		c := chunk.Choices[0]
		if c.FinishReason != "" {
			result.StopReason = mapOpenAIStopReason(string(c.FinishReason))
			done = true
		}
		if c.Delta.Content != "" {
			result.Content += c.Delta.Content
			emit(StreamChunk{TextDelta: c.Delta.Content})
		}
		if reason := openAIReasoningText(c.Delta.RawJSON()); reason != "" {
			result.Reasoning += reason
			emit(StreamChunk{ReasoningDelta: reason})
		}
		for _, tc := range c.Delta.ToolCalls {
			i := int(tc.Index)
			b := builders[i]
			if b == nil {
				b = &ToolCall{}
				builders[i] = b
			}
			if tc.ID != "" {
				b.ID = tc.ID
			}
			if tc.Function.Name != "" {
				b.Name = tc.Function.Name
			}
			b.InputJSON += tc.Function.Arguments
			if !named[i] && b.ID != "" && b.Name != "" {
				named[i] = true
				emit(StreamChunk{ToolCallNamed: &ToolCall{ID: b.ID, Name: b.Name}})
			}
			if named[i] && len(b.InputJSON) > progress[i] {
				emit(StreamChunk{ToolCallDelta: &ToolCall{ID: b.ID, Name: b.Name, InputJSON: b.InputJSON[progress[i]:]}})
				progress[i] = len(b.InputJSON)
			}
		}
	}
	if err == nil && scanner.Err() != nil {
		cause := scanner.Err()
		if ctx.Err() != nil {
			cause = ctx.Err()
		}
		err = &streamTransportError{cause: cause, emitted: emitted}
	}
	if err == nil && !done {
		if ctx.Err() != nil {
			err = &streamTransportError{cause: ctx.Err(), emitted: emitted}
		} else {
			err = &streamTruncatedError{emitted: emitted}
		}
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			for _, i := range kimiChatToolIndices(builders) {
				result.ToolCalls = append(result.ToolCalls, *builders[i])
			}
		}
		if strings.TrimSpace(result.Content) != "" || strings.TrimSpace(result.Reasoning) != "" || len(result.ToolCalls) > 0 {
			return result, fmt.Errorf("kimi chat stream: %w", err)
		}
		return nil, fmt.Errorf("kimi chat stream: %w", err)
	}
	for _, i := range kimiChatToolIndices(builders) {
		b := builders[i]
		if !kimiChatValidToolCall(*b) {
			err := &streamUndecodableError{snippet: "invalid tool call", cause: errors.New("invalid tool call")}
			if result.Content != "" || result.Reasoning != "" {
				return result, fmt.Errorf("kimi chat stream: %w", err)
			}
			return nil, fmt.Errorf("kimi chat stream: %w", err)
		}
	}
	for _, i := range kimiChatToolIndices(builders) {
		b := builders[i]
		result.ToolCalls = append(result.ToolCalls, *b)
		tc := *b
		emit(StreamChunk{ToolCall: &tc})
	}
	if result.StopReason == "" {
		if len(result.ToolCalls) > 0 {
			result.StopReason = "tool_use"
		} else {
			result.StopReason = "end_turn"
		}
	}
	return result, nil
}

func kimiChatToolIndices(builders map[int]*ToolCall) []int {
	indices := make([]int, 0, len(builders))
	for i := range builders {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	return indices
}

func kimiChatValidToolCall(call ToolCall) bool {
	var args map[string]json.RawMessage
	return strings.TrimSpace(call.ID) != "" && strings.TrimSpace(call.Name) != "" && json.Unmarshal([]byte(call.InputJSON), &args) == nil && args != nil
}
