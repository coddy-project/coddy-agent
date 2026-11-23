package llm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

func (p *kimiChatProvider) request(ctx context.Context, messages []Message, tools []ToolDefinition, stream bool, wire *[]byte) (*http.Response, error) {
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
	if wire != nil {
		*wire = data
	}
	resp, err := p.transport.do(ctx, bytes.NewReader(data), false, "kimi chat")
	if err != nil {
		return nil, withToolSchemaRequest(err, body, false)
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
	resp, err := p.request(ctx, messages, tools, false, nil)
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
	var wire []byte
	resp, err := p.request(ctx, messages, tools, true, &wire)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	result := &Response{UsageIncomplete: true}
	builders := map[int]*ToolCall{}
	named := map[int]bool{}
	idPresent := map[int]bool{}
	progress := map[int]int{}
	lastAnnounced := 0
	finishReason := ""
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
			if finishReason == "" {
				finishReason = "[DONE]"
			}
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
			finishReason = string(c.FinishReason)
			result.StopReason = mapOpenAIStopReason(finishReason)
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
			if strings.TrimSpace(tc.ID) != "" {
				idPresent[i] = true
			}
			if strings.TrimSpace(tc.ID) != "" && !named[i] {
				b.ID = tc.ID
			}
			if tc.Function.Name != "" {
				b.Name = tc.Function.Name
			}
			b.InputJSON += tc.Function.Arguments
			if !named[i] && strings.TrimSpace(b.Name) != "" && strings.TrimSpace(b.ID) == "" {
				var id [16]byte
				if _, e := rand.Read(id[:]); e != nil {
					return nil, fmt.Errorf("kimi chat stream: generate tool call id: %w", e)
				}
				b.ID = "call_" + hex.EncodeToString(id[:])
			}
			if !named[i] && b.ID != "" && strings.TrimSpace(b.Name) != "" {
				lastAnnounced = i
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
		// Only upstream/transport failures can be schema refusals. Final tool
		// validation below never attaches request metadata to its diagnostic.
		err = withToolSchemaRequest(err, wire, false)
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
	indices := kimiChatToolIndices(builders)
	for _, i := range indices {
		b := builders[i]
		if result.StopReason == "max_tokens" && named[i] && i == lastAnnounced {
			continue
		}
		if !kimiChatValidToolCall(*b) {
			err := kimiStreamedToolError(i, finishReason, idPresent[i], *b, p.input, resp)
			err.RequestID = kimiChatRequestID(resp, wire, result, builders)
			if result.Content != "" || result.Reasoning != "" {
				return result, fmt.Errorf("kimi chat stream: %w", err)
			}
			return nil, fmt.Errorf("kimi chat stream: %w", err)
		}
	}
	for _, i := range indices {
		b := *builders[i]
		if result.StopReason == "max_tokens" && named[i] && i == lastAnnounced {
			result.CutToolCall = &ToolCall{ID: b.ID, Name: b.Name}
			continue
		}
		result.ToolCalls = append(result.ToolCalls, b)
		emit(StreamChunk{ToolCall: &b})
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

// StreamedToolCallError describes invalid terminal model output without retaining arguments.
type StreamedToolCallError struct {
	Index                                            int
	FinishReason                                     string
	IDPresent, NamePresent                           bool
	ArgumentBytes                                    int
	JSONErrorClass, JSONKind                         string
	RequestID, Model, Mode, Region, CredentialSource string
}

func (e *StreamedToolCallError) Error() string {
	var out strings.Builder
	fmt.Fprintf(&out, "invalid streamed tool call: index=%d id_present=%t name_present=%t argument_bytes=%d", e.Index, e.IDPresent, e.NamePresent, e.ArgumentBytes)
	for _, field := range [...]struct{ key, value string }{
		{"finish", e.FinishReason}, {"json_error", e.JSONErrorClass}, {"json_kind", e.JSONKind},
		{"request_id", e.RequestID}, {"model", e.Model}, {"mode", e.Mode}, {"region", e.Region}, {"credential_source", e.CredentialSource},
	} {
		if field.value != "" {
			fmt.Fprintf(&out, " %s=%q", field.key, field.value)
		}
	}
	return out.String()
}

func kimiStreamedToolError(index int, finish string, idPresent bool, call ToolCall, input ProviderInput, resp *http.Response) *StreamedToolCallError {
	e := &StreamedToolCallError{
		Index: index, IDPresent: idPresent, NamePresent: strings.TrimSpace(call.Name) != "",
		ArgumentBytes: len(call.InputJSON), CredentialSource: kimiResponseCredentialSource(resp),
	}
	switch finish {
	case "stop", "tool_calls", "length", "content_filter", "function_call", "[DONE]":
		e.FinishReason = finish
	}
	switch input.Mode {
	case "", "subscription":
		e.Mode = "subscription"
	case "platform":
		e.Mode = input.Mode
	}
	switch input.Region {
	case "", "global":
		e.Region = "global"
	case "china":
		e.Region = input.Region
	}
	// The model comes from local configuration, never a response field.
	if len(input.Model) <= 256 && !strings.ContainsAny(input.Model, "\r\n") && !kimiChatReflectsIdentity(input.Model, resp.Request) {
		e.Model = input.Model
	}
	e.JSONKind, e.JSONErrorClass = kimiChatArgumentKind(call.InputJSON)
	return e
}

func kimiChatArgumentKind(args string) (kind, errorClass string) {
	if !json.Valid([]byte(args)) {
		return "", "syntax"
	}
	// Valid JSON has a non-whitespace first byte that identifies its kind, even
	// for a number beyond float64's range. No decoded argument data is retained.
	switch strings.TrimLeft(args, " \t\r\n")[0] {
	case '{':
		return "object", ""
	case '[':
		return "array", ""
	case '"':
		return "string", ""
	case 'n':
		return "null", ""
	case 't', 'f':
		return "boolean", ""
	default:
		return "number", ""
	}
}

func kimiChatRequestID(resp *http.Response, wire []byte, result *Response, builders map[int]*ToolCall) string {
	for _, key := range []string{"X-Trace-Id", "X-Request-Id", "Request-Id"} {
		for _, id := range resp.Header.Values(key) {
			if !safeKimiRequestID(id) || kimiChatReflectsIdentity(id, resp.Request) ||
				bytes.Contains(wire, []byte(id)) || strings.Contains(result.Content, id) || strings.Contains(result.Reasoning, id) {
				continue
			}
			reflected := false
			for _, call := range builders {
				if call.ID == id || call.Name == id || strings.Contains(call.InputJSON, id) {
					reflected = true
					break
				}
			}
			if !reflected {
				return id
			}
		}
	}
	return ""
}

// A reflected credential or device ID may look like an ordinary trace ID.
func kimiChatReflectsIdentity(value string, request *http.Request) bool {
	if request == nil || value == "" {
		return false
	}
	token := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	device := request.Header.Get("X-Msh-Device-Id")
	return (token != "" && strings.Contains(value, token)) ||
		(device != "" && strings.Contains(strings.ToLower(value), strings.ToLower(device)))
}

func safeKimiRequestID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}
