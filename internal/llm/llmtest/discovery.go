// Package llmtest adapts scripted model fixtures used by integration tests.
// Production packages must not import it.
package llmtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/llmstub"
)

const activationDescription = "Load complete native definitions for permitted tools by exact catalog name. Call those tools only in a later response, after receiving their definitions. Activation does not grant execution permission."
const catalogLabel = "Lossy discovery summaries (first sentence, at most 24 words)."

type wireCall struct {
	ID       string `json:"id"`
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}
type wireMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id"`
	ToolCalls  []wireCall      `json:"tool_calls"`
}

func messageText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		for _, part := range parts {
			text += part.Text
		}
	}
	return text
}
func stripTags(text string, tags []string) string {
	for _, tag := range tags {
		for {
			start := strings.Index(text, "<"+tag+">")
			if start < 0 {
				break
			}
			end := strings.Index(text[start:], "</"+tag+">")
			if end < 0 {
				text = text[:start]
				break
			}
			text = text[:start] + text[start+end+len(tag)+3:]
		}
	}
	return text
}

// DiscoveryHandler changes only the fake model's decision after schema
// activation. Every original wire request and history remains untouched;
// stub.Calls counts activation, action and answer separately. Excluded names
// and requests without the exact discovery contract use the original handler.
func DiscoveryHandler(stub *llmstub.Server) http.Handler {
	original := stub.Handler()
	var sequence atomic.Uint64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			original.ServeHTTP(w, r)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
		if err != nil {
			http.Error(w, "fixture request read failed", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var request struct {
			Stream   bool          `json:"stream"`
			Messages []wireMessage `json:"messages"`
			Tools    []struct {
				Function struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				} `json:"function"`
			} `json:"tools"`
		}
		if json.Unmarshal(raw, &request) != nil {
			original.ServeHTTP(w, r)
			return
		}
		native := map[string]bool{}
		bootstrap := false
		for _, tool := range request.Tools {
			native[tool.Function.Name] = true
			if tool.Function.Name == "activate_tools" && tool.Function.Description == activationDescription {
				bootstrap = true
			}
		}
		if !bootstrap {
			original.ServeHTTP(w, r)
			return
		}
		catalog, prompt, userAt := "", "", -1
		for i, message := range request.Messages {
			if message.Role == "system" {
				catalog += messageText(message.Content) + "\n"
			}
			if message.Role == "user" {
				if text := strings.TrimSpace(stripTags(messageText(message.Content), stub.StripTags)); text != "" {
					prompt, userAt = text, i
				}
			}
		}
		if !strings.Contains(catalog, catalogLabel) {
			original.ServeHTTP(w, r)
			return
		}
		var selected *llmstub.ToolCall
		for _, rule := range stub.Rules {
			if rule.Match == "" || strings.Contains(strings.ToLower(prompt), strings.ToLower(rule.Match)) {
				selected = rule.Tool
				break
			}
		}
		if selected == nil {
			original.ServeHTTP(w, r)
			return
		}
		allowed := false
		for _, line := range strings.Split(catalog, "\n") {
			if strings.HasPrefix(line, "- `"+selected.Name+"`:") {
				allowed = true
			}
		}
		if !allowed {
			original.ServeHTTP(w, r)
			return
		}
		names := map[string]string{}
		for _, message := range request.Messages[userAt+1:] {
			for _, call := range message.ToolCalls {
				names[call.ID] = call.Function.Name
			}
			if message.Role == "tool" && names[message.ToolCallID] == selected.Name {
				original.ServeHTTP(w, r)
				return
			}
		}
		call := *selected
		if !native[call.Name] {
			call.Name = "activate_tools"
			call.Arguments, _ = json.Marshal(map[string]any{"names": []string{selected.Name}})
		}
		// Answer preserves the original stub's counter and rule selection; the
		// explicit tool response has the same usage and streaming shape as llmstub.
		_ = stub.Answer(prompt)
		n := sequence.Add(1)
		model := stub.Model
		if model == "" {
			model = llmstub.DefaultModel
		}
		id := fmt.Sprintf("discovery-fixture-%d", n)
		args := strings.TrimSpace(string(call.Arguments))
		if args == "" {
			args = "{}"
		}
		function := map[string]any{"id": id, "type": "function", "function": map[string]any{"name": call.Name, "arguments": args}}
		usage := map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
		created := time.Now().Unix()
		if !request.Stream {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "object": "chat.completion", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls", "message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{function}}}}, "usage": usage})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(value any) {
			data, _ := json.Marshal(value)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		chunk := func(delta any, finish any) any {
			return map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
		}
		function["index"] = 0
		send(chunk(map[string]any{"role": "assistant", "tool_calls": []any{function}}, nil))
		send(chunk(map[string]any{}, "tool_calls"))
		send(map[string]any{"id": id, "object": "chat.completion.chunk", "model": model, "choices": []any{}, "usage": usage})
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
}
