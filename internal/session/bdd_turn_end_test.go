package session_test

// Godog harness for features/turn_end_reasons.feature: a real ReAct turn
// through the session manager, whose LLM is the REAL openai provider pointed
// at a scripted OpenAI-compatible stream. The script decides per request what
// the model does - call coddy's read tool, answer, or break off mid-answer
// with the error frame a LiteLLM proxy sends when its fallback fails - so the
// turn's end is asserted on the manager's result, the transcript and the
// session's UI log.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const turnEndAnswer = "The notes hold forty numbered lines."

// scriptedOpenAI is an OpenAI-compatible chat completions stream whose answer
// to the n-th step of the agent (1-based) is written by script. A request
// that offers no tools is not a step of the turn - the session's title is
// generated that way - and gets a plain answer outside the count.
type scriptedOpenAI struct {
	mu       sync.Mutex
	requests [][]byte
	script   func(n int, w io.Writer)
}

func (o *scriptedOpenAI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "text/event-stream")
	var probe struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(raw, &probe) != nil || len(probe.Tools) == 0 {
		sseAnswer(w, "Notes summary")
		return
	}
	o.mu.Lock()
	o.requests = append(o.requests, raw)
	n := len(o.requests)
	o.mu.Unlock()
	o.script(n, w)
}

func (o *scriptedOpenAI) calls() [][]byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([][]byte(nil), o.requests...)
}

func sseChunk(w io.Writer, delta map[string]any, finish any) {
	b, _ := json.Marshal(map[string]any{
		"id": "chatcmpl-stand", "object": "chat.completion.chunk", "model": "stand",
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
}

func sseAnswer(w io.Writer, text string) {
	sseChunk(w, map[string]any{"role": "assistant", "content": text}, nil)
	sseChunk(w, map[string]any{}, "stop")
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func sseReadCall(w io.Writer, n int) {
	args, _ := json.Marshal(map[string]any{"path": "notes.txt", "offset": n, "limit": 1})
	sseChunk(w, map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
		"index": 0, "id": fmt.Sprintf("call_%d", n), "type": "function",
		"function": map[string]any{"name": "read", "arguments": string(args)},
	}}}, nil)
	sseChunk(w, map[string]any{}, "tool_calls")
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

type turnEndState struct {
	root   string
	cwd    string
	home   string
	stub   *scriptedOpenAI
	ts     *httptest.Server
	cfg    *config.Config
	state  *session.State
	stop   acp.StopReason
	runErr error
	// partial is the text the scripted model streamed before breaking off.
	partial string
}

func (s *turnEndState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-turn-end-*")
	if err != nil {
		return err
	}
	s.root = root
	s.home = filepath.Join(root, "home")
	s.cwd = filepath.Join(root, "workspace")
	for _, dir := range []string{s.home, s.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	var lines []string
	for i := 1; i <= 40; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	return os.WriteFile(filepath.Join(s.cwd, "notes.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func (s *turnEndState) close() {
	if s.state != nil {
		s.state.CloseAll()
		s.state = nil
	}
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
	s.stop, s.runErr, s.partial, s.cfg = "", nil, "", nil
}

func (s *turnEndState) serve(script func(n int, w io.Writer)) {
	s.stub = &scriptedOpenAI{script: script}
	s.ts = httptest.NewServer(s.stub)
}

func (s *turnEndState) modelReadsBeforeAnswering(reads int) error {
	s.serve(func(n int, w io.Writer) {
		if n <= reads {
			sseReadCall(w, n)
			return
		}
		sseAnswer(w, turnEndAnswer)
	})
	return nil
}

func (s *turnEndState) modelBreaksOffOnce(code int, partial string) error {
	s.partial = partial
	s.serve(func(n int, w io.Writer) {
		if n == 1 {
			sseChunk(w, map[string]any{"role": "assistant", "content": partial}, nil)
			b, _ := json.Marshal(map[string]any{"error": map[string]any{
				"code":    code,
				"message": "litellm.MidStreamFallbackError: litellm.APIConnectionError: peer closed connection without sending complete message body (incomplete chunked read)",
			}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
			return
		}
		sseAnswer(w, " "+turnEndAnswer)
	})
	return nil
}

func (s *turnEndState) agentWithMaxTurns(maxTurns int) error {
	s.cfg = &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.cwd},
		Providers: []config.ProviderConfig{{Name: "stand", Type: "openai", APIBase: s.ts.URL, APIKey: "test-key"}},
		Models:    []config.ModelEntry{{Model: "stand/model"}},
		// A millisecond retry base keeps the provider-failure pause short.
		Agent: config.Agent{Model: "stand/model", MaxTurns: maxTurns, LLMRetryBaseMS: 1},
	}
	return nil
}

func (s *turnEndState) agentWithoutLimit() error { return s.agentWithMaxTurns(0) }

func (s *turnEndState) userSendsPrompt() error {
	log := slog.Default()
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		s.state = st
		return agent.NewAgent(s.cfg, st, snd, log).Run(ctx, prompt)
	}
	mgr := session.NewManager(s.cfg, noopSender{}, runner, log, s.cwd, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	newRes, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: s.cwd})
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	res, err := mgr.HandleSessionPrompt(ctx, acp.SessionPromptParams{
		SessionID: newRes.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "read the notes and sum them up"}},
	})
	s.runErr = err
	if res != nil {
		s.stop = res.StopReason
	}
	return nil
}

func (s *turnEndState) lastAssistantText() string {
	msgs := s.state.GetMessages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant && strings.TrimSpace(msgs[i].Content) != "" {
			return msgs[i].Content
		}
	}
	return ""
}

func (s *turnEndState) turnEndsWithAnswer() error {
	if s.runErr != nil {
		return fmt.Errorf("the turn failed: %v", s.runErr)
	}
	if s.stop != acp.StopReasonEndTurn {
		return fmt.Errorf("stop reason = %q, want end_turn", s.stop)
	}
	if got := s.lastAssistantText(); !strings.Contains(got, turnEndAnswer) {
		return fmt.Errorf("last answer %q is not the model's answer", got)
	}
	return nil
}

func (s *turnEndState) modelCalledTimes(n int) error {
	if got := len(s.stub.calls()); got != n {
		return fmt.Errorf("the model was called %d times, want %d", got, n)
	}
	return nil
}

func (s *turnEndState) turnStopsAtLimit() error {
	if s.runErr != nil {
		return fmt.Errorf("the turn failed: %v", s.runErr)
	}
	if s.stop != acp.StopReasonMaxTurns {
		return fmt.Errorf("stop reason = %q, want max_turns", s.stop)
	}
	return nil
}

func (s *turnEndState) noticeContaining(level string, wants ...string) error {
	var seen []string
	for _, e := range s.state.GetUILog() {
		seen = append(seen, e.Level+": "+e.Message)
		if e.Level != level {
			continue
		}
		all := true
		for _, want := range wants {
			if !strings.Contains(e.Message, want) {
				all = false
			}
		}
		if all {
			return nil
		}
	}
	return fmt.Errorf("no %s row names %q in the UI log: %q", level, wants, seen)
}

func (s *turnEndState) noticeNamesMaxTurns() error {
	return s.noticeContaining(session.UILogLevelNotice, "agent.max_turns", "3")
}

func (s *turnEndState) transcriptKeepsPartial(text string) error {
	for _, m := range s.state.GetMessages() {
		if m.Role == llm.RoleAssistant && strings.HasPrefix(strings.TrimSpace(m.Content), text) && !strings.Contains(m.Content, turnEndAnswer) {
			return nil
		}
	}
	return fmt.Errorf("no assistant message keeps the partial answer %q", text)
}

func (s *turnEndState) nextRequestContinued() error {
	calls := s.stub.calls()
	if len(calls) < 2 {
		return fmt.Errorf("the model was called %d time(s), want a second request", len(calls))
	}
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(calls[1], &req); err != nil {
		return err
	}
	// The partial answer, then the request to go on, in that order; the
	// turn context block may follow them.
	partialAt := -1
	for i, m := range req.Messages {
		text := fmt.Sprint(m.Content)
		switch {
		case m.Role == "assistant" && strings.Contains(text, s.partial):
			partialAt = i
		case partialAt >= 0 && m.Role == "user" && strings.Contains(strings.ToLower(text), "continue"):
			return nil
		}
	}
	if partialAt < 0 {
		return fmt.Errorf("the second request does not replay the partial answer: %s", calls[1])
	}
	return fmt.Errorf("the second request does not ask the model to continue after the partial answer: %s", calls[1])
}

func (s *turnEndState) noticeProviderRecovered() error {
	return s.noticeContaining(session.UILogLevelNotice, "server error 500")
}

func initializeTurnEndScenario(sc *godog.ScenarioContext) {
	s := &turnEndState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a model that reads a file (\d+) times before it answers$`, s.modelReadsBeforeAnswering)
	sc.Step(`^a model whose first answer breaks off with "server error (\d+)" after "([^"]+)"$`, s.modelBreaksOffOnce)
	sc.Step(`^an agent with no step limit configured$`, s.agentWithoutLimit)
	sc.Step(`^an agent whose agent\.max_turns is (\d+)$`, s.agentWithMaxTurns)
	sc.Step(`^the user sends a prompt$`, s.userSendsPrompt)
	sc.Step(`^the turn ends with the model's answer$`, s.turnEndsWithAnswer)
	sc.Step(`^the model was called (\d+) times$`, s.modelCalledTimes)
	sc.Step(`^the turn stops at its step limit$`, s.turnStopsAtLimit)
	sc.Step(`^the session's log carries a notice that names agent\.max_turns$`, s.noticeNamesMaxTurns)
	sc.Step(`^the transcript keeps "([^"]+)" that the user already saw$`, s.transcriptKeepsPartial)
	sc.Step(`^the next request carried that part and asked the model to continue$`, s.nextRequestContinued)
	sc.Step(`^the session's log carries a notice that the provider failed and the turn went on$`, s.noticeProviderRecovered)
}

func TestTurnEndReasonsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "turn_end_reasons",
		ScenarioInitializer: initializeTurnEndScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/turn_end_reasons.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("turn end reasons feature failed")
	}
}

// sseFailMidAnswer streams part of an answer, then the error frame of a
// failed proxy with the given status.
func sseFailMidAnswer(w io.Writer, partial string, code int) {
	sseChunk(w, map[string]any{"role": "assistant", "content": partial}, nil)
	b, _ := json.Marshal(map[string]any{"error": map[string]any{"code": code, "message": "upstream failed"}})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
}

// runTurnEnd runs one turn of the stand with cfgFn adjusting the agent
// section, and returns the state for assertions.
func runTurnEnd(t *testing.T, script func(n int, w io.Writer), adjust func(*config.Agent)) *turnEndState {
	t.Helper()
	s := &turnEndState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	s.serve(script)
	_ = s.agentWithoutLimit()
	if adjust != nil {
		adjust(&s.cfg.Agent)
	}
	if err := s.userSendsPrompt(); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestProviderRecoveryBreakerOpens: a lane that keeps failing costs the turn
// two recoveries, then the turn ends with the provider's error.
func TestProviderRecoveryBreakerOpens(t *testing.T) {
	s := runTurnEnd(t, func(n int, w io.Writer) { sseFailMidAnswer(w, "Part", 500) }, nil)
	if s.runErr == nil || !strings.Contains(s.runErr.Error(), "server error 500") {
		t.Fatalf("turn error = %v, want the provider's 500", s.runErr)
	}
	if got := len(s.stub.calls()); got != 1+2 {
		t.Fatalf("the model was called %d times, want 3 (the call and two recoveries)", got)
	}
}

// TestProviderRecoveryLeavesRefusalsAlone: a 400 in the middle of an answer
// is the provider refusing the request, which no pause changes.
func TestProviderRecoveryLeavesRefusalsAlone(t *testing.T) {
	s := runTurnEnd(t, func(n int, w io.Writer) { sseFailMidAnswer(w, "Part", 400) }, nil)
	if s.runErr == nil || len(s.stub.calls()) != 1 {
		t.Fatalf("turn error = %v after %d calls, want the refusal after one", s.runErr, len(s.stub.calls()))
	}
}

// TestProviderRecoveryOffWithoutRetries: llm_retry_max: 0 turns the recovery
// off together with every other retry.
func TestProviderRecoveryOffWithoutRetries(t *testing.T) {
	zero := 0
	s := runTurnEnd(t, func(n int, w io.Writer) {
		if n == 1 {
			sseFailMidAnswer(w, "Part", 500)
			return
		}
		sseAnswer(w, turnEndAnswer)
	}, func(a *config.Agent) { a.LLMRetryMax = &zero })
	if s.runErr == nil || len(s.stub.calls()) != 1 {
		t.Fatalf("turn error = %v after %d calls, want the provider's error after one", s.runErr, len(s.stub.calls()))
	}
}

// TestProviderRecoveryBeforeAnyOutput: a lane that answers 503 until the
// resilient wrapper's own retries are spent is run again once more after the
// pause, with nothing kept and nothing asked to continue.
func TestProviderRecoveryBeforeAnyOutput(t *testing.T) {
	var failed int
	s := &turnEndState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	s.stub = &scriptedOpenAI{script: func(n int, w io.Writer) { sseAnswer(w, turnEndAnswer) }}
	s.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		// Four refusals: the call and the wrapper's three retries.
		if strings.Contains(string(raw), `"tools"`) && failed < 4 {
			failed++
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"no healthy upstream"}}`)
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		s.stub.ServeHTTP(w, r)
	}))
	_ = s.agentWithoutLimit()
	if err := s.userSendsPrompt(); err != nil {
		t.Fatal(err)
	}
	if err := s.turnEndsWithAnswer(); err != nil {
		t.Fatal(err)
	}
	for _, m := range s.state.GetMessages() {
		if m.Role == llm.RoleAssistant && !strings.Contains(m.Content, turnEndAnswer) {
			t.Fatalf("a partial answer was kept though nothing streamed: %+v", m)
		}
	}
	if strings.Contains(string(s.stub.calls()[0]), "cut off by a provider error") {
		t.Fatal("the model was asked to continue an answer it never started")
	}
}

// TestProviderRecoveryPauseHonoursStop: a Stop during the pause ends the turn
// as a stop, not as the provider's error.
func TestProviderRecoveryPauseHonoursStop(t *testing.T) {
	s := &turnEndState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	s.serve(func(n int, w io.Writer) { sseFailMidAnswer(w, "Part", 500) })
	_ = s.agentWithoutLimit()
	s.cfg.Agent.LLMRetryBaseMS = 60000 // a five-minute pause, capped at two
	st := &session.State{ID: "sess_turn_end_stop", CWD: s.cwd, Mode: session.ModeAgent, SessionDir: filepath.Join(s.root, "bundle")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st.SetCancel(cancel)
	// What a Stop does (Manager.HandleSessionCancel): mark it, then cancel.
	time.AfterFunc(200*time.Millisecond, func() {
		st.SetUserCancelledTurn()
		st.Cancel()
	})
	start := time.Now()
	stop, err := agent.NewAgent(s.cfg, st, noopSender{}, slog.Default()).Run(ctx, []acp.ContentBlock{{Type: "text", Text: "go"}})
	if err != nil || stop != string(acp.StopReasonCancelled) {
		t.Fatalf("Run = %q, %v; want cancelled", stop, err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("the pause did not end on Stop (%s)", time.Since(start))
	}
}

// TestProviderRecoveryNoticeNamesThePause: the recovery leaves a notice in the
// UI log naming the failure, the pause and the count.
func TestProviderRecoveryNoticeNamesThePause(t *testing.T) {
	s := runTurnEnd(t, func(n int, w io.Writer) {
		if n == 1 {
			sseFailMidAnswer(w, "Part", 502)
			return
		}
		sseAnswer(w, turnEndAnswer)
	}, nil)
	if err := s.turnEndsWithAnswer(); err != nil {
		t.Fatal(err)
	}
	if err := s.noticeContaining(session.UILogLevelNotice, "recovery 1 of 2", "5ms"); err != nil {
		t.Fatal(err)
	}
}

// TestProviderRecoveryPauseCutByADeadline: a deadline (not the user) that
// ends the pause ends the turn with the provider's failure, not as a Stop.
func TestProviderRecoveryPauseCutByADeadline(t *testing.T) {
	s := &turnEndState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	s.serve(func(n int, w io.Writer) { sseFailMidAnswer(w, "Part", 500) })
	_ = s.agentWithoutLimit()
	s.cfg.Agent.LLMRetryBaseMS = 60000
	st := &session.State{ID: "sess_turn_end_deadline", CWD: s.cwd, Mode: session.ModeAgent, SessionDir: filepath.Join(s.root, "bundle")}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	stop, err := agent.NewAgent(s.cfg, st, noopSender{}, slog.Default()).Run(ctx, []acp.ContentBlock{{Type: "text", Text: "go"}})
	if stop == string(acp.StopReasonCancelled) || err == nil || !strings.Contains(err.Error(), "server error 500") {
		t.Fatalf("Run = %q, %v; want the provider's failure", stop, err)
	}
}
