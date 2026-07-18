package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sse writes one Server-Sent Event with the given type and JSON data payload.
func sse(w io.Writer, eventType string, data map[string]any) {
	data["type"] = eventType
	b, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, b)
}

// newCodexTestProvider wires a codex provider at a fake backend with a valid
// on-disk credential so no token refresh is attempted.
func newCodexTestProvider(t *testing.T, baseURL string) *codexProvider {
	t.Helper()
	dir := t.TempDir()
	path := writeCodexAuth(t, dir, codexAuthFile{
		AuthMode: codexAuthModeChatGPT,
		Tokens: codexTokens{
			AccessToken:  makeJWT(time.Now().Add(time.Hour)),
			RefreshToken: "rt",
			AccountID:    "acct-1",
		},
	})
	// A non-zero generic max token limit must still be omitted from the Codex
	// request because its Responses endpoint rejects max_output_tokens.
	p := newCodexProvider("gpt-5.6", path, baseURL, http.DefaultClient, 4096, "")
	return p
}

func TestCodexProviderStreamsTextAndToolCalls(t *testing.T) {
	var gotAuth, gotAccount, gotOriginator string
	var reqBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("chatgpt-account-id")
		gotOriginator = r.Header.Get("originator")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &reqBody)

		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.output_text.delta", map[string]any{"delta": "Hello "})
		sse(w, "response.output_text.delta", map[string]any{"delta": "world"})
		sse(w, "response.output_item.done", map[string]any{
			"item": map[string]any{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "get_weather",
				"arguments": `{"city":"Paris"}`,
			},
		})
		sse(w, "response.completed", map[string]any{
			"response": map[string]any{
				"usage": map[string]any{"input_tokens": 11, "output_tokens": 5},
			},
		})
	}))
	defer srv.Close()

	p := newCodexTestProvider(t, srv.URL)

	var streamedText strings.Builder
	var streamedCalls []ToolCall
	resp, err := p.Stream(context.Background(),
		[]Message{
			{Role: RoleSystem, Content: "be brief"},
			{Role: RoleUser, Content: "first"},
			{Role: RoleAssistant, Content: "first answer"},
			{Role: RoleUser, Content: "second"},
		},
		[]ToolDefinition{{
			Name:        "get_weather",
			Description: "Get weather",
			InputSchema: map[string]any{"type": "object"},
		}},
		func(c StreamChunk) {
			streamedText.WriteString(c.TextDelta)
			if c.ToolCall != nil {
				streamedCalls = append(streamedCalls, *c.ToolCall)
			}
		})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if resp.Content != "Hello world" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello world")
	}
	if streamedText.String() != "Hello world" {
		t.Errorf("streamed text = %q", streamedText.String())
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "get_weather" || resp.ToolCalls[0].ID != "call_1" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].InputJSON != `{"city":"Paris"}` {
		t.Errorf("tool args = %q", resp.ToolCalls[0].InputJSON)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", resp.StopReason)
	}
	if resp.InputTokens != 11 || resp.OutputTokens != 5 {
		t.Errorf("tokens = %d/%d, want 11/5", resp.InputTokens, resp.OutputTokens)
	}

	// Auth + Codex headers must be present.
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("Authorization = %q, want Bearer prefix", gotAuth)
	}
	if gotAccount != "acct-1" {
		t.Errorf("chatgpt-account-id = %q, want acct-1", gotAccount)
	}
	if gotOriginator != "codex_cli_rs" {
		t.Errorf("originator = %q, want codex_cli_rs", gotOriginator)
	}

	// The request must use the Responses schema: system -> instructions, and store=false.
	if instr, _ := reqBody["instructions"].(string); instr != "be brief" {
		t.Errorf("instructions = %v, want 'be brief'", reqBody["instructions"])
	}
	if store, ok := reqBody["store"].(bool); !ok || store {
		t.Errorf("store = %v, want false", reqBody["store"])
	}
	if _, ok := reqBody["input"]; !ok {
		t.Error("request missing input")
	}
	input, ok := reqBody["input"].([]any)
	if !ok || len(input) != 3 {
		t.Fatalf("input = %#v, want three conversation messages", reqBody["input"])
	}
	assistant, ok := input[1].(map[string]any)
	if !ok || assistant["role"] != "assistant" {
		t.Fatalf("assistant history item = %#v", input[1])
	}
	content, ok := assistant["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("assistant content = %#v", assistant["content"])
	}
	part, ok := content[0].(map[string]any)
	if !ok || part["type"] != "output_text" {
		t.Fatalf("assistant content part = %#v, want type=output_text", content[0])
	}
	if _, ok := reqBody["max_output_tokens"]; ok {
		t.Errorf("request contains max_output_tokens, which the Codex backend rejects: %v", reqBody["max_output_tokens"])
	}
}

func TestCodexProviderSurfacesStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "error", map[string]any{"message": "rate limit exceeded"})
	}))
	defer srv.Close()

	p := newCodexTestProvider(t, srv.URL)
	_, err := p.Stream(context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if err == nil || !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Fatalf("expected stream error surfaced, got %v", err)
	}
}

func TestCodexProviderDefaultsBaseURL(t *testing.T) {
	p := newCodexProvider("gpt-5.6", filepath.Join(t.TempDir(), "auth.json"), "", nil, 0, "")
	if p.baseURL != codexDefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", p.baseURL, codexDefaultBaseURL)
	}
}
