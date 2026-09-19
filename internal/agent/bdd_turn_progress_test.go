package agent

// Godog harness for features/turn_progress.feature: a real Agent turn over a
// scripted provider that stays silent, streams slowly, calls a tool or reports
// no usage, and a sender that keeps every update in the order it was sent.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// progressCall is one scripted LLM call.
type progressCall struct {
	silence  time.Duration // before the first chunk
	text     string        // streamed as text deltas
	over     time.Duration // how long the text takes to stream
	tool     *llm.ToolCall // announced after the text, when set
	outUsage int           // provider-reported output tokens; 0 reports none
}

type progressProvider struct {
	mu    sync.Mutex
	calls []progressCall
	next  int
}

func (p *progressProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("the turn progress harness streams")
}

func (p *progressProvider) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.mu.Lock()
	if p.next >= len(p.calls) {
		p.mu.Unlock()
		return nil, fmt.Errorf("the script has no call %d", p.next+1)
	}
	call := p.calls[p.next]
	p.next++
	p.mu.Unlock()

	if !sleepCtx(ctx, call.silence) {
		return nil, ctx.Err()
	}
	const pieces = 12
	runes := []rune(call.text)
	for i := 0; i < pieces && len(runes) > 0; i++ {
		from, to := i*len(runes)/pieces, (i+1)*len(runes)/pieces
		if from == to {
			continue
		}
		onChunk(llm.StreamChunk{TextDelta: string(runes[from:to])})
		if !sleepCtx(ctx, call.over/pieces) {
			return nil, ctx.Err()
		}
	}
	resp := &llm.Response{Content: call.text, StopReason: "end_turn", OutputTokens: call.outUsage}
	if call.tool != nil {
		onChunk(llm.StreamChunk{ToolCall: call.tool})
		resp.ToolCalls = []llm.ToolCall{*call.tool}
		resp.StopReason = "tool_use"
	}
	return resp, nil
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// progressSender keeps every update with the order it arrived in.
type progressSender struct {
	resumePermissionSender
	mu      sync.Mutex
	updates []interface{}
}

func (s *progressSender) SendSessionUpdate(_ string, update interface{}) error {
	s.mu.Lock()
	s.updates = append(s.updates, update)
	s.mu.Unlock()
	return nil
}

// progress returns the turn_progress updates with their positions in the
// stream of all updates.
func (s *progressSender) progress() (updates []acp.TurnProgressUpdate, at []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, u := range s.updates {
		if p, ok := u.(acp.TurnProgressUpdate); ok {
			updates = append(updates, p)
			at = append(at, i)
		}
	}
	return updates, at
}

// textSpan returns the positions of the first and the last text delta.
func (s *progressSender) textSpan() (first, last int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	first, last = -1, -1
	for i, u := range s.updates {
		chunk, ok := u.(acp.MessageChunkUpdate)
		if !ok || chunk.SessionUpdate != acp.UpdateTypeAgentMessageChunk || chunk.Content.Type != acp.ContentTypeText {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	return first, last
}

type turnProgressBDD struct {
	provider *progressProvider
	sender   *progressSender
	state    *session.State
	tmp      []string
	began    time.Time
	runErr   error
}

func (s *turnProgressBDD) reset() {
	s.provider = nil
	s.sender = &progressSender{}
	s.state = nil
	s.runErr = nil
}

func (s *turnProgressBDD) close() {
	for _, d := range s.tmp {
		_ = os.RemoveAll(d)
	}
	s.tmp = nil
}

func (s *turnProgressBDD) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-turn-progress-")
	if err != nil {
		return "", err
	}
	s.tmp = append(s.tmp, d)
	return d, nil
}

func (s *turnProgressBDD) script(calls ...progressCall) error {
	s.provider = &progressProvider{calls: calls}
	return nil
}

func (s *turnProgressBDD) aSilentModel(silenceMS int, reply string, tokens int) error {
	return s.script(progressCall{silence: time.Duration(silenceMS) * time.Millisecond, text: reply, outUsage: tokens})
}

func (s *turnProgressBDD) aStreamingModel(chars, overMS, tokens int) error {
	return s.script(progressCall{text: strings.Repeat("a", chars), over: time.Duration(overMS) * time.Millisecond, outUsage: tokens})
}

func (s *turnProgressBDD) aToolCallingModel(first, second int) error {
	tool := &llm.ToolCall{ID: "call_1", Name: "glob", InputJSON: `{"pattern":"**/*.nothing"}`}
	return s.script(
		progressCall{tool: tool, outUsage: first},
		progressCall{text: "done", outUsage: second},
	)
}

func (s *turnProgressBDD) aModelWithoutUsage(chars int) error {
	return s.script(progressCall{text: strings.Repeat("a", chars)})
}

func (s *turnProgressBDD) theUserSendsATurn() error {
	cwd, err := s.tempDir()
	if err != nil {
		return err
	}
	sessionDir, err := s.tempDir()
	if err != nil {
		return err
	}
	s.state = &session.State{ID: "sess_bdd_turn_progress", CWD: cwd, Mode: session.ModeAgent, SessionDir: sessionDir}
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	ag := NewAgent(cfg, s.state, s.sender, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil }

	// The manager stamps the start when it admits the turn; the harness plays
	// its part so the scenarios can tell the admitted start from a later one.
	s.began = time.Now().Add(-2 * time.Second).UTC().Truncate(time.Millisecond)
	s.state.BeginTurnProgress(s.began)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, s.runErr = ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: "hello"}})
	return s.runErr
}

func (s *turnProgressBDD) theFirstUpdateCarriesTheStartAndNoTokens() error {
	updates, _ := s.sender.progress()
	if len(updates) == 0 {
		return fmt.Errorf("the turn sent no turn_progress update")
	}
	first := updates[0]
	if first.OutputTokens != 0 {
		return fmt.Errorf("first update carries %d tokens, want none", first.OutputTokens)
	}
	if first.StartedAt != s.began.Format(time.RFC3339Nano) {
		return fmt.Errorf("first update started at %q, want %q", first.StartedAt, s.began.Format(time.RFC3339Nano))
	}
	if first.ElapsedMs < 2000 {
		return fmt.Errorf("first update reports %d ms elapsed, want the 2 s since admission", first.ElapsedMs)
	}
	return nil
}

func (s *turnProgressBDD) thatUpdateCameBeforeTheFirstText() error {
	_, at := s.sender.progress()
	first, _ := s.sender.textSpan()
	if first < 0 {
		return fmt.Errorf("the answer never streamed")
	}
	if at[0] > first {
		return fmt.Errorf("first progress update at %d, after the first text at %d", at[0], first)
	}
	return nil
}

func (s *turnProgressBDD) anUpdateDuringTheStreamIsEstimated() error {
	updates, at := s.sender.progress()
	first, last := s.sender.textSpan()
	for i, u := range updates {
		if at[i] > first && at[i] < last && u.Estimated && u.OutputTokens > 0 {
			return nil
		}
	}
	return fmt.Errorf("no estimated update between the first and the last text delta: %+v", updates)
}

func (s *turnProgressBDD) theLastUpdateCarries(tokens int, kind string) error {
	updates, _ := s.sender.progress()
	if len(updates) == 0 {
		return fmt.Errorf("the turn sent no turn_progress update")
	}
	last := updates[len(updates)-1]
	if last.OutputTokens != tokens {
		return fmt.Errorf("last update carries %d tokens, want %d", last.OutputTokens, tokens)
	}
	if want := kind == "an estimate"; last.Estimated != want {
		return fmt.Errorf("last update estimated = %v, want %v", last.Estimated, want)
	}
	return nil
}

func (s *turnProgressBDD) everyUpdateCarriesTheSameStart() error {
	updates, _ := s.sender.progress()
	for _, u := range updates {
		if u.StartedAt != updates[0].StartedAt {
			return fmt.Errorf("start moved from %q to %q within one turn", updates[0].StartedAt, u.StartedAt)
		}
	}
	return nil
}

func (s *turnProgressBDD) theStateHoldsTheProgress(tokens int) error {
	got, ok := s.state.TurnProgress()
	if !ok {
		return fmt.Errorf("the session state holds no turn progress")
	}
	if !got.StartedAt.Equal(s.began) {
		return fmt.Errorf("state start = %v, want %v", got.StartedAt, s.began)
	}
	if got.OutputTokens != tokens || got.Estimated {
		return fmt.Errorf("state progress = %+v, want %d exact tokens", got, tokens)
	}
	return nil
}

func initializeTurnProgressScenario(sc *godog.ScenarioContext) {
	s := &turnProgressBDD{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^an agent whose model stays silent for (\d+) ms and then answers "([^"]+)" reporting (\d+) output tokens$`, s.aSilentModel)
	sc.Step(`^an agent whose model streams (\d+) characters over (\d+) ms and reports (\d+) output tokens$`, s.aStreamingModel)
	sc.Step(`^an agent whose model calls a tool reporting (\d+) output tokens and then answers reporting (\d+) output tokens$`, s.aToolCallingModel)
	sc.Step(`^an agent whose model answers (\d+) characters and reports no usage$`, s.aModelWithoutUsage)
	sc.Step(`^the user sends a turn$`, s.theUserSendsATurn)
	sc.Step(`^the first progress update carries the turn start and no tokens$`, s.theFirstUpdateCarriesTheStartAndNoTokens)
	sc.Step(`^that update was sent before the first text of the answer$`, s.thatUpdateCameBeforeTheFirstText)
	sc.Step(`^a progress update sent while the answer streamed carries an estimated token count above zero$`, s.anUpdateDuringTheStreamIsEstimated)
	sc.Step(`^the last progress update carries (\d+) tokens and is (not an estimate|an estimate)$`, s.theLastUpdateCarries)
	sc.Step(`^every progress update carries the same turn start$`, s.everyUpdateCarriesTheSameStart)
	sc.Step(`^the session state holds the turn start and (\d+) output tokens$`, s.theStateHoldsTheProgress)
}

func TestTurnProgressFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "turn_progress",
		ScenarioInitializer: initializeTurnProgressScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/turn_progress.feature"},
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("turn_progress feature failed")
	}
}
