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
// cached; the manager hands itself to every state it registers.
type providerContextWindows interface {
	reportedContextWindow(cfg *config.Config, ent *config.ModelEntry) (int, bool)
}

// ModelListerFunc lists a provider's models; llm.ListModels in production.
type ModelListerFunc func(ctx context.Context, in llm.ProviderInput) ([]llm.ModelEntry, error)

type contextWindowEntry struct {
	// windows maps the API model id to its reported window; nil until the
	// listing answered once.
	windows   map[string]int
	fetchedAt time.Time
	failedAt  time.Time
	// inflight is closed when the running fetch ends; nil while idle.
	inflight chan struct{}
}

type contextWindowState struct {
	mu      sync.Mutex
	entries map[string]*contextWindowEntry
	list    ModelListerFunc
	now     func() time.Time
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
// LM Studio, the hub itself on type openai) may, and api.openai.com, Anthropic
// and Codex do not.
func providerListsContextWindows(p *config.ProviderConfig) bool {
	if p == nil {
		return false
	}
	switch strings.TrimSpace(p.Type) {
	case "neuraldeep":
		return true
	case "openai":
		return strings.TrimSpace(p.APIBase) != ""
	default:
		return false
	}
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
	n := e.windows[apiModel]
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
		if ent == nil || ent.MaxContextTokens > 0 {
			continue
		}
		prov := cfg.FindProvider(ent.ProviderName())
		if !providerListsContextWindows(prov) {
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
	if w.entries == nil {
		w.entries = make(map[string]*contextWindowEntry)
	}
	e := w.entries[key]
	if e == nil {
		e = &contextWindowEntry{}
		w.entries[key] = e
	}
	if e.inflight != nil {
		if e.windows == nil {
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
	done := make(chan struct{})
	e.inflight = done
	list := w.list
	if list == nil {
		list = llm.ListModels
	}
	authPath := config.ProviderAuthPath(cfg.Paths.Home, prov.Name, prov.Type)
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), contextWindowFetchTimeout)
		defer cancel()
		models, err := list(ctx, llm.ProviderInput{
			Name:     prov.Name,
			Type:     prov.Type,
			APIKey:   prov.EffectiveAPIKeyContext(ctx),
			BaseURL:  prov.APIBase,
			ProxyURL: prov.Proxy,
			AuthPath: authPath,
		})
		w.mu.Lock()
		defer w.mu.Unlock()
		e.inflight = nil
		if err != nil {
			e.failedAt = w.nowLocked()
			m.log.Debug("provider model listing unavailable; context windows fall back to max_context_tokens or the default",
				"provider", prov.Name, "error", err)
			return
		}
		windows := make(map[string]int, len(models))
		for _, model := range models {
			if model.ContextWindow > 0 {
				windows[model.ID] = model.ContextWindow
			}
		}
		e.windows = windows
		e.fetchedAt = w.nowLocked()
		e.failedAt = time.Time{}
		m.log.Debug("provider context windows read", "provider", prov.Name, "models", len(windows))
	}()
	if e.windows == nil {
		return done
	}
	return nil
}

func (w *contextWindowState) nowLocked() time.Time {
	if w.now != nil {
		return w.now()
	}
	return time.Now()
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

// inflightContextWindowFetches snapshots the fetches running right now.
func (m *Manager) inflightContextWindowFetches() []chan struct{} {
	m.windows.mu.Lock()
	defer m.windows.mu.Unlock()
	var out []chan struct{}
	for _, e := range m.windows.entries {
		if e.inflight != nil {
			out = append(out, e.inflight)
		}
	}
	return out
}
