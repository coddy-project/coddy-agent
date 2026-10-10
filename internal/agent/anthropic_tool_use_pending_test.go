package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// anthropicToolAfterBlocksSSE is a Messages API answer whose tool_use block
// comes after other blocks: the index of a content block counts the text and
// thinking blocks before it, so the call is block number len(before).
func anthropicToolAfterBlocksSSE(before []string, id, name, arguments string) string {
	var out strings.Builder
	event := func(kind, data string) { fmt.Fprintf(&out, "event: %s\ndata: %s\n\n", kind, data) }
	event("message_start", `{"type":"message_start","message":{"id":"msg_fixture","type":"message","role":"assistant","model":"model","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`)
	for i, text := range before {
		event("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, i))
		event("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%q}}`, i, text))
		event("content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, i))
	}
	at := len(before)
	event("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"tool_use","id":%q,"name":%q,"input":{}}}`, at, id, name))
	event("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":%q}}`, at, arguments))
	event("content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, at))
	event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":1}}`)
	event("message_stop", `{"type":"message_stop"}`)
	return out.String()
}

func anthropicAnswerSSE(text string) string {
	var out strings.Builder
	event := func(kind, data string) { fmt.Fprintf(&out, "event: %s\ndata: %s\n\n", kind, data) }
	event("message_start", `{"type":"message_start","message":{"id":"msg_fixture","type":"message","role":"assistant","model":"model","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`)
	event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	event("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text))
	event("content_block_stop", `{"type":"content_block_stop","index":0}`)
	event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`)
	event("message_stop", `{"type":"message_stop"}`)
	return out.String()
}

// A tool call the model writes after a sentence of text is announced as a
// pending row while its arguments stream, and it has to run: the row closes
// with the call's result, and the model gets that result back. When the
// provider reader lost such a call, the row stayed pending for good and the
// turn ended on the sentence in front of it.
func TestAnthropicToolCallAfterTextRunsAndClosesItsRow(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		mu.Lock()
		bodies = append(bodies, string(body))
		n := len(bodies)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			_, _ = io.WriteString(w, anthropicToolAfterBlocksSSE([]string{"Let me look."}, "toolu_1", "glob", `{"pattern":"**/*.nothing"}`))
			return
		}
		_, _ = io.WriteString(w, anthropicAnswerSSE("Nothing matched."))
	}))
	defer srv.Close()

	stream := true
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fixture", Type: "anthropic", APIBase: srv.URL, APIKey: "fixture-only", Proxy: "none"}},
		Models:    []config.ModelEntry{{Model: "fixture/model", MaxTokens: 100, MaxContextTokens: 128000, Stream: &stream}},
		Agent:     config.Agent{Model: "fixture/model"},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	state := &session.State{ID: "sess_anthropic_tool_after_text", CWD: t.TempDir(), SessionDir: t.TempDir(), Mode: session.ModeAgent}
	sender := &progressSender{}
	ag := NewAgent(cfg, state, sender, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stop, err := ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: "look for files"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stop != string(acp.StopReasonEndTurn) {
		t.Errorf("stop reason = %q, want %q", stop, acp.StopReasonEndTurn)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Errorf("provider requests = %d, want 2: the call runs and its result goes back", len(bodies))
	} else if !strings.Contains(bodies[1], `"tool_use_id":"toolu_1"`) {
		t.Errorf("second request carries no result for toolu_1: %s", bodies[1])
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	announced := false
	last := ""
	for _, u := range sender.updates {
		switch u := u.(type) {
		case acp.ToolCallUpdate:
			if u.ToolCallID == "toolu_1" {
				announced = true
			}
		case acp.ToolCallStatusUpdate:
			if u.ToolCallID == "toolu_1" {
				last = u.Status
			}
		}
	}
	if !announced {
		t.Fatal("the call was never announced as a row")
	}
	if last != "completed" {
		t.Errorf("last status of the row = %q, want %q: the row stays open", last, "completed")
	}
}
