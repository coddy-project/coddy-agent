package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// OpenCode's Gemini models use the native Google API, including signed parts
// on tool calls. Keep those parts intact across turns instead of translating
// them through Chat Completions, which loses the thought signatures.
type opencodeGoogleProvider struct {
	input  ProviderInput
	client *http.Client
	base   string
}

type opencodeGoogleCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type opencodeGooglePart struct {
	Text             string              `json:"text,omitempty"`
	Thought          bool                `json:"thought,omitempty"`
	ThoughtSignature string              `json:"thoughtSignature,omitempty"`
	FunctionCall     *opencodeGoogleCall `json:"functionCall,omitempty"`
	FunctionResponse any                 `json:"functionResponse,omitempty"`
	InlineData       any                 `json:"inlineData,omitempty"`
}

type opencodeGoogleContent struct {
	Role  string               `json:"role,omitempty"`
	Parts []opencodeGooglePart `json:"parts"`
}

type opencodeGoogleCarrier struct {
	Model string               `json:"opencode_google_model"`
	Parts []opencodeGooglePart `json:"parts"`
}

func (p *opencodeGoogleProvider) requestBody(messages []Message, tools []ToolDefinition) ([]byte, error) {
	var contents []opencodeGoogleContent
	var system []opencodeGooglePart
	calls := map[string]string{}
	wireIDs := map[string]string{}
	for _, m := range messages {
		role := "user"
		var parts []opencodeGooglePart
		switch m.Role {
		case RoleSystem:
			system = append(system, opencodeGooglePart{Text: m.Content})
			continue
		case RoleAssistant:
			role = "model"
			var carrier opencodeGoogleCarrier
			if json.Unmarshal([]byte(m.ReasoningSignature), &carrier) == nil && carrier.Model == p.input.Model {
				parts = carrier.Parts
			} else {
				if m.Content != "" {
					parts = append(parts, opencodeGooglePart{Text: m.Content})
				}
				for _, tc := range m.ToolCalls {
					args := json.RawMessage(tc.InputJSON)
					if len(args) == 0 {
						args = json.RawMessage(`{}`)
					}
					parts = append(parts, opencodeGooglePart{FunctionCall: &opencodeGoogleCall{ID: tc.ID, Name: tc.Name, Args: args}})
				}
			}
			for _, tc := range m.ToolCalls {
				calls[tc.ID] = tc.Name
			}
			callIndex := 0
			for _, part := range parts {
				if part.FunctionCall != nil && callIndex < len(m.ToolCalls) {
					wireIDs[m.ToolCalls[callIndex].ID] = part.FunctionCall.ID
					callIndex++
				}
			}
		case RoleTool:
			name := calls[m.ToolCallID]
			if name == "" {
				return nil, fmt.Errorf("opencode google: tool result %q has no matching call", m.ToolCallID)
			}
			response := map[string]any{"name": name, "response": map[string]string{"output": m.Content}}
			if id := wireIDs[m.ToolCallID]; id != "" {
				response["id"] = id
			}
			parts = append(parts, opencodeGooglePart{FunctionResponse: response})
		case RoleUser:
			text := m.Content
			for _, ip := range m.ImageParts {
				kind, mime, payload := sortAttachment(ip)
				if kind == attachedPicture && payload != "" {
					parts = append(parts, opencodeGooglePart{InlineData: map[string]string{"mimeType": mime, "data": payload}})
				} else {
					if kind == attachedPicture {
						kind = attachedUnsendable
					}
					text += attachmentText(ip, kind, mime)
				}
			}
			if text != "" {
				parts = append([]opencodeGooglePart{{Text: text}}, parts...)
			}
		}
		if len(parts) == 0 {
			continue
		}
		if n := len(contents); n > 0 && contents[n-1].Role == role {
			contents[n-1].Parts = append(contents[n-1].Parts, parts...)
		} else {
			contents = append(contents, opencodeGoogleContent{Role: role, Parts: parts})
		}
	}
	body := map[string]any{"contents": contents}
	if len(system) > 0 {
		body["systemInstruction"] = opencodeGoogleContent{Parts: system}
	}
	generation := map[string]any{}
	if p.input.MaxTokens > 0 {
		generation["maxOutputTokens"] = p.input.MaxTokens
	}
	if p.input.TemperatureSet || p.input.Temperature != 0 {
		generation["temperature"] = p.input.Temperature
	}
	if effort := opencodeReasoningEffort(p.input.ReasoningEffort); effort != "" {
		generation["thinkingConfig"] = map[string]any{"thinkingLevel": strings.ToUpper(effort), "includeThoughts": true}
	}
	if len(generation) > 0 {
		body["generationConfig"] = generation
	}
	if len(tools) > 0 {
		declarations := make([]map[string]any, 0, len(tools))
		for _, tool := range tools {
			declarations = append(declarations, map[string]any{"name": tool.Name, "description": tool.Description, "parametersJsonSchema": tool.InputSchema})
		}
		body["tools"] = []any{map[string]any{"functionDeclarations": declarations}}
	}
	return json.Marshal(body)
}

func (p *opencodeGoogleProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	return p.generate(ctx, messages, tools, nil, false)
}

func (p *opencodeGoogleProvider) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	return p.generate(ctx, messages, tools, onChunk, true)
}

func (p *opencodeGoogleProvider) generate(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk), streaming bool) (*Response, error) {
	body, err := p.requestBody(messages, tools)
	if err != nil {
		return nil, err
	}
	method := ":generateContent"
	if streaming {
		method = ":streamGenerateContent?alt=sse"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/models/"+url.PathEscape(p.input.Model)+method, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if streaming {
		req.Header.Set("Accept", "text/event-stream")
	}
	raw, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode google: %w", err)
	}
	defer func() { _ = raw.Body.Close() }()
	if raw.StatusCode < 200 || raw.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(raw.Body, 512))
		return nil, fmt.Errorf("opencode google: HTTP %d: %s", raw.StatusCode, strings.TrimSpace(string(snippet)))
	}
	result := &Response{}
	var parts []opencodeGooglePart
	var terminal, emitted bool
	emit := func(chunk StreamChunk) {
		if onChunk != nil {
			emitted = true
			onChunk(chunk)
		}
	}
	consume := func(data []byte) error {
		var event struct {
			Candidates []struct {
				Content      opencodeGoogleContent `json:"content"`
				FinishReason string                `json:"finishReason"`
			} `json:"candidates"`
			Usage struct {
				Prompt   int `json:"promptTokenCount"`
				Output   int `json:"candidatesTokenCount"`
				Thoughts int `json:"thoughtsTokenCount"`
				Cached   int `json:"cachedContentTokenCount"`
			} `json:"usageMetadata"`
			Error          json.RawMessage `json:"error"`
			PromptFeedback struct {
				BlockReason string `json:"blockReason"`
			} `json:"promptFeedback"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			if trunc := streamDecodeTruncation(err, emitted, 0); trunc != nil {
				return trunc
			}
			return &streamUndecodableError{cause: err, snippet: streamErrorSnippet(data)}
		}
		if len(event.Error) > 0 && string(event.Error) != "null" {
			return newStreamServerError(event.Error, emitted)
		}
		if event.PromptFeedback.BlockReason != "" {
			return fmt.Errorf("opencode google: prompt blocked: %s", event.PromptFeedback.BlockReason)
		}
		if event.Usage.Prompt > 0 || event.Usage.Output > 0 || event.Usage.Thoughts > 0 {
			result.InputTokens = event.Usage.Prompt
			result.OutputTokens = event.Usage.Output + event.Usage.Thoughts
			result.ReasoningTokens = event.Usage.Thoughts
			result.CachedInputTokens = event.Usage.Cached
		}
		if len(event.Candidates) == 0 {
			return nil
		}
		candidate := event.Candidates[0]
		for _, part := range candidate.Content.Parts {
			if part.FunctionCall != nil {
				call := part.FunctionCall
				id := call.ID
				if id == "" {
					id = "call_" + newCodexSessionID()
				}
				args := string(call.Args)
				if args == "" {
					args = "{}"
				}
				tc := ToolCall{ID: id, Name: call.Name, InputJSON: args}
				result.ToolCalls = append(result.ToolCalls, tc)
				emit(StreamChunk{ToolCall: &tc})
			} else if part.Thought {
				result.Reasoning += part.Text
				if part.Text != "" {
					emit(StreamChunk{ReasoningDelta: part.Text})
				}
			} else {
				result.Content += part.Text
				if part.Text != "" {
					emit(StreamChunk{TextDelta: part.Text})
				}
			}
			parts = append(parts, part)
		}
		if candidate.FinishReason != "" {
			terminal = true
			switch candidate.FinishReason {
			case "STOP":
				result.StopReason = codexStopReason(result.ToolCalls)
			case "MAX_TOKENS":
				result.StopReason = "max_tokens"
			case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
				result.StopReason = "content_filter"
			default:
				return fmt.Errorf("opencode google: generation ended: %s", candidate.FinishReason)
			}
		}
		return nil
	}
	if streaming {
		scanner := newSSEScanner(raw.Body)
		for scanner.Next() {
			frame := scanner.Frame()
			if len(frame.errData) > 0 {
				err = fmt.Errorf("opencode google: %s", streamErrorSnippet(frame.errData))
			} else {
				err = consume(frame.data)
			}
			if err != nil {
				break
			}
		}
		if err == nil {
			err = scanner.Err()
		}
	} else {
		var data []byte
		data, err = io.ReadAll(io.LimitReader(raw.Body, sseMaxScanSize))
		if err == nil {
			err = consume(data)
		}
	}
	if err != nil || !terminal {
		result.ToolCalls = nil
		if err == nil {
			err = &streamTruncatedError{emitted: emitted}
		} else {
			err = &streamTransportError{cause: err, emitted: emitted}
		}
		return result, fmt.Errorf("opencode google: %w", err)
	}
	carrier, err := json.Marshal(opencodeGoogleCarrier{Model: p.input.Model, Parts: parts})
	if err != nil {
		return nil, err
	}
	result.ReasoningSignature = string(carrier)
	return result, nil
}
