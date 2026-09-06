package session

// Provider usage: the account quota behind a session's model provider,
// fetched from the provider (today: NeuralDeep GET /v1/limits) and published
// as acp.ProviderUsageUpdate. The manager owns one cache and one schedule so
// the console footer, the remote console, the SPA and scripts read the same
// numbers. Design record: docs/plans/neuraldeep-usage.md.
//
// Pacing follows the hub's advice (no point polling more often than every
// 15-30 s): automatic reads are served from the cache while the snapshot is
// younger than the TTL; refreshes run at once past the floor and are deferred
// to the floor's end otherwise; a Retry-After becomes a backoff during which
// the stale snapshot is served. A rejected key is sticky for automatic reads.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

const (
	// providerUsageTTL is how long an automatic read trusts the cache.
	providerUsageTTL = 20 * time.Second
	// providerUsageFloor is the least gap between two fetches of one account;
	// a refresh inside it is deferred to its end.
	providerUsageFloor = 15 * time.Second
	// providerUsageBackoffCap bounds the pause a Retry-After can impose: a
	// hub that asks for hours would otherwise freeze the line until a login.
	providerUsageBackoffCap = 5 * time.Minute
	// providerUsageReadyBudget bounds the session-ready refresh.
	providerUsageReadyBudget = 5 * time.Second
)

// Failure kinds carried by ProviderUsageUpdate.Error.
const (
	ProviderUsageErrorUnauthorized = "unauthorized"
	ProviderUsageErrorUnavailable  = "unavailable"
	ProviderUsageErrorInvalid      = "invalid"
)

// providerUsageBlockerUser is the blocker id of a 403 from the hub: the
// account itself is blocked, independent of any window.
const providerUsageBlockerUser = "user_blocked"

// providerUsageTimerFunc schedules fn after d and returns its stop; tests
// inject a fake clock through it.
type providerUsageTimerFunc func(d time.Duration, fn func()) (stop func() bool)

type providerUsageEntry struct {
	fingerprint string
	// generation invalidates asynchronous work: a fetch or a deferred refresh
	// started under an older generation discards its result.
	generation uint64
	// update is the last snapshot built for this account: a successful
	// mapping, or the previous one marked stale plus the latest error.
	update       *acp.ProviderUsageUpdate
	fetchedAt    time.Time
	lastAttempt  time.Time
	unauthorized bool
	backoffUntil time.Time
	// inflight is closed when the running fetch ends; nil while idle.
	inflight       chan struct{}
	inflightCancel context.CancelFunc
	// pendingStop cancels the deferred refresh timer; pendingAt says when it
	// fires and pendingSessions which sessions asked for it.
	pendingStop     func() bool
	pendingAt       time.Time
	pendingSessions []string
	// waiters are the sessions that asked for the running fetch: the one
	// that started it and the ones that joined; every one of them receives
	// the result, through the manager sender and the observers.
	waiters []string
}

type providerUsageState struct {
	mu      sync.Mutex
	entries map[string]*providerUsageEntry
	// generation is one monotonic counter for every entry, so a callback that
	// outlived a dropped entry can never match its replacement.
	generation  uint64
	now         func() time.Time
	after       providerUsageTimerFunc
	wg          sync.WaitGroup
	observers   map[int]func(string, acp.ProviderUsageUpdate)
	observerSeq int
}

func (m *Manager) usageNow() time.Time {
	if m.usage.now != nil {
		return m.usage.now()
	}
	return time.Now()
}

func (m *Manager) usageAfter(d time.Duration, fn func()) func() bool {
	if m.usage.after != nil {
		return m.usage.after(d, fn)
	}
	t := time.AfterFunc(d, fn)
	return t.Stop
}

// SetProviderUsageClock injects the clock and the timer of the usage cache:
// now replaces time.Now, after schedules fn after d and returns its stop.
// Stands and tests use it to cross the pacing floor without waiting; nil
// restores the real clock.
func (m *Manager) SetProviderUsageClock(now func() time.Time, after func(d time.Duration, fn func()) (stop func() bool)) {
	m.usage.mu.Lock()
	defer m.usage.mu.Unlock()
	m.usage.now = now
	if after == nil {
		m.usage.after = nil
	} else {
		m.usage.after = providerUsageTimerFunc(after)
	}
}

// turnRanKey marks, on a turn's own context, that the turn reached its
// runner; the marker is per admission, so a concurrent admission that loses
// the lock cannot touch another turn's state.
type turnRanKey struct{}

// MarkTurnRan records on the turn context that the turn is about to call the
// model, so its release refreshes the provider usage. Doors that admit a
// turn through BeginTurn call it right before running the agent.
func MarkTurnRan(ctx context.Context) {
	if ctx == nil {
		return
	}
	if ran, ok := ctx.Value(turnRanKey{}).(*atomic.Bool); ok {
		ran.Store(true)
	}
}

func withTurnRanMarker(ctx context.Context) (context.Context, *atomic.Bool) {
	ran := &atomic.Bool{}
	return context.WithValue(ctx, turnRanKey{}, ran), ran
}

// AddUsageObserver registers fn for every fresh usage snapshot the manager
// builds, with the id of the session that asked for it ("" for a plain
// read). Same contract as AddTurnObserver: fn runs on the goroutine that
// completed the fetch and must not block.
func (m *Manager) AddUsageObserver(fn func(sessionID string, u acp.ProviderUsageUpdate)) (remove func()) {
	if fn == nil {
		return func() {}
	}
	m.usage.mu.Lock()
	if m.usage.observers == nil {
		m.usage.observers = make(map[int]func(string, acp.ProviderUsageUpdate))
	}
	m.usage.observerSeq++
	id := m.usage.observerSeq
	m.usage.observers[id] = fn
	m.usage.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.usage.mu.Lock()
			delete(m.usage.observers, id)
			m.usage.mu.Unlock()
		})
	}
}

func (m *Manager) notifyUsageObservers(sessionID string, u acp.ProviderUsageUpdate) {
	m.usage.mu.Lock()
	fns := make([]func(string, acp.ProviderUsageUpdate), 0, len(m.usage.observers))
	for _, fn := range m.usage.observers {
		fns = append(fns, fn)
	}
	m.usage.mu.Unlock()
	for _, fn := range fns {
		fn(sessionID, u)
	}
}

// providerUsageSource says whether a provider type has a usage source.
func providerUsageSource(providerType string) bool {
	return strings.EqualFold(strings.TrimSpace(providerType), "neuraldeep")
}

// ProviderUsage returns the account usage behind the named provider row. A
// provider type without a usage source answers Unsupported; otherwise the
// cached snapshot inside the TTL, or a fresh one. refresh bypasses the TTL:
// past the floor it fetches at once, inside it the cached snapshot comes
// back with RefreshPending and the fetch is deferred to the floor's end
// (the caller reads again when it says so). A fetch failure answers the
// previous snapshot marked Stale with the error kind, so a surface keeps the
// last known numbers.
func (m *Manager) ProviderUsage(ctx context.Context, providerName string, refresh bool) (*acp.ProviderUsageUpdate, error) {
	return m.providerUsageRead(ctx, providerName, refresh, "")
}

// ProviderUsageForSession is ProviderUsage for a surface that cannot read
// again on its own schedule, the console: when the read is deferred by the
// floor, the deferred fetch delivers its result to sessionID through the
// manager sender, so the caller applies the answer it gets now and the
// fresh one when it lands.
func (m *Manager) ProviderUsageForSession(ctx context.Context, sessionID, providerName string, refresh bool) (*acp.ProviderUsageUpdate, error) {
	return m.providerUsageRead(ctx, providerName, refresh, strings.TrimSpace(sessionID))
}

// providerUsageRead is the shared read; deferTo names the session a deferred
// fetch reports to ("" for a plain read, whose caller reads again itself).
// A fetch the read starts delivers to nobody: the caller receives that
// result directly.
func (m *Manager) providerUsageRead(ctx context.Context, providerName string, refresh bool, deferTo string) (*acp.ProviderUsageUpdate, error) {
	cfg := m.activeCfg()
	prov := cfg.FindProvider(strings.TrimSpace(providerName))
	if prov == nil {
		return nil, fmt.Errorf("unknown provider %q", providerName)
	}
	if !providerUsageSource(prov.Type) {
		return &acp.ProviderUsageUpdate{
			SessionUpdate: acp.UpdateTypeProviderUsage,
			Provider:      prov.Name,
			ProviderType:  prov.Type,
			Unsupported:   true,
		}, nil
	}
	authPath := config.ProviderAuthPath(cfg.Paths.Home, prov.Name, prov.Type)
	fingerprint := llm.NeuralDeepUsageFingerprint(*prov, authPath)

	m.usage.mu.Lock()
	e := m.usageEntryLocked(prov.Name, fingerprint)
	now := m.usageNow()
	age := now.Sub(e.fetchedAt)
	sinceAttempt := now.Sub(e.lastAttempt)
	switch {
	case e.update != nil && !e.backoffUntil.IsZero() && now.Before(e.backoffUntil):
		// The hub asked for a pause: serve what we have until it passes; a
		// refresh is deferred to the pause's end and the answer says so.
		if refresh {
			m.usageDeferLocked(prov.Name, e, deferTo, now)
		}
		u := m.usageDeliverableLocked(e, now)
		m.usage.mu.Unlock()
		return &u, nil
	case e.unauthorized && !refresh:
		u := m.usageDeliverableLocked(e, now)
		m.usage.mu.Unlock()
		return &u, nil
	case e.inflight != nil:
		// A fetch is running: a fresh-enough cache answers at once, anything
		// else joins it.
		if e.update != nil && !refresh && age < providerUsageTTL {
			u := m.usageDeliverableLocked(e, now)
			m.usage.mu.Unlock()
			return &u, nil
		}
		done := e.inflight
		m.usage.mu.Unlock()
		return m.usageAwait(ctx, done, prov.Name)
	case e.update != nil && !refresh && age < providerUsageTTL:
		u := m.usageDeliverableLocked(e, now)
		m.usage.mu.Unlock()
		return &u, nil
	case e.update != nil && !e.lastAttempt.IsZero() && sinceAttempt < providerUsageFloor:
		// Inside the floor: the snapshot comes back now, the fetch later.
		m.usageDeferLocked(prov.Name, e, deferTo, now)
		u := m.usageDeliverableLocked(e, now)
		m.usage.mu.Unlock()
		return &u, nil
	}
	done := m.usageStartFetchLocked(prov, authPath, e, "")
	m.usage.mu.Unlock()
	return m.usageAwait(ctx, done, prov.Name)
}

// usageAwait waits for a running fetch and returns the stored snapshot.
func (m *Manager) usageAwait(ctx context.Context, done <-chan struct{}, providerName string) (*acp.ProviderUsageUpdate, error) {
	select {
	case <-done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	m.usage.mu.Lock()
	defer m.usage.mu.Unlock()
	e := m.usage.entries[providerName]
	if e == nil || e.update == nil {
		return nil, fmt.Errorf("provider %q: usage fetch produced no snapshot", providerName)
	}
	u := m.usageDeliverableLocked(e, m.usageNow())
	return &u, nil
}

// usageEntryLocked returns the entry of a provider, replacing it when the
// account behind the row changed (a rotated key, another deployment).
func (m *Manager) usageEntryLocked(name, fingerprint string) *providerUsageEntry {
	if m.usage.entries == nil {
		m.usage.entries = make(map[string]*providerUsageEntry)
	}
	e := m.usage.entries[name]
	if e != nil && e.fingerprint == fingerprint {
		return e
	}
	if e != nil {
		m.usageInvalidateLocked(e)
	}
	m.usage.generation++
	e = &providerUsageEntry{fingerprint: fingerprint, generation: m.usage.generation}
	m.usage.entries[name] = e
	return e
}

// usageInvalidateLocked stops the entry's asynchronous work: the deferred
// timer is cancelled, the in-flight fetch is cancelled and its result will
// be discarded because the generation moved on.
func (m *Manager) usageInvalidateLocked(e *providerUsageEntry) {
	m.usage.generation++
	e.generation = m.usage.generation
	if e.pendingStop != nil {
		e.pendingStop()
		e.pendingStop, e.pendingAt, e.pendingSessions = nil, time.Time{}, nil
	}
	if e.inflightCancel != nil {
		e.inflightCancel()
	}
}

// usageDeliverableLocked renders the entry's snapshot for delivery now:
// relative durations corrected for the snapshot's age, the deferred refresh
// announced when one is pending.
func (m *Manager) usageDeliverableLocked(e *providerUsageEntry, now time.Time) acp.ProviderUsageUpdate {
	var u acp.ProviderUsageUpdate
	if e.update != nil {
		u = ageCorrectedUsage(*e.update, now)
	}
	if e.pendingStop != nil && !e.pendingAt.IsZero() {
		u.RefreshPending = true
		if in := int(e.pendingAt.Sub(now).Seconds() + 0.999); in > 0 {
			u.RefreshInSec = in
		}
	}
	return u
}

// usageDeferLocked schedules the refresh for the end of the floor, unless
// one is already pending.
func (m *Manager) usageDeferLocked(name string, e *providerUsageEntry, sessionID string, now time.Time) {
	if sessionID != "" {
		e.pendingSessions = appendSession(e.pendingSessions, sessionID)
	}
	if e.pendingStop != nil {
		return
	}
	// The floor's end, or the backoff's end when the hub asked for a pause.
	at := e.lastAttempt.Add(providerUsageFloor)
	if e.backoffUntil.After(at) {
		at = e.backoffUntil
	}
	delay := at.Sub(now)
	if delay < 0 {
		delay = 0
	}
	generation := e.generation
	e.pendingAt = at
	e.pendingStop = m.usageAfter(delay, func() { m.usageDeferredFire(name, generation) })
}

// usageDeferredFire runs the deferred refresh when its timer fires.
func (m *Manager) usageDeferredFire(name string, generation uint64) {
	m.usage.mu.Lock()
	defer m.usage.mu.Unlock()
	e := m.usage.entries[name]
	if e == nil || e.generation != generation {
		return
	}
	sessions := e.pendingSessions
	e.pendingStop, e.pendingAt, e.pendingSessions = nil, time.Time{}, nil
	if e.inflight != nil {
		// A fetch is already running: the deferred sessions join it.
		for _, id := range sessions {
			e.waiters = appendSession(e.waiters, id)
		}
		return
	}
	cfg := m.activeCfg()
	prov := cfg.FindProvider(name)
	if prov == nil || !providerUsageSource(prov.Type) {
		return
	}
	m.usageStartFetchLocked(prov, config.ProviderAuthPath(cfg.Paths.Home, prov.Name, prov.Type), e, "")
	e.waiters = appendSessions(e.waiters, sessions)
}

// appendSessions adds every id once.
func appendSessions(list []string, ids []string) []string {
	for _, id := range ids {
		list = appendSession(list, id)
	}
	return list
}

// usageStartFetchLocked starts the fetch of an entry and returns the channel
// closed when it ends. The result is stored only when the entry's
// generation is unchanged, then delivered to the requesting session and to
// the observers.
func (m *Manager) usageStartFetchLocked(prov *config.ProviderConfig, authPath string, e *providerUsageEntry, sessionID string) <-chan struct{} {
	done := make(chan struct{})
	// Cancel-only: the fetcher bounds its HTTP read itself and the
	// credential helper keeps its own budget; a logout, a config swap or a
	// rotated key cancels both through the entry's invalidation.
	ctx, cancel := context.WithCancel(context.Background())
	e.inflight, e.inflightCancel = done, cancel
	e.lastAttempt = m.usageNow()
	e.waiters = nil
	if sessionID != "" {
		e.waiters = appendSession(e.waiters, sessionID)
	}
	generation := e.generation
	name := prov.Name
	provider := *prov
	m.usage.wg.Add(1)
	go func() {
		defer m.usage.wg.Done()
		defer cancel()
		usage, err := llm.NeuralDeepUsageForProvider(ctx, provider, authPath)
		fetchedAt := m.usageNow()
		m.usage.mu.Lock()
		if e.generation != generation {
			// Superseded by a logout, a config swap or a rotated key: the
			// account this answer describes is gone.
			m.usage.mu.Unlock()
			close(done)
			return
		}
		e.inflight, e.inflightCancel = nil, nil
		if err == nil {
			mapped := mapNeuralDeepUsage(usage, name, fetchedAt)
			e.update, e.fetchedAt = &mapped, fetchedAt
			e.unauthorized, e.backoffUntil = false, time.Time{}
		} else {
			m.usageRecordFailureLocked(e, name, provider.Type, err, fetchedAt)
		}
		delivered := m.usageDeliverableLocked(e, fetchedAt)
		// Every session that asked while the fetch ran gets the result: the
		// one that started it and the ones that joined it, each through the
		// manager sender and through the observers (coddy http listens there
		// only). A fetch nobody asked for by session reaches the observers
		// once, unattributed.
		waiters := e.waiters
		e.waiters = nil
		m.usage.mu.Unlock()
		close(done)
		if len(waiters) == 0 {
			m.notifyUsageObservers("", delivered)
			return
		}
		for _, id := range waiters {
			if m.server != nil {
				_ = m.server.SendSessionUpdate(id, delivered)
			}
			m.notifyUsageObservers(id, delivered)
		}
	}()
	return done
}

// appendSession adds a session id to a waiter list once.
func appendSession(list []string, id string) []string {
	for _, have := range list {
		if have == id {
			return list
		}
	}
	return append(list, id)
}

// usageRecordFailureLocked folds a failed fetch into the entry: the
// previous windows stay, marked stale, with the failure kind; a blocked
// account becomes a Blocked snapshot; a rejected key sticks; a requested
// pause becomes the backoff.
func (m *Manager) usageRecordFailureLocked(e *providerUsageEntry, name, providerType string, err error, at time.Time) {
	fresh := acp.ProviderUsageUpdate{SessionUpdate: acp.UpdateTypeProviderUsage, Provider: name, ProviderType: providerType}
	base := fresh
	if e.update != nil {
		base = *e.update
		base.Stale = true
	}
	base.Error, base.RefreshPending, base.RefreshInSec = "", false, 0
	ue, ok := llm.IsNeuralDeepUsageError(err)
	kind := ProviderUsageErrorUnavailable
	if ok {
		kind = ue.Kind
	}
	switch kind {
	case llm.NeuralDeepUsageUnauthorized:
		// The hub's word is final: numbers read with a key it no longer
		// honours are not this account's numbers any more.
		e.unauthorized = true
		base = fresh
		base.Error = ProviderUsageErrorUnauthorized
	case llm.NeuralDeepUsageForbidden:
		// The account is blocked: the old windows stay for reference, the
		// old blockers do not, the block is the reason now.
		base.Blocked = true
		base.Blockers = []string{providerUsageBlockerUser}
		base.RetryAt, base.RetryInSec = "", 0
	case llm.NeuralDeepUsageInvalid:
		base.Error = ProviderUsageErrorInvalid
	default:
		base.Error = ProviderUsageErrorUnavailable
		if ok && ue.RetryAfter > 0 {
			pause := ue.RetryAfter
			if pause > providerUsageBackoffCap {
				pause = providerUsageBackoffCap
			}
			e.backoffUntil = at.Add(pause)
		}
	}
	if e.update == nil {
		e.fetchedAt = at
		base.FetchedAt = at.UTC().Format(time.RFC3339)
	}
	e.update = &base
	if m.log != nil && !errors.Is(err, context.Canceled) {
		m.log.Debug("provider usage fetch failed", "provider", name, "error", err)
	}
}

// DropProviderUsage forgets the cached usage of a provider, including the
// sticky rejected-key mark, and discards its asynchronous work. The
// credential handlers call it after a login or a logout.
func (m *Manager) DropProviderUsage(providerName string) {
	m.usage.mu.Lock()
	defer m.usage.mu.Unlock()
	if e := m.usage.entries[providerName]; e != nil {
		m.usageInvalidateLocked(e)
		delete(m.usage.entries, providerName)
	}
}

// resetProviderUsage discards every cached snapshot and its asynchronous
// work (shutdown).
func (m *Manager) resetProviderUsage() {
	m.usage.mu.Lock()
	defer m.usage.mu.Unlock()
	for name, e := range m.usage.entries {
		m.usageInvalidateLocked(e)
		delete(m.usage.entries, name)
	}
}

// pauseProviderUsage stops the asynchronous work of every entry but keeps
// the snapshots and the pacing state: storeConfig calls it because the rows
// behind the cache may have changed. A row whose credential or endpoint did
// change is caught by the fingerprint on its next read; a row that did not
// keeps its floor, so a settings save is not a free extra fetch.
func (m *Manager) pauseProviderUsage() {
	m.usage.mu.Lock()
	defer m.usage.mu.Unlock()
	for _, e := range m.usage.entries {
		m.usageInvalidateLocked(e)
		e.inflight, e.inflightCancel = nil, nil
	}
}

// WaitProviderUsageIdle blocks until every in-flight usage fetch returned,
// or the timeout passes.
func (m *Manager) WaitProviderUsageIdle(timeout time.Duration) error {
	done := make(chan struct{})
	go func() {
		m.usage.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("provider usage fetches still running after %s", timeout)
	}
}

// ShutdownProviderUsage cancels deferred refreshes and in-flight fetches and
// joins them: nothing runs after it returns, so a test's stand-in server can
// close and a process can exit cleanly.
func (m *Manager) ShutdownProviderUsage(timeout time.Duration) {
	m.resetProviderUsage()
	_ = m.WaitProviderUsageIdle(timeout)
}

// usageProviderForSession resolves the provider row behind a session's
// effective model; ok is false when it has no usage source.
func (m *Manager) usageProviderForSession(st *State) (*config.ProviderConfig, bool) {
	if st == nil {
		return nil, false
	}
	cfg := m.activeCfg()
	entry := cfg.FindModelEntry(st.EffectiveModelID(cfg))
	if entry == nil {
		return nil, false
	}
	prov := cfg.FindProvider(entry.ProviderName())
	if prov == nil || !providerUsageSource(prov.Type) {
		return nil, false
	}
	return prov, true
}

// publishProviderUsageAsync refreshes the usage behind a session's model and
// sends the result to the session through the manager sender (the console
// and ACP clients) and to the observers (the HTTP events hub). A refresh
// deferred by the floor delivers the current snapshot at once, marked
// RefreshPending, and the fresh one when the timer fires. It returns
// without waiting for the network.
func (m *Manager) publishProviderUsageAsync(sessionID string, st *State) {
	prov, ok := m.usageProviderForSession(st)
	if !ok {
		return
	}
	cfg := m.activeCfg()
	authPath := config.ProviderAuthPath(cfg.Paths.Home, prov.Name, prov.Type)
	fingerprint := llm.NeuralDeepUsageFingerprint(*prov, authPath)

	m.usage.mu.Lock()
	e := m.usageEntryLocked(prov.Name, fingerprint)
	now := m.usageNow()
	var deliverNow *acp.ProviderUsageUpdate
	switch {
	case e.inflight != nil:
		// The running fetch delivers to every session that joined it; the
		// current snapshot, when there is one, goes out right away.
		e.waiters = appendSession(e.waiters, sessionID)
		if e.update != nil {
			u := m.usageDeliverableLocked(e, now)
			deliverNow = &u
		}
	case e.update != nil && !e.backoffUntil.IsZero() && now.Before(e.backoffUntil):
		m.usageDeferLocked(prov.Name, e, sessionID, now)
		u := m.usageDeliverableLocked(e, now)
		deliverNow = &u
	case e.update != nil && !e.lastAttempt.IsZero() && now.Sub(e.lastAttempt) < providerUsageFloor:
		m.usageDeferLocked(prov.Name, e, sessionID, now)
		u := m.usageDeliverableLocked(e, now)
		deliverNow = &u
	default:
		m.usageStartFetchLocked(prov, authPath, e, sessionID)
	}
	m.usage.mu.Unlock()
	if deliverNow != nil {
		if m.server != nil {
			_ = m.server.SendSessionUpdate(sessionID, *deliverNow)
		}
		// The HTTP server listens through the observers only: a deferred or
		// backed-off turn end still has to reach its events stream.
		m.notifyUsageObservers(sessionID, *deliverNow)
	}
}

// publishProviderUsageOnReady is the session-ready trigger: an automatic
// read, so the footer is populated before the first prompt without a fetch
// when the cache is warm.
func (m *Manager) publishProviderUsageOnReady(sessionID string, st *State) {
	prov, ok := m.usageProviderForSession(st)
	if !ok || m.server == nil {
		return
	}
	name := prov.Name
	m.usage.wg.Add(1)
	go func() {
		defer m.usage.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), providerUsageReadyBudget)
		defer cancel()
		// A plain read: the fetch it may start delivers to nobody, so the
		// session receives the snapshot exactly once, from here.
		u, err := m.providerUsageRead(ctx, name, false, "")
		if err != nil || u == nil || u.Unsupported {
			return
		}
		_ = m.server.SendSessionUpdate(sessionID, *u)
	}()
}

// mapNeuralDeepUsage turns the hub payload into the surface-facing update.
// Money stays in rubles, percentages are computed here from the counters and
// clamped, and every relative duration is as the hub reported it (age
// correction happens at delivery).
func mapNeuralDeepUsage(u *llm.NeuralDeepUsage, providerName string, fetchedAt time.Time) acp.ProviderUsageUpdate {
	out := acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage,
		Provider:      providerName,
		ProviderType:  "neuraldeep",
		ObservedAt:    strings.TrimSpace(u.ObservedAt),
		FetchedAt:     fetchedAt.UTC().Format(time.RFC3339),
		Plan:          strings.TrimSpace(u.Tier),
		KeyName:       strings.TrimSpace(u.Key.Name),
		// A missing fair_use reads as metered: only an explicit false lifts
		// the windows.
		Unlimited: u.Bypass || (u.FairUse != nil && !*u.FairUse) || u.UnlimitedVolume,
	}
	observedAt, _ := time.Parse(time.RFC3339, out.ObservedAt)
	if w, ok := countedUsageWindow("session", u.Chat.Session); ok {
		out.Windows = append(out.Windows, w)
	}
	if w, ok := countedUsageWindow("week", u.Chat.Week); ok {
		out.Windows = append(out.Windows, w)
	}
	if d := u.DailyCapacity; d != nil {
		w := acp.UsageWindow{ID: "day", Label: "day", UsedPercent: clampPercent(d.PctUsed), Exhausted: d.Exhausted}
		if at, err := time.Parse(time.RFC3339, strings.TrimSpace(d.ResetsAt)); err == nil {
			w.ResetsAt = at.UTC().Format(time.RFC3339)
			if !observedAt.IsZero() {
				if in := int(at.Sub(observedAt).Seconds()); in > 0 {
					w.ResetInSec = in
				}
			}
		}
		out.Windows = append(out.Windows, w)
	}
	if r := u.Chat.RPM; r.Limit != nil && r.Used != nil {
		rate := &acp.UsageRate{Used: *r.Used, Limit: *r.Limit}
		if r.Remaining != nil {
			rate.Remaining = *r.Remaining
		} else if rate.Limit > rate.Used {
			rate.Remaining = rate.Limit - rate.Used
		}
		if r.ResetInSec != nil {
			rate.ResetInSec = *r.ResetInSec
		}
		out.Rate = rate
	}
	if u.Chat.CooldownSec != nil && *u.Chat.CooldownSec > 0 {
		out.CooldownSec = *u.Chat.CooldownSec
	}
	if w := u.Wallet; w != nil {
		out.Wallet = &acp.UsageWallet{BalanceRub: w.BalanceRub, SpentRub30d: w.SpentRub30d}
	}
	for _, opt := range u.Options {
		for _, model := range opt.Models {
			if model = strings.TrimSpace(model); model != "" {
				out.UnlimitedModels = append(out.UnlimitedModels, model)
			}
		}
	}
	out.Blocked = !u.Decision.CanRequest
	for _, b := range u.Decision.Blockers {
		if b = strings.TrimSpace(b); b != "" {
			out.Blockers = append(out.Blockers, b)
		}
	}
	if out.Blocked {
		switch {
		case u.Decision.RetryAfterSec != nil && *u.Decision.RetryAfterSec > 0:
			out.RetryInSec = *u.Decision.RetryAfterSec
			if !observedAt.IsZero() {
				out.RetryAt = observedAt.Add(time.Duration(out.RetryInSec) * time.Second).UTC().Format(time.RFC3339)
			}
		default:
			// The hub sent no delay: the window the blocker names says when.
			if w := blockerWindow(out, out.Blockers); w != nil {
				out.RetryAt, out.RetryInSec = w.ResetsAt, w.ResetInSec
			}
		}
	}
	return out
}

// countedUsageWindow maps a session or week gate; a null limit means the
// window does not apply to the key and it is dropped.
func countedUsageWindow(id string, g llm.NeuralDeepUsageGate) (acp.UsageWindow, bool) {
	if g.Limit == nil {
		return acp.UsageWindow{}, false
	}
	used := 0
	if g.Used != nil {
		used = *g.Used
	}
	limit := *g.Limit
	w := acp.UsageWindow{ID: id, Label: usageWindowLabel(id, g.Window), Used: intPtr(used), Limit: intPtr(limit)}
	if g.Remaining != nil {
		w.Remaining = intPtr(*g.Remaining)
	} else if limit > used {
		w.Remaining = intPtr(limit - used)
	} else {
		w.Remaining = intPtr(0)
	}
	if limit > 0 {
		w.UsedPercent = clampPercent(float64(used) / float64(limit) * 100)
		w.Exhausted = used >= limit
	}
	if g.ResetInSec != nil && *g.ResetInSec > 0 {
		w.ResetInSec = *g.ResetInSec
	}
	if at, err := time.Parse(time.RFC3339, strings.TrimSpace(g.ResetsAt)); err == nil {
		w.ResetsAt = at.UTC().Format(time.RFC3339)
	}
	return w, true
}

// usageWindowLabel is the display label of a window: the hub's own word
// when it is short, "week" for the iso-week, the id otherwise.
func usageWindowLabel(id, window string) string {
	window = strings.TrimSpace(window)
	switch {
	case window == "":
		return id
	case strings.EqualFold(window, "iso-week"):
		return "week"
	default:
		return window
	}
}

// blockerWindow finds the window a timed blocker refers to.
func blockerWindow(u acp.ProviderUsageUpdate, blockers []string) *acp.UsageWindow {
	want := ""
	for _, b := range blockers {
		switch b {
		case "session_exhausted", "session_cooldown":
			want = "session"
		case "week_exhausted":
			want = "week"
		case "daily_capacity_exhausted":
			want = "day"
		}
		if want != "" {
			break
		}
	}
	if want == "" {
		return nil
	}
	for i := range u.Windows {
		if u.Windows[i].ID == want {
			return &u.Windows[i]
		}
	}
	return nil
}

func clampPercent(v float64) float64 {
	switch {
	case v < 0 || v != v: // negative or NaN
		return 0
	case v > 100:
		return 100
	}
	return v
}

func intPtr(v int) *int { return &v }

// ageCorrectedUsage returns a copy of u with every relative duration
// decremented by the snapshot's age at now, clamped at zero, so a client can
// schedule from the values as received. The cached snapshot is not touched.
func ageCorrectedUsage(u acp.ProviderUsageUpdate, now time.Time) acp.ProviderUsageUpdate {
	fetchedAt, err := time.Parse(time.RFC3339, u.FetchedAt)
	if err != nil {
		return u
	}
	age := int(now.Sub(fetchedAt).Seconds())
	if age <= 0 {
		return u
	}
	out := u
	out.Windows = make([]acp.UsageWindow, len(u.Windows))
	for i, w := range u.Windows {
		w.ResetInSec = decremented(w.ResetInSec, age)
		out.Windows[i] = w
	}
	if u.Rate != nil {
		rate := *u.Rate
		rate.ResetInSec = decremented(rate.ResetInSec, age)
		out.Rate = &rate
	}
	out.RetryInSec = decremented(u.RetryInSec, age)
	if u.CooldownSec > 0 {
		out.CooldownSec = decremented(u.CooldownSec, age)
	}
	return out
}

func decremented(v, by int) int {
	if v <= 0 {
		return 0
	}
	if v <= by {
		return 0
	}
	return v - by
}
