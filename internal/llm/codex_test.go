package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sse writes one Server-Sent Event with the given type and JSON data payload.
func sse(w io.Writer, eventType string, data map[string]any) {
	data["type"] = eventType
	b, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, b)
}

// sseCompleted ends a scripted Codex response the way the backend does, with
// the terminal event a stream needs to count as a whole answer.
func sseCompleted(w io.Writer) {
	sse(w, "response.completed", map[string]any{"response": map[string]any{"status": "completed"}})
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

// TestCodexProviderReasoningRequest pins the reasoning contract of the Codex
// backend: it rejects the coddy level "minimal" (its models accept only
// none/low/medium/high/xhigh) and streams reasoning summaries only when the
// request asks for them.
func TestCodexProviderReasoningRequest(t *testing.T) {
	cases := []struct {
		level      string
		wantEffort string
	}{
		{"minimal", "none"},
		{"low", "low"},
		{"high", "high"},
	}
	for _, tc := range cases {
		t.Run(tc.level, func(t *testing.T) {
			var reqBody map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &reqBody)
				w.Header().Set("Content-Type", "text/event-stream")
				sse(w, "response.reasoning_summary_text.delta", map[string]any{"delta": "**Checking**"})
				sse(w, "response.output_text.delta", map[string]any{"delta": "done"})
				sseCompleted(w)
			}))
			defer srv.Close()

			p := newCodexTestProvider(t, srv.URL)
			p.reasoningEffort = tc.level

			var reasoning strings.Builder
			resp, err := p.Stream(context.Background(),
				[]Message{{Role: RoleUser, Content: "hi"}}, nil,
				func(c StreamChunk) { reasoning.WriteString(c.ReasoningDelta) })
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}

			reasoningParam, ok := reqBody["reasoning"].(map[string]any)
			if !ok {
				t.Fatalf("request has no reasoning object: %#v", reqBody["reasoning"])
			}
			if reasoningParam["effort"] != tc.wantEffort {
				t.Errorf("reasoning.effort = %v, want %q", reasoningParam["effort"], tc.wantEffort)
			}
			if reasoningParam["summary"] != "auto" {
				t.Errorf("reasoning.summary = %v, want auto (no summary, no thinking in the UI)", reasoningParam["summary"])
			}
			if reasoning.String() != "**Checking**" || resp.Reasoning != "**Checking**" {
				t.Errorf("reasoning delta = %q / response reasoning = %q", reasoning.String(), resp.Reasoning)
			}
		})
	}
}

// TestCodexProviderReplaysEncryptedReasoning follows the Codex CLI flow: ask
// for encrypted reasoning, keep the returned reasoning items with the assistant
// turn, and hand them back verbatim on the next request so the model resumes
// its own chain of thought across tool calls.
func TestCodexProviderReplaysEncryptedReasoning(t *testing.T) {
	const encrypted = "gAAAAAB-test-encrypted-content"
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &reqBody)
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.reasoning_summary_text.delta", map[string]any{"delta": "**Planning**"})
		sse(w, "response.output_item.done", map[string]any{
			"item": map[string]any{
				"type":              "reasoning",
				"id":                "rs_1",
				"summary":           []map[string]any{{"type": "summary_text", "text": "**Planning**"}},
				"content":           []any{},
				"encrypted_content": encrypted,
			},
		})
		sse(w, "response.output_item.done", map[string]any{
			"item": map[string]any{
				"type": "function_call", "call_id": "call_1", "name": "get_weather", "arguments": `{}`,
			},
		})
		sseCompleted(w)
	}))
	defer srv.Close()

	p := newCodexTestProvider(t, srv.URL)
	p.reasoningEffort = "medium"
	tools := []ToolDefinition{{Name: "get_weather", Description: "Get weather", InputSchema: map[string]any{"type": "object"}}}

	resp, err := p.Stream(context.Background(),
		[]Message{{Role: RoleUser, Content: "weather?"}}, tools, func(StreamChunk) {})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	include, _ := reqBody["include"].([]any)
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %v, want [reasoning.encrypted_content]", reqBody["include"])
	}
	if resp.ReasoningSignature == "" {
		t.Fatal("response carries no reasoning signature, so the items cannot be replayed")
	}

	// Second turn: the stored signature travels with the assistant message.
	_, err = p.Stream(context.Background(), []Message{
		{Role: RoleUser, Content: "weather?"},
		{
			Role:               RoleAssistant,
			Reasoning:          resp.Reasoning,
			ReasoningSignature: resp.ReasoningSignature,
			ToolCalls:          resp.ToolCalls,
		},
		{Role: RoleTool, ToolCallID: "call_1", Content: "sunny"},
	}, tools, func(StreamChunk) {})
	if err != nil {
		t.Fatalf("Stream (second turn): %v", err)
	}
	input, _ := reqBody["input"].([]any)
	reasoningIdx, callIdx := -1, -1
	for i, raw := range input {
		item, _ := raw.(map[string]any)
		switch item["type"] {
		case "reasoning":
			reasoningIdx = i
			if item["encrypted_content"] != encrypted {
				t.Errorf("replayed reasoning lost its encrypted content: %v", item["encrypted_content"])
			}
			if item["id"] != "rs_1" {
				t.Errorf("replayed reasoning id = %v, want rs_1", item["id"])
			}
		case "function_call":
			callIdx = i
		}
	}
	if reasoningIdx < 0 {
		t.Fatalf("no reasoning item replayed; input = %#v", input)
	}
	if callIdx >= 0 && reasoningIdx > callIdx {
		t.Errorf("reasoning item must precede the function call it produced (got %d > %d)", reasoningIdx, callIdx)
	}

	// A different model must not receive another model's encrypted reasoning.
	other := newCodexTestProvider(t, srv.URL)
	other.model = "gpt-5.4"
	other.reasoningEffort = "medium"
	if _, err := other.Stream(context.Background(), []Message{
		{Role: RoleUser, Content: "weather?"},
		{Role: RoleAssistant, ReasoningSignature: resp.ReasoningSignature, ToolCalls: resp.ToolCalls},
		{Role: RoleTool, ToolCallID: "call_1", Content: "sunny"},
	}, tools, func(StreamChunk) {}); err != nil {
		t.Fatalf("Stream (other model): %v", err)
	}
	for _, raw := range reqBody["input"].([]any) {
		if item, _ := raw.(map[string]any); item["type"] == "reasoning" {
			t.Fatalf("reasoning from another model was replayed: %#v", item)
		}
	}
}

// TestCodexProviderOmitsReasoningWhenUnset keeps non-reasoning turns clean.
func TestCodexProviderOmitsReasoningWhenUnset(t *testing.T) {
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &reqBody)
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.output_text.delta", map[string]any{"delta": "ok"})
		sseCompleted(w)
	}))
	defer srv.Close()

	p := newCodexTestProvider(t, srv.URL)
	if _, err := p.Stream(context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if _, ok := reqBody["reasoning"]; ok {
		t.Errorf("request carries reasoning without a configured level: %v", reqBody["reasoning"])
	}
}

// TestCodexProviderSurfacesHTTPErrorDetail covers the Codex error envelope:
// it reports failures as {"detail": ...}, which the OpenAI SDK error message
// drops (it only reads "error"), leaving a bare "400 Bad Request".
func TestCodexProviderSurfacesHTTPErrorDetail(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "detail envelope",
			body: `{"detail":"The 'gpt-5.1-codex' model is not supported when using Codex with a ChatGPT account."}`,
			want: "not supported when using Codex with a ChatGPT account",
		},
		{
			name: "error envelope",
			body: `{"error":{"message":"Unsupported value: 'minimal' is not supported","param":"reasoning.effort"}}`,
			want: "Unsupported value: 'minimal' is not supported",
		},
		{
			name: "plain text body",
			body: `upstream rejected the request`,
			want: "upstream rejected the request",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			p := newCodexTestProvider(t, srv.URL)
			_, err := p.Stream(context.Background(),
				[]Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
			if err == nil {
				t.Fatal("expected an error for HTTP 400")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not explain the failure (want %q)", err.Error(), tc.want)
			}
		})
	}
}

func TestCodexProviderDefaultsBaseURL(t *testing.T) {
	p := newCodexProvider("gpt-5.6", filepath.Join(t.TempDir(), "auth.json"), "", nil, 0, "")
	if p.baseURL != codexDefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", p.baseURL, codexDefaultBaseURL)
	}
}

// TestCodexReasoningSummaryPartsKeepTheirBoundary pins the separator between
// reasoning summary parts. The Responses API sends one part per headed block and
// each part opens with its own bold title, so concatenating the deltas raw runs
// the titles into a single paragraph where the markdown of one swallows the
// next: "Reviewing findings**Checking status**Reading the report". A blank line
// on the part boundary keeps the blocks the model actually drew.
func TestCodexReasoningSummaryPartsKeepTheirBoundary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.reasoning_summary_part.added", map[string]any{"summary_index": 0})
		sse(w, "response.reasoning_summary_text.delta", map[string]any{"delta": "**Reviewing findings**"})
		sse(w, "response.reasoning_summary_part.added", map[string]any{"summary_index": 1})
		sse(w, "response.reasoning_summary_text.delta", map[string]any{"delta": "**Checking status**"})
		sse(w, "response.output_text.delta", map[string]any{"delta": "done"})
		sseCompleted(w)
	}))
	defer srv.Close()

	p := newCodexTestProvider(t, srv.URL)
	var streamed strings.Builder
	resp, err := p.Stream(context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}}, nil,
		func(c StreamChunk) { streamed.WriteString(c.ReasoningDelta) })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	const want = "**Reviewing findings**\n\n**Checking status**"
	if resp.Reasoning != want {
		t.Errorf("response reasoning = %q, want %q", resp.Reasoning, want)
	}
	// The live stream has to carry the same break, or the body redraws when the
	// turn is persisted.
	if streamed.String() != want {
		t.Errorf("streamed reasoning = %q, want %q", streamed.String(), want)
	}
}

// TestCodexReasoningLeadingPartAddsNoBlankLine keeps the first part flush: a
// separator before any text would open the reasoning body with an empty line.
func TestCodexReasoningLeadingPartAddsNoBlankLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.reasoning_summary_part.added", map[string]any{"summary_index": 0})
		sse(w, "response.reasoning_summary_text.delta", map[string]any{"delta": "**Planning**"})
		sseCompleted(w)
	}))
	defer srv.Close()

	p := newCodexTestProvider(t, srv.URL)
	resp, err := p.Stream(context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Reasoning != "**Planning**" {
		t.Errorf("response reasoning = %q, want %q", resp.Reasoning, "**Planning**")
	}
}

// TestCodexAnnouncesToolCallWhenItsNameIsKnown covers the silence the operator
// sees while the model composes a tool call. The name arrives with
// response.output_item.added and the arguments stream after it, which can take
// seconds; announcing only on .done leaves the transcript with nothing to show
// and a Stop button that looks like a hang.
func TestCodexAnnouncesToolCallWhenItsNameIsKnown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.output_item.added", map[string]any{
			"output_index": 0,
			"item": map[string]any{
				"type": "function_call", "call_id": "call_1", "name": "run_command", "arguments": "",
			},
		})
		sse(w, "response.function_call_arguments.delta", map[string]any{"delta": `{"command":"ls"}`})
		sse(w, "response.output_item.done", map[string]any{
			"item": map[string]any{
				"type": "function_call", "call_id": "call_1", "name": "run_command", "arguments": `{"command":"ls"}`,
			},
		})
		sseCompleted(w)
	}))
	defer srv.Close()

	p := newCodexTestProvider(t, srv.URL)
	var announced []ToolCall
	resp, err := p.Stream(context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}}, nil,
		func(c StreamChunk) {
			if c.ToolCallNamed != nil {
				announced = append(announced, *c.ToolCallNamed)
			}
			if c.ToolCall != nil {
				announced = append(announced, *c.ToolCall)
			}
		})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if len(announced) < 2 {
		t.Fatalf("announced %d tool calls, want the name first and the complete call after: %+v", len(announced), announced)
	}
	first := announced[0]
	if first.Name != "run_command" || first.ID != "call_1" {
		t.Errorf("first announcement = %+v, want the name and id of the call", first)
	}
	if first.InputJSON != "" {
		t.Errorf("first announcement carries arguments %q; they have not streamed yet", first.InputJSON)
	}
	// The call is executed from the response, so announcing early must not
	// duplicate it there.
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("response tool calls = %d, want 1: %+v", len(resp.ToolCalls), resp.ToolCalls)
	}
	if resp.ToolCalls[0].InputJSON != `{"command":"ls"}` {
		t.Errorf("response tool call arguments = %q", resp.ToolCalls[0].InputJSON)
	}
}

// TestCodexStreamTerminalStates pins how a Codex Responses stream ends: only a
// terminal event makes an answer, response.incomplete names why it stopped
// short, and a stream cut or failed after deltas reached the caller is never
// retried into a second copy of them.
func TestCodexStreamTerminalStates(t *testing.T) {
	delta := func(w io.Writer, text string) {
		sse(w, "response.output_text.delta", map[string]any{"delta": text})
	}
	incomplete := func(w io.Writer, reason string) {
		details := map[string]any{}
		if reason != "" {
			details["reason"] = reason
		}
		sse(w, "response.incomplete", map[string]any{"response": map[string]any{
			"status": "incomplete", "incomplete_details": details,
			"usage": map[string]any{"input_tokens": 9, "output_tokens": 4},
		}})
	}
	for name, tc := range map[string]struct {
		script        func(w io.Writer)
		wantContent   string
		wantResponse  bool
		wantStop      string
		wantErr       string
		wantTruncated bool
		wantRetryable bool
	}{
		"clean EOF before any event": {
			script:        func(io.Writer) {},
			wantErr:       "stream truncated",
			wantTruncated: true,
			wantRetryable: true,
		},
		"clean EOF after text": {
			script:        func(w io.Writer) { delta(w, "Hello "); delta(w, "wor") },
			wantResponse:  true,
			wantContent:   "Hello wor",
			wantErr:       "stream truncated",
			wantTruncated: true,
		},
		"clean EOF after a tool call drops the call": {
			script: func(w io.Writer) {
				sse(w, "response.output_item.done", map[string]any{"item": map[string]any{
					"type": "function_call", "call_id": "call_1", "name": "run_command", "arguments": `{"command":"rm -rf build"}`,
				}})
			},
			wantErr:       "stream truncated",
			wantTruncated: true,
		},
		"incomplete at the output cap": {
			script:       func(w io.Writer) { delta(w, "Hello"); incomplete(w, "max_output_tokens") },
			wantResponse: true,
			wantContent:  "Hello",
			wantStop:     "max_tokens",
		},
		"incomplete on the content filter": {
			script:       func(w io.Writer) { delta(w, "Hel"); incomplete(w, "content_filter") },
			wantResponse: true,
			wantContent:  "Hel",
			wantStop:     "content_filter",
		},
		"incomplete for a reason nothing maps": {
			script:       func(w io.Writer) { delta(w, "Hel"); incomplete(w, "server_overloaded") },
			wantResponse: true,
			wantContent:  "Hel",
			wantErr:      "response incomplete: server_overloaded",
		},
		"incomplete after text for a reason that reads retryable": {
			script:       func(w io.Writer) { delta(w, "Hel"); incomplete(w, "upstream 503 Service Unavailable") },
			wantResponse: true,
			wantContent:  "Hel",
			wantErr:      "response incomplete: upstream 503 Service Unavailable",
		},
		"incomplete without a reason": {
			script:  func(w io.Writer) { incomplete(w, "") },
			wantErr: "response incomplete: no reason given",
		},
		"failed before any delta": {
			script: func(w io.Writer) {
				sse(w, "response.failed", map[string]any{"response": map[string]any{
					"status": "failed", "error": map[string]any{"code": "server_error", "message": "The model crashed"},
				}})
			},
			wantErr: "codex stream: server_error: The model crashed",
		},
		"failed after deltas is not replayed": {
			script: func(w io.Writer) {
				delta(w, "Hello")
				sse(w, "response.failed", map[string]any{"response": map[string]any{
					"status": "failed", "error": map[string]any{"code": "server_error", "message": "upstream said 503 Service Unavailable"},
				}})
			},
			wantErr: "upstream said 503 Service Unavailable",
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				tc.script(w)
			}))
			defer srv.Close()

			var streamed []StreamChunk
			resp, err := newCodexTestProvider(t, srv.URL).Stream(context.Background(),
				[]Message{{Role: RoleUser, Content: "hi"}}, nil,
				func(c StreamChunk) { streamed = append(streamed, c) })

			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("Stream: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("Stream error = %v, want %q", err, tc.wantErr)
			}
			if err != nil {
				if got := IsStreamTruncated(err); got != tc.wantTruncated {
					t.Errorf("IsStreamTruncated = %v, want %v", got, tc.wantTruncated)
				}
				if got := isRetryableLLMError(err); got != tc.wantRetryable {
					t.Errorf("retryable = %v, want %v (%d chunks already streamed)", got, tc.wantRetryable, len(streamed))
				}
			}
			if (resp != nil) != tc.wantResponse {
				t.Fatalf("response = %+v, want one: %v", resp, tc.wantResponse)
			}
			if resp == nil {
				return
			}
			if resp.Content != tc.wantContent || resp.StopReason != tc.wantStop {
				t.Errorf("response content %q stop %q, want %q and %q", resp.Content, resp.StopReason, tc.wantContent, tc.wantStop)
			}
			if len(resp.ToolCalls) != 0 {
				t.Errorf("a response that did not finish carries tool calls: %+v", resp.ToolCalls)
			}
			if tc.wantStop != "" && (resp.InputTokens != 9 || resp.OutputTokens != 4) {
				t.Errorf("usage of the incomplete response = %d/%d, want 9/4", resp.InputTokens, resp.OutputTokens)
			}
		})
	}
}

// TestCodexStreamCancelKeepsPartialText covers a Stop mid-answer: the text
// already streamed comes back next to the cancellation, which is not
// retried.
func TestCodexStreamCancelKeepsPartialText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.output_text.delta", map[string]any{"delta": "Hello"})
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := newCodexTestProvider(t, srv.URL).Stream(ctx,
		[]Message{{Role: RoleUser, Content: "hi"}}, nil,
		func(c StreamChunk) {
			if c.TextDelta != "" {
				cancel()
			}
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream error = %v, want context.Canceled", err)
	}
	if isRetryableLLMError(err) {
		t.Error("a cancelled stream must not be retried")
	}
	if resp == nil || resp.Content != "Hello" {
		t.Fatalf("partial response = %+v, want the streamed text", resp)
	}
}

// TestCodexCompleteRetriesACutStream covers the callers that take the answer
// whole (compaction, a JSON direct completion, prompt enhancement): nothing
// was shown to them, so a stream cut midway is retried instead of failing.
func TestCodexCompleteRetriesACutStream(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.output_text.delta", map[string]any{"delta": "Hello"})
		if calls.Add(1) == 1 {
			return
		}
		sse(w, "response.output_text.delta", map[string]any{"delta": " world"})
		sseCompleted(w)
	}))
	defer srv.Close()

	provider := applyResilientWrap(newCodexTestProvider(t, srv.URL), ProviderInput{
		RetryMax: 1, RetryBase: time.Millisecond, RetryMaxDelay: time.Millisecond,
	})
	resp, err := provider.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v (after %d requests)", err, calls.Load())
	}
	if resp.Content != "Hello world" || calls.Load() != 2 {
		t.Fatalf("content %q after %d requests, want the retried whole answer after 2", resp.Content, calls.Load())
	}
}
