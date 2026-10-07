package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestParseGoalVerdict(t *testing.T) {
	for name, tc := range map[string]struct {
		text      string
		verdict   GoalVerdict
		remaining int
		wantErr   bool
	}{
		"bare":     {text: `{"verdict":"met","reason":"tests pass"}`, verdict: GoalVerdictMet},
		"fenced":   {text: "Here it is:\n```json\n{\"verdict\":\"not_met\",\"remaining\":[\"fix TestX\"]}\n```", verdict: GoalVerdictNotMet, remaining: 1},
		"prose":    {text: `I checked. {"analysis":"has } in a string","verdict":"needs_user","reason":"which DB?"} Thanks.`, verdict: GoalVerdictNeedsUser},
		"legacy":   {text: `{"done":true,"remaining":""}`, verdict: GoalVerdictMet},
		"unknown":  {text: `{"verdict":"probably"}`, verdict: GoalVerdictNotMet},
		"open met": {text: `{"verdict":"met","checklist":[{"text":"tests pass","status":"met"},{"text":"docs","status":"unverified"}]}`, verdict: GoalVerdictNotMet, remaining: 1},
		"no json":  {text: "The goal looks done to me.", wantErr: true},
		"no field": {text: `{"reason":"x"}`, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseGoalVerdict(tc.text)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("no error for %q: %+v", tc.text, got)
				}
				return
			}
			if err != nil || got.Verdict != tc.verdict || len(got.Remaining) != tc.remaining {
				t.Fatalf("got %+v err=%v", got, err)
			}
		})
	}
}

// judgeServer is an OpenAI-compatible endpoint that answers with the given
// texts in turn and records the requests.
func judgeServer(t *testing.T, answers ...string) (*httptest.Server, *[]map[string]interface{}) {
	t.Helper()
	var mu sync.Mutex
	var requests []map[string]interface{}
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, body)
		answer := answers[min(n, len(answers)-1)]
		n++
		mu.Unlock()
		content, _ := json.Marshal(answer)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"c1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":%s},"finish_reason":"stop"}]}`, content)
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

func judgeConfig(url string) *config.Config {
	off := false
	return &config.Config{
		Providers:  []config.ProviderConfig{{Name: "test", Type: "openai", APIKey: "key", APIBase: url + "/v1"}},
		Models:     []config.ModelEntry{{Model: "test/judge", Stream: &off}},
		Supervisor: config.Supervisor{Model: "test/judge"},
	}
}

func TestJudgeChecksTheGoalWithoutTools(t *testing.T) {
	srv, requests := judgeServer(t, `{"verdict":"met","reason":"go test passed","checklist":[{"text":"tests pass","status":"met","evidence":"ok ./..."}]}`)
	st := &State{ID: "sess_judge", Mode: ModeAgent}
	got, err := judgeSessionGoal(context.Background(), judgeConfig(srv.URL), st, GoalCheckRequest{Objective: "ship the fix", Digest: "[tool] run_command go test\n  -> ok"})
	if err != nil || got.Verdict != GoalVerdictMet || got.Model != "test/judge" || len(got.Checklist) != 1 {
		t.Fatalf("verdict=%+v err=%v", got, err)
	}
	req := (*requests)[0]
	if _, has := req["tools"]; has {
		t.Fatalf("the check offered tools: %v", req["tools"])
	}
	msgs := req["messages"].([]interface{})
	user := msgs[1].(map[string]interface{})["content"].(string)
	if len(msgs) != 2 || !strings.Contains(user, "<goal_objective>\nship the fix") || !strings.Contains(user, "<evidence>") {
		t.Fatalf("check request = %v", msgs)
	}
}

func TestJudgeAsksAgainForABadFormat(t *testing.T) {
	srv, requests := judgeServer(t, "Looks complete.", `{"verdict":"not_met","remaining":["add docs"]}`)
	got, err := judgeSessionGoal(context.Background(), judgeConfig(srv.URL), &State{ID: "s", Mode: ModeAgent}, GoalCheckRequest{Objective: "x"})
	if err != nil || got.Verdict != GoalVerdictNotMet || len(*requests) != 2 {
		t.Fatalf("verdict=%+v err=%v requests=%d", got, err, len(*requests))
	}
}

func TestGoalDigestRecordsEvidenceFlagsAndClaims(t *testing.T) {
	goal, _ := NewGoal("fix the parser")
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "earlier chat"},
		{Role: llm.RoleUser, Content: "kickoff", GoalTurn: &llm.GoalTurn{Kind: "kickoff", Objective: "fix the parser"}},
		{Role: llm.RoleAssistant, Content: "Let me look.", ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "edit", InputJSON: `{"path":"parser/parse_test.go","old":"x","new":"t.Skip(\"later\")"}`},
			{ID: "2", Name: "run_command", InputJSON: `{"command":"go test ./parser"}`},
		}},
		{Role: llm.RoleTool, ToolCallID: "1", Content: "edited"},
		{Role: llm.RoleTool, ToolCallID: "2", Content: "ok  parser 0.1s"},
		{Role: llm.RoleAssistant, Content: "All tests pass now."},
	}
	d := buildGoalDigest(msgs, goal, false)
	if strings.Contains(d, "earlier chat") {
		t.Fatalf("the digest reaches before the goal:\n%s", d)
	}
	for _, want := range []string{
		"[supervisor] Goal set: fix the parser",
		"[tool] run_command", "ok  parser 0.1s",
		"parser/parse_test.go",
		"## Red flags", "tests, test data or CI files were changed", "skip or suppression marker",
		"(claims, not evidence)\nAll tests pass now.",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("digest lacks %q:\n%s", want, d)
		}
	}
}

func TestGoalDigestKeepsTheNewestStepsWithinItsBudget(t *testing.T) {
	goal, _ := NewGoal("x")
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "k", GoalTurn: &llm.GoalTurn{Kind: "kickoff", Objective: "x"}}}
	for i := 0; i < 200; i++ {
		id := fmt.Sprint(i)
		msgs = append(msgs,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read", InputJSON: fmt.Sprintf(`{"path":"f%d"}`, i)}}},
			llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: strings.Repeat("line\n", 200)})
	}
	d := buildGoalDigest(msgs, goal, false)
	if len(d) > goalDigestBudget+6000 || !strings.Contains(d, "earlier steps omitted") || !strings.Contains(d, `"path":"f199"`) || strings.Contains(d, `"path":"f0"}`) {
		t.Fatalf("digest of %d bytes keeps the wrong end", len(d))
	}
}
