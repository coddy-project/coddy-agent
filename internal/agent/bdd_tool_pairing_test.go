package agent

// This file defines the BDD regression scenarios for interrupted tool batches
// and legacy histories with missing tool outputs.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type pairingProvider struct {
	mu          sync.Mutex
	requests    [][]llm.Message
	calls       int
	activations int
	legacy      bool
}

func (p *pairingProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("Complete must not be used")
}

func (p *pairingProvider) Stream(_ context.Context, messages []llm.Message, definitions []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.mu.Lock()
	p.requests = append(p.requests, append([]llm.Message(nil), messages...))
	p.calls++
	call := p.calls - p.activations
	p.mu.Unlock()
	if call == 1 && !p.legacy {
		if response := fixtureActivateTools(messages, definitions, onChunk, "run_command"); response != nil {
			p.mu.Lock()
			p.activations++
			p.mu.Unlock()
			return response, nil
		}
		calls := []llm.ToolCall{
			{ID: "pair-1", Name: "run_command", InputJSON: `{"command":"printf first > pair-1"}`},
			{ID: "pair-2", Name: "run_command", InputJSON: `{"command":"printf second > pair-2; sleep 10"}`},
			{ID: "pair-3", Name: "run_command", InputJSON: `{"command":"printf third > pair-3"}`},
		}
		for i := range calls {
			tc := calls[i]
			onChunk(llm.StreamChunk{ToolCall: &tc})
		}
		return &llm.Response{ToolCalls: calls, StopReason: "tool_use"}, nil
	}
	onChunk(llm.StreamChunk{TextDelta: "resumed"})
	return &llm.Response{Content: "resumed", StopReason: "end_turn"}, nil
}

type pairingSender struct {
	mu       sync.Mutex
	cancel   context.CancelFunc
	cancelOn string
	updates  []interface{}
}

func (s *pairingSender) SendSessionUpdate(_ string, update interface{}) error {
	s.mu.Lock()
	s.updates = append(s.updates, update)
	cancel := s.cancel
	shouldCancel := false
	if u, ok := update.(acp.ToolCallStatusUpdate); ok {
		shouldCancel = u.ToolCallID == s.cancelOn && u.Status == "in_progress"
	}
	s.mu.Unlock()
	if shouldCancel && cancel != nil {
		cancel()
	}
	return nil
}

func (s *pairingSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}

func (s *pairingSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func newPairingAgent(provider llm.Provider, state *session.State, sender acp.UpdateSender) *Agent {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "fake/model"},
		Tools:     config.Tools{PermissionMode: config.PermModeBypass},
	}
	ag := NewAgent(cfg, state, sender, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	return ag
}

func assertPairedBatch(req []llm.Message, ids ...string) error {
	for i := 0; i < len(req); i++ {
		if req[i].Role != llm.RoleAssistant || len(req[i].ToolCalls) != len(ids) {
			continue
		}
		for j, id := range ids {
			if req[i].ToolCalls[j].ID != id {
				return fmt.Errorf("call %d is %q, want %q", j, req[i].ToolCalls[j].ID, id)
			}
		}
		if i+len(ids) >= len(req) {
			return fmt.Errorf("batch has no adjacent results: %#v", req)
		}
		for j, id := range ids {
			m := req[i+1+j]
			if m.Role != llm.RoleTool || m.ToolCallID != id {
				return fmt.Errorf("result %d is %#v, want tool result for %q", j, m, id)
			}
		}
		return nil
	}
	return fmt.Errorf("paired batch not found in request: %#v", req)
}

type toolPairingFeatureState struct {
	root, cwd, sessionDir string
	state                 *session.State
	provider              *pairingProvider
	sender                *pairingSender
	cancelErr             error
	legacyBytes           []byte
	store                 *session.FileStore
}

func (s *toolPairingFeatureState) reset() error {
	s.root = ""
	root, err := os.MkdirTemp("", "coddy-bdd-pairing-*")
	if err != nil {
		return err
	}
	s.root = root
	s.cwd = filepath.Join(root, "workspace")
	s.sessionDir = filepath.Join(root, "bundle")
	if err := os.MkdirAll(s.cwd, 0o755); err != nil {
		return err
	}
	return os.MkdirAll(s.sessionDir, 0o755)
}

func (s *toolPairingFeatureState) close() {
	if s.root != "" {
		_ = os.RemoveAll(s.root)
	}
}

func (s *toolPairingFeatureState) batch() error {
	s.provider = &pairingProvider{}
	s.sender = &pairingSender{cancelOn: "pair-2"}
	s.state = &session.State{ID: "sess_pairing", CWD: s.cwd, Mode: session.ModeAgent, SessionDir: s.sessionDir}
	return nil
}

func (s *toolPairingFeatureState) stalePermission() error {
	s.provider = &pairingProvider{legacy: true}
	s.sender = &pairingSender{}
	s.state = &session.State{
		ID:         "sess_stale_permission",
		CWD:        s.cwd,
		Mode:       session.ModeAgent,
		SessionDir: s.sessionDir,
		Messages: []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
				ID:        "stale-call",
				Name:      "run_command",
				InputJSON: `{"command":"printf SHOULD_NOT_RUN"}`,
			}}},
			{Role: llm.RoleUser, Content: "newer user message"},
		},
	}
	return session.WritePendingPermission(s.sessionDir, acp.PermissionRequestParams{
		SessionID: s.state.ID,
		ToolCall:  acp.PermissionToolCall{ToolCallID: "stale-call", Status: "pending"},
	}, "run_command", `{"command":"printf SHOULD_NOT_RUN"}`)
}

func (s *toolPairingFeatureState) allowStalePermission() error {
	_, err := newPairingAgent(s.provider, s.state, s.sender).ResumeAfterPermission(
		context.Background(), "stale-call", &acp.PermissionResult{Outcome: "selected", OptionID: "allow"},
	)
	return err
}

func (s *toolPairingFeatureState) staleGateCleared() error {
	if session.PendingPermissionHeld(s.sessionDir) {
		return fmt.Errorf("stale permission gate is still held")
	}
	return nil
}

func (s *toolPairingFeatureState) newerUserRemains() error {
	msgs := s.state.GetMessages()
	if len(msgs) == 0 || msgs[len(msgs)-1].Role != llm.RoleUser || msgs[len(msgs)-1].Content != "newer user message" {
		return fmt.Errorf("newer user message was changed: %+v", msgs)
	}
	return nil
}

func (s *toolPairingFeatureState) providerReceivedNoResumeRequest() error {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	if s.provider.calls != 0 {
		return fmt.Errorf("provider received %d resume requests", s.provider.calls)
	}
	return nil
}

func (s *toolPairingFeatureState) cancelSecond() error {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		marker := filepath.Join(s.cwd, "pair-2")
		for {
			if _, err := os.Stat(marker); err == nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	defer cancel()
	s.cancelErr = nil
	ag := newPairingAgent(s.provider, s.state, s.sender)
	_, s.cancelErr = ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: "run batch"}})
	return nil
}

func (s *toolPairingFeatureState) firstTwoExecuted() error {
	for _, name := range []string{"pair-1", "pair-2"} {
		if _, err := os.Stat(filepath.Join(s.cwd, name)); err != nil {
			return fmt.Errorf("%s did not execute: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(s.cwd, "pair-3")); !os.IsNotExist(err) {
		return fmt.Errorf("third call executed: %v", err)
	}
	return nil
}

func (s *toolPairingFeatureState) cancelledBatchResults() error {
	counts := map[string]int{}
	for _, m := range s.state.GetMessages() {
		if m.Role == llm.RoleTool {
			counts[m.ToolCallID]++
		}
	}
	for _, id := range []string{"pair-1", "pair-2", "pair-3"} {
		if counts[id] != 1 {
			return fmt.Errorf("%s has %d results", id, counts[id])
		}
	}
	return nil
}

func (s *toolPairingFeatureState) followUp() error {
	_, err := newPairingAgent(s.provider, s.state, s.sender).Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "continue"}})
	return err
}

func (s *toolPairingFeatureState) followUpPaired() error {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	if len(s.provider.requests) < 2 {
		return fmt.Errorf("only %d provider requests", len(s.provider.requests))
	}
	return assertPairedBatch(s.provider.requests[len(s.provider.requests)-1], "pair-1", "pair-2", "pair-3")
}

func (s *toolPairingFeatureState) legacy() error {
	s.provider = &pairingProvider{}
	s.provider.legacy = true
	s.sender = &pairingSender{}
	s.store = &session.FileStore{Root: s.root}
	legacyDir := filepath.Join(s.root, "sess_legacy")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		return err
	}
	s.state = &session.State{ID: "sess_legacy", CWD: s.cwd, Mode: session.ModeAgent, SessionDir: legacyDir}
	s.state.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "legacy-1", Name: "read"}, {ID: "legacy-2", Name: "read"}}})
	s.state.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "legacy-1", Content: "old result"})
	if err := s.store.Save(s.state); err != nil {
		return err
	}
	var err error
	s.legacyBytes, err = os.ReadFile(filepath.Join(legacyDir, "messages.json"))
	if err != nil {
		return err
	}
	snapshot, err := s.store.ReadSnapshot(s.state.ID)
	if err != nil {
		return err
	}
	s.state = &session.State{ID: s.state.ID, CWD: s.cwd, Mode: session.ModeAgent, SessionDir: snapshot.Dir}
	s.state.ReplaceMessagesWithoutPersist(snapshot.Messages)
	return nil
}

func (s *toolPairingFeatureState) resume() error {
	_, err := newPairingAgent(s.provider, s.state, s.sender).Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "resume"}})
	return err
}

func (s *toolPairingFeatureState) legacyUnchanged() error {
	got, err := os.ReadFile(filepath.Join(s.root, "sess_legacy", "messages.json"))
	if err != nil {
		return err
	}
	if string(got) != string(s.legacyBytes) {
		return fmt.Errorf("legacy history changed on disk")
	}
	return nil
}

func (s *toolPairingFeatureState) legacyPaired() error {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	if len(s.provider.requests) == 0 {
		return fmt.Errorf("provider received no request")
	}
	return assertPairedBatch(s.provider.requests[0], "legacy-1", "legacy-2")
}

func (s *toolPairingFeatureState) succeeded() error {
	msgs := s.state.GetMessages()
	if len(msgs) == 0 || msgs[len(msgs)-1].Content != "resumed" {
		return fmt.Errorf("last message is not resumed answer: %#v", msgs)
	}
	return nil
}

func initializeToolPairingScenario(sc *godog.ScenarioContext) {
	s := &toolPairingFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) { return ctx, s.reset() })
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a batch of three shell tool calls$`, s.batch)
	sc.Step(`^a pending permission followed by a newer user message$`, s.stalePermission)
	sc.Step(`^the stale permission is allowed$`, s.allowStalePermission)
	sc.Step(`^the stale permission gate is cleared$`, s.staleGateCleared)
	sc.Step(`^the newer user message remains in history$`, s.newerUserRemains)
	sc.Step(`^the provider receives no resume request$`, s.providerReceivedNoResumeRequest)
	sc.Step(`^the second tool call cancels the turn$`, s.cancelSecond)
	sc.Step(`^only the first two tool calls execute$`, s.firstTwoExecuted)
	sc.Step(`^every call in the cancelled batch has exactly one result$`, s.cancelledBatchResults)
	sc.Step(`^the follow-up request has adjacent paired tool history$`, func() error {
		if err := s.followUp(); err != nil {
			return err
		}
		return s.followUpPaired()
	})
	sc.Step(`^a legacy history with one missing tool output$`, s.legacy)
	sc.Step(`^the agent resumes the session$`, s.resume)
	sc.Step(`^the legacy history remains unchanged on disk$`, s.legacyUnchanged)
	sc.Step(`^the resume request has adjacent paired tool history$`, s.legacyPaired)
	sc.Step(`^the resumed turn succeeds$`, s.succeeded)
}

func TestToolCallPairingFeature(t *testing.T) {
	suite := godog.TestSuite{Name: "tool-call-pairing", ScenarioInitializer: initializeToolPairingScenario, Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/tool_call_pairing.feature"}, TestingT: t, Strict: true}}
	if suite.Run() != 0 {
		t.Fatal("tool call pairing feature failed")
	}
}
