package session

// Context windows: the size of the window a session's model reads, which the
// composer context ring (GET /v1/models), usage_update and the automatic
// compaction trigger all measure against. Every reader applies one order: the
// model's max_context_tokens, then the window its provider's model listing
// reports, then config.DefaultContextWindowTokens. Before this cache the web UI
// drew its ring against a fallback window while the trigger, which only read
// max_context_tokens, stayed off for the same model (issue #245).
//
// The manager owns one cache of the listings. Readers never wait on it: a turn
// being admitted and the model list being served fetch what is missing, and
// wait a bounded moment for a listing that has never answered, so the first
// turn of a session already sees the provider's number.
//
// The cache keeps the WHOLE record of every listed model, not only its window.
// A model a remote Coddy shares (provider type coddy) reports a revision with
// it, which the client sends as expected_revision; a request the remote
// answers stale_revision refreshes the record at once and the reader that
// follows it waits for the new one (RefreshProviderModelEntry). The listing
// lives here and never in the configuration: a refresh writes no key of any
// models[] row (docs/plans/remote-model-provider.md, 4.3).

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

const (
	// ContextWindowWait bounds how long a caller waits for a provider listing
	// that has never answered; past it the default window serves until the
	// fetch completes.
	ContextWindowWait = 3 * time.Second
	// contextWindowTTL is how long a fetched listing is trusted before the next
	// caller refreshes it (the old windows keep serving meanwhile).
	contextWindowTTL = time.Hour
	// contextWindowRetry is how long a failed listing is not asked again.
	contextWindowRetry = 5 * time.Minute
	// contextWindowFetchTimeout bounds one listing request, credential helper
	// included.
	contextWindowFetchTimeout = 20 * time.Second
)

// Sources of a resolved context window.
const (
	// ContextWindowFromConfig is the model's own max_context_tokens.
	ContextWindowFromConfig = "config"
	// ContextWindowFromProvider is the window the provider's model listing reports.
	ContextWindowFromProvider = "provider"
	// ContextWindowDefault is config.DefaultContextWindowTokens.
	ContextWindowDefault = "default"
)

// providerContextWindows reads the provider-reported windows a manager has
// cached, and fetches the ones it lacks; the manager hands itself to every
// state it registers.
type providerContextWindows interface {
	reportedContextWindow(cfg *config.Config, ent *config.ModelEntry) (int, bool)
	AwaitContextWindows(ctx context.Context, cfg *config.Config, modelRefs []string, maxWait time.Duration)
	ProviderModelEntry(cfg *config.Config, providerName, apiModel string) (llm.ModelEntry, bool)
	RefreshProviderModelEntry(ctx context.Context, cfg *config.Config, providerName, apiModel, staleRevision string, maxWait time.Duration) (*llm.ModelEntry, error)
}

// ModelListerFunc lists a provider's models; llm.ListModels in production.
type ModelListerFunc func(ctx context.Context, in llm.ProviderInput) ([]llm.ModelEntry, error)

type contextWindowEntry struct {
	// provider names the row whose listing this is.
	provider string
	// gen counts the credential changes of the row (ForgetContextWindows): a
	// read that returns under another gen than it started with is dropped.
	gen int
	// cancel calls off the running read; nil while idle.
	cancel context.CancelFunc
	// models maps the API model id (the alias, for a coddy row) to its whole
	// listing record; nil until the listing answered once.
	models    map[string]llm.ModelEntry
	fetchedAt time.Time
	failedAt  time.Time
	// lastErr is why the latest read failed; nil after a read that answered.
	lastErr error
	// inflight is closed when the running fetch ends; nil while idle.
	inflight chan struct{}
}

type contextWindowState struct {
	mu      sync.Mutex
	entries map[string]*contextWindowEntry
	// detached holds the reads a credential change called off
	// (ForgetContextWindows) until they return: no entry waits for them any
	// more, but WaitContextWindowsIdle still does.
	detached map[chan struct{}]struct{}
	list     ModelListerFunc
	now      func() time.Time
}

// resolveContextWindow applies the resolution order for modelRef. tokens is 0
// only when modelRef names no configured model.
func resolveContextWindow(cfg *config.Config, modelRef string, reported providerContextWindows) (tokens int, source string) {
	if cfg == nil {
		return 0, ""
	}
	ent := cfg.FindModelEntry(modelRef)
	if ent == nil {
		return 0, ""
	}
	if ent.MaxContextTokens > 0 {
		return ent.MaxContextTokens, ContextWindowFromConfig
	}
	if reported != nil {
		if n, ok := reported.reportedContextWindow(cfg, ent); ok {
			return n, ContextWindowFromProvider
		}
	}
	return config.DefaultContextWindowTokens, ContextWindowDefault
}

// providerListsContextWindows reports whether a provider's model listing is
// worth asking for context windows: the NeuralDeep hub reports them, an
// OpenAI-compatible server behind an explicit api_base (vLLM, OpenRouter,
// LM Studio, the hub itself on type openai) may, the Devin catalog reports one
// per family, the Codex catalog one per model, a remote Coddy one per shared
// model together with its revision, and api.openai.com and Anthropic do not.
func providerListsContextWindows(p *config.ProviderConfig) bool {
	if p == nil {
		return false
	}
	switch strings.TrimSpace(p.Type) {
	case "neuraldeep", "devin", "codex", "coddy":
		return true
	case "openai":
		return strings.TrimSpace(p.APIBase) != ""
	default:
		return false
	}
}

// providerListingCarriesRevision reports whether a row's listing holds more
// than a window, so that a model with a local max_context_tokens still needs
// it read: a coddy row sends the revision of its listing record with every
// request.
func providerListingCarriesRevision(p *config.ProviderConfig) bool {
	return p != nil && strings.TrimSpace(p.Type) == "coddy"
}

// contextWindowKey names the listing a provider row reads. The credential is
// not part of it: a model's window does not depend on the account asking.
func contextWindowKey(p *config.ProviderConfig) string {
	return strings.Join([]string{
		strings.TrimSpace(p.Type),
		strings.TrimSpace(p.Name),
		strings.TrimRight(strings.TrimSpace(p.APIBase), "/"),
		strings.TrimSpace(p.Proxy),
	}, "|")
}

// ContextWindowFor resolves the window of an arbitrary configured model on the
// same cache, for a reader that measures against a model other than the
// session's - the compaction summarizer, which compaction.model may point at a
// model with a window of its own.
func (s *State) ContextWindowFor(cfg *config.Config, modelRef string) (tokens int, source string) {
	if s == nil || cfg == nil {
		return 0, ""
	}
	if strings.TrimSpace(modelRef) == "" {
		return s.ContextWindow(cfg)
	}
	return resolveContextWindow(cfg, modelRef, s.contextWindows)
}

// AwaitContextWindow makes sure the window of modelRef is read before a
// request measures against it - the model a running turn just switched to -
// waiting up to ContextWindowWait, or until ctx ends, for a provider listing
// that has never answered. A state no manager built has nothing to fetch.
func (s *State) AwaitContextWindow(ctx context.Context, cfg *config.Config, modelRef string) {
	if s == nil || cfg == nil || s.contextWindows == nil || strings.TrimSpace(modelRef) == "" {
		return
	}
	s.contextWindows.AwaitContextWindows(ctx, cfg, []string{modelRef}, ContextWindowWait)
}

// ProviderModelEntry is the record the manager's listing cache holds for the
// model apiModel of the provider row providerName (see Manager.ProviderModelEntry);
// ok is false for a state no manager built.
func (s *State) ProviderModelEntry(cfg *config.Config, providerName, apiModel string) (llm.ModelEntry, bool) {
	if s == nil || s.contextWindows == nil {
		return llm.ModelEntry{}, false
	}
	return s.contextWindows.ProviderModelEntry(cfg, providerName, apiModel)
}

// RefreshProviderModelEntry reads the listing of the provider row again after
// a request answered stale_revision and returns the model's new record,
// waiting for at most ContextWindowWait (see Manager.RefreshProviderModelEntry).
// A state no manager built has no listing to read and answers an error.
func (s *State) RefreshProviderModelEntry(ctx context.Context, cfg *config.Config, providerName, apiModel, staleRevision string) (*llm.ModelEntry, error) {
	if s == nil || s.contextWindows == nil {
		return nil, fmt.Errorf("this session has no model listing to refresh")
	}
	return s.contextWindows.RefreshProviderModelEntry(ctx, cfg, providerName, apiModel, staleRevision, ContextWindowWait)
}

// ContextWindow resolves the context window of modelRef without waiting: the
// model's max_context_tokens, then the window its provider's listing reported
// the last time it was read, then config.DefaultContextWindowTokens. tokens is
// 0 only when modelRef names no configured model.
func (m *Manager) ContextWindow(cfg *config.Config, modelRef string) (tokens int, source string) {
	return resolveContextWindow(cfg, modelRef, m)
}

func (m *Manager) reportedContextWindow(cfg *config.Config, ent *config.ModelEntry) (int, bool) {
	prov := cfg.FindProvider(ent.ProviderName())
	if !providerListsContextWindows(prov) {
		return 0, false
	}
	_, apiModel, err := config.SplitModelRef(ent.Model)
	if err != nil {
		return 0, false
	}
	m.windows.mu.Lock()
	defer m.windows.mu.Unlock()
	e := m.windows.entries[contextWindowKey(prov)]
	if e == nil {
		return 0, false
	}
	n := e.models[apiModel].ContextWindow
	return n, n > 0
}

// AwaitContextWindows makes sure the listings behind modelRefs are cached or
// being fetched, and waits up to maxWait (or until ctx ends) for the ones that
// have never answered. It returns at once when every listing is cached, failed
// recently, or does not apply (a model with max_context_tokens, a provider
// that reports no windows); a listing past its TTL is refreshed in the
// background while its windows keep serving.
func (m *Manager) AwaitContextWindows(ctx context.Context, cfg *config.Config, modelRefs []string, maxWait time.Duration) {
	if cfg == nil {
		return
	}
	var waits []<-chan struct{}
	seen := make(map[string]bool)
	for _, ref := range modelRefs {
		ent := cfg.FindModelEntry(strings.TrimSpace(ref))
		if ent == nil {
			continue
		}
		prov := cfg.FindProvider(ent.ProviderName())
		if !providerListsContextWindows(prov) {
			continue
		}
		if ent.MaxContextTokens > 0 && !providerListingCarriesRevision(prov) {
			continue
		}
		key := contextWindowKey(prov)
		if seen[key] {
			continue
		}
		seen[key] = true
		if ch := m.refreshContextWindows(cfg, *prov, key); ch != nil {
			waits = append(waits, ch)
		}
	}
	if len(waits) == 0 || maxWait <= 0 {
		return
	}
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	for _, ch := range waits {
		select {
		case <-ch:
		case <-timer.C:
			return
		case <-ctx.Done():
			return
		}
	}
}

// refreshContextWindows starts a fetch of a provider's listing unless one is
// running, fresh, or failed recently. It returns a channel to wait on only
// when the listing has never answered; stale windows serve while they refresh.
func (m *Manager) refreshContextWindows(cfg *config.Config, prov config.ProviderConfig, key string) <-chan struct{} {
	w := &m.windows
	w.mu.Lock()
	defer w.mu.Unlock()
	e := w.entryLocked(prov, key)
	if e.inflight != nil {
		if e.models == nil {
			return e.inflight
		}
		return nil
	}
	now := w.nowLocked()
	if !e.fetchedAt.IsZero() && now.Sub(e.fetchedAt) < contextWindowTTL {
		return nil
	}
	if !e.failedAt.IsZero() && now.Sub(e.failedAt) < contextWindowRetry {
		return nil
	}
	done := m.startListingLocked(cfg, prov, e)
	if e.models == nil {
		return done
	}
	return nil
}

func (w *contextWindowState) entryLocked(prov config.ProviderConfig, key string) *contextWindowEntry {
	if w.entries == nil {
		w.entries = make(map[string]*contextWindowEntry)
	}
	e := w.entries[key]
	if e == nil {
		e = &contextWindowEntry{provider: prov.Name}
		w.entries[key] = e
	}
	return e
}

// startListingLocked starts the read of a provider's listing and returns the
// channel closed when it ends. The caller holds the state lock, found no read
// running for e, and decided that one is wanted (a reader past the TTL, or a
// refresh that follows a stale answer).
func (m *Manager) startListingLocked(cfg *config.Config, prov config.ProviderConfig, e *contextWindowEntry) chan struct{} {
	w := &m.windows
	done := make(chan struct{})
	e.inflight = done
	gen := e.gen
	list := w.list
	if list == nil {
		list = llm.ListModels
	}
	authPath := config.ProviderAuthPath(cfg.Paths.Home, prov.Name, prov.Type)
	ctx, cancel := context.WithTimeout(context.Background(), contextWindowFetchTimeout)
	e.cancel = cancel
	go func() {
		defer close(done)
		defer cancel()
		models, err := list(ctx, llm.ProviderInput{
			Name:     prov.Name,
			Type:     prov.Type,
			APIKey:   prov.EffectiveAPIKeyContext(ctx),
			BaseURL:  prov.APIBase,
			ProxyURL: prov.Proxy,
			AuthPath: authPath,
			// The same account a request of this row would use.
			NoCLILogin: !cfg.ProviderMayUseCLILogin(prov.Name, prov.Type),
		})
		w.mu.Lock()
		defer w.mu.Unlock()
		if e.gen != gen {
			// A credential change called this read off and let it go: what it
			// brought describes the previous credential, and the entry may
			// already run the read of the new one, which this one leaves alone.
			delete(w.detached, done)
			return
		}
		e.inflight = nil
		e.cancel = nil
		if err != nil {
			e.failedAt = w.nowLocked()
			e.lastErr = err
			m.log.Debug("provider model listing unavailable; context windows fall back to max_context_tokens or the default",
				"provider", prov.Name, "error", err)
			return
		}
		records := make(map[string]llm.ModelEntry, len(models))
		for _, model := range models {
			records[model.ID] = copyModelEntry(model)
		}
		e.models = records
		e.fetchedAt = w.nowLocked()
		e.failedAt = time.Time{}
		e.lastErr = nil
		m.log.Debug("provider model listing read", "provider", prov.Name, "models", len(records))
	}()
	return done
}

// copyModelEntry returns e with its slice detached, so that a record handed to
// a caller and the one in the cache never share memory.
func copyModelEntry(e llm.ModelEntry) llm.ModelEntry {
	e.ReasoningLevels = append([]string(nil), e.ReasoningLevels...)
	return e
}

// ProviderModelEntry returns the listing record the cache holds for the model
// apiModel of the provider row providerName: the whole entry, the revision of
// a shared model included. It never fetches; ok is false until the row's
// listing has answered once, and for a model the listing does not carry. The
// record is a copy.
func (m *Manager) ProviderModelEntry(cfg *config.Config, providerName, apiModel string) (llm.ModelEntry, bool) {
	if cfg == nil {
		return llm.ModelEntry{}, false
	}
	prov := cfg.FindProvider(strings.TrimSpace(providerName))
	if !providerListsContextWindows(prov) {
		return llm.ModelEntry{}, false
	}
	m.windows.mu.Lock()
	defer m.windows.mu.Unlock()
	e := m.windows.entries[contextWindowKey(prov)]
	if e == nil {
		return llm.ModelEntry{}, false
	}
	rec, ok := e.models[apiModel]
	if !ok {
		return llm.ModelEntry{}, false
	}
	return copyModelEntry(rec), true
}

// RefreshProviderModelEntry reads the listing of the provider row again, past
// its TTL and its failure backoff, and returns the record of apiModel in it.
// It is what follows a request a remote Coddy answered stale_revision: the
// caller's view of the row (staleRevision, the revision it sent) is out of
// date, and it waits for the new one for at most maxWait. The entry that was
// cached keeps serving every other reader while the fetch runs.
//
// A fetch that was already running when this call came in may have read the
// listing before the remote changed; when its record still carries
// staleRevision one more read follows, and whatever the second one shows is
// returned. The record is nil with no error when the listing answered without
// the model (the remote withdrew the alias). Nothing in the configuration is
// written.
func (m *Manager) RefreshProviderModelEntry(ctx context.Context, cfg *config.Config, providerName, apiModel, staleRevision string, maxWait time.Duration) (*llm.ModelEntry, error) {
	var prov *config.ProviderConfig
	if cfg != nil {
		prov = cfg.FindProvider(strings.TrimSpace(providerName))
	}
	if prov == nil {
		return nil, fmt.Errorf("unknown provider %q", providerName)
	}
	if !providerListsContextWindows(prov) {
		return nil, fmt.Errorf("provider %q (type %s) has no model listing to refresh", prov.Name, prov.Type)
	}
	key := contextWindowKey(prov)
	timer := time.NewTimer(maxWait)
	defer timer.Stop()

	// The first round may join a read that was already out, the second starts
	// a read of its own after it.
	for round := 0; round < 2; round++ {
		if rec, ok := m.cachedPastRevision(key, apiModel, staleRevision); ok {
			return rec, nil
		}
		done, started := m.listingToWaitFor(cfg, *prov, key)
		select {
		case <-done:
		case <-timer.C:
			return nil, fmt.Errorf("the listing of provider %q did not answer within %s", prov.Name, maxWait.Round(time.Millisecond))
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		rec, listed, failed := m.cachedAfterFetch(key, apiModel)
		if failed != nil {
			return nil, failed
		}
		if !listed {
			return nil, nil
		}
		if rec.Revision != staleRevision || staleRevision == "" || started || round == 1 {
			// A read this call started itself began after the stale answer, so
			// what it shows is the remote's own word, whatever it is: the
			// caller finds out on its one more try.
			return &rec, nil
		}
	}
	return nil, nil
}

// cachedPastRevision returns the cached record when it already differs from
// the revision the caller found stale: another reader refreshed it.
func (m *Manager) cachedPastRevision(key, apiModel, staleRevision string) (*llm.ModelEntry, bool) {
	if staleRevision == "" {
		return nil, false
	}
	m.windows.mu.Lock()
	defer m.windows.mu.Unlock()
	e := m.windows.entries[key]
	if e == nil || e.inflight != nil {
		return nil, false
	}
	rec, ok := e.models[apiModel]
	if !ok || rec.Revision == staleRevision {
		return nil, false
	}
	cp := copyModelEntry(rec)
	return &cp, true
}

// listingToWaitFor returns the channel of the read a refresh waits for: the
// one already running (started false), or a new one this call starts.
func (m *Manager) listingToWaitFor(cfg *config.Config, prov config.ProviderConfig, key string) (done chan struct{}, started bool) {
	w := &m.windows
	w.mu.Lock()
	defer w.mu.Unlock()
	e := w.entryLocked(prov, key)
	if e.inflight != nil {
		// Joined, whichever round this is: a read that is out in the second
		// round began after the one the first round waited for.
		return e.inflight, false
	}
	return m.startListingLocked(cfg, prov, e), true
}

// cachedAfterFetch reads what the last read left: the record of the model,
// whether the listing carries it, and the failure of that read.
func (m *Manager) cachedAfterFetch(key, apiModel string) (rec llm.ModelEntry, listed bool, failed error) {
	m.windows.mu.Lock()
	defer m.windows.mu.Unlock()
	e := m.windows.entries[key]
	if e == nil {
		return llm.ModelEntry{}, false, fmt.Errorf("the listing was dropped while it was read")
	}
	if e.inflight == nil && e.lastErr != nil {
		return llm.ModelEntry{}, false, e.lastErr
	}
	rec, listed = e.models[apiModel]
	return copyModelEntry(rec), listed, nil
}

func (w *contextWindowState) nowLocked() time.Time {
	if w.now != nil {
		return w.now()
	}
	return time.Now()
}

// ForgetContextWindows makes the next reader of the provider row named
// providerName read its model listing again, instead of trusting the last
// read for the rest of its hour or waiting out the retry backoff of a failed
// one. The credential handlers call it after a login or a logout: a read that
// failed for want of a sign-in says nothing once the row signs in, and a
// listing read with another account may lack the models this one is offered.
// The windows already read keep serving until the next read lands, since a
// model's window does not depend on the account asking. A read still out asks
// with the previous credential: it is called off and let go, so the next
// reader starts the read of the new credential at once instead of waiting for
// it, and whatever it brings is dropped.
func (m *Manager) ForgetContextWindows(providerName string) {
	w := &m.windows
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, e := range w.entries {
		if e.provider != providerName {
			continue
		}
		e.gen++
		e.fetchedAt = time.Time{}
		e.failedAt = time.Time{}
		e.lastErr = nil
		if e.inflight != nil {
			e.cancel()
			if w.detached == nil {
				w.detached = make(map[chan struct{}]struct{})
			}
			w.detached[e.inflight] = struct{}{}
			e.inflight, e.cancel = nil, nil
		}
	}
}

// SetContextWindowLister replaces how provider listings are read (and the
// clock their freshness is measured with): stands and tests point it at a
// stand-in instead of the network. nil restores llm.ListModels and time.Now.
// Cached listings are dropped.
func (m *Manager) SetContextWindowLister(list ModelListerFunc, now func() time.Time) {
	m.windows.mu.Lock()
	defer m.windows.mu.Unlock()
	m.windows.list = list
	m.windows.now = now
	m.windows.entries = nil
}

// WaitContextWindowsIdle blocks until every in-flight listing fetch returned,
// or the timeout passes. It waits on the fetches themselves rather than on a
// counter: a fetch started while the wait is in flight is picked up by the
// next round, and no wait outlives this call.
func (m *Manager) WaitContextWindowsIdle(timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		inflight := m.inflightContextWindowFetches()
		if len(inflight) == 0 {
			return nil
		}
		for _, ch := range inflight {
			select {
			case <-ch:
			case <-deadline.C:
				return fmt.Errorf("provider listing fetches still running after %s", timeout)
			}
		}
	}
}

// inflightContextWindowFetches snapshots the fetches running right now, the
// ones a credential change called off included.
func (m *Manager) inflightContextWindowFetches() []chan struct{} {
	m.windows.mu.Lock()
	defer m.windows.mu.Unlock()
	var out []chan struct{}
	for _, e := range m.windows.entries {
		if e.inflight != nil {
			out = append(out, e.inflight)
		}
	}
	for ch := range m.windows.detached {
		out = append(out, ch)
	}
	return out
}
