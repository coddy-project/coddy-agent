package session

// The listing cache keeps the WHOLE record of a model a remote Coddy shares
// (type coddy): its context window next to the revision the client sends as
// expected_revision. A refresh reads the listing again and never writes a key
// of the configuration (docs/plans/remote-model-provider.md, 4.3).

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// recordListing is a stand-in listing that answers with whole records and can
// be held at a gate.
type recordListing struct {
	mu      sync.Mutex
	calls   atomic.Int32
	inputs  []llm.ProviderInput
	entries []llm.ModelEntry
	err     error
	gate    chan struct{}
}

func (l *recordListing) list(ctx context.Context, in llm.ProviderInput) ([]llm.ModelEntry, error) {
	l.calls.Add(1)
	l.mu.Lock()
	l.inputs = append(l.inputs, in)
	gate, entries, err := l.gate, append([]llm.ModelEntry(nil), l.entries...), l.err
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
	return entries, nil
}

func (l *recordListing) set(entries []llm.ModelEntry, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries, l.err = entries, err
}

func (l *recordListing) hold() chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gate = make(chan struct{})
	return l.gate
}

func (l *recordListing) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.gate != nil {
		close(l.gate)
		l.gate = nil
	}
}

func coddyRecord(revision string, window int) llm.ModelEntry {
	return llm.ModelEntry{
		ID: "coder", ContextWindow: window, Revision: revision, Multimodal: true,
		ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "low", AllowReasoningOff: true,
	}
}

func coddyWindowConfig(home string) *config.Config {
	return &config.Config{
		Paths: config.Paths{Home: home},
		Providers: []config.ProviderConfig{
			{Name: "remote", Type: "coddy", APIBase: "https://remote.example", APIKey: "k"},
			{Name: "oai", Type: "openai", APIKey: "k"},
		},
		Models: []config.ModelEntry{
			{Model: "remote/coder"},
			{Model: "remote/pinned", MaxContextTokens: 32000},
			{Model: "oai/gpt-4o"},
		},
		Agent: config.Agent{Model: "remote/coder"},
	}
}

func newRecordManager(t *testing.T, cfg *config.Config, listing *recordListing, clock *windowTestClock) *Manager {
	t.Helper()
	m := NewManager(cfg, &contextUsageCapture{}, nil, slog.Default(), t.TempDir(), nil)
	var now func() time.Time
	if clock != nil {
		now = clock.Now
	}
	m.SetContextWindowLister(listing.list, now)
	t.Cleanup(func() {
		listing.release()
		if err := m.WaitContextWindowsIdle(5 * time.Second); err != nil {
			t.Error(err)
		}
	})
	return m
}

func TestCoddyListingKeepsTheWholeRecord(t *testing.T) {
	cfg := coddyWindowConfig(t.TempDir())
	listing := &recordListing{entries: []llm.ModelEntry{coddyRecord("rev-1", 128000)}}
	m := newRecordManager(t, cfg, listing, nil)

	m.AwaitContextWindows(context.Background(), cfg, []string{"remote/coder"}, time.Second)

	got, ok := m.ProviderModelEntry(cfg, "remote", "coder")
	if !ok {
		t.Fatal("the cache holds no record for the shared model after the listing answered")
	}
	want := coddyRecord("rev-1", 128000)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("record = %+v, want the whole listing entry %+v", got, want)
	}
	if tokens, source := m.ContextWindow(cfg, "remote/coder"); tokens != 128000 || source != ContextWindowFromProvider {
		t.Fatalf("ContextWindow = %d/%q, want 128000 from the provider: coddy joins the types that list windows", tokens, source)
	}
	if in := listing.inputs[0]; in.Type != "coddy" || in.BaseURL != "https://remote.example" || in.APIKey != "k" {
		t.Fatalf("listing asked with %+v, want the coddy row's type, base and key", in)
	}

	// The record is the caller's to keep: changing it changes nothing cached.
	got.ReasoningLevels[0] = "tampered"
	again, _ := m.ProviderModelEntry(cfg, "remote", "coder")
	if again.ReasoningLevels[0] != "low" {
		t.Fatalf("a caller edited the cached record through the returned slice: %v", again.ReasoningLevels)
	}
	if _, ok := m.ProviderModelEntry(cfg, "remote", "unlisted"); ok {
		t.Fatal("a record appeared for an alias the listing does not carry")
	}
	if _, ok := m.ProviderModelEntry(cfg, "nobody", "coder"); ok {
		t.Fatal("a record appeared for a provider row that does not exist")
	}
}

// A local max_context_tokens still wins over the listing, but the listing is
// read all the same: the revision it carries is what expected_revision needs.
func TestCoddyLocalContextWindowWinsAndTheRevisionIsStillRead(t *testing.T) {
	cfg := coddyWindowConfig(t.TempDir())
	pinned := coddyRecord("rev-9", 200000)
	pinned.ID = "pinned"
	listing := &recordListing{entries: []llm.ModelEntry{pinned}}
	m := newRecordManager(t, cfg, listing, nil)

	m.AwaitContextWindows(context.Background(), cfg, []string{"remote/pinned"}, time.Second)

	if n := listing.calls.Load(); n != 1 {
		t.Fatalf("listing read %d times for a coddy row with a local window, want 1 (the revision lives in it)", n)
	}
	if tokens, source := m.ContextWindow(cfg, "remote/pinned"); tokens != 32000 || source != ContextWindowFromConfig {
		t.Fatalf("ContextWindow = %d/%q, want the local 32000", tokens, source)
	}
	if e, ok := m.ProviderModelEntry(cfg, "remote", "pinned"); !ok || e.Revision != "rev-9" {
		t.Fatalf("record = %+v/%v, want revision rev-9", e, ok)
	}
}

// Other provider types with a local window are not read, as before: only a
// coddy row needs the listing for more than the window.
func TestOtherTypesWithALocalWindowAreStillNotRead(t *testing.T) {
	cfg := windowTestConfig(t.TempDir())
	listing := &windowListing{windows: map[string]int{"configured": 999}}
	m := newWindowTestManager(t, cfg, listing, nil)

	m.AwaitContextWindows(context.Background(), cfg, []string{"hub/configured"}, time.Second)
	if n := listing.calls.Load(); n != 0 {
		t.Fatalf("listing read %d times for an openai row with max_context_tokens, want 0", n)
	}
}

func TestCoddyStaleRecordKeepsServingWhileAFetchRuns(t *testing.T) {
	cfg := coddyWindowConfig(t.TempDir())
	clock := &windowTestClock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	listing := &recordListing{entries: []llm.ModelEntry{coddyRecord("rev-1", 128000)}}
	m := newRecordManager(t, cfg, listing, clock)
	m.AwaitContextWindows(context.Background(), cfg, []string{"remote/coder"}, time.Second)

	clock.Advance(contextWindowTTL + time.Minute)
	listing.set([]llm.ModelEntry{coddyRecord("rev-2", 64000)}, nil)
	listing.hold()
	// A reader finding the record older than its TTL starts a fetch and does
	// not wait for it: the old values serve meanwhile.
	start := time.Now()
	m.AwaitContextWindows(context.Background(), cfg, []string{"remote/coder"}, time.Second)
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("a reader waited for the refresh of a record that was only past its TTL")
	}
	if e, _ := m.ProviderModelEntry(cfg, "remote", "coder"); e.Revision != "rev-1" {
		t.Fatalf("revision = %q while the fetch runs, want the stale rev-1 to keep serving", e.Revision)
	}
	if tokens, _ := m.ContextWindow(cfg, "remote/coder"); tokens != 128000 {
		t.Fatalf("window = %d while the fetch runs, want the stale 128000", tokens)
	}
	listing.release()
	if err := m.WaitContextWindowsIdle(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if e, _ := m.ProviderModelEntry(cfg, "remote", "coder"); e.Revision != "rev-2" {
		t.Fatalf("revision = %q after the fetch, want rev-2", e.Revision)
	}
	if tokens, _ := m.ContextWindow(cfg, "remote/coder"); tokens != 64000 {
		t.Fatalf("window = %d after the fetch, want 64000", tokens)
	}
}

// A request answered stale_revision refreshes at once, TTL or not, and the
// reader that follows it gets the new record.
func TestRefreshProviderModelEntryReadsAgainWithinTheTTL(t *testing.T) {
	cfg := coddyWindowConfig(t.TempDir())
	listing := &recordListing{entries: []llm.ModelEntry{coddyRecord("rev-1", 128000)}}
	m := newRecordManager(t, cfg, listing, nil)
	m.AwaitContextWindows(context.Background(), cfg, []string{"remote/coder"}, time.Second)

	listing.set([]llm.ModelEntry{coddyRecord("rev-2", 64000)}, nil)
	got, err := m.RefreshProviderModelEntry(context.Background(), cfg, "remote", "coder", "rev-1", time.Second)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got == nil || got.Revision != "rev-2" || got.ContextWindow != 64000 {
		t.Fatalf("refreshed record = %+v, want rev-2 with the new window", got)
	}
	if n := listing.calls.Load(); n != 2 {
		t.Fatalf("listing read %d times, want 2 (the first read and the forced one)", n)
	}
	if e, _ := m.ProviderModelEntry(cfg, "remote", "coder"); e.Revision != "rev-2" {
		t.Fatalf("the cache holds %q after the refresh, want rev-2", e.Revision)
	}
	if tokens, _ := m.ContextWindow(cfg, "remote/coder"); tokens != 64000 {
		t.Fatalf("window = %d after the refresh, want 64000", tokens)
	}
}

// A refresh that finds the cache already past the stale revision (another
// session refreshed it) does not read the listing again.
func TestRefreshProviderModelEntryUsesWhatAnotherReaderAlreadyRefreshed(t *testing.T) {
	cfg := coddyWindowConfig(t.TempDir())
	listing := &recordListing{entries: []llm.ModelEntry{coddyRecord("rev-2", 64000)}}
	m := newRecordManager(t, cfg, listing, nil)
	m.AwaitContextWindows(context.Background(), cfg, []string{"remote/coder"}, time.Second)

	got, err := m.RefreshProviderModelEntry(context.Background(), cfg, "remote", "coder", "rev-1", time.Second)
	if err != nil || got == nil || got.Revision != "rev-2" {
		t.Fatalf("refresh = %+v, %v; want the cached rev-2", got, err)
	}
	if n := listing.calls.Load(); n != 1 {
		t.Fatalf("listing read %d times, want 1: the cache was already past the stale revision", n)
	}
}

// A fetch that was already running when the answer came back stale may have
// started before the remote changed: when it still shows the stale revision,
// one more read follows.
func TestRefreshProviderModelEntryReadsOnceMoreWhenTheRunningFetchWasOld(t *testing.T) {
	cfg := coddyWindowConfig(t.TempDir())
	clock := &windowTestClock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	gate := make(chan struct{})
	var calls atomic.Int32
	list := func(ctx context.Context, _ llm.ProviderInput) ([]llm.ModelEntry, error) {
		switch calls.Add(1) {
		case 1:
			return []llm.ModelEntry{coddyRecord("rev-1", 128000)}, nil
		case 2:
			// The TTL fetch: it read the listing before the remote changed.
			select {
			case <-gate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return []llm.ModelEntry{coddyRecord("rev-1", 128000)}, nil
		default:
			return []llm.ModelEntry{coddyRecord("rev-2", 64000)}, nil
		}
	}
	m := NewManager(cfg, &contextUsageCapture{}, nil, slog.Default(), t.TempDir(), nil)
	m.SetContextWindowLister(list, clock.Now)
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
		if err := m.WaitContextWindowsIdle(5 * time.Second); err != nil {
			t.Error(err)
		}
	})
	m.AwaitContextWindows(context.Background(), cfg, []string{"remote/coder"}, time.Second)
	clock.Advance(contextWindowTTL + time.Minute)
	m.AwaitContextWindows(context.Background(), cfg, []string{"remote/coder"}, time.Second) // starts the TTL fetch

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(gate)
	}()
	got, err := m.RefreshProviderModelEntry(context.Background(), cfg, "remote", "coder", "rev-1", 3*time.Second)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got == nil || got.Revision != "rev-2" {
		t.Fatalf("refresh returned %+v, want rev-2: the stale rev-1 of the running fetch must not come back as the refreshed record", got)
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("listing read %d times, want 3 (first, the old running one, the one that followed)", n)
	}
}

func TestRefreshProviderModelEntryWaitsAtMostMaxWait(t *testing.T) {
	cfg := coddyWindowConfig(t.TempDir())
	listing := &recordListing{entries: []llm.ModelEntry{coddyRecord("rev-1", 128000)}}
	m := newRecordManager(t, cfg, listing, nil)
	m.AwaitContextWindows(context.Background(), cfg, []string{"remote/coder"}, time.Second)

	listing.set([]llm.ModelEntry{coddyRecord("rev-2", 64000)}, nil)
	listing.hold()
	start := time.Now()
	got, err := m.RefreshProviderModelEntry(context.Background(), cfg, "remote", "coder", "rev-1", 60*time.Millisecond)
	if err == nil {
		t.Fatalf("refresh returned %+v with a listing that never answered, want a timeout error", got)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("refresh waited %v, want about the 60 ms bound", elapsed)
	}
	// The fetch goes on, and lands later for the next reader.
	listing.release()
	if err := m.WaitContextWindowsIdle(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if e, _ := m.ProviderModelEntry(cfg, "remote", "coder"); e.Revision != "rev-2" {
		t.Fatalf("revision = %q after the late fetch, want rev-2", e.Revision)
	}
}

func TestRefreshProviderModelEntryEndsWithTheContext(t *testing.T) {
	cfg := coddyWindowConfig(t.TempDir())
	listing := &recordListing{entries: []llm.ModelEntry{coddyRecord("rev-1", 128000)}}
	m := newRecordManager(t, cfg, listing, nil)
	m.AwaitContextWindows(context.Background(), cfg, []string{"remote/coder"}, time.Second)

	listing.hold()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, err := m.RefreshProviderModelEntry(ctx, cfg, "remote", "coder", "rev-1", time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("refresh error = %v, want the context's", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("a cancelled refresh waited for the listing")
	}
}

func TestRefreshProviderModelEntryReportsAnAliasThatIsNoLongerListed(t *testing.T) {
	cfg := coddyWindowConfig(t.TempDir())
	listing := &recordListing{entries: []llm.ModelEntry{coddyRecord("rev-1", 128000)}}
	m := newRecordManager(t, cfg, listing, nil)
	m.AwaitContextWindows(context.Background(), cfg, []string{"remote/coder"}, time.Second)

	other := coddyRecord("rev-7", 8000)
	other.ID = "someone-else"
	listing.set([]llm.ModelEntry{other}, nil)
	got, err := m.RefreshProviderModelEntry(context.Background(), cfg, "remote", "coder", "rev-1", time.Second)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got != nil {
		t.Fatalf("refresh returned %+v for an alias the remote withdrew, want nil", got)
	}
	if _, ok := m.ProviderModelEntry(cfg, "remote", "coder"); ok {
		t.Fatal("the withdrawn alias is still cached")
	}
}

func TestRefreshProviderModelEntryFailureKeepsTheOldRecord(t *testing.T) {
	cfg := coddyWindowConfig(t.TempDir())
	listing := &recordListing{entries: []llm.ModelEntry{coddyRecord("rev-1", 128000)}}
	m := newRecordManager(t, cfg, listing, nil)
	m.AwaitContextWindows(context.Background(), cfg, []string{"remote/coder"}, time.Second)

	listing.set(nil, errors.New("connection refused"))
	got, err := m.RefreshProviderModelEntry(context.Background(), cfg, "remote", "coder", "rev-1", time.Second)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("refresh = %+v, %v; want the listing's error", got, err)
	}
	if e, ok := m.ProviderModelEntry(cfg, "remote", "coder"); !ok || e.Revision != "rev-1" {
		t.Fatalf("record = %+v/%v after a failed refresh, want rev-1 to keep serving", e, ok)
	}
}

// The listing lives in the manager's cache, never in the row: a refresh does
// not write a single key of the configuration.
func TestRefreshNeverWritesTheConfiguration(t *testing.T) {
	cfg := coddyWindowConfig(t.TempDir())
	before := *cfg
	before.Providers = append([]config.ProviderConfig(nil), cfg.Providers...)
	before.Models = append([]config.ModelEntry(nil), cfg.Models...)
	listing := &recordListing{entries: []llm.ModelEntry{coddyRecord("rev-1", 128000)}}
	m := newRecordManager(t, cfg, listing, nil)

	m.AwaitContextWindows(context.Background(), cfg, []string{"remote/coder"}, time.Second)
	listing.set([]llm.ModelEntry{coddyRecord("rev-2", 64000)}, nil)
	if _, err := m.RefreshProviderModelEntry(context.Background(), cfg, "remote", "coder", "rev-1", time.Second); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Models, before.Models) || !reflect.DeepEqual(cfg.Providers, before.Providers) {
		t.Fatalf("a refresh changed the configuration:\nmodels   %+v\nproviders %+v", cfg.Models, cfg.Providers)
	}
	if ent := cfg.FindModelEntry("remote/coder"); ent.MaxContextTokens != 0 {
		t.Fatalf("a refresh pinned max_context_tokens = %d on the row", ent.MaxContextTokens)
	}
}

func TestRefreshProviderModelEntryNeedsAProviderThatLists(t *testing.T) {
	cfg := coddyWindowConfig(t.TempDir())
	listing := &recordListing{}
	m := newRecordManager(t, cfg, listing, nil)

	if _, err := m.RefreshProviderModelEntry(context.Background(), cfg, "oai", "gpt-4o", "", time.Second); err == nil {
		t.Fatal("refresh of a provider that reports no listing must fail, not read one")
	}
	if _, err := m.RefreshProviderModelEntry(context.Background(), cfg, "nobody", "x", "", time.Second); err == nil {
		t.Fatal("refresh of an unknown provider row must fail")
	}
	if n := listing.calls.Load(); n != 0 {
		t.Fatalf("listing read %d times, want 0", n)
	}
}

// A state the manager registered reaches the cache through the same two calls.
func TestStateReachesTheCapabilityCache(t *testing.T) {
	cfg := coddyWindowConfig(t.TempDir())
	listing := &recordListing{entries: []llm.ModelEntry{coddyRecord("rev-1", 128000)}}
	m := newRecordManager(t, cfg, listing, nil)
	st := &State{ID: "sess_caps", contextWindows: m}
	st.AwaitContextWindow(context.Background(), cfg, "remote/coder")

	e, ok := st.ProviderModelEntry(cfg, "remote", "coder")
	if !ok || e.Revision != "rev-1" {
		t.Fatalf("State.ProviderModelEntry = %+v/%v, want rev-1", e, ok)
	}
	listing.set([]llm.ModelEntry{coddyRecord("rev-2", 64000)}, nil)
	got, err := st.RefreshProviderModelEntry(context.Background(), cfg, "remote", "coder", "rev-1")
	if err != nil || got == nil || got.Revision != "rev-2" {
		t.Fatalf("State.RefreshProviderModelEntry = %+v, %v; want rev-2", got, err)
	}

	bare := &State{ID: "sess_bare"}
	if _, ok := bare.ProviderModelEntry(cfg, "remote", "coder"); ok {
		t.Fatal("a state no manager built has a record")
	}
	if _, err := bare.RefreshProviderModelEntry(context.Background(), cfg, "remote", "coder", "rev-1"); err == nil {
		t.Fatal("a state no manager built cannot refresh")
	}
}

// provider_usage has no source for a coddy row in phase 1: the read says
// unsupported and nothing fails.
func TestProviderUsageForACoddyRowDegradesToUnsupported(t *testing.T) {
	cfg := coddyWindowConfig(t.TempDir())
	m := NewManager(cfg, &contextUsageCapture{}, nil, slog.Default(), t.TempDir(), nil)

	u, err := m.ProviderUsage(context.Background(), "remote", false)
	if err != nil {
		t.Fatalf("ProviderUsage: %v", err)
	}
	if u == nil || !u.Unsupported || u.ProviderType != "coddy" || u.Provider != "remote" {
		t.Fatalf("usage = %+v, want an unsupported answer for the coddy row", u)
	}
	if u.Disabled {
		t.Fatalf("usage = %+v: a type with no usage source is unsupported, not switched off", u)
	}
}
