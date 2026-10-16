package agent

// This file checks that repaired tool-call histories remain paired in the
// Codex, OpenAI-compatible, and Anthropic provider payloads.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestPromptCacheToolPairingProviders(t *testing.T) {
	legacy := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "continue"},
		{Role: llm.RoleAssistant, Reasoning: "checking", ReasoningSignature: "sig-1", ToolCalls: []llm.ToolCall{
			{ID: "call-1", Name: "read", InputJSON: `{"path":"one"}`},
			{ID: "call-2", Name: "read", InputJSON: `{"path":"two"}`},
		}},
		{Role: llm.RoleTool, ToolCallID: "call-1", Content: "one"},
	}
	repaired, issues := session.RepairMissingToolResults(legacy)
	if len(issues) == 0 || len(repaired) != len(legacy)+1 {
		t.Fatalf("repair = %d messages, issues=%+v; want one inserted result", len(repaired), issues)
	}
	if repaired[len(repaired)-1].ToolCallID != "call-2" || repaired[len(repaired)-1].CreatedAt != "" {
		t.Fatalf("synthetic result = %+v", repaired[len(repaired)-1])
	}
	if len(legacy) != 4 || legacy[3].ToolCallID != "call-1" {
		t.Fatalf("repair mutated legacy history: %+v", legacy)
	}

	for _, tc := range []struct {
		name string
		kind string
	}{
		{name: "codex responses", kind: "codex"},
		{name: "openai chat", kind: "openai"},
		{name: "anthropic messages", kind: "anthropic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, captured := newWireCapture(t, tc.kind)

			projection := append([]llm.Message(nil), repaired...)
			if tc.kind == "codex" {
				projection[2].ReasoningSignature = `{"codex":true,"model":"test-model","items":[{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"checking"}],"encrypted_content":"opaque"}]}`
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			provider := newPairingWireProvider(t, tc.kind, srv.URL)
			for i := 0; i < 2; i++ {
				var err error
				if tc.kind == "codex" {
					_, err = provider.Stream(ctx, projection, nil, func(llm.StreamChunk) {})
				} else {
					_, err = provider.Complete(ctx, projection, nil)
				}
				if err != nil {
					t.Fatalf("send %d: %v", i+1, err)
				}
			}

			bodies := captured()
			if len(bodies) != 2 {
				t.Fatalf("captured %d requests, want 2", len(bodies))
			}
			if string(bodies[0]) != string(bodies[1]) {
				t.Fatalf("serialized repaired history changed between sends:\n%s\n---\n%s", bodies[0], bodies[1])
			}
			assertWirePairing(t, tc.kind, bodies[0])
		})
	}
}

// TestPromptCacheCallObserverLeavesRequestsAlone: the call observer runs after
// an attempt, on a copy of names and numbers, so what a provider sends must be
// the same whether anybody watches or not. Devin regenerates identifiers and a
// timestamp on every request, so it has no byte comparison; the structural test
// TestCallEventHoldsNoRequestReference (internal/llm) covers it.
func TestPromptCacheCallObserverLeavesRequestsAlone(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "continue"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "read", InputJSON: `{"path":"one"}`}}},
		{Role: llm.RoleTool, ToolCallID: "call-1", Content: "one"},
		{Role: llm.RoleUser, Content: "go on"},
	}
	for _, kind := range []string{"openai", "neuraldeep", "anthropic", "codex"} {
		t.Run(kind, func(t *testing.T) {
			srv, captured := newWireCapture(t, kind)
			provider := newPairingWireProvider(t, kind, srv.URL)
			send := func() {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				var err error
				if kind == "codex" {
					_, err = provider.Stream(ctx, history, nil, func(llm.StreamChunk) {})
				} else {
					_, err = provider.Complete(ctx, history, nil)
				}
				if err != nil {
					t.Fatalf("send: %v", err)
				}
			}
			var heard atomic.Int32
			remove := llm.AddCallObserver(func(ev llm.CallEvent) {
				if ev.Provider == kind {
					heard.Add(1)
				}
			})
			defer remove()
			send()
			remove()
			send()
			if heard.Load() != 1 {
				t.Fatalf("the observer heard %d attempts, want only the first", heard.Load())
			}
			bodies := captured()
			if len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) {
				t.Fatalf("the request changed with the observer detached (%d captured):\n%s\n---\n%s", len(bodies), bodies[0], bodies[len(bodies)-1])
			}
		})
	}

	t.Run("an agent turn", func(t *testing.T) {
		cwd, sessionDir := t.TempDir(), t.TempDir()
		turn := func(observed bool) []string {
			f := newRetryBudgetFixture(t, nil, 10, "tool", "answer")
			// One workspace for both runs: the system prompt names it.
			f.st.CWD, f.st.SessionDir = cwd, sessionDir
			f.st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "Earlier question."})
			f.st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "Earlier answer."})
			var ticks atomic.Int64
			f.ag.clock = func() time.Time {
				return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Add(time.Duration(ticks.Add(1)) * 2 * time.Second)
			}
			var heard atomic.Int32
			if observed {
				remove := llm.AddCallObserver(func(ev llm.CallEvent) {
					if ev.Provider == "fixture" {
						heard.Add(1)
					}
				})
				defer remove()
			}
			f.run()
			if f.err != nil || f.stop != string(acp.StopReasonEndTurn) {
				t.Fatalf("stop=%s err=%v", f.stop, f.err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if want := int32(len(f.requests)); observed && heard.Load() != want {
				t.Fatalf("the observer heard %d attempts for %d requests", heard.Load(), want)
			}
			return slices.Clone(f.requests)
		}
		watched, unwatched := turn(true), turn(false)
		if len(watched) != 2 || len(unwatched) != len(watched) {
			t.Fatalf("requests: %d watched, %d unwatched, want 2 each", len(watched), len(unwatched))
		}
		for i := range watched {
			// The trailing turn context block carries a clock and is left out.
			watchedParams, watchedMessages := retryCacheRequest(t, watched[i])
			unwatchedParams, unwatchedMessages := retryCacheRequest(t, unwatched[i])
			if !reflect.DeepEqual(watchedParams, unwatchedParams) {
				t.Fatalf("request %d changed its system, tools or parameters with the observer detached", i+1)
			}
			if len(watchedMessages) != len(unwatchedMessages) {
				t.Fatalf("request %d has %d history messages watched, %d unwatched", i+1, len(watchedMessages), len(unwatchedMessages))
			}
			for j := range watchedMessages {
				if !bytes.Equal(watchedMessages[j], unwatchedMessages[j]) {
					t.Fatalf("request %d message %d changed with the observer detached:\n%s\n---\n%s", i+1, j, watchedMessages[j], unwatchedMessages[j])
				}
			}
		}
	})
}

// newWireCapture starts a stand-in backend of the given provider type that
// records every request body and answers it with a finished reply.
func newWireCapture(t *testing.T, kind string) (*httptest.Server, func() [][]byte) {
	t.Helper()
	var mu sync.Mutex
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		mu.Lock()
		bodies = append(bodies, append([]byte(nil), body...))
		mu.Unlock()
		if kind == "codex" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"done\"}\n\n")
			_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if kind == "anthropic" {
			_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"done"}}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() [][]byte {
		mu.Lock()
		defer mu.Unlock()
		return append([][]byte(nil), bodies...)
	}
}

func newPairingWireProvider(t *testing.T, kind, baseURL string) llm.Provider {
	t.Helper()
	input := llm.ProviderInput{Name: kind, Type: kind, Model: "test-model", APIKey: "test-key", BaseURL: baseURL, RetryDisabled: true}
	if kind == "neuraldeep" {
		// The hub's address is pinned; the process override is the way a stand answers it.
		t.Setenv(llm.EnvNeuralDeepBaseURL, baseURL)
	}
	if kind == "anthropic" {
		input.ReasoningEffort = "medium"
	}
	if kind == "codex" {
		home := t.TempDir()
		authPath := filepath.Join(home, "auth.json")
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":` + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `}`))
		auth := fmt.Sprintf(`{"auth_mode":"chatgpt","tokens":{"access_token":"eyJhbGciOiJub25lIn0.%s.sig","refresh_token":"refresh","account_id":"account"}}`, payload)
		if err := os.WriteFile(authPath, []byte(auth), 0o600); err != nil {
			t.Fatal(err)
		}
		input.AuthPath = authPath
		input.NoCLILogin = true
		t.Setenv(llm.EnvCodexBaseURL, baseURL)
	}
	provider, err := llm.NewProvider(input)
	if err != nil {
		t.Fatalf("NewProvider(%s): %v", kind, err)
	}
	return provider
}

func assertWirePairing(t *testing.T, kind string, body []byte) {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("wire body is not JSON: %v", err)
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	wire := string(encoded)
	var request struct {
		Input []struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Output string `json:"output"`
		} `json:"input"`
		Messages []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	calls, results := map[string]int{}, map[string]int{}
	outputs := map[string]string{}
	answer := func(id, output string) {
		if calls[id] != 1 {
			t.Errorf("result for %q without exactly one preceding call", id)
		}
		results[id]++
		outputs[id] = output
	}
	for _, item := range request.Input {
		switch item.Type {
		case "function_call":
			calls[item.CallID]++
		case "function_call_output":
			answer(item.CallID, item.Output)
		}
	}
	for _, message := range request.Messages {
		if kind == "openai" {
			for _, call := range message.ToolCalls {
				calls[call.ID]++
			}
			if message.Role == "tool" {
				var output string
				if err := json.Unmarshal(message.Content, &output); err != nil {
					t.Fatal(err)
				}
				answer(message.ToolCallID, output)
			}
			continue
		}
		var blocks []struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(message.Content, &blocks); err != nil {
			t.Fatal(err)
		}
		for _, block := range blocks {
			switch block.Type {
			case "tool_use":
				calls[block.ID]++
			case "tool_result":
				var content string
				if err := json.Unmarshal(block.Content, &content); err != nil {
					var parts []struct {
						Text string `json:"text"`
					}
					if err := json.Unmarshal(block.Content, &parts); err != nil {
						t.Fatal(err)
					}
					for _, part := range parts {
						content += part.Text
					}
				}
				answer(block.ToolUseID, content)
			}
		}
	}
	if len(calls) != 2 || len(results) != 2 {
		t.Fatalf("calls=%v results=%v", calls, results)
	}
	for _, id := range []string{"call-1", "call-2"} {
		if calls[id] != 1 || results[id] != 1 {
			t.Errorf("%s: calls=%v results=%v", kind, calls, results)
		}
	}
	if outputs["call-1"] != "one" || !strings.Contains(outputs["call-2"], "may or may not have run") {
		t.Errorf("%s result contents changed: %v", kind, outputs)
	}
	if !strings.Contains(wire, "checking") {
		t.Errorf("%s wire lost assistant reasoning: %s", kind, wire)
	}
	if !strings.Contains(wire, "no result was recorded") {
		t.Errorf("%s wire lost repaired result: %s", kind, wire)
	}
}
