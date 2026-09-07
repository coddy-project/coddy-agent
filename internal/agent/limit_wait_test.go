package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type limitWaitCapture struct {
	resumePermissionSender
	mu      sync.Mutex
	updates []acp.ProviderUsageUpdate
}

func (c *limitWaitCapture) SendSessionUpdate(_ string, update interface{}) error {
	if u, ok := update.(acp.ProviderUsageUpdate); ok {
		c.mu.Lock()
		c.updates = append(c.updates, u)
		c.mu.Unlock()
	}
	return nil
}

func (c *limitWaitCapture) snapshot() []acp.ProviderUsageUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]acp.ProviderUsageUpdate(nil), c.updates...)
}

func limitWaitAgent(t *testing.T, on bool, maxMS *int) (*Agent, *limitWaitCapture) {
	t.Helper()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "neuraldeep", Type: "neuraldeep", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "neuraldeep/qwen3.8-27b", MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "neuraldeep/qwen3.8-27b", WaitForLimitReset: on, WaitForLimitResetMaxMS: maxMS},
	}
	cfg.Agent.ApplyDefaults()
	state := &session.State{ID: "sess_limit_wait_unit", CWD: t.TempDir(), Mode: session.ModeAgent}
	sender := &limitWaitCapture{}
	return NewAgent(cfg, state, sender, nil), sender
}

func quotaReset(pause time.Duration) *llm.QuotaResetError {
	return &llm.QuotaResetError{ResetAt: time.Now().Add(pause), Delay: pause, Cause: errors.New("429")}
}

// The guard: only a top-level turn with the option on, nothing streamed and
// the pause inside what is left of the turn's maximum waits.
func TestLimitResetToWaitForGuards(t *testing.T) {
	maxMS := 5000
	ag, _ := limitWaitAgent(t, true, &maxMS)
	reset := quotaReset(time.Second)
	if _, ok := ag.limitResetToWaitFor(reset, nil, "", false); !ok {
		t.Fatal("a plain reset on a top-level turn must be waited for")
	}
	if _, ok := ag.limitResetToWaitFor(errors.New("500 boom"), nil, "", false); ok {
		t.Fatal("an ordinary error is not a reset")
	}
	if _, ok := ag.limitResetToWaitFor(reset, nil, "", true); ok {
		t.Fatal("a call that already streamed is never re-issued")
	}
	if _, ok := ag.limitResetToWaitFor(reset, nil, "thinking...", false); ok {
		t.Fatal("buffered reasoning counts as streamed output")
	}
	if _, ok := ag.limitResetToWaitFor(reset, &llm.Response{Content: "partial"}, "", false); ok {
		t.Fatal("a partial answer counts as streamed output")
	}
	// The ledger is a total per turn: what the wrapper charged for its
	// sleeps (a call that succeeded after one included) and earlier waits.
	ag.limitLedgerFor().Charge(4500 * time.Millisecond)
	if _, ok := ag.limitResetToWaitFor(reset, nil, "", false); ok {
		t.Fatal("the maximum is a total per turn: 4.5 s spent plus 1 s exceeds 5 s")
	}
	ag.limitLedger = &limitWaitLedger{}
	ag.subagent = &session.SubagentMeta{Depth: 1}
	if _, ok := ag.limitResetToWaitFor(reset, nil, "", false); ok {
		t.Fatal("a subagent's turn fails fast")
	}
	ag.subagent = nil
	off, _ := limitWaitAgent(t, false, nil)
	if _, ok := off.limitResetToWaitFor(reset, nil, "", false); ok {
		t.Fatal("off by default")
	}
	zero := 0
	never, _ := limitWaitAgent(t, true, &zero)
	if _, ok := never.limitResetToWaitFor(reset, nil, "", false); ok {
		t.Fatal("an explicit 0 never waits")
	}
}

// The wrapper the agent builds shares the turn's ledger only with the wait
// on, and a new Run starts the account afresh.
func TestLLMProviderInputSharesTheTurnLedger(t *testing.T) {
	on, _ := limitWaitAgent(t, true, nil)
	in := on.turnProviderInput(&config.ResolvedLLM{ProviderType: "neuraldeep", Model: "m", Stream: true})
	if in.LimitLedger == nil || in.LimitLedger != llm.LimitLedger(on.limitLedgerFor()) {
		t.Fatal("with the wait on the wrapper must charge the turn's ledger")
	}
	on.limitLedgerFor().Charge(time.Second)
	on.limitLedger = &limitWaitLedger{}
	if on.limitLedgerFor().Spent() != 0 {
		t.Fatal("a fresh ledger starts at zero")
	}
	off, _ := limitWaitAgent(t, false, nil)
	if in := off.turnProviderInput(&config.ResolvedLLM{ProviderType: "neuraldeep", Model: "m", Stream: true}); in.LimitLedger != nil {
		t.Fatal("with the wait off the wrapper keeps its per-call accounting")
	}
}

// The wait sends the countdown at once and on every heartbeat, and a cancel
// ends it with the context's error.
func TestWaitForLimitResetHeartbeatsAndCancel(t *testing.T) {
	ag, sender := limitWaitAgent(t, true, nil)
	ag.limitWaitHeartbeat = 20 * time.Millisecond
	if err := ag.waitForLimitReset(context.Background(), "sess_limit_wait_unit", quotaReset(110*time.Millisecond)); err != nil {
		t.Fatalf("wait: %v", err)
	}
	updates := sender.snapshot()
	if len(updates) < 3 {
		t.Fatalf("want the first update plus heartbeats, got %d", len(updates))
	}
	for i, u := range updates {
		if !u.Resuming || !u.Blocked || u.Provider != "neuraldeep" || u.ProviderType != "neuraldeep" || u.RetryAt == "" {
			t.Fatalf("update %d is incomplete: %+v", i, u)
		}
		if i > 0 && u.RetryInSec > updates[i-1].RetryInSec {
			t.Fatalf("the countdown must not grow: %d after %d", u.RetryInSec, updates[i-1].RetryInSec)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := ag.waitForLimitReset(ctx, "sess_limit_wait_unit", quotaReset(time.Hour))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled turn must end the wait with the context's error, got %v", err)
	}
}

// The wrapper's bounds: the first-token timeout as the call's own budget
// for streamed transports, the wait's maximum as the turn's budget when the
// option is on, nothing otherwise; helpers get neither.
func TestLLMProviderInputRetryBudget(t *testing.T) {
	ag, _ := limitWaitAgent(t, false, nil)
	in := ag.turnProviderInput(&config.ResolvedLLM{ProviderType: "neuraldeep", Model: "m", Stream: true})
	if in.CallBudget != 90*time.Second || in.RetryBudgetSet {
		t.Fatalf("streamed, wait off: call budget %v, turn budget set %v; want the 90 s timer and no turn budget", in.CallBudget, in.RetryBudgetSet)
	}
	if in := ag.turnProviderInput(&config.ResolvedLLM{ProviderType: "neuraldeep", Model: "m", Stream: false}); in.CallBudget != 0 || in.RetryBudgetSet {
		t.Fatalf("blocking, wait off: %+v, want no bounds", in)
	}
	maxMS := 300
	on, _ := limitWaitAgent(t, true, &maxMS)
	in = on.turnProviderInput(&config.ResolvedLLM{ProviderType: "neuraldeep", Model: "m", Stream: true})
	if in.CallBudget != 90*time.Second || !in.RetryBudgetSet || in.RetryBudget != 300*time.Millisecond {
		t.Fatalf("streamed, wait on: %+v, want the timer and the 300 ms maximum", in)
	}
	if in := on.turnProviderInput(&config.ResolvedLLM{ProviderType: "neuraldeep", Model: "m", Stream: false}); in.CallBudget != 0 || !in.RetryBudgetSet || in.RetryBudget != 300*time.Millisecond {
		t.Fatalf("blocking, wait on: %+v, want the 300 ms maximum alone", in)
	}
	zero := 0
	never, _ := limitWaitAgent(t, true, &zero)
	if in := never.turnProviderInput(&config.ResolvedLLM{ProviderType: "neuraldeep", Model: "m", Stream: true}); !in.RetryBudgetSet || in.RetryBudget != 0 {
		t.Fatalf("an explicit 0: %+v, want a set zero so the wrapper never sleeps on a limit", in)
	}
	if in := on.llmProviderInput(&config.ResolvedLLM{ProviderType: "neuraldeep", Model: "m", Stream: true}); in.CallBudget != 0 || in.RetryBudgetSet || in.LimitLedger != nil {
		t.Fatalf("a helper's provider input carries no bound and no ledger: %+v", in)
	}
}
