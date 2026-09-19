// Package llmstub is a scripted model server with the OpenAI shape Coddy's
// openai provider speaks: GET /v1/models and POST /v1/chat/completions, with
// and without streaming. It exists so the Telegram stand of cmd/tgfake runs
// with no key and no network: the answer to a prompt is chosen by rule, by
// turn, or by echoing the prompt back, and streamed word by word at a chosen
// pace so the gateway's edit and draft paths have something to do.
package llmstub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Rule maps a prompt to an answer: the first rule whose Match occurs in the
// last user message (case-insensitive) wins; an empty Match matches all.
//
// A rule with a Tool makes the model act before it answers: the first request
// of the turn gets the tool call, and the request that carries the tool's
// result gets Answer. That is enough to script a turn that starts work - a
// background command, a gated tool that asks for permission - without a model.
type Rule struct {
	Match  string    `json:"match"`
	Answer string    `json:"answer"`
	Tool   *ToolCall `json:"tool,omitempty"`
}

// ToolCall is the call a Rule makes. Arguments is the JSON object the tool is
// called with; a script writes it as an object, not as an encoded string.
type ToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Server is the scripted model.
type Server struct {
	// Model is the id reported by /v1/models and echoed in answers.
	Model string
	// Answers are used in turn, one per completion call, when no Rule matches.
	Answers []string
	// Rules are checked first, in order.
	Rules []Rule
	// Delay is the pause between streamed chunks.
	Delay time.Duration
	// ChunkWords is how many words each streamed chunk carries; at most one
	// by default.
	ChunkWords int

	mu    sync.Mutex
	calls int
}

const defaultModel = "coddy-demo"

// Handler serves the two routes under /v1.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", s.models)
	mux.HandleFunc("POST /v1/chat/completions", s.completions)
	return mux
}

func (s *Server) model() string {
	if s.Model == "" {
		return defaultModel
	}
	return s.Model
}

func (s *Server) models(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"object": "list",
		"data":   []map[string]any{{"id": s.model(), "object": "model", "owned_by": "tgfake"}},
	})
}

type chatRequest struct {
	Stream   bool `json:"stream"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

// answersToolResult reports whether the request's newest message - past the
// <turn_context> block Coddy appends to every request - is a tool result: the
// model already made its call this turn and now answers what came back.
func (r *chatRequest) answersToolResult() bool {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		m := r.Messages[i]
		if m.Role == "user" && strings.TrimSpace(stripTurnContext(userText(m.Content))) == "" {
			continue
		}
		return m.Role == "tool"
	}
	return false
}

// lastUserText is the text of the newest user message that a person wrote;
// content is a string or an array of typed parts, and only the text parts
// count. Coddy appends its runtime state to every request as one more user
// message, a <turn_context> block (internal/agent/turn_context.go), so the
// newest user message is usually not the person's: those blocks are cut out
// and a message with nothing else in it is skipped.
func (r *chatRequest) lastUserText() string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role != "user" {
			continue
		}
		if text := strings.TrimSpace(stripTurnContext(userText(r.Messages[i].Content))); text != "" {
			return text
		}
	}
	return ""
}

// userText flattens the content of one message.
func userText(content json.RawMessage) string {
	var str string
	if json.Unmarshal(content, &str) == nil {
		return str
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) == nil {
		var sb strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				sb.WriteString(p.Text)
			}
		}
		return sb.String()
	}
	return string(content)
}

const (
	turnContextOpen  = "<turn_context>"
	turnContextClose = "</turn_context>"
)

// stripTurnContext removes every <turn_context>...</turn_context> block from a
// message; an unclosed block runs to the end.
func stripTurnContext(text string) string {
	for {
		start := strings.Index(text, turnContextOpen)
		if start < 0 {
			return text
		}
		end := strings.Index(text[start:], turnContextClose)
		if end < 0 {
			return text[:start]
		}
		text = text[:start] + text[start+end+len(turnContextClose):]
	}
}

// Answer picks the reply to a prompt and counts the call.
func (s *Server) Answer(prompt string) string {
	answer, _, _ := s.pick(prompt)
	return answer
}

// pick chooses the reply and returns the ordinal of the call it counted,
// under one lock, so two completions served at once get distinct ids. tool is
// the matched rule's call, nil when the reply is text only.
func (s *Server) pick(prompt string) (answer string, tool *ToolCall, call int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	lower := strings.ToLower(prompt)
	for _, r := range s.Rules {
		if r.Match == "" || strings.Contains(lower, strings.ToLower(r.Match)) {
			return r.Answer, r.Tool, s.calls
		}
	}
	if len(s.Answers) > 0 {
		return s.Answers[(s.calls-1)%len(s.Answers)], nil, s.calls
	}
	return "You said: " + strings.TrimSpace(prompt), nil, s.calls
}

// Calls is how many completions the stub has answered.
func (s *Server) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *Server) completions(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON body"}}`, http.StatusBadRequest)
		return
	}
	answer, tool, call := s.pick(req.lastUserText())
	id := fmt.Sprintf("chatcmpl-tgfake-%d", call)
	created := time.Now().Unix()
	usage := map[string]any{"prompt_tokens": 1, "completion_tokens": len(strings.Fields(answer)), "total_tokens": 1 + len(strings.Fields(answer))}
	if tool != nil && !req.answersToolResult() {
		s.callTool(w, req.Stream, id, created, fmt.Sprintf("call_llmstub_%d", call), tool)
		return
	}

	if !req.Stream {
		writeJSON(w, map[string]any{
			"id": id, "object": "chat.completion", "created": created, "model": s.model(),
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": answer}}},
			"usage": usage,
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	send := func(v any) bool {
		raw, _ := json.Marshal(v)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": s.model(),
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		}
	}
	if !send(chunk(map[string]any{"role": "assistant", "content": ""}, nil)) {
		return
	}
	for _, piece := range splitWords(answer, s.ChunkWords) {
		if s.Delay > 0 {
			select {
			case <-time.After(s.Delay):
			case <-r.Context().Done():
				return
			}
		}
		if !send(chunk(map[string]any{"content": piece}, nil)) {
			return
		}
	}
	if !send(chunk(map[string]any{}, "stop")) {
		return
	}
	// The usage-only chunk the OpenAI stream ends with.
	if !send(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": s.model(),
		"choices": []map[string]any{}, "usage": usage}) {
		return
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// callTool answers with one tool call instead of text, in the shape of the
// request: a whole message, or a stream of one tool_calls delta and a
// tool_calls finish.
func (s *Server) callTool(w http.ResponseWriter, stream bool, id string, created int64, callID string, tool *ToolCall) {
	args := strings.TrimSpace(string(tool.Arguments))
	if args == "" {
		args = "{}"
	}
	call := map[string]any{
		"id": callID, "type": "function",
		"function": map[string]any{"name": tool.Name, "arguments": args},
	}
	usage := map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
	if !stream {
		writeJSON(w, map[string]any{
			"id": id, "object": "chat.completion", "created": created, "model": s.model(),
			"choices": []map[string]any{{"index": 0, "finish_reason": "tool_calls",
				"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []map[string]any{call}}}},
			"usage": usage,
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	send := func(v any) {
		raw, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		if flusher != nil {
			flusher.Flush()
		}
	}
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": s.model(),
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		}
	}
	call["index"] = 0
	send(chunk(map[string]any{"role": "assistant", "tool_calls": []map[string]any{call}}, nil))
	send(chunk(map[string]any{}, "tool_calls"))
	send(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": s.model(),
		"choices": []map[string]any{}, "usage": usage})
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// splitWords cuts text into chunks of n words, each keeping the whitespace
// that follows it, so the pieces concatenate back to the text.
func splitWords(text string, n int) []string {
	if n <= 0 {
		n = 1
	}
	var pieces []string
	var cur strings.Builder
	words := 0
	inSpace := false
	for _, r := range text {
		space := r == ' ' || r == '\n' || r == '\t'
		if inSpace && !space {
			words++
			if words%n == 0 {
				pieces = append(pieces, cur.String())
				cur.Reset()
			}
		}
		inSpace = space
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		pieces = append(pieces, cur.String())
	}
	return pieces
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
