package session

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// windowListing is a stand-in model listing: it counts calls, records what it
// was asked, and answers with windows (or err) unless gate holds it.
type windowListing struct {
	mu      sync.Mutex
	calls   atomic.Int32
	inputs  []llm.ProviderInput
	windows map[string]int
	err     error
	gate    chan struct{}
}

func (l *windowListing) list(ctx context.Context, in llm.ProviderInput) ([]llm.ModelEntry, error) {
	l.calls.Add(1)
	l.mu.Lock()
	l.inputs = append(l.inputs, in)
	gate, windows, err := l.gate, l.windows, l.err
	l.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	var out []llm.ModelEntry
	for id, n := range windows {
		out = append(out, llm.ModelEntry{ID: id, ContextWindow: n})
	}
	return out, nil
}

func (l *windowListing) set(windows map[string]int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.windows, l.err = windows, err
}

// windowTestClock is a settable clock for the listing TTL and the retry.
type windowTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *windowTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *windowTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func windowTestConfig(home string) *config.Config {
	return &config.Config{
		Paths: config.Paths{Home: home},
		Providers: []config.ProviderConfig{
			{Name: "hub", Type: "openai", APIBase: "https://hub.example/v1", APIKey: "k"},
			{Name: "nd", Type: "neuraldeep", APIKey: "k"},
			{Name: "oai", Type: "openai", APIKey: "k"},
			{Name: "ant", Type: "anthropic", APIKey: "k"},
			{Name: "cdx", Type: "codex"},
		},
		Models: []config.ModelEntry{
			{Model: "hub/reported"},
			{Model: "hub/unreported"},
			{Model: "hub/configured", MaxContextTokens: 32000},
			{Model: "nd/qwen3.8-27b"},
			{Model: "oai/gpt-4o"},
			{Model: "ant/claude"},
			{Model: "cdx/gpt-5.6"},
		},
		Agent: config.Agent{Model: "hub/reported"},
	}
}

func newWindowTestManager(t *testing.T, cfg *config.Config, listing *windowListing, clock *windowTestClock) *Manager {
	t.Helper()
	m := NewManager(cfg, &contextUsageCapture{}, nil, slog.Default(), t.TempDir(), nil)
	var now func() time.Time
	if clock != nil {
		now = clock.Now
	}
	m.SetContextWindowLister(listing.list, now)
	t.Cleanup(func() {
		listing.mu.Lock()
		if listing.gate != nil {
			select {
			case <-listing.gate:
			default:
				close(listing.gate)
			}
		}
		listing.mu.Unlock()
		if err := m.WaitContextWindowsIdle(5 * time.Second); err != nil {
			t.Error(err)
		}
	})
	return m
}

func TestContextWindowResolutionOrder(t *testing.T) {
	cfg := windowTestConfig(t.TempDir())
	listing := &windowListing{windows: map[string]int{"reported": 262144, "configured": 999}}
	m := newWindowTestManager(t, cfg, listing, nil)

	m.AwaitContextWindows(context.Background(), cfg, []string{"hub/reported", "hub/unreported", "hub/configured"}, time.Second)

	cases := []struct {
		model      string
		wantTokens int
		wantSource string
	}{
		{"hub/configured", 32000, ContextWindowFromConfig},
		{"hub/reported", 262144, ContextWindowFromProvider},
		{"hub/unreported", config.DefaultContextWindowTokens, ContextWindowDefault},
		{"hub/not-configured", 0, ""},
	}
	for _, tc := range cases {
		tokens, source := m.ContextWindow(cfg, tc.model)
		if tokens != tc.wantTokens || source != tc.wantSource {
			t.Errorf("%s: ContextWindow = %d/%q, want %d/%q", tc.model, tokens, source, tc.wantTokens, tc.wantSource)
		}
	}
	if n := listing.calls.Load(); n != 1 {
		t.Fatalf("listing read %d times for one provider, want 1", n)
	}
	in := listing.inputs[0]
	if in.Type != "openai" || in.BaseURL != "https://hub.example/v1" || in.APIKey != "k" {
		t.Fatalf("listing asked with %+v, want the provider row's type, base and key", in)
	}
}

func TestContextWindowAsksOnlyProvidersThatReportWindows(t *testing.T) {
	cfg := windowTestConfig(t.TempDir())
	listing := &windowListing{windows: map[string]int{"qwen3.8-27b": 262144}}
	m := newWindowTestManager(t, cfg, listing, nil)

	// A model with max_context_tokens needs no listing either.
	m.AwaitContextWindows(context.Background(), cfg, []string{"oai/gpt-4o", "ant/claude", "cdx/gpt-5.6", "hub/configured"}, time.Second)
	if n := listing.calls.Load(); n != 0 {
		t.Fatalf("listing read %d times for providers that report no windows, want 0", n)
	}
	for _, model := range []string{"oai/gpt-4o", "ant/claude", "cdx/gpt-5.6"} {
		if tokens, source := m.ContextWindow(cfg, model); tokens != config.DefaultContextWindowTokens || source != ContextWindowDefault {
			t.Errorf("%s: ContextWindow = %d/%q, want the default", model, tokens, source)
		}
	}

	m.AwaitContextWindows(context.Background(), cfg, []string{"nd/qwen3.8-27b"}, time.Second)
	if tokens, source := m.ContextWindow(cfg, "nd/qwen3.8-27b"); tokens != 262144 || source != ContextWindowFromProvider {
		t.Fatalf("neuraldeep model: ContextWindow = %d/%q, want 262144 from the provider", tokens, source)
	}
}

func TestContextWindowFailedListingRetriesAfterBackoff(t *testing.T) {
	cfg := windowTestConfig(t.TempDir())
	clock := &windowTestClock{now: time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)}
	listing := &windowListing{err: errors.New("list models: HTTP 404")}
	m := newWindowTestManager(t, cfg, listing, clock)
	ctx := context.Background()

	m.AwaitContextWindows(ctx, cfg, []string{"hub/reported"}, time.Second)
	if tokens, source := m.ContextWindow(cfg, "hub/reported"); tokens != config.DefaultContextWindowTokens || source != ContextWindowDefault {
		t.Fatalf("after a failed listing: %d/%q, want the default", tokens, source)
	}

	listing.set(map[string]int{"reported": 65536}, nil)
	clock.Advance(contextWindowRetry - time.Second)
	m.AwaitContextWindows(ctx, cfg, []string{"hub/reported"}, time.Second)
	if n := listing.calls.Load(); n != 1 {
		t.Fatalf("listing read %d times inside the retry backoff, want 1", n)
	}

	clock.Advance(2 * time.Second)
	m.AwaitContextWindows(ctx, cfg, []string{"hub/reported"}, time.Second)
	if n := listing.calls.Load(); n != 2 {
		t.Fatalf("listing read %d times after the backoff, want 2", n)
	}
	if tokens, source := m.ContextWindow(cfg, "hub/reported"); tokens != 65536 || source != ContextWindowFromProvider {
		t.Fatalf("after the retry answered: %d/%q, want 65536 from the provider", tokens, source)
	}
}

func TestContextWindowRefreshesAfterTTLWhileServingTheOldWindow(t *testing.T) {
	cfg := windowTestConfig(t.TempDir())
	clock := &windowTestClock{now: time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)}
	listing := &windowListing{windows: map[string]int{"reported": 100000}}
	m := newWindowTestManager(t, cfg, listing, clock)
	ctx := context.Background()

	m.AwaitContextWindows(ctx, cfg, []string{"hub/reported"}, time.Second)
	listing.set(map[string]int{"reported": 200000}, nil)

	clock.Advance(contextWindowTTL - time.Minute)
	m.AwaitContextWindows(ctx, cfg, []string{"hub/reported"}, time.Second)
	if n := listing.calls.Load(); n != 1 {
		t.Fatalf("a fresh listing was read again: %d calls", n)
	}

	clock.Advance(2 * time.Minute)
	m.AwaitContextWindows(ctx, cfg, []string{"hub/reported"}, time.Second)
	if err := m.WaitContextWindowsIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if n := listing.calls.Load(); n != 2 {
		t.Fatalf("a stale listing was not refreshed: %d calls", n)
	}
	if tokens, _ := m.ContextWindow(cfg, "hub/reported"); tokens != 200000 {
		t.Fatalf("after the refresh: %d, want 200000", tokens)
	}
}

func TestContextWindowWaitIsBoundedAndTheFetchLandsLater(t *testing.T) {
	cfg := windowTestConfig(t.TempDir())
	listing := &windowListing{windows: map[string]int{"reported": 131072}, gate: make(chan struct{})}
	m := newWindowTestManager(t, cfg, listing, nil)

	start := time.Now()
	m.AwaitContextWindows(context.Background(), cfg, []string{"hub/reported"}, 50*time.Millisecond)
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("AwaitContextWindows waited %s for a stuck listing, want about 50ms", waited)
	}
	if tokens, source := m.ContextWindow(cfg, "hub/reported"); tokens != config.DefaultContextWindowTokens || source != ContextWindowDefault {
		t.Fatalf("while the listing is stuck: %d/%q, want the default", tokens, source)
	}

	// Callers arriving while the fetch runs join it instead of starting another.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.AwaitContextWindows(context.Background(), cfg, []string{"hub/reported"}, 10*time.Millisecond)
		}()
	}
	wg.Wait()
	if n := listing.calls.Load(); n != 1 {
		t.Fatalf("listing read %d times by concurrent callers, want 1", n)
	}

	listing.mu.Lock()
	close(listing.gate)
	listing.mu.Unlock()
	if err := m.WaitContextWindowsIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if tokens, source := m.ContextWindow(cfg, "hub/reported"); tokens != 131072 || source != ContextWindowFromProvider {
		t.Fatalf("after the listing answered: %d/%q, want 131072 from the provider", tokens, source)
	}
}

func TestContextWindowChangedAPIBaseReadsItsOwnListing(t *testing.T) {
	cfg := windowTestConfig(t.TempDir())
	listing := &windowListing{windows: map[string]int{"reported": 100000}}
	m := newWindowTestManager(t, cfg, listing, nil)
	m.AwaitContextWindows(context.Background(), cfg, []string{"hub/reported"}, time.Second)

	moved := windowTestConfig(cfg.Paths.Home)
	moved.Providers[0].APIBase = "https://mirror.example/v1"
	listing.set(map[string]int{"reported": 50000}, nil)
	if tokens, _ := m.ContextWindow(moved, "hub/reported"); tokens != config.DefaultContextWindowTokens {
		t.Fatalf("a listing of another base was served for the moved provider: %d", tokens)
	}
	m.AwaitContextWindows(context.Background(), moved, []string{"hub/reported"}, time.Second)
	if tokens, _ := m.ContextWindow(moved, "hub/reported"); tokens != 50000 {
		t.Fatalf("moved provider: %d, want 50000", tokens)
	}
	if n := listing.calls.Load(); n != 2 {
		t.Fatalf("listing read %d times for two bases, want 2", n)
	}
}

// Every session a manager registers - new, loaded, a child - resolves its
// window through that manager, and the first turn waits for the listing.
func TestSessionsResolveTheirWindowThroughTheManager(t *testing.T) {
	root := t.TempDir()
	cfg := windowTestConfig(t.TempDir())
	listing := &windowListing{windows: map[string]int{"reported": 262144}}
	var seenInTurn int
	runner := func(_ context.Context, st *State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		seenInTurn, _ = st.ContextWindow(cfg)
		return string(acp.StopReasonEndTurn), nil
	}
	store := &FileStore{Root: t.TempDir()}
	m := NewManager(cfg, &contextUsageCapture{}, runner, slog.Default(), root, store)
	m.SetContextWindowLister(listing.list, nil)
	t.Cleanup(func() { _ = m.WaitContextWindowsIdle(5 * time.Second) })
	ctx := context.Background()

	res, err := m.HandleSessionNew(ctx, acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	// The first turn is admitted with the listing never read: admission
	// fetches it, so the turn already measures against the provider's window.
	if _, err := m.HandleSessionPrompt(ctx, acp.SessionPromptParams{
		SessionID: res.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "hello"}},
	}); err != nil {
		t.Fatal(err)
	}
	if seenInTurn != 262144 {
		t.Fatalf("first turn measured against %d, want the provider's 262144", seenInTurn)
	}

	child, err := m.CreateSubagentSession(ctx, SubagentSpec{
		ID: NewSessionID(), ParentSessionID: res.SessionID, Name: "explore", TaskID: "bg_1", CWD: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tokens, source := child.ContextWindow(cfg); tokens != 262144 || source != ContextWindowFromProvider {
		t.Fatalf("child session: %d/%q, want 262144 from the provider", tokens, source)
	}

	reloaded := NewManager(cfg, &contextUsageCapture{}, runner, slog.Default(), root, store)
	reloaded.SetContextWindowLister(listing.list, nil)
	t.Cleanup(func() { _ = reloaded.WaitContextWindowsIdle(5 * time.Second) })
	if _, err := reloaded.HandleSessionLoad(ctx, acp.SessionLoadParams{SessionID: res.SessionID, CWD: root}); err != nil {
		t.Fatal(err)
	}
	reloaded.AwaitContextWindows(ctx, cfg, []string{"hub/reported"}, time.Second)
	st := reloaded.SessionByID(res.SessionID)
	if st == nil {
		t.Fatal("loaded session not registered")
	}
	if tokens, source := st.ContextWindow(cfg); tokens != 262144 || source != ContextWindowFromProvider {
		t.Fatalf("loaded session: %d/%q, want 262144 from the provider", tokens, source)
	}

	// A state no manager built still resolves: config, then the default.
	bare := &State{ID: "sess_bare"}
	if tokens, source := bare.ContextWindow(cfg); tokens != config.DefaultContextWindowTokens || source != ContextWindowDefault {
		t.Fatalf("bare state: %d/%q, want the default", tokens, source)
	}
}
