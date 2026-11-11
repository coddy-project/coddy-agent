package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/openai/openai-go"
	"github.com/tidwall/gjson"
)

type kimiResponsesProvider struct {
	transport *KimiInferenceTransport
	input     ProviderInput
}

var _ Provider = (*kimiResponsesProvider)(nil)

// NewKimiResponsesProvider binds Responses inference to a fixed subscription endpoint.
func NewKimiResponsesProvider(input ProviderInput, source KimiCredentialSource) (Provider, error) {
	client, err := providerHTTPClient(input.ProxyURL, input.Timeout, input.StreamIdleTimeout)
	if err != nil {
		return nil, err
	}
	tr, err := newKimiInferenceTransport(input.Mode, input.Region, "openai_responses", source, client)
	if err != nil {
		return nil, err
	}
	return newKimiResponsesProvider(tr, input), nil
}
func newKimiResponsesProvider(tr *KimiInferenceTransport, input ProviderInput) *kimiResponsesProvider {
	return &kimiResponsesProvider{transport: tr, input: input}
}

type kimiResponsesCarrier struct {
	KimiResponses bool              `json:"kimi_responses"`
	Model         string            `json:"model"`
	Items         []json.RawMessage `json:"items"`
}

func (p *kimiResponsesProvider) encode(items []json.RawMessage) string {
	if len(items) == 0 {
		return ""
	}
	b, err := json.Marshal(kimiResponsesCarrier{true, p.input.Model, items})
	if err != nil {
		return ""
	}
	return string(b)
}
func (p *kimiResponsesProvider) decode(s string) []json.RawMessage {
	var c kimiResponsesCarrier
	if json.Unmarshal([]byte(s), &c) != nil || !c.KimiResponses || c.Model != p.input.Model {
		return nil
	}
	return c.Items
}
func (p *kimiResponsesProvider) request(ctx context.Context, m []Message, t []ToolDefinition, stream bool) (*http.Response, error) {
	if p.transport == nil {
		return nil, errors.New("kimi responses: missing transport")
	}
	params := buildResponsesParams(p.input.Model, opencodeResponsesEffort(p.input.ReasoningEffort), p.decode, m, t)
	if p.input.MaxTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(p.input.MaxTokens))
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("kimi responses encode: %w", err)
	}
	var body map[string]any
	if err = json.Unmarshal(b, &body); err != nil {
		return nil, err
	}
	body["stream"] = stream
	if p.input.TemperatureSet {
		body["temperature"] = p.input.Temperature
	}
	if p.input.TopP != nil {
		body["top_p"] = *p.input.TopP
	}
	b, err = json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return p.transport.do(ctx, bytes.NewReader(b), false, "kimi responses")
}
func kimiResponsesUsage(raw gjson.Result, r *Response) {
	if raw.Get("input_tokens").Type != gjson.Number || raw.Get("output_tokens").Type != gjson.Number {
		return
	}
	r.UsageKnown = true
	r.UsageIncomplete = false
	r.InputTokens = int(raw.Get("input_tokens").Int())
	r.OutputTokens = int(raw.Get("output_tokens").Int())
	r.CachedInputTokens = int(raw.Get("input_tokens_details.cached_tokens").Int())
	r.ReasoningTokens = int(raw.Get("output_tokens_details.reasoning_tokens").Int())
}

func kimiResponsesValidCall(tc ToolCall) bool {
	if strings.TrimSpace(tc.ID) == "" || strings.TrimSpace(tc.Name) == "" {
		return false
	}
	var args map[string]json.RawMessage
	return json.Unmarshal([]byte(tc.InputJSON), &args) == nil && args != nil
}

func (p *kimiResponsesProvider) Complete(ctx context.Context, m []Message, t []ToolDefinition) (*Response, error) {
	resp, err := p.request(ctx, m, t, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("kimi responses read: %w", err)
	}
	if !json.Valid(data) {
		return nil, errors.New("kimi responses: invalid JSON")
	}
	root := gjson.ParseBytes(data)
	r := &Response{UsageIncomplete: true}
	var reasoning []json.RawMessage
	var calls []ToolCall
	for _, item := range root.Get("output").Array() {
		switch item.Get("type").String() {
		case "message":
			for _, c := range item.Get("content").Array() {
				if c.Get("type").String() == "output_text" {
					r.Content += c.Get("text").String()
				}
			}
		case "reasoning":
			reasoning = append(reasoning, json.RawMessage(item.Raw))
			for _, s := range item.Get("summary").Array() {
				r.Reasoning += s.Get("text").String()
			}
		case "function_call":
			calls = append(calls, ToolCall{ID: item.Get("call_id").String(), Name: item.Get("name").String(), InputJSON: item.Get("arguments").String()})
		}
	}
	r.ReasoningSignature = p.encode(reasoning)
	kimiResponsesUsage(root.Get("usage"), r)
	switch root.Get("status").String() {
	case "completed":
	case "incomplete":
		if root.Get("incomplete_details.reason").String() == "max_output_tokens" {
			r.StopReason = "max_tokens"
		} else {
			return r, errors.New("kimi responses: incomplete response")
		}
	default:
		return r, errors.New("kimi responses: response not completed")
	}
	for _, call := range calls {
		if !kimiResponsesValidCall(call) {
			return r, errors.New("kimi responses: invalid tool call")
		}
	}
	r.ToolCalls = calls
	if r.StopReason == "" {
		r.StopReason = codexStopReason(r.ToolCalls)
	}
	return r, nil
}
func (p *kimiResponsesProvider) Stream(ctx context.Context, m []Message, t []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	resp, err := p.request(ctx, m, t, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	r := &Response{UsageIncomplete: true}
	var reasoning []json.RawMessage
	calls := map[string]*ToolCall{}
	indices := map[int64]string{}
	finished := map[string]bool{}
	var order []string
	emitted := false
	observed := false
	terminal := false
	emit := func(c StreamChunk) {
		if onChunk != nil {
			emitted = true
			onChunk(c)
		}
	}
	scan := newSSEScanner(resp.Body)
	for scan.Next() {
		frame := scan.Frame()
		data := bytes.TrimSpace(frame.data)
		if len(frame.errData) > 0 {
			err = newStreamServerError(frame.errData, emitted)
			break
		}
		if len(data) == 0 {
			continue
		}
		if !json.Valid(data) {
			var value any
			decodeErr := json.Unmarshal(data, &value)
			und := &streamUndecodableError{snippet: streamErrorSnippet(data), cause: decodeErr}
			if trunc := streamDecodeTruncation(und, emitted, len(data)); trunc != nil {
				err = trunc
			} else {
				err = und
			}
			break
		}
		e := gjson.ParseBytes(data)
		kimiResponsesUsage(e.Get("response.usage"), r)
		key := func(id string) string {
			if id != "" {
				return id
			}
			if idx := e.Get("output_index"); idx.Exists() {
				return indices[idx.Int()]
			}
			return ""
		}
		switch e.Get("type").String() {
		case "response.output_text.delta":
			d := e.Get("delta").String()
			r.Content += d
			if d != "" {
				emit(StreamChunk{TextDelta: d})
			}
		case "response.reasoning_summary_part.added":
			if r.Reasoning != "" {
				r.Reasoning += "\n\n"
				emit(StreamChunk{ReasoningDelta: "\n\n"})
			}
		case "response.reasoning_summary_text.delta":
			d := e.Get("delta").String()
			r.Reasoning += d
			if d != "" {
				emit(StreamChunk{ReasoningDelta: d})
			}
		case "response.output_item.added":
			item := e.Get("item")
			if item.Get("type").String() == "function_call" {
				id := item.Get("id").String()
				if idx := e.Get("output_index"); idx.Exists() {
					if id == "" {
						id = fmt.Sprintf("index:%d", idx.Int())
					}
					indices[idx.Int()] = id
				}
				tc := &ToolCall{ID: item.Get("call_id").String(), Name: item.Get("name").String(), InputJSON: item.Get("arguments").String()}
				observed = true
				calls[id] = tc
				order = append(order, id)
				emit(StreamChunk{ToolCallNamed: &ToolCall{ID: tc.ID, Name: tc.Name}})
			}
		case "response.function_call_arguments.delta":
			if tc := calls[key(e.Get("item_id").String())]; tc != nil {
				d := e.Get("delta").String()
				tc.InputJSON += d
				emit(StreamChunk{ToolCallDelta: &ToolCall{ID: tc.ID, Name: tc.Name, InputJSON: d}})
			}
		case "response.output_item.done":
			item := e.Get("item")
			switch item.Get("type").String() {
			case "reasoning":
				reasoning = append(reasoning, json.RawMessage(item.Raw))
			case "function_call":
				id := key(item.Get("id").String())
				if tc := calls[id]; tc != nil {
					args := item.Get("arguments").String()
					if !strings.HasPrefix(args, tc.InputJSON) {
						err = &streamUndecodableError{snippet: "function call arguments disagree with deltas"}
						break
					}
					if suffix := args[len(tc.InputJSON):]; suffix != "" {
						emit(StreamChunk{ToolCallDelta: &ToolCall{ID: tc.ID, Name: tc.Name, InputJSON: suffix}})
					}
					tc.InputJSON = args
					finished[id] = true
				}
			}
		case "response.completed", "response.incomplete":
			terminal = true
			kimiResponsesUsage(e.Get("response.usage"), r)
			if e.Get("type").String() == "response.incomplete" {
				if e.Get("response.incomplete_details.reason").String() == "max_output_tokens" {
					r.StopReason = "max_tokens"
				} else {
					err = errors.New("kimi responses: incomplete response")
				}
			}
		case "error", "response.failed":
			payload := data
			if e.Get("type").String() == "response.failed" && e.Get("response.error").Exists() {
				payload = []byte(e.Get("response.error").Raw)
			}
			err = newStreamServerError(payload, emitted)
		}
		if terminal || err != nil {
			break
		}
	}
	r.ReasoningSignature = p.encode(reasoning)
	if err == nil && !terminal && ctx.Err() != nil {
		err = &streamTransportError{cause: ctx.Err(), emitted: emitted}
	}
	if err == nil && scan.Err() != nil {
		err = &streamTransportError{cause: scan.Err(), emitted: emitted}
	}
	if err == nil && !terminal {
		err = &streamTruncatedError{emitted: emitted}
	}
	if err == nil {
		for _, id := range order {
			if !finished[id] {
				err = &streamTruncatedError{emitted: emitted}
				break
			}
			if tc := calls[id]; tc == nil || !kimiResponsesValidCall(*tc) {
				err = &streamUndecodableError{snippet: "invalid tool call"}
				break
			}
		}
	}
	if err != nil {
		if observed || r.UsageKnown || strings.TrimSpace(r.Content) != "" || strings.TrimSpace(r.Reasoning) != "" {
			return r, fmt.Errorf("kimi responses stream: %w", err)
		}
		return nil, fmt.Errorf("kimi responses stream: %w", err)
	}
	// Preserve the order in which the server announced calls.
	seen := map[string]bool{}
	for _, id := range order {
		if seen[id] {
			continue
		}
		seen[id] = true
		if tc := calls[id]; tc != nil {
			r.ToolCalls = append(r.ToolCalls, *tc)
			v := *tc
			emit(StreamChunk{ToolCall: &v})
		}
	}
	if r.StopReason == "" {
		r.StopReason = codexStopReason(r.ToolCalls)
	}
	return r, nil
}
