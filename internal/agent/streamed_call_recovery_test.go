package agent

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestMalformedStreamedCallRecovery(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		budget, turns, requests int
		repeat, succeeds        bool
	}{
		{"replacement", 1, 5, 3, false, true}, {"repeated", 4, 5, 2, true, false}, {"no retry allowance", 0, 5, 1, false, false}, {"last iteration", 4, 1, 1, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diagnostic := &llm.StreamedToolCallError{Index: 3, FinishReason: "tool_calls"}
			sibling := notesRead("sibling", 1)
			bad := llm.ToolCall{ID: "bad", Name: "write", InputJSON: `{"content":"secret-marker`}
			failed := outputLimitStep{text: "Already shown.", reasoning: "Plan.", err: fmt.Errorf("provider: %w", diagnostic), chunks: []llm.StreamChunk{{ToolCallNamed: &sibling}, {ToolCallNamed: &bad}, {ToolCallDelta: &bad}}}
			replacement := notesRead("sibling", 2)
			p := &outputLimitProvider{steps: []outputLimitStep{failed, {calls: []llm.ToolCall{replacement}, chunks: []llm.StreamChunk{{ToolCallNamed: &replacement}, {ToolCall: &replacement}}}, answered}}
			if tc.repeat {
				p.steps[1] = failed
			}
			ag, st, sender := cutCallAgent(t, p, func(c *config.Config) { llmRetryMax(tc.budget)(c); c.Agent.MaxTurns = tc.turns })
			checkClosed := func(id string) {
				t.Helper()
				statuses := sender.toolStatuses(id)
				if len(statuses) == 0 || statuses[len(statuses)-1] != "failed" || strings.Count(strings.Join(statuses, ","), "failed") != 1 {
					t.Errorf("%s statuses %v", id, statuses)
				}
				meta, err := session.ReadToolCallMeta(st.SessionDir, id)
				if err != nil || meta.Status != "failed" {
					t.Errorf("%s meta %v err %v", id, meta, err)
				}
			}
			p.steps[1].during = func() { checkClosed("bad"); checkClosed("sibling") }
			reason, err := runOutputLimitTurn(ag)
			if p.calls != tc.requests {
				t.Errorf("requests=%d want %d", p.calls, tc.requests)
			}
			if tc.succeeds {
				if err != nil || reason != string(acp.StopReasonEndTurn) {
					t.Errorf("reason=%s err=%v", reason, err)
				}
			} else if !errors.Is(err, diagnostic) {
				t.Errorf("lost diagnostic: %v", err)
			}
			checkClosed("bad")
			checkClosed("sibling")
			if tc.repeat && p.calls == 2 {
				checkClosed("bad_2")
				checkClosed("sibling_2")
			}
			hist := st.GetMessages()
			results := toolResults(hist)
			if tc.succeeds {
				if len(results) != 1 || !strings.Contains(results["sibling_2"], "line 2") {
					t.Errorf("wrong executions %v", results)
				}
			} else if len(results) != 0 {
				t.Errorf("unexpected executions %v", results)
			}
			var kept int
			for _, m := range hist {
				if m.Content == failed.text && m.Reasoning == failed.reasoning && len(m.ToolCalls) == 0 {
					kept++
				}
				if m.Ephemeral {
					t.Error("ephemeral message persisted")
				}
			}
			want := 1
			if tc.repeat {
				want++
			}
			if kept != want {
				t.Errorf("kept partials %d want %d", kept, want)
			}
			if p.calls > 1 {
				var nudges int
				for _, m := range p.seen[1] {
					if strings.Contains(m.Content, "secret-marker") || len(m.ToolCalls) > 0 || m.Role == llm.RoleTool {
						t.Error("discarded batch leaked into history")
					}
					if m.Ephemeral && strings.Contains(m.Content, "JSON object") {
						nudges++
					}
				}
				if nudges != 1 {
					t.Errorf("safe repair nudges=%d", nudges)
				}
			}
		})
	}
}

func TestParseableLengthCutRunsOnlyWholeSibling(t *testing.T) {
	cut := notesRead("cut", 1)
	whole := notesRead("whole", 2)
	step := outputLimitStep{text: "before", stop: "max_tokens", calls: []llm.ToolCall{whole}, cut: &cut, chunks: []llm.StreamChunk{{ToolCallNamed: &cut}}}
	p := &outputLimitProvider{steps: []outputLimitStep{step, answered}}
	ag, st, sender := cutCallAgent(t, p, nil)
	reason, err := runOutputLimitTurn(ag)
	results := toolResults(st.GetMessages())
	if err != nil || reason != string(acp.StopReasonEndTurn) || len(results) != 1 || !strings.Contains(results["whole"], "line 2") || p.calls != 2 || countNudges(p.seen[1], cutOffToolCallNudge("read")) != 1 || strings.Join(sender.toolStatuses("cut"), ",") != "failed" {
		t.Errorf("reason=%s err=%v results=%v cut statuses=%v", reason, err, results, sender.toolStatuses("cut"))
	}
}
