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
	if _, ok := ag.limitResetToWaitFor(reset, nil, "", false, limitWaitLedger{}); !ok {
		t.Fatal("a plain reset on a top-level turn must be waited for")
	}
	if _, ok := ag.limitResetToWaitFor(errors.New("500 boom"), nil, "", false, limitWaitLedger{}); ok {
		t.Fatal("an ordinary error is not a reset")
	}
	if _, ok := ag.limitResetToWaitFor(reset, nil, "", true, limitWaitLedger{}); ok {
		t.Fatal("a call that already streamed is never re-issued")
	}
	if _, ok := ag.limitResetToWaitFor(reset, nil, "thinking...", false, limitWaitLedger{}); ok {
		t.Fatal("buffered reasoning counts as streamed output")
	}
	if _, ok := ag.limitResetToWaitFor(reset, &llm.Response{Content: "partial"}, "", false, limitWaitLedger{}); ok {
		t.Fatal("a partial answer counts as streamed output")
	}
	if _, ok := ag.limitResetToWaitFor(reset, nil, "", false, limitWaitLedger{waited: 4500 * time.Millisecond}); ok {
		t.Fatal("the maximum is a total per turn: 4.5 s spent plus 1 s exceeds 5 s")
	}
	slept := quotaReset(time.Second)
	slept.Elapsed = 4500 * time.Millisecond
	if _, ok := ag.limitResetToWaitFor(slept, nil, "", false, limitWaitLedger{}); ok {
		t.Fatal("the wrapper's own sleeps on the call count: 4.5 s slept plus 1 s exceeds 5 s")
	}
	ag.subagent = &session.SubagentMeta{Depth: 1}
	if _, ok := ag.limitResetToWaitFor(reset, nil, "", false, limitWaitLedger{}); ok {
		t.Fatal("a subagent's turn fails fast")
	}
	ag.subagent = nil
	off, _ := limitWaitAgent(t, false, nil)
	if _, ok := off.limitResetToWaitFor(reset, nil, "", false, limitWaitLedger{}); ok {
		t.Fatal("off by default")
	}
	zero := 0
	never, _ := limitWaitAgent(t, true, &zero)
	if _, ok := never.limitResetToWaitFor(reset, nil, "", false, limitWaitLedger{}); ok {
		t.Fatal("an explicit 0 never waits")
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

// The wrapper's budget: the first-token timeout for streamed transports,
// the wait's maximum on top when the option is on, nothing otherwise.
func TestLLMProviderInputRetryBudget(t *testing.T) {
	ag, _ := limitWaitAgent(t, false, nil)
	if in := ag.llmProviderInput(&config.ResolvedLLM{ProviderType: "neuraldeep", Model: "m", Stream: true}); !in.RetryBudgetSet || in.RetryBudget != 90*time.Second {
		t.Fatalf("streamed: budget %v (set %v), want the 90 s first-token timeout", in.RetryBudget, in.RetryBudgetSet)
	}
	if in := ag.llmProviderInput(&config.ResolvedLLM{ProviderType: "neuraldeep", Model: "m", Stream: false}); in.RetryBudgetSet {
		t.Fatalf("blocking: budget %v set, want the ladder alone", in.RetryBudget)
	}
	maxMS := 300
	on, _ := limitWaitAgent(t, true, &maxMS)
	if in := on.llmProviderInput(&config.ResolvedLLM{ProviderType: "neuraldeep", Model: "m", Stream: true}); !in.RetryBudgetSet || in.RetryBudget != 300*time.Millisecond {
		t.Fatalf("with the wait on: budget %v, want its 300 ms maximum", in.RetryBudget)
	}
	if in := on.llmProviderInput(&config.ResolvedLLM{ProviderType: "neuraldeep", Model: "m", Stream: false}); !in.RetryBudgetSet || in.RetryBudget != 300*time.Millisecond {
		t.Fatalf("with the wait on, blocking: budget %v, want its 300 ms maximum", in.RetryBudget)
	}
	zero := 0
	never, _ := limitWaitAgent(t, true, &zero)
	if in := never.llmProviderInput(&config.ResolvedLLM{ProviderType: "neuraldeep", Model: "m", Stream: true}); !in.RetryBudgetSet || in.RetryBudget != 0 {
		t.Fatalf("an explicit 0: budget %v (set %v), want a set zero so the wrapper never sleeps on a limit", in.RetryBudget, in.RetryBudgetSet)
	}
}
