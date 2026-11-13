package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks/hooktest"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestPromptCacheFirstPromptKeepsTheSystemPromptByteStable(t *testing.T) {
	assertFirstPromptRequestContract(t, hooktest.Handler("context", "first-only-working-state"))
}

func TestFirstPromptPlainStdoutBecomesTurnContext(t *testing.T) {
	assertFirstPromptRequestContract(t, hooktest.Handler("text", "plain working state"))
}

func TestFirstPromptContextWorksWithVolatileTemplates(t *testing.T) {
	a := &Agent{state: &session.State{CWD: t.TempDir()}, firstPromptContext: []string{"first-only-working-state"}}
	got := a.buildTurnContext(&systemPromptBuild{Volatile: true})
	if !strings.Contains(got, "first-only-working-state") {
		t.Fatalf("volatile template dropped FirstPrompt context: %q", got)
	}
}

func TestNoFirstPromptDoesNotAddEmptyHookContext(t *testing.T) {
	a := &Agent{state: &session.State{CWD: t.TempDir()}}
	if got := a.buildTurnContext(nil); strings.Contains(got, "Hook context") {
		t.Fatalf("ordinary turn gained an empty Hook context: %q", got)
	}
}

// cancelledAfterAnswerContext makes cancellation visible only after the
// executor has answered. Done stays open so the runner cannot abort the
// completed external call before its result reaches the agent.
type cancelledAfterAnswerContext struct {
	context.Context
	done chan struct{}
}

func (c *cancelledAfterAnswerContext) Done() <-chan struct{} { return c.done }
func (c *cancelledAfterAnswerContext) Err() error            { return context.Canceled }

func TestToolExecuteHandledResultSurvivesCancellationAfterAnswer(t *testing.T) {
	ag, st, dir := newDecisionsAgent(t, false)
	if err := hooktest.Write(filepath.Join(ag.cfg.Paths.Home, "hooks.json"), hooktest.Entry{
		Event: hooks.EventToolExecute, Matcher: "run_command",
		Handlers: []hooks.Handler{hooktest.Handler("handle", "external result")},
	}); err != nil {
		t.Fatal(err)
	}
	ag.cfg.Paths.CWD = st.CWD
	ag.cfg.Hooks.ApplyDefaults(ag.cfg.Paths)
	ctx := &cancelledAfterAnswerContext{Context: context.Background(), done: make(chan struct{})}
	result, err := ag.executeToolCall(ctx, llm.ToolCall{
		ID: "call-external-cancel", Name: "run_command", InputJSON: `{"command":"echo must-not-run-locally"}`,
	}, ag.buildToolEnv(string(session.ModeAgent), dir), string(session.ModeAgent), st.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if result != "external result" {
		t.Fatalf("handled result was lost after cancellation: %q", result)
	}
	if strings.Contains(result, "must-not-run-locally") || strings.Contains(result, session.ToolResultCancelledBeforeRun) {
		t.Fatalf("handled result was replaced by local execution or cancellation: %q", result)
	}
}

func TestToolExecuteBlockDoesNotFallBackToLocalExecution(t *testing.T) {
	s := &hooksFeatureState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if err := s.addHook(hooks.EventToolExecute, "run_command", hooktest.Handler("exit2", "executor refused")); err != nil {
		t.Fatal(err)
	}
	if err := s.agentSession(); err != nil {
		t.Fatal(err)
	}
	if err := s.modelRunsCommand("echo must-not-run-locally"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.lastResult, "executor refused") {
		t.Fatalf("tool result %q must carry the executor's refusal", s.lastResult)
	}
	if strings.Contains(s.lastResult, "must-not-run-locally") {
		t.Fatalf("a blocked executor fell back to local execution: %q", s.lastResult)
	}
}

func TestToolExecuteRefusalsDoNotFallBackToLocalExecution(t *testing.T) {
	tests := []struct {
		name    string
		entries []hooktest.Entry
		want    string
		wantErr bool
	}{
		{
			name: "fail closed",
			entries: []hooktest.Entry{{
				Event: hooks.EventToolExecute, Matcher: "run_command",
				Handlers: []hooks.Handler{func() hooks.Handler {
					h := hooktest.Handler("fail", "3")
					h.FailClosed = true
					return h
				}()},
			}},
			want: "exit status 3",
		},
		{
			name: "deny before handled",
			entries: []hooktest.Entry{
				{Event: hooks.EventToolExecute, Matcher: "run_command", Handlers: []hooks.Handler{hooktest.Handler("deny", "must-not-run")}},
				{Event: hooks.EventToolExecute, Matcher: "run_command", Handlers: []hooks.Handler{hooktest.Handler("handle", "external result")}},
			},
			want: "destructive command",
		},
		{
			name: "stop before handled",
			entries: []hooktest.Entry{
				{Event: hooks.EventToolExecute, Matcher: "run_command", Handlers: []hooks.Handler{hooktest.Handler("continue-false", "executor stopped")}},
				{Event: hooks.EventToolExecute, Matcher: "run_command", Handlers: []hooks.Handler{hooktest.Handler("handle", "external result")}},
			},
			want: "executor stopped", wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &hooksFeatureState{}
			if err := s.reset(); err != nil {
				t.Fatal(err)
			}
			defer s.close()
			s.entries = append(s.entries, tt.entries...)
			if err := s.agentSession(); err != nil {
				t.Fatal(err)
			}
			err := s.modelRunsCommand("echo must-not-run-locally")
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("turn error = %v, want one containing %q", err, tt.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(s.lastResult, tt.want) {
				t.Fatalf("tool result %q must contain %q", s.lastResult, tt.want)
			}
			if strings.Contains(s.lastResult, "must-not-run-locally\n") || strings.Contains(s.lastResult, "external result") {
				t.Fatalf("refused executor produced a local or handled result: %q", s.lastResult)
			}
		})
	}
}

func TestToolExecuteCancellationDoesNotFallBackToLocalExecution(t *testing.T) {
	s := &hooksFeatureState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if err := s.addHook(hooks.EventToolExecute, "run_command", hooktest.Handler("sleep", "2")); err != nil {
		t.Fatal(err)
	}
	if err := s.agentSession(); err != nil {
		t.Fatal(err)
	}
	s.provider.mu.Lock()
	s.provider.steps = []scriptStep{toolStep(commandCall("call-1", "echo must-not-run-locally", false)), answerStep("done")}
	s.provider.calls = 0
	s.provider.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = runLifecycleTurn(ctx, s, "run the command")
	for _, message := range s.sess.GetMessages() {
		if message.Role == llm.RoleTool && strings.Contains(message.Content, "must-not-run-locally") {
			t.Fatalf("a cancelled executor fell back to local execution: %q", message.Content)
		}
	}
}

func assertFirstPromptRequestContract(t *testing.T, handler hooks.Handler) {
	t.Helper()
	s := &hooksFeatureState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if err := s.addHook(hooks.EventFirstPrompt, "*", handler); err != nil {
		t.Fatal(err)
	}
	if err := s.agentSession(); err != nil {
		t.Fatal(err)
	}
	s.provider.mu.Lock()
	s.provider.steps = []scriptStep{answerStep("first answer"), answerStep("second answer")}
	s.provider.calls = 0
	s.provider.requests = nil
	s.provider.mu.Unlock()
	loop := NewAgent(s.cfg, s.sess, s.client, slog.Default())
	loop.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil })
	for _, prompt := range []string{"first", "second"} {
		if _, err := loop.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: prompt}}); err != nil {
			t.Fatal(err)
		}
	}
	s.provider.mu.Lock()
	requests := append([][]llm.Message(nil), s.provider.requests...)
	s.provider.mu.Unlock()
	if len(requests) != 2 || len(requests[0]) == 0 || len(requests[1]) == 0 {
		t.Fatalf("provider requests = %d, want two populated requests", len(requests))
	}
	if requests[0][0].Content != requests[1][0].Content {
		t.Fatal("FirstPrompt changed the system prompt between the first and second requests")
	}
	if !requestContains(requests[0], "working state") && !requestContains(requests[0], "working-state") {
		t.Fatal("the first request lacks the FirstPrompt enrichment")
	}
	if requestContains(requests[1], "working state") || requestContains(requests[1], "working-state") {
		t.Fatal("one-time FirstPrompt enrichment leaked into the second request")
	}
}

func requestContains(messages []llm.Message, fragment string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, fragment) {
			return true
		}
	}
	return false
}

type cancelTurnProvider struct {
	cancel context.CancelFunc
}

func (p cancelTurnProvider) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDefinition) (*llm.Response, error) {
	return p.Stream(ctx, messages, defs, func(llm.StreamChunk) {})
}

func (p cancelTurnProvider) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
	p.cancel()
	return nil, ctx.Err()
}

type failTurnProvider struct{}

func (failTurnProvider) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDefinition) (*llm.Response, error) {
	return failTurnProvider{}.Stream(ctx, messages, defs, func(llm.StreamChunk) {})
}

func (failTurnProvider) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
	return nil, errors.New("provider failed")
}

type compactFailTurnProvider struct {
	state *session.State
}

func (p compactFailTurnProvider) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDefinition) (*llm.Response, error) {
	return p.Stream(ctx, messages, defs, func(llm.StreamChunk) {})
}

func (p compactFailTurnProvider) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
	p.state.InsertCompactionSummary(0, llm.Message{Role: llm.RoleSystem, Content: "compaction summary"})
	return nil, errors.New("provider failed after compaction")
}

func TestTurnEndDoesNotQuoteAnEarlierTurnAfterCompaction(t *testing.T) {
	s := newTurnEndRegressionState(t, "record")
	defer s.close()
	s.providerFactory = func() llm.Provider { return compactFailTurnProvider{state: s.sess} }
	if err := runLifecycleTurn(context.Background(), s, "compact and fail"); err == nil {
		t.Fatal("the failed turn must return its error")
	}
	payload := readTurnEndPayload(t, s)
	if got := payload["last_assistant_message"]; got != "" {
		t.Fatalf("compacted failed turn quoted an earlier answer: %#v", got)
	}
}

func TestTurnEndSurvivesCancellationAndDoesNotQuoteAnEarlierTurn(t *testing.T) {
	s := newTurnEndRegressionState(t, "record")
	defer s.close()
	ctx, cancel := context.WithCancel(context.Background())
	s.providerFactory = func() llm.Provider { return cancelTurnProvider{cancel: cancel} }
	if err := runLifecycleTurn(ctx, s, "stop this turn"); err == nil {
		t.Fatal("the cancelled turn must return its error")
	}
	payload := readTurnEndPayload(t, s)
	if got := payload["stop_reason"]; got != "agent_refused" {
		t.Fatalf("stop_reason = %#v, want the existing agent_refused classification", got)
	}
	if got := payload["last_assistant_message"]; got != "" {
		t.Fatalf("cancelled turn quoted an earlier answer: %#v", got)
	}
}

func TestTurnEndReportsAProviderErrorWithoutQuotingAnEarlierTurn(t *testing.T) {
	s := newTurnEndRegressionState(t, "record")
	defer s.close()
	s.providerFactory = func() llm.Provider { return failTurnProvider{} }
	if err := runLifecycleTurn(context.Background(), s, "fail this turn"); err == nil {
		t.Fatal("the failed turn must return its error")
	}
	payload := readTurnEndPayload(t, s)
	if got := payload["stop_reason"]; got != "agent_refused" {
		t.Fatalf("stop_reason = %#v, want agent_refused", got)
	}
	if got := payload["last_assistant_message"]; got != "" {
		t.Fatalf("failed turn quoted an earlier answer: %#v", got)
	}
}

func newTurnEndRegressionState(t *testing.T, mode string) *hooksFeatureState {
	t.Helper()
	s := &hooksFeatureState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	if err := s.addHook(hooks.EventTurnEnd, "*", hooktest.Handler(mode, s.recordFile)); err != nil {
		s.close()
		t.Fatal(err)
	}
	if err := s.agentSession(); err != nil {
		s.close()
		t.Fatal(err)
	}
	s.sess.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "earlier answer"})
	return s
}

func runLifecycleTurn(ctx context.Context, s *hooksFeatureState, text string) error {
	_, err := s.mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: s.sess.ID,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}},
	}, s.client, nil)
	return err
}

func readTurnEndPayload(t *testing.T, s *hooksFeatureState) map[string]interface{} {
	t.Helper()
	b, err := os.ReadFile(s.recordFile)
	if err != nil {
		t.Fatalf("TurnEnd did not run: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(b, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}
