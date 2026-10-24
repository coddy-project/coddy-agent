package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// A compaction pass waits for the whole summary before it returns. The
// Anthropic client refuses such a one-shot request before sending it once
// max_tokens promises more than ten minutes of generation (above 21333), so a
// Claude model configured with a large max_tokens could not compact at all.
// This goes through CompactSession and the real provider factory, against a
// stub of the Messages API.
func TestCompactSessionOnAnthropicModelWithLargeMaxTokens(t *testing.T) {
	const summary = "Claude summary of the session."
	var (
		mu     sync.Mutex
		bodies []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		if body["stream"] != true {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":%q}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":5}}`, summary)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range []string{
			`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, summary),
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}`,
			`{"type":"message_stop"}`,
		} {
			var head struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal([]byte(e), &head)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", head.Type, e)
		}
	}))
	defer srv.Close()

	const model = "claude/claude-sonnet-4-5"
	keep := 1
	st := seededCompactState(t, 3)
	ag := NewAgent(&config.Config{
		Providers:  []config.ProviderConfig{{Name: "claude", Type: "anthropic", APIKey: "key", APIBase: srv.URL}},
		Models:     []config.ModelEntry{{Model: model, MaxTokens: 40000}},
		Agent:      config.Agent{Model: model},
		Compaction: config.Compaction{KeepRecentTurns: &keep},
	}, st, resumePermissionSender{}, nil)

	res, err := ag.CompactSession(context.Background(), CompactOptions{Force: true})
	if err != nil {
		t.Fatalf("compaction on a Claude model with max_tokens 40000: %v", err)
	}
	if res.Model != model || !strings.Contains(res.Summary, summary) {
		t.Fatalf("result = model %q, summary %q; want the Claude summary", res.Model, res.Summary)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) == 0 {
		t.Fatal("the stub received no request")
	}
	if got, _ := bodies[0]["max_tokens"].(float64); int(got) != 40000 {
		t.Errorf("max_tokens = %v, want the model's 40000", bodies[0]["max_tokens"])
	}
}
