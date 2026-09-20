package agent

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// Compare the actual HTTP bodies, not just the Agent's projection: retry
// bookkeeping must not change the prefix the provider uses for its KV cache.
func TestReActRetryBudgetPreservesPromptCachePrefix(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replies []string
		added   []int // Messages added to the first request's history.
	}{
		{"transport", []string{"error", "answer"}, []int{0, 0}},
		{"empty", []string{"empty", "answer"}, []int{0, 0}},
		{"reasoning", []string{"reasoning", "answer"}, []int{0, 0}},
		{"first token timeout", []string{"silent", "answer"}, []int{0, 0}},
		{"transport then recovery", []string{"error", "reasoning", "answer"}, []int{0, 0, 0}},
		{"recovery then transport", []string{"reasoning", "error", "answer"}, []int{0, 0, 0}},
		{"nudges append only", []string{"reasoning", "reasoning", "reasoning", "answer"}, []int{0, 0, 1, 2}},
		{"tool progress", []string{"reasoning", "tool", "reasoning", "answer"}, []int{0, 0, 2, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRetryBudgetFixture(t, nil, 10, tc.replies...)
			f.st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "Earlier question."})
			f.st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "Earlier answer."})
			var ticks atomic.Int64
			f.ag.clock = func() time.Time {
				return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Add(time.Duration(ticks.Add(1)) * 2 * time.Second)
			}
			f.run()
			if f.err != nil || f.stop != string(acp.StopReasonEndTurn) {
				t.Fatalf("stop=%s err=%v", f.stop, f.err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.requests) != len(tc.added) {
				t.Fatalf("requests=%d, want %d", len(f.requests), len(tc.added))
			}

			var firstParams map[string]json.RawMessage
			var previous []json.RawMessage
			var initialCount int
			for i, raw := range f.requests {
				params, messages := retryCacheRequest(t, raw)
				if i == 0 {
					firstParams, initialCount = params, len(messages)
				} else if !reflect.DeepEqual(params, firstParams) {
					t.Fatalf("request %d changed system, tools or other request parameters", i+1)
				}
				if len(messages) != initialCount+tc.added[i] {
					t.Fatalf("request %d has %d history messages, want %d", i+1, len(messages), initialCount+tc.added[i])
				}
				for j, before := range previous {
					if !bytes.Equal(before, messages[j]) {
						t.Fatalf("request %d rewrote cached history message %d:\nbefore: %s\nafter: %s", i+1, j, before, messages[j])
					}
				}
				// With no new tool result or nudge, even the trailing runtime
				// context is identical despite the advancing test clock.
				if i > 0 && tc.added[i] == tc.added[i-1] && raw != f.requests[i-1] {
					t.Fatalf("request %d is not a byte-identical replay of request %d", i+1, i)
				}
				previous = messages
			}
		})
	}
}

func retryCacheRequest(t *testing.T, raw string) (map[string]json.RawMessage, []json.RawMessage) {
	t.Helper()
	var params map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"system", "tools"} {
		if len(params[key]) == 0 {
			t.Fatalf("request carries no %s", key)
		}
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(params["messages"], &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) < 4 {
		t.Fatalf("request lost prior history, user prompt or turn context: %s", params["messages"])
	}
	var last struct {
		Role    string
		Content []struct{ Type, Text string }
	}
	if err := json.Unmarshal(messages[len(messages)-1], &last); err != nil {
		t.Fatal(err)
	}
	if last.Role != "user" || len(last.Content) != 1 || last.Content[0].Type != "text" || !strings.HasPrefix(last.Content[0].Text, turnContextOpenTag+"\n") {
		t.Fatal("runtime state is not a trailing user message")
	}
	delete(params, "messages")
	return params, messages[:len(messages)-1]
}
