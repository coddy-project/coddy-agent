package agent

// Godog harness for features/llm_limit_wait.feature: a real Agent turn over
// a fake provider whose first call fails with a QuotaResetError (the typed
// error the resilient wrapper returns for a pause beyond the retry budget)
// and whose second call answers. The scenarios pin the opt-in wait, the
// default fail-fast, and the configured maximum.

import (
	"context"
	"errors"
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

// limitWaitProvider fails its first calls (fails of them) with a limit and
// answers the next one; every call is counted. With raw set the limit is the
// provider's own 429 text naming the pause, for the retry wrapper to judge;
// otherwise it is the wrapper's typed verdict, handed over directly.
type limitWaitProvider struct {
	mu    sync.Mutex
	calls int
	fails int
	raw   bool
	pause time.Duration
	reply string
	// script, when set, names each call's answer in order: "limit" (a raw
	// 429 naming the pause), "empty" (an answer the loop nudges past), or
	// "reply"; calls past its end reply.
	script []string
}

func (p *limitWaitProvider) next() (*llm.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if len(p.script) > 0 {
		step := "reply"
		if p.calls <= len(p.script) {
			step = p.script[p.calls-1]
		}
		switch step {
		case "limit":
			return nil, fmt.Errorf("openai stream: 429 Too Many Requests: rate limit exceeded, retry in %ds", int(p.pause/time.Second))
		case "limit-unnamed":
			return nil, errors.New("openai stream: 429 Too Many Requests: rate limit exceeded")
		case "empty":
			return &llm.Response{Content: "", StopReason: "end_turn"}, nil
		default:
			return &llm.Response{Content: p.reply, StopReason: "end_turn"}, nil
		}
	}
	if p.calls <= p.fails {
		if p.raw {
			return nil, fmt.Errorf("openai stream: 429 Too Many Requests: rate limit exceeded, retry in %ds", int(p.pause/time.Second))
		}
		return nil, &llm.QuotaResetError{
			ResetAt: time.Now().Add(p.pause),
			Delay:   p.pause,
			Cause:   errors.New("429 Too Many Requests: limit reached"),
		}
	}
	return &llm.Response{Content: p.reply, StopReason: "end_turn"}, nil
}

func (p *limitWaitProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *limitWaitProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return p.next()
}

func (p *limitWaitProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	resp, err := p.next()
	if err == nil && onChunk != nil {
		onChunk(llm.StreamChunk{TextDelta: resp.Content})
	}
	return resp, err
}

// limitWaitSender records every provider_usage update the turn sends.
type limitWaitSender struct {
	resumePermissionSender
	mu      sync.Mutex
	updates []acp.ProviderUsageUpdate
}

func (s *limitWaitSender) SendSessionUpdate(_ string, update interface{}) error {
	if u, ok := update.(acp.ProviderUsageUpdate); ok {
		s.mu.Lock()
		s.updates = append(s.updates, u)
		s.mu.Unlock()
	}
	return nil
}

func (s *limitWaitSender) resuming() []acp.ProviderUsageUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []acp.ProviderUsageUpdate
	for _, u := range s.updates {
		if u.Resuming {
			out = append(out, u)
		}
	}
	return out
}

type limitWaitState struct {
	provider   *limitWaitProvider
	viaWrapper bool
	wrapperCap time.Duration
	sender     *limitWaitSender
	cfg        *config.Config
	state      *session.State
	tmp        []string

	started time.Time
	took    time.Duration
	reply   string
	stop    string
	runErr  error
}

func (s *limitWaitState) reset() error {
	s.provider = nil
	s.viaWrapper = false
	s.wrapperCap = 100 * time.Millisecond
	s.sender = &limitWaitSender{}
	s.cfg = nil
	s.state = nil
	s.took = 0
	s.reply = ""
	s.stop = ""
	s.runErr = nil
	return nil
}

func (s *limitWaitState) close() {
	for _, d := range s.tmp {
		_ = os.RemoveAll(d)
	}
	s.tmp = nil
}

func (s *limitWaitState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-limit-wait-")
	if err != nil {
		return "", err
	}
	s.tmp = append(s.tmp, d)
	return d, nil
}

func (s *limitWaitState) anAgentWhoseProviderFirstReportsALimit(pauseSec int, reply string) error {
	return s.agentOver(&limitWaitProvider{fails: 1, pause: time.Duration(pauseSec) * time.Second, reply: reply})
}

func (s *limitWaitState) anAgentWhoseProviderReportsALimitTwice(pauseSec int, reply string) error {
	return s.agentOver(&limitWaitProvider{fails: 2, pause: time.Duration(pauseSec) * time.Second, reply: reply})
}

func (s *limitWaitState) anAgentWhoseProviderAnswersA429ThroughTheWrapper(pauseSec int, reply string) error {
	s.viaWrapper = true
	return s.agentOver(&limitWaitProvider{fails: 1, raw: true, pause: time.Duration(pauseSec) * time.Second, reply: reply})
}

func (s *limitWaitState) anAgentWhoseProviderSleepsThenSucceedsThenLimitsAgain(pauseSec int, reply string) error {
	s.viaWrapper = true
	s.wrapperCap = time.Second
	// A 429 the wrapper sleeps through, an answer the loop nudges past (so
	// the turn goes on to another model call), then a 429 again.
	return s.agentOver(&limitWaitProvider{raw: true, pause: time.Duration(pauseSec) * time.Second, reply: reply,
		script: []string{"limit", "empty", "limit"}})
}

func (s *limitWaitState) anAgentWhoseProviderKeepsAnsweringUnnamed429s(reply string) error {
	s.viaWrapper = true
	// Four unnamed 429s outlast the wrapper's three retries.
	return s.agentOver(&limitWaitProvider{raw: true, reply: reply,
		script: []string{"limit-unnamed", "limit-unnamed", "limit-unnamed", "limit-unnamed"}})
}

func (s *limitWaitState) theTurnFailsWithTheProviderErrorAfterCalls(calls int) error {
	if s.runErr == nil {
		return fmt.Errorf("turn succeeded with %q, want the provider's error", s.reply)
	}
	var reset *llm.QuotaResetError
	if errors.As(s.runErr, &reset) {
		return fmt.Errorf("turn failed with a quota reset (%v), want the provider's own error", s.runErr)
	}
	if !strings.Contains(s.runErr.Error(), "429") {
		return fmt.Errorf("turn failed with %v, want the provider's 429", s.runErr)
	}
	if got := s.provider.count(); got != calls {
		return fmt.Errorf("provider calls = %d, want %d", got, calls)
	}
	return nil
}

func (s *limitWaitState) anAgentWhoseProviderAnswersA429TwiceThroughTheWrapper(pauseSec int, reply string) error {
	s.viaWrapper = true
	// A ladder that can sleep the first pause, so the wrapper spends time
	// before its verdict and that time counts against the turn's maximum.
	s.wrapperCap = time.Second
	return s.agentOver(&limitWaitProvider{fails: 2, raw: true, pause: time.Duration(pauseSec) * time.Second, reply: reply})
}

func (s *limitWaitState) agentOver(p *limitWaitProvider) error {
	cwd, err := s.tempDir()
	if err != nil {
		return err
	}
	sessionDir, err := s.tempDir()
	if err != nil {
		return err
	}
	s.provider = p
	s.state = &session.State{ID: "sess_bdd_limit_wait", CWD: cwd, Mode: session.ModeAgent, SessionDir: sessionDir}
	s.cfg = &config.Config{
		Providers: []config.ProviderConfig{{Name: "neuraldeep", Type: "neuraldeep", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "neuraldeep/qwen3.8-27b", MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "neuraldeep/qwen3.8-27b"},
	}
	s.cfg.Agent.ApplyDefaults()
	s.cfg.Prompts.ApplyDefaults()
	return nil
}

func (s *limitWaitState) waitIsOn() error {
	s.cfg.Agent.WaitForLimitReset = true
	return nil
}

func (s *limitWaitState) waitIsOnWithAMaximumOf(maxMS int) error {
	s.cfg.Agent.WaitForLimitReset = true
	s.cfg.Agent.WaitForLimitResetMaxMS = &maxMS
	return nil
}

func (s *limitWaitState) theUserSendsATurnAndStopsItWhileItWaits() error {
	ag := s.agent()
	s.started = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.stop, s.runErr = ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: "hello"}})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(s.sender.resuming()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(s.sender.resuming()) == 0 {
		return fmt.Errorf("the turn never started waiting")
	}
	// Esc / Stop: the manager marks the turn as the user's cancel and
	// cancels the turn context.
	s.state.SetUserCancelledTurn()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		return fmt.Errorf("the turn did not end after the stop")
	}
	s.took = time.Since(s.started)
	return nil
}

func (s *limitWaitState) theUsersClientGoesAwayWhileItWaits() error {
	ag := s.agent()
	s.started = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.stop, s.runErr = ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: "hello"}})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(s.sender.resuming()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(s.sender.resuming()) == 0 {
		return fmt.Errorf("the turn never started waiting")
	}
	// Not the user's Stop: a shutdown or a dropped client cancels the turn
	// context without marking the turn as the user's cancel.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		return fmt.Errorf("the turn did not end after the cancel")
	}
	s.took = time.Since(s.started)
	return nil
}

func (s *limitWaitState) theErrorNamesTheInterruptedWait() error {
	if s.runErr == nil || !strings.Contains(s.runErr.Error(), "the wait for the reset was interrupted") {
		return fmt.Errorf("turn error %v, want it to name the interrupted wait", s.runErr)
	}
	return nil
}

func (s *limitWaitState) theTurnEndsAsCancelledAfterCalls(calls int) error {
	if s.runErr != nil || s.stop != string(acp.StopReasonCancelled) {
		return fmt.Errorf("turn ended with %q / %v, want cancelled and no error", s.stop, s.runErr)
	}
	if got := s.provider.count(); got != calls {
		return fmt.Errorf("provider calls = %d, want %d", got, calls)
	}
	return nil
}

func (s *limitWaitState) agent() *Agent {
	ag := NewAgent(s.cfg, s.state, s.sender, nil)
	ag.providerFactory = func(in llm.ProviderInput) (llm.Provider, error) {
		if !s.viaWrapper {
			return s.provider, nil
		}
		// The wrapper as the agent configures it, with a ladder the
		// scenario sizes: 100 ms per wait leaves a one-second pause beyond
		// what the retries could wait, one second lets them sleep it.
		return llm.WrapResilient(s.provider, llm.ResilientOptions{
			RetryMax:       in.RetryMax,
			RetryBase:      5 * time.Millisecond,
			RetryMaxDelay:  s.wrapperCap,
			CallBudget:     in.CallBudget,
			RetryBudget:    in.RetryBudget,
			RetryBudgetSet: in.RetryBudgetSet,
			Ledger:         in.LimitLedger,
		}), nil
	}
	return ag
}

func (s *limitWaitState) theUserSendsATurn() error {
	ag := s.agent()
	s.started = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s.stop, s.runErr = ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: "hello"}})
	s.took = time.Since(s.started)
	for _, m := range s.state.Messages {
		if m.Role == llm.RoleAssistant {
			s.reply = m.Content
		}
	}
	return nil
}

func (s *limitWaitState) theTurnEndsWithAfterCalls(reply string, calls int) error {
	if s.runErr != nil {
		return fmt.Errorf("turn failed: %v", s.runErr)
	}
	if strings.TrimSpace(s.reply) != reply {
		return fmt.Errorf("assistant reply = %q, want %q", s.reply, reply)
	}
	if got := s.provider.count(); got != calls {
		return fmt.Errorf("provider calls = %d, want %d", got, calls)
	}
	return nil
}

func (s *limitWaitState) theTurnTookAtLeast(sec int) error {
	if s.took < time.Duration(sec)*time.Second {
		return fmt.Errorf("turn took %v, want at least %ds", s.took, sec)
	}
	return nil
}

func (s *limitWaitState) theTurnTookLessThan(ms int) error {
	if s.took >= time.Duration(ms)*time.Millisecond {
		return fmt.Errorf("turn took %v, want less than %dms", s.took, ms)
	}
	return nil
}

func (s *limitWaitState) theClientSawAResumingUpdate() error {
	updates := s.sender.resuming()
	if len(updates) == 0 {
		return fmt.Errorf("no resuming provider_usage update reached the client")
	}
	u := updates[0]
	if !u.Blocked || u.RetryAt == "" || u.Provider != "neuraldeep" || u.ProviderType != "neuraldeep" {
		return fmt.Errorf("resuming update is incomplete: %+v", u)
	}
	at, err := time.Parse(time.RFC3339, u.RetryAt)
	if err != nil {
		return fmt.Errorf("retryAt %q: %v", u.RetryAt, err)
	}
	if at.Before(s.started) || at.After(s.started.Add(5*time.Second)) {
		return fmt.Errorf("retryAt %v is not the provider's reset time (turn started %v)", at, s.started)
	}
	return nil
}

func (s *limitWaitState) theClientSawNoResumingUpdate() error {
	if n := len(s.sender.resuming()); n != 0 {
		return fmt.Errorf("client saw %d resuming updates, want none", n)
	}
	return nil
}

func (s *limitWaitState) theTurnFailsWithTheQuotaResetErrorAfterCalls(calls int) error {
	if s.runErr == nil {
		return fmt.Errorf("turn succeeded with %q, want the quota reset error", s.reply)
	}
	var reset *llm.QuotaResetError
	if !errors.As(s.runErr, &reset) {
		return fmt.Errorf("turn failed with %v, want a QuotaResetError", s.runErr)
	}
	if got := s.provider.count(); got != calls {
		return fmt.Errorf("provider calls = %d, want %d", got, calls)
	}
	return nil
}

func initializeLimitWaitScenario(sc *godog.ScenarioContext) {
	s := &limitWaitState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^an agent whose provider first reports a limit that lifts in (\d+) s and then answers "([^"]+)"$`, s.anAgentWhoseProviderFirstReportsALimit)
	sc.Step(`^an agent whose provider reports a limit that lifts in (\d+) s twice and then answers "([^"]+)"$`, s.anAgentWhoseProviderReportsALimitTwice)
	sc.Step(`^an agent whose provider answers a 429 naming a reset in (\d+) s through the retry wrapper and then answers "([^"]+)"$`, s.anAgentWhoseProviderAnswersA429ThroughTheWrapper)
	sc.Step(`^an agent whose provider answers a 429 naming a reset in (\d+) s twice through the retry wrapper and then answers "([^"]+)"$`, s.anAgentWhoseProviderAnswersA429TwiceThroughTheWrapper)
	sc.Step(`^an agent whose provider sleeps through a 429 naming a reset in (\d+) s, answers nothing, hits the limit again and then answers "([^"]+)"$`, s.anAgentWhoseProviderSleepsThenSucceedsThenLimitsAgain)
	sc.Step(`^an agent whose provider keeps answering 429 without naming a pause and would then answer "([^"]+)"$`, s.anAgentWhoseProviderKeepsAnsweringUnnamed429s)
	sc.Step(`^the turn fails with the provider's error after (\d+) provider calls?$`, s.theTurnFailsWithTheProviderErrorAfterCalls)
	sc.Step(`^wait_for_limit_reset is on$`, s.waitIsOn)
	sc.Step(`^wait_for_limit_reset is on with a maximum of (\d+) ms$`, s.waitIsOnWithAMaximumOf)
	sc.Step(`^the user sends a turn$`, s.theUserSendsATurn)
	sc.Step(`^the user sends a turn and stops it while it waits$`, s.theUserSendsATurnAndStopsItWhileItWaits)
	sc.Step(`^the user sends a turn and the client goes away while it waits$`, s.theUsersClientGoesAwayWhileItWaits)
	sc.Step(`^the error names the interrupted wait$`, s.theErrorNamesTheInterruptedWait)
	sc.Step(`^the turn ends as cancelled after (\d+) provider calls?$`, s.theTurnEndsAsCancelledAfterCalls)
	sc.Step(`^the turn ends with "([^"]+)" after (\d+) provider calls$`, s.theTurnEndsWithAfterCalls)
	sc.Step(`^the turn took at least (\d+) s$`, s.theTurnTookAtLeast)
	sc.Step(`^the turn took less than (\d+) ms$`, s.theTurnTookLessThan)
	sc.Step(`^the client saw a resuming usage update with the reset time$`, s.theClientSawAResumingUpdate)
	sc.Step(`^the client saw no resuming usage update$`, s.theClientSawNoResumingUpdate)
	sc.Step(`^the turn fails with the quota reset error after (\d+) provider calls?$`, s.theTurnFailsWithTheQuotaResetErrorAfterCalls)
}

func TestLLMLimitWaitFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "llm_limit_wait",
		ScenarioInitializer: initializeLimitWaitScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/llm_limit_wait.feature"},
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("llm_limit_wait feature failed")
	}
}
