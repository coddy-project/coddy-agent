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

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/tidwall/gjson"
)

type kimiAnthropicProvider struct {
	transport *KimiInferenceTransport
	input     ProviderInput
}

type kimiAnthropicCarrier struct {
	Kimi   bool                   `json:"kimi_anthropic"`
	Blocks []anthropicReplayBlock `json:"blocks"`
}

func kimiAnthropicSignature(blocks []anthropicReplayBlock) string {
	for _, b := range blocks {
		if b.Type == anthropicBlockThinking && b.Signature == "" && b.Thinking != "" {
			data, _ := json.Marshal(kimiAnthropicCarrier{Kimi: true, Blocks: blocks})
			return string(data)
		}
	}
	return anthropicThinkingSignature(blocks)
}

func kimiAnthropicAssistant(m Message, thinking bool) any {
	var carrier kimiAnthropicCarrier
	if !thinking || json.Unmarshal([]byte(m.ReasoningSignature), &carrier) != nil || !carrier.Kimi {
		return anthropicAssistantBlocks(m, thinking)
	}
	var out []any
	calls := make(map[string]ToolCall, len(m.ToolCalls))
	for _, c := range m.ToolCalls {
		calls[c.ID] = c
	}
	var oldText strings.Builder
	used := map[string]bool{}
	for _, b := range carrier.Blocks {
		if b.Type == anthropicBlockText {
			oldText.WriteString(b.Text)
		}
		if b.Type == anthropicBlockToolUse {
			if _, ok := calls[b.ID]; ok {
				used[b.ID] = true
			}
		}
	}
	unchanged := oldText.String() == m.Content && len(used) == len(calls)
	for _, b := range carrier.Blocks {
		switch b.Type {
		case anthropicBlockThinking:
			block := map[string]any{"type": "thinking", "thinking": b.Thinking}
			if b.Signature != "" {
				block["signature"] = b.Signature
			}
			out = append(out, block)
		case anthropicBlockRedacted:
			out = append(out, map[string]any{"type": "redacted_thinking", "data": b.Data})
		case anthropicBlockText:
			if unchanged {
				out = append(out, map[string]any{"type": "text", "text": b.Text})
			}
		case anthropicBlockToolUse:
			if c, ok := calls[b.ID]; ok && unchanged {
				out = append(out, anthropicToolUseParam(c))
			}
		}
	}
	if !unchanged {
		if m.Content != "" {
			out = append(out, map[string]any{"type": "text", "text": m.Content})
		}
		for _, c := range m.ToolCalls {
			out = append(out, anthropicToolUseParam(c))
		}
	}
	return out
}

var _ Provider = (*kimiAnthropicProvider)(nil)

// NewKimiAnthropicProvider binds Anthropic beta inference to the selected subscription region.
func NewKimiAnthropicProvider(input ProviderInput, source KimiCredentialSource) (Provider, error) {
	client, err := providerHTTPClient(input.ProxyURL, input.Timeout, input.StreamIdleTimeout)
	if err != nil {
		return nil, err
	}
	tr, err := newKimiInferenceTransport(input.Mode, input.Region, "anthropic", source, client)
	if err != nil {
		return nil, err
	}
	return newKimiAnthropicProvider(tr, input), nil
}
func newKimiAnthropicProvider(tr *KimiInferenceTransport, input ProviderInput) *kimiAnthropicProvider {
	return &kimiAnthropicProvider{transport: tr, input: input}
}
func (p *kimiAnthropicProvider) request(ctx context.Context, messages []Message, tools []ToolDefinition, stream bool) (*http.Response, error) {
	if p.transport == nil {
		return nil, errors.New("kimi anthropic: missing transport")
	}
	max := p.input.MaxTokens
	if max <= 0 {
		max = 8192
	}
	body := map[string]any{"model": p.input.Model, "max_tokens": max, "stream": stream}
	var msgs []map[string]any
	var system []any
	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			system = append(system, map[string]any{"type": "text", "text": m.Content})
		case RoleUser:
			msgs = append(msgs, map[string]any{"role": "user", "content": anthropicUserBlocks(m)})
		case RoleAssistant:
			blocks := kimiAnthropicAssistant(m, p.input.ReasoningEffort != "off")
			msgs = append(msgs, map[string]any{"role": "assistant", "content": blocks})
		case RoleTool:
			msgs = append(msgs, map[string]any{"role": "user", "content": []anthropic.ContentBlockParamUnion{anthropic.NewToolResultBlock(m.ToolCallID, m.Content, false)}})
		}
	}
	if len(system) > 0 {
		body["system"] = system
	}
	if msgs == nil {
		msgs = []map[string]any{}
	}
	body["messages"] = msgs
	if p.input.ReasoningEffort != "" {
		thinking := map[string]any{"type": "enabled"}
		if p.input.ReasoningEffort == "off" {
			thinking["type"] = "disabled"
		} else if p.input.ReasoningEffort != "on" {
			body["output_config"] = map[string]any{"effort": p.input.ReasoningEffort}
		}
		body["thinking"] = thinking
	}
	if p.input.TemperatureSet {
		body["temperature"] = p.input.Temperature
	}
	if p.input.TopP != nil {
		body["top_p"] = *p.input.TopP
	}
	if len(tools) > 0 {
		var ts []map[string]any
		for _, t := range tools {
			ts = append(ts, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.InputSchema})
		}
		body["tools"] = ts
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("kimi anthropic encode: %w", err)
	}
	resp, err := p.transport.do(ctx, bytes.NewReader(data), p.input.ReasoningEffort != "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, withToolSchemaRequest(kimiStatusError("kimi anthropic", resp), body, true)
	}
	return resp, nil
}
func (p *kimiAnthropicProvider) Complete(ctx context.Context, m []Message, t []ToolDefinition) (*Response, error) {
	resp, err := p.request(ctx, m, t, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("kimi anthropic read: %w", err)
	}
	if !json.Valid(data) {
		return nil, errors.New("kimi anthropic: invalid JSON")
	}
	root := gjson.ParseBytes(data)
	r := &Response{StopReason: mapAnthropicStopReason(root.Get("stop_reason").String()), UsageIncomplete: true}
	var blocks []anthropicReplayBlock
	toolIDs := make(map[string]struct{})
	for _, b := range root.Get("content").Array() {
		switch b.Get("type").String() {
		case "thinking":
			v := b.Get("thinking").String()
			r.Reasoning += v
			blocks = append(blocks, anthropicReplayBlock{Type: anthropicBlockThinking, Thinking: v, Signature: b.Get("signature").String()})
		case "redacted_thinking":
			blocks = append(blocks, anthropicReplayBlock{Type: anthropicBlockRedacted, Data: b.Get("data").String()})
		case "text":
			v := b.Get("text").String()
			r.Content += v
			blocks = append(blocks, anthropicReplayBlock{Type: anthropicBlockText, Text: v})
		case "tool_use":
			id := b.Get("id").String()
			if id == "" || b.Get("name").String() == "" || !b.Get("input").IsObject() {
				return nil, errors.New("kimi anthropic: invalid tool use")
			}
			if _, exists := toolIDs[id]; exists {
				return nil, errors.New("kimi anthropic: duplicate tool use ID")
			}
			toolIDs[id] = struct{}{}
			r.ToolCalls = append(r.ToolCalls, ToolCall{ID: id, Name: b.Get("name").String(), InputJSON: b.Get("input").Raw})
			blocks = append(blocks, anthropicReplayBlock{Type: anthropicBlockToolUse, ID: id})
		}
	}
	r.ReasoningSignature = kimiAnthropicSignature(blocks)
	u := root.Get("usage")
	if u.Get("input_tokens").Type == gjson.Number && u.Get("output_tokens").Type == gjson.Number {
		r.UsageKnown = true
		r.UsageIncomplete = false
		r.CachedInputTokens = int(u.Get("cache_read_input_tokens").Int())
		r.InputTokens = int(u.Get("input_tokens").Int()+u.Get("cache_creation_input_tokens").Int()) + r.CachedInputTokens
		r.OutputTokens = int(u.Get("output_tokens").Int())
	}
	if r.StopReason == "" {
		return r, errors.New("kimi anthropic: missing stop reason")
	}
	return r, nil
}

func (p *kimiAnthropicProvider) Stream(ctx context.Context, m []Message, t []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	resp, err := p.request(ctx, m, t, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	r := &Response{UsageIncomplete: true}
	log := anthropicBlockLog{}
	calls := map[int64]*ToolCall{}
	toolIDs := make(map[string]struct{})
	done := map[int64]bool{}
	toolDelta := map[int64]bool{}
	var inputKnown, outputKnown, terminal, emitted bool
	var uncachedInput, cacheCreation, cacheRead int64
	usageInput := func(u gjson.Result) {
		if v := u.Get("input_tokens"); v.Type == gjson.Number {
			inputKnown = true
			uncachedInput = v.Int()
		}
		if v := u.Get("cache_creation_input_tokens"); v.Type == gjson.Number {
			cacheCreation = v.Int()
		}
		if v := u.Get("cache_read_input_tokens"); v.Type == gjson.Number {
			cacheRead = v.Int()
		}
		r.CachedInputTokens = int(cacheRead)
		r.InputTokens = int(uncachedInput + cacheCreation + cacheRead)
	}
	emit := func(c StreamChunk) {
		if onChunk != nil {
			emitted = true
			onChunk(c)
		}
	}
	scan := newSSEScanner(resp.Body)
	for scan.Next() {
		frame := scan.Frame()
		if len(frame.errData) > 0 {
			err = newStreamServerError(frame.errData, emitted)
			break
		}
		data := bytes.TrimSpace(frame.data)
		if len(data) == 0 {
			continue
		}
		if !json.Valid(data) {
			err = &streamTruncatedError{emitted: emitted}
			break
		}
		e := gjson.ParseBytes(data)
		index := e.Get("index").Int()
		switch e.Get("type").String() {
		case "message_start":
			u := e.Get("message.usage")
			usageInput(u)
		case "content_block_start":
			b := e.Get("content_block")
			if log[index] != nil {
				err = errors.New("kimi anthropic: duplicate block index")
				break
			}
			switch b.Get("type").String() {
			case "text":
				v := b.Get("text").String()
				log[index] = &anthropicReplayBlock{Type: anthropicBlockText, Text: v}
				r.Content += v
				if v != "" {
					emit(StreamChunk{TextDelta: v})
				}
			case "thinking":
				v := b.Get("thinking").String()
				log[index] = &anthropicReplayBlock{Type: anthropicBlockThinking, Thinking: v, Signature: b.Get("signature").String()}
				if v != "" {
					if r.Reasoning != "" {
						r.Reasoning += "\n\n"
						emit(StreamChunk{ReasoningDelta: "\n\n"})
					}
					r.Reasoning += v
					emit(StreamChunk{ReasoningDelta: v})
				}
			case "redacted_thinking":
				log[index] = &anthropicReplayBlock{Type: anthropicBlockRedacted, Data: b.Get("data").String()}
			case "tool_use":
				id := b.Get("id").String()
				if strings.TrimSpace(id) == "" || strings.TrimSpace(b.Get("name").String()) == "" {
					err = errors.New("kimi anthropic: invalid tool use")
					break
				}
				if _, exists := toolIDs[id]; exists {
					err = errors.New("kimi anthropic: duplicate tool use ID")
					break
				}
				toolIDs[id] = struct{}{}
				log[index] = &anthropicReplayBlock{Type: anthropicBlockToolUse, ID: id}
				calls[index] = &ToolCall{ID: id, Name: b.Get("name").String(), InputJSON: b.Get("input").Raw}
				emit(StreamChunk{ToolCallNamed: &ToolCall{ID: id, Name: b.Get("name").String()}})
			}
		case "content_block_delta":
			d := e.Get("delta")
			b := log[index]
			if b == nil || done[index] {
				err = errors.New("kimi anthropic: delta for unopened block")
				break
			}
			switch d.Get("type").String() {
			case "text_delta":
				if b.Type != anthropicBlockText {
					err = errors.New("kimi anthropic: text delta for wrong block")
					break
				}
				v := d.Get("text").String()
				r.Content += v
				if b := log[index]; b != nil {
					b.Text += v
				}
				if v != "" {
					emit(StreamChunk{TextDelta: v})
				}
			case "thinking_delta":
				if b.Type != anthropicBlockThinking {
					err = errors.New("kimi anthropic: thinking delta for wrong block")
					break
				}
				v := d.Get("thinking").String()
				if b.Thinking == "" && r.Reasoning != "" && v != "" {
					r.Reasoning += "\n\n"
					emit(StreamChunk{ReasoningDelta: "\n\n"})
				}
				r.Reasoning += v
				if b := log[index]; b != nil {
					b.Thinking += v
				}
				if v != "" {
					emit(StreamChunk{ReasoningDelta: v})
				}
			case "signature_delta":
				if b.Type != anthropicBlockThinking {
					err = errors.New("kimi anthropic: signature delta for wrong block")
					break
				}
				if b := log[index]; b != nil {
					b.Signature += d.Get("signature").String()
				}
			case "input_json_delta":
				if c := calls[index]; c != nil {
					if !toolDelta[index] {
						c.InputJSON = ""
						toolDelta[index] = true
					}
					v := d.Get("partial_json").String()
					c.InputJSON += v
					emit(StreamChunk{ToolCallDelta: &ToolCall{ID: c.ID, Name: c.Name, InputJSON: v}})
				} else {
					err = errors.New("kimi anthropic: tool delta for non-tool block")
				}
			}
		case "content_block_stop":
			if log[index] == nil || done[index] {
				err = errors.New("kimi anthropic: invalid block stop")
				break
			}
			done[index] = true
		case "message_delta":
			r.StopReason = mapAnthropicStopReason(e.Get("delta.stop_reason").String())
			u := e.Get("usage")
			usageInput(u)
			if u.Get("output_tokens").Type == gjson.Number {
				outputKnown = true
				r.OutputTokens = int(u.Get("output_tokens").Int())
			}
		case "message_stop":
			terminal = true
		case "error":
			code := 0
			switch e.Get("error.type").String() {
			case "overloaded_error", "engine_overloaded_error":
				code = 529
			case "rate_limit_error", "rate_limit_reached_error", "rate_limit_exceeded":
				code = 429
			}
			err = &streamServerError{code: code, msg: "kimi anthropic: server stream error", emitted: emitted}
		}
		if err != nil || terminal {
			break
		}
	}
	r.ReasoningSignature = kimiAnthropicSignature(log.ordered())
	r.UsageKnown = inputKnown && outputKnown
	r.UsageIncomplete = !r.UsageKnown
	if err == nil && !terminal && ctx.Err() != nil {
		err = &streamTransportError{cause: ctx.Err(), emitted: emitted}
	}
	if err == nil && scan.Err() != nil {
		err = &streamTransportError{cause: scan.Err(), emitted: emitted}
	}
	if err == nil && (!terminal || r.StopReason == "") {
		err = &streamTruncatedError{emitted: emitted}
	}
	for i := range log {
		if !done[i] && err == nil {
			err = &streamTruncatedError{emitted: emitted}
		}
	}
	for i := range calls {
		if err == nil && calls[i].InputJSON != "" {
			var args map[string]any
			if json.Unmarshal([]byte(calls[i].InputJSON), &args) != nil || args == nil {
				err = &streamTruncatedError{emitted: emitted}
			}
		}
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			indices := make([]int64, 0, len(calls))
			for i := range calls {
				indices = append(indices, i)
			}
			sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })
			for _, i := range indices {
				r.ToolCalls = append(r.ToolCalls, *calls[i])
			}
			if len(r.ToolCalls) > 0 && r.StopReason == "" {
				r.StopReason = "tool_use"
			}
		}
		if r.Content != "" || r.Reasoning != "" || len(r.ToolCalls) > 0 {
			return r, fmt.Errorf("kimi anthropic stream: %w", err)
		}
		return nil, fmt.Errorf("kimi anthropic stream: %w", err)
	}
	indices := make([]int64, 0, len(calls))
	for i := range calls {
		indices = append(indices, i)
	}
	sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })
	for _, i := range indices {
		c := calls[i]
		if c.InputJSON == "" {
			c.InputJSON = "{}"
		}
		r.ToolCalls = append(r.ToolCalls, *c)
		v := *c
		emit(StreamChunk{ToolCall: &v})
	}
	return r, nil
}
