package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// retryBudgetFixture runs the real Anthropic adapter, SDK and resilient wrapper.
// The HTTP request count is deliberately independent of Agent's call count.
type retryBudgetFixture struct {
	ag       *Agent
	st       *session.State
	mu       sync.Mutex
	requests []string
	log      bytes.Buffer
	stop     string
	err      error
}

func newRetryBudgetFixture(t *testing.T, retryMax *int, maxTurns int, replies ...string) *retryBudgetFixture {
	t.Helper()
	f := &retryBudgetFixture{}
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		f.mu.Lock()
		index := len(f.requests)
		f.requests = append(f.requests, string(body))
		f.mu.Unlock()
		reply := replies[min(index, len(replies)-1)]
		if reply == "error" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"temporary failure"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if reply == "silent" {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		_, _ = io.WriteString(w, retryBudgetSSE(reply, index))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	stream, guard, ms := true, false, 500
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fixture", Type: "anthropic", APIBase: srv.URL, APIKey: "fixture-only", Proxy: "none"}},
		Models:    []config.ModelEntry{{Model: "fixture/model", MaxTokens: 100, Stream: &stream}},
		Agent:     config.Agent{Model: "fixture/model", MaxTurns: maxTurns, LLMRetryMax: retryMax, LLMRetryBaseMS: 1, LLMFirstTokenTimeoutMS: &ms, LoopGuard: &guard},
	}
	f.st = &session.State{ID: "sess_retry_budget", CWD: t.TempDir(), SessionDir: t.TempDir(), Mode: session.ModeAgent}
	f.ag = NewAgent(cfg, f.st, &loopGuardSender{}, slog.New(slog.NewJSONHandler(&f.log, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return f
}

func retryBudgetSSE(reply string, index int) string {
	var out strings.Builder
	event := func(kind, data string) { fmt.Fprintf(&out, "event: %s\ndata: %s\n\n", kind, data) }
	event("message_start", `{"type":"message_start","message":{"id":"msg_fixture","type":"message","role":"assistant","model":"model","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`)
	stop := "end_turn"
	switch reply {
	case "reasoning", "max_tokens":
		event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`)
		event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"thinking without an answer"}}`)
		event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"fixture-signature"}}`)
		event("content_block_stop", `{"type":"content_block_stop","index":0}`)
		if reply == "max_tokens" {
			stop = "max_tokens"
		}
	case "tool":
		stop = "tool_use"
		event("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_%d","name":"coddy_todo_plan_read","input":{}}}`, index))
		event("content_block_stop", `{"type":"content_block_stop","index":0}`)
	}
	if reply != "tool" {
		event("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`)
		if reply == "answer" {
			event("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"The answer."}}`)
		}
		event("content_block_stop", `{"type":"content_block_stop","index":1}`)
	}
	event("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":1}}`, stop))
	event("message_stop", `{"type":"message_stop"}`)
	return out.String()
}

func (f *retryBudgetFixture) run() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f.stop, f.err = f.ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: "Answer once."}})
}

func (f *retryBudgetFixture) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *retryBudgetFixture) noEmptyAssistantText() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, raw := range f.requests {
		var req struct {
			Messages []struct {
				Role    string
				Content []struct {
					Type string
					Text string
				}
			}
		}
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			return err
		}
		for _, msg := range req.Messages {
			for _, block := range msg.Content {
				if msg.Role == "assistant" && block.Type == "text" && strings.TrimSpace(block.Text) == "" {
					return fmt.Errorf("request %d replays an empty assistant text block", i+1)
				}
			}
		}
	}
	return nil
}

func TestReActRetryBudget(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		retries, turns, requests int
		replies                  []string
		stop                     acp.StopReason
		wantErr                  bool
	}{
		{"zero empty", 0, 10, 1, []string{"empty"}, acp.StopReasonRefused, true},
		{"zero reasoning", 0, 10, 1, []string{"reasoning"}, acp.StopReasonRefused, true},
		{"zero silent", 0, 10, 1, []string{"silent"}, acp.StopReasonRefused, true},
		{"zero single turn", 0, 1, 1, []string{"reasoning"}, acp.StopReasonRefused, true},
		{"zero single turn silence", 0, 1, 1, []string{"silent"}, acp.StopReasonRefused, true},
		{"single turn silence", 3, 1, 1, []string{"silent", "answer"}, acp.StopReasonMaxTurns, false},
		{"single turn empty", 3, 1, 1, []string{"empty", "answer"}, acp.StopReasonMaxTurns, false},
		{"token limit is terminal", 3, 10, 1, []string{"max_tokens", "answer"}, acp.StopReasonMaxTokens, false},
		{"one recovery", 1, 10, 2, []string{"reasoning"}, acp.StopReasonRefused, true},
		{"transport then empty share allowance", 1, 10, 2, []string{"error", "reasoning", "answer"}, acp.StopReasonRefused, true},
		{"empty then transport share allowance", 1, 10, 2, []string{"reasoning", "error", "answer"}, acp.StopReasonRefused, true},
		{"mixed recovery succeeds", 2, 10, 3, []string{"error", "reasoning", "answer"}, acp.StopReasonEndTurn, false},
		{"silence recovers", 1, 10, 2, []string{"silent", "answer"}, acp.StopReasonEndTurn, false},
		{"tool progress resets budget", 1, 10, 4, []string{"reasoning", "tool", "reasoning", "answer"}, acp.StopReasonEndTurn, false},
		{"strategy cap still applies", 20, 10, 4, []string{"reasoning"}, acp.StopReasonRefused, true},
		{"silence and empty share allowance", 1, 10, 2, []string{"silent", "reasoning", "answer"}, acp.StopReasonRefused, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRetryBudgetFixture(t, &tc.retries, tc.turns, tc.replies...)
			f.run()
			if got := f.requestCount(); got != tc.requests {
				t.Errorf("upstream requests = %d, want %d", got, tc.requests)
			}
			if f.stop != string(tc.stop) || (f.err != nil) != tc.wantErr {
				t.Errorf("stop = %s, err = %v; want %s, error=%v", f.stop, f.err, tc.stop, tc.wantErr)
			}
		})
	}
}

func TestReActRetryBudgetDefault(t *testing.T) {
	f := newRetryBudgetFixture(t, nil, 10, "reasoning", "reasoning", "reasoning", "answer")
	f.run()
	if f.requestCount() != 4 || f.err != nil || f.stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("requests=%d stop=%s err=%v", f.requestCount(), f.stop, f.err)
	}
	if err := f.noEmptyAssistantText(); err != nil {
		t.Error(err)
	}
	var signed int
	for _, m := range f.st.GetMessages() {
		if m.Role == llm.RoleAssistant && m.ReasoningSignature == "fixture-signature" {
			signed++
		}
	}
	if signed != 3 {
		t.Errorf("signed reasoning messages = %d, want 3", signed)
	}
}

func TestReActRetryBudgetDiagnostics(t *testing.T) {
	two := 2
	f := newRetryBudgetFixture(t, &two, 10, "error", "reasoning", "answer")
	f.run()
	if f.err != nil {
		t.Fatal(f.err)
	}
	type callLog struct {
		Message          string `json:"msg"`
		Reason           string `json:"call_reason"`
		Attempts         int    `json:"provider_attempts"`
		TransportRetries int    `json:"transport_retries"`
		Remaining        int    `json:"retries_remaining"`
	}
	var calls []callLog
	decoder := json.NewDecoder(&f.log)
	for {
		var row callLog
		err := decoder.Decode(&row)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if row.Message == "llm call finished" {
			calls = append(calls, row)
		}
	}
	if len(calls) != 2 {
		t.Fatalf("call logs=%+v", calls)
	}
	if calls[0].Reason != "step" || calls[0].Attempts != 2 || calls[0].TransportRetries != 1 || calls[0].Remaining != 1 {
		t.Errorf("first call=%+v", calls[0])
	}
	if calls[1].Reason != "empty_reissue" || calls[1].Attempts != 1 || calls[1].TransportRetries != 0 || calls[1].Remaining != 0 {
		t.Errorf("recovery call=%+v", calls[1])
	}
	if got := calls[0].Attempts + calls[1].Attempts; got != f.requestCount() {
		t.Errorf("logged attempts=%d HTTP requests=%d", got, f.requestCount())
	}
}

type retryBudgetStreamFunc func(context.Context, func(llm.StreamChunk)) (*llm.Response, error)

func (f retryBudgetStreamFunc) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("unexpected non-streaming call")
}

func (f retryBudgetStreamFunc) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, emit func(llm.StreamChunk)) (*llm.Response, error) {
	return f(ctx, emit)
}

func TestReActRetryBudgetCancellationAndPartialOutput(t *testing.T) {
	for _, mode := range []string{"parent cancellation", "user stop", "first token user stop", "partial text", "partial reasoning"} {
		t.Run(mode, func(t *testing.T) {
			f := newRetryBudgetFixture(t, nil, 10, "answer")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			p := retryBudgetStreamFunc(func(callCtx context.Context, emit func(llm.StreamChunk)) (*llm.Response, error) {
				calls++
				switch mode {
				case "parent cancellation":
					cancel()
					return &llm.Response{}, nil
				case "user stop":
					f.st.SetUserCancelledTurn()
					return &llm.Response{}, nil
				}
				// Output delivered before cancellation still forbids a replay
				// when the provider returns no response object.
				switch mode {
				case "first token user stop":
					<-callCtx.Done()
					f.st.SetUserCancelledTurn()
				case "partial text":
					emit(llm.StreamChunk{TextDelta: "partial answer"})
					cancel()
				case "partial reasoning":
					emit(llm.StreamChunk{ReasoningDelta: "partial reasoning"})
					cancel()
				}
				return nil, callCtx.Err()
			})
			f.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
				return llm.WrapResilient(p, llm.ResilientOptions{RetryMax: 3}), nil
			}
			stop, err := f.ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: "stop"}})
			wantStop := acp.StopReasonCancelled
			// An interrupted stream without an explicit user stop keeps its
			// existing refused/error outcome. Neither outcome may replay it.
			partial := strings.HasPrefix(mode, "partial ")
			if partial {
				wantStop = acp.StopReasonRefused
			}
			if calls != 1 || stop != string(wantStop) || (partial && err == nil) {
				t.Fatalf("calls=%d stop=%s err=%v", calls, stop, err)
			}
		})
	}
}

// TestProviderRecoveryDelay: the pause before a provider recovery climbs from
// five retry bases, takes a longer pause the provider named, and stops at the
// cap.
func TestProviderRecoveryDelay(t *testing.T) {
	plain := errors.New("server error 500")
	if got := providerRecoveryDelay(0, 1, plain); got != 5*time.Second {
		t.Fatalf("first pause with the default base = %s, want 5s", got)
	}
	if got := providerRecoveryDelay(1000, 2, plain); got != 20*time.Second {
		t.Fatalf("second pause = %s, want 20s", got)
	}
	named := &llm.QuotaResetError{Delay: 45 * time.Second, Cause: plain}
	if got := providerRecoveryDelay(1000, 1, named); got != 45*time.Second {
		t.Fatalf("pause with a named 45s = %s, want 45s", got)
	}
	if got := providerRecoveryDelay(60000, 2, plain); got != maxProviderRecoveryDelay {
		t.Fatalf("pause past the cap = %s, want %s", got, maxProviderRecoveryDelay)
	}
}

// TestStopNoticeNamesTheLimitAndTheWayOn: a top-level turn stopped by its
// step limit is told to continue with a message; a subagent's transcript
// takes none, so its notice points at the limit or a new run instead.
func TestStopNoticeNamesTheLimitAndTheWayOn(t *testing.T) {
	top := &session.State{ID: "sess_top"}
	(&Agent{cfg: &config.Config{}, state: top}).noteStopReason("max_turns", nil, 40)
	if got := top.TakeTurnStopNotice(); !strings.Contains(got, "40 steps") || !strings.Contains(got, "agent.max_turns") || !strings.Contains(got, "send a message") {
		t.Fatalf("top-level notice = %q", got)
	}
	child := &session.State{ID: "sess_child"}
	(&Agent{cfg: &config.Config{Subagents: config.Subagents{MaxTurns: 8}}, state: child, subagent: &session.SubagentMeta{Name: "explore"}}).noteStopReason("max_turns", nil, 8)
	got := child.TakeTurnStopNotice()
	if !strings.Contains(got, "subagents.max_turns") || strings.Contains(got, "send a message") {
		t.Fatalf("subagent notice = %q", got)
	}
	failed := &session.State{ID: "sess_failed"}
	(&Agent{cfg: &config.Config{}, state: failed}).noteStopReason("max_turns", errors.New("boom"), 40)
	if got := failed.TakeTurnStopNotice(); got != "" {
		t.Fatalf("a failed turn left a stop notice: %q", got)
	}
}
