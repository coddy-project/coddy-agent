//go:build metrics

// Package metrics is the Prometheus exporter of coddy serve: the process's
// counters, each family in a private set of github.com/VictoriaMetrics/metrics,
// the state read from one snapshot per scrape, and the listener that serves
// them. Every file of the package is built with the metrics tag, and
// cmd/coddy/metrics.go is its only importer.
package metrics

import (
	"bytes"
	"log/slog"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	vm "github.com/VictoriaMetrics/metrics"

	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Source is what the exporter reads off the process it runs in.
type Source struct {
	// Status reads the process once: the subsystems, the turns and tasks in
	// flight, the disk of the session store.
	Status func() serve.Snapshot
	// Cfg is the live configuration, read when an attempt is observed and
	// when a scrape asks for the token.
	Cfg func() *config.Config
	// Mgr is the session manager, nil in a process without a session store.
	Mgr *session.Manager
	// AddConfigObserver registers fn for every replacement of the live
	// configuration and returns the function that removes it.
	AddConfigObserver func(func(*config.Config)) (remove func())
	// Log says when a reload leaves the exporter open beyond this machine.
	Log *slog.Logger
}

// The bounds of the duration histograms, in seconds: an attempt lasts as long
// as its whole stream, its first token comes within seconds, a tool runs from
// a file read to a long command, a turn from one answer to an afternoon of
// work.
var (
	attemptBuckets    = []float64{0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600}
	firstTokenBuckets = []float64{0.25, 0.5, 1, 2, 4, 8, 15, 30, 60}
	toolBuckets       = []float64{0.1, 0.5, 1, 5, 15, 60, 300}
	turnBuckets       = []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600}
)

// The sampler keeps the last hour of the exporter's own counters for the
// status document: seven series of samplePoints points, sampleEvery apart,
// about 13.5 KB in all, in memory only and empty again after a restart.
const (
	sampleEvery  = 15 * time.Second
	samplePoints = 240
)

// providerFailureOutcomes are the outcomes that make an attempt a failure of
// its provider: the ones coddy-rules.yml counts in CoddyProviderFailing. A
// test holds the two lists to each other.
var providerFailureOutcomes = []string{"server_error", "timeout", "auth", "rate_limited"}

// outcomeOK is the outcome of an attempt that succeeded.
const outcomeOK = "ok"

// The series, in the order the ring stores them.
const (
	serAttemptsOK = iota
	serProviderErrors
	serAttemptSecondsMean
	serTokensIn
	serTokensOut
	serTurnsRunning
	serTasksUnfinished
	seriesCount
)

// tickSource waits for the next time to sample at and returns it, or returns
// false once stop is closed. The real one ticks on the slot boundaries; a test
// hands in its own.
type tickSource func(stop <-chan struct{}) (at time.Time, ok bool)

// untilNextSlot is how long from now to the next slot boundary: a whole
// sampleEvery when now is on one.
func untilNextSlot(now time.Time) time.Duration {
	return sampleEvery - time.Duration(now.UnixNano()%int64(sampleEvery))
}

// slotTicks ticks just after each slot boundary of the wall clock, so a sample
// is named for the slot it was meant for however late the process started.
func slotTicks(stop <-chan struct{}) (time.Time, bool) {
	timer := time.NewTimer(untilNextSlot(time.Now()))
	defer timer.Stop()
	select {
	case at := <-timer.C:
		return at, true
	case <-stop:
		return time.Time{}, false
	}
}

// totals are counters summed over some series: one provider's, or every
// provider's, over its models and over both values of the attempt label. They
// only grow, so a rise is the difference of two readings.
type totals struct {
	attempts, ok, failures float64 // attempts of every outcome, successes, the failure outcomes
	count, seconds         float64 // the attempt durations: how many, and their sum
	tokensIn, tokensOut    float64
}

func (t totals) minus(o totals) totals {
	return totals{
		attempts: t.attempts - o.attempts, ok: t.ok - o.ok, failures: t.failures - o.failures,
		count: t.count - o.count, seconds: t.seconds - o.seconds,
		tokensIn: t.tokensIn - o.tokensIn, tokensOut: t.tokensOut - o.tokensOut,
	}
}

// reading is one read of the exporter's counters and of the process.
type reading struct {
	at        time.Time
	providers map[string]totals
	all       totals
	// turns and tasks are gauges: the turns running, and the background tasks
	// that are queued or running, of every kind.
	turns, tasks float64
}

// Collector is the state of the exporter of one coddy serve process: the
// instruments the observers feed, each family in a private set, and the
// readers of the process's state. It outlives the listener that serves it, so
// a listener rebuilt on another address continues the same counters.
type Collector struct {
	src    Source
	budget *budget

	// families are the instrument families in the order a scrape writes them.
	families []*instruments

	attempts       *instruments
	attemptSeconds *instruments
	firstToken     *instruments
	inputTokens    *instruments
	cachedTokens   *instruments
	outputTokens   *instruments
	toolCalls      *instruments
	toolSeconds    *instruments
	turnsStarted   *vm.Counter
	turnsEnded     *vm.Counter
	turnSeconds    *vm.PrometheusHistogram
	reloads        *vm.Counter

	// turnStarts holds when each running turn began, by session, for the
	// duration its ended edge observes.
	turnMu     sync.Mutex
	turnStarts map[string]time.Time

	// inflight bounds the scrapes of every listener the collector serves
	// through, so a rebuilt listener does not bring fresh room for scrapes
	// that are still stuck.
	inflight chan struct{}
	// exposed is whether the live configuration leaves the exporter open
	// beyond this machine without a token, for the warning a reload owes.
	exposed atomic.Bool

	// The sampler. sampleStop ends its goroutine and sampleDone is closed once
	// it has ended. sampleMu guards the rest: prev is the latest reading, the
	// baseline of the next rise, lastSlot the slot of the newest point (zero
	// before the first), points the ring (the point of a slot lives at the slot's
	// number modulo samplePoints, so it needs no head) and rows what the counters
	// say of each provider. The sampler keeps no counter of its own, takes no
	// scrape slot and needs nobody to scrape.
	sampleStop chan struct{}
	sampleDone chan struct{}
	sampleMu   sync.Mutex
	prev       reading
	lastSlot   time.Time
	points     [seriesCount][samplePoints]float64
	rows       map[string]*serve.ProviderStats

	closeOnce sync.Once
	removers  []func()
}

// NewCollector builds the families and starts observing: the provider
// attempts of the whole process, the turns of src.Mgr and the replacements of
// the live configuration. It also starts the sampler, which keeps the last
// hour of the counters for the status document (Metrics). Close stops all of
// that.
func NewCollector(src Source) *Collector {
	return newCollectorAt(src, time.Now(), slotTicks)
}

// newCollectorAt is NewCollector with the sampler's clock in the caller's
// hands: the time of its first read, and where the times of the next ones come
// from.
func newCollectorAt(src Source, start time.Time, ticks tickSource) *Collector {
	c := &Collector{
		src: src, budget: newBudget(), turnStarts: map[string]time.Time{}, inflight: make(chan struct{}, maxScrapesInFlight),
		sampleStop: make(chan struct{}), sampleDone: make(chan struct{}), rows: map[string]*serve.ProviderStats{},
	}
	add := func(name, typ, help string) *instruments {
		f := newInstruments(family{name: name, typ: typ, help: help})
		c.families = append(c.families, f)
		return f
	}
	// A counter without labels exists from the start, so it reads 0 until it moves.
	counter := func(name, help string) *vm.Counter { return add(name, "counter", help).set.NewCounter(name) }

	c.attempts = add("coddy_llm_attempts_total", "counter",
		"Provider invocation attempts, retries included; some fail before a request is sent.")
	c.attemptSeconds = add("coddy_llm_attempt_duration_seconds", "histogram",
		"Duration of a provider invocation attempt, the whole stream included.")
	c.firstToken = add("coddy_llm_time_to_first_token_seconds", "histogram",
		"Time from the start of a streamed attempt to its first chunk with content: text, reasoning or a tool call.")
	c.inputTokens = add("coddy_llm_input_tokens_total", "counter", "Input tokens the provider reported for attempts that returned a response.")
	c.cachedTokens = add("coddy_llm_cached_input_tokens_total", "counter", "Cached input tokens: a subset of the input tokens.")
	c.outputTokens = add("coddy_llm_output_tokens_total", "counter", "Output tokens the provider reported for attempts that returned a response.")
	c.toolCalls = add("coddy_tool_calls_total", "counter",
		"Tool calls the agent decided on, by built-in tool (mcp for any tool of an MCP server) and outcome: ok, error, refused before running, denied by the person.")
	c.toolSeconds = add("coddy_tool_call_duration_seconds", "histogram", "How long a tool ran, for the calls that ran.")
	c.turnsStarted = counter("coddy_turns_started_total", "Turns started in this process. The turn families are read independently, so started minus ended need not equal coddy_turns_running.")
	c.turnsEnded = counter("coddy_turns_ended_total", "Turns ended in this process. The turn families are read independently, so started minus ended need not equal coddy_turns_running.")
	turns := add("coddy_turn_duration_seconds", "histogram", "Duration of the turns that ended in this process, from the moment a session became busy.")
	c.turnSeconds = turns.histogram(turnBuckets)
	c.reloads = counter("coddy_config_reloads_total", "Replacements of the live configuration in this process.")

	c.seed(src.Cfg())
	if cfg := src.Cfg(); cfg != nil {
		c.exposed.Store(cfg.Metrics.Enable && cfg.Metrics.ExposedWithoutToken())
	}
	// The first read comes after the seeds, so every configured provider has a
	// row from the start, and before the observers, so an attempt counted from
	// here on is a rise the first tick sees.
	c.clearRing()
	c.record(c.read(start), start.Truncate(sampleEvery))
	go c.sample(ticks)
	c.removers = append(c.removers, llm.AddCallObserver(c.observeCall), agent.AddToolCallObserver(c.observeTool))
	if src.Mgr != nil {
		c.removers = append(c.removers, src.Mgr.AddTurnObserver(c.observeTurn))
	}
	if src.AddConfigObserver != nil {
		c.removers = append(c.removers, src.AddConfigObserver(c.observeConfig))
	}
	return c
}

// Close stops the observers and the sampler, and returns once its goroutine
// has. The counters and the samples stay readable, and a second Close does
// nothing.
func (c *Collector) Close() {
	c.closeOnce.Do(func() {
		for _, remove := range c.removers {
			remove()
		}
		close(c.sampleStop)
	})
	<-c.sampleDone
}

// sample takes a sample at every time ticks gives, until it is stopped.
func (c *Collector) sample(ticks tickSource) {
	defer close(c.sampleDone)
	for {
		select {
		case <-c.sampleStop:
			return
		default:
		}
		at, ok := ticks(c.sampleStop)
		if !ok {
			return
		}
		c.tick(at)
	}
}

// tick takes the sample of the slot now falls in. A second tick in the same
// slot reads nothing, and a slot before the newest one means the clock went
// back, so the series starts again. Only one goroutine ticks at a time.
func (c *Collector) tick(now time.Time) {
	slot := now.Truncate(sampleEvery)
	c.sampleMu.Lock()
	seen := slot.Equal(c.lastSlot)
	c.sampleMu.Unlock()
	if seen {
		return
	}
	c.record(c.read(now), slot)
}

// read writes the four sampled families as a scrape writes them and sums what
// it parses, per provider and overall, and takes the gauges off the same
// snapshot of the process the state families read. It holds no lock, so a
// slow process delays the sample and nothing else.
func (c *Collector) read(at time.Time) reading {
	r := reading{at: at, providers: map[string]totals{}}
	var text bytes.Buffer
	for _, f := range []*instruments{c.attempts, c.attemptSeconds, c.inputTokens, c.outputTokens} {
		f.writeTo(&text)
	}
	for line := range strings.Lines(text.String()) {
		name, labels, value, ok := parseSample(strings.TrimSuffix(line, "\n"))
		if !ok {
			continue
		}
		provider := labels["provider"]
		t := r.providers[provider]
		if !c.fold(&t, name, labels["outcome"], value) {
			continue
		}
		r.providers[provider] = t
		c.fold(&r.all, name, labels["outcome"], value)
	}
	snap := c.src.Status()
	r.turns = float64(snap.TurnsRunning)
	for _, byStatus := range snap.Tasks {
		for _, n := range byStatus {
			r.tasks += float64(n)
		}
	}
	return r
}

// fold adds one parsed sample to t when it belongs to a sampled family, and
// says whether it did.
func (c *Collector) fold(t *totals, name, outcome string, value float64) bool {
	switch name {
	case c.attempts.name:
		t.attempts += value
		switch {
		case outcome == outcomeOK:
			t.ok += value
		case slices.Contains(providerFailureOutcomes, outcome):
			t.failures += value
		}
	case c.attemptSeconds.name + "_count":
		t.count += value
	case c.attemptSeconds.name + "_sum":
		t.seconds += value
	case c.inputTokens.name:
		t.tokensIn += value
	case c.outputTokens.name:
		t.tokensOut += value
	default:
		return false
	}
	return true
}

// record writes the point of slot: the rises since the previous reading as
// rates per minute over the time since it, the mean of the attempts it timed,
// and the gauges as they are. Slots nobody sampled in between are gaps, and so
// is everything the first reading has no earlier one to compare with. A
// provider is stamped with the slot of a tick in which its successes or its
// failures rose, so one tick that saw both moved both.
func (c *Collector) record(cur reading, slot time.Time) {
	c.sampleMu.Lock()
	defer c.sampleMu.Unlock()
	first := c.lastSlot.IsZero()
	if slot.Before(c.lastSlot) {
		c.clearRing()
		first = true
	}
	if !first {
		for k := 1; k < int(slot.Sub(c.lastSlot)/sampleEvery) && k <= samplePoints; k++ {
			c.put(c.lastSlot.Add(time.Duration(k)*sampleEvery), gaps())
		}
	}

	point := gaps()
	rise := cur.all.minus(c.prev.all)
	if elapsed := cur.at.Sub(c.prev.at).Seconds(); !first && elapsed > 0 {
		perMinute := func(n float64) float64 {
			if n < 0 {
				return math.NaN()
			}
			return n * 60 / elapsed
		}
		point[serAttemptsOK] = perMinute(rise.ok)
		point[serProviderErrors] = perMinute(rise.failures)
		point[serTokensIn] = perMinute(rise.tokensIn)
		point[serTokensOut] = perMinute(rise.tokensOut)
		if rise.count > 0 && rise.seconds >= 0 {
			point[serAttemptSecondsMean] = rise.seconds / rise.count
		}
	}
	point[serTurnsRunning] = cur.turns
	point[serTasksUnfinished] = cur.tasks
	c.put(slot, point)

	for name, t := range cur.providers {
		row := c.rows[name]
		if row == nil {
			row = &serve.ProviderStats{Name: name}
			c.rows[name] = row
		}
		row.Attempts, row.Failures = uint64(t.attempts), uint64(t.failures)
		// A new value each time: a copy of the row handed out by Metrics holds
		// the pointer, so it is replaced and never written through.
		row.MeanSeconds = nil
		if t.count > 0 {
			if mean := t.seconds / float64(t.count); !math.IsNaN(mean) && !math.IsInf(mean, 0) {
				row.MeanSeconds = &mean
			}
		}
		if !c.prev.at.IsZero() {
			before := c.prev.providers[name]
			if t.failures > before.failures {
				row.LastFailureAt = slot
			}
			if t.ok > before.ok {
				row.LastOKAt = slot
			}
		}
	}
	c.prev, c.lastSlot = cur, slot
}

// clearRing empties the ring and forgets the newest slot.
func (c *Collector) clearRing() {
	for i := range c.points {
		for j := range c.points[i] {
			c.points[i][j] = math.NaN()
		}
	}
	c.lastSlot = time.Time{}
}

func gaps() [seriesCount]float64 {
	var p [seriesCount]float64
	for i := range p {
		p[i] = math.NaN()
	}
	return p
}

// slotIndex is where the point of slot lives in the ring.
func slotIndex(slot time.Time) int {
	n := slot.Unix() / int64(sampleEvery/time.Second)
	return int(((n % samplePoints) + samplePoints) % samplePoints)
}

func (c *Collector) put(slot time.Time, point [seriesCount]float64) {
	i := slotIndex(slot)
	for k := range point {
		c.points[k][i] = point[k]
	}
}

// Metrics is what the status document shows of the exporter: a row for each
// provider the counters know, by name, as of the latest sample, and the last
// hour, samplePoints points sampleEvery apart, oldest first and ending at the
// latest sample, a gap being a NaN. Both are copies made under one mutex.
func (c *Collector) Metrics() *serve.MetricsBlock {
	c.sampleMu.Lock()
	defer c.sampleMu.Unlock()
	block := &serve.MetricsBlock{
		Providers: make([]serve.ProviderStats, 0, len(c.rows)),
		Series:    serve.Series{StepSeconds: sampleEvery.Seconds(), End: c.lastSlot},
	}
	for _, row := range c.rows {
		block.Providers = append(block.Providers, *row)
	}
	sort.Slice(block.Providers, func(i, j int) bool { return block.Providers[i].Name < block.Providers[j].Name })
	s := &block.Series
	cols := [seriesCount]*serve.Samples{
		serAttemptsOK: &s.AttemptsOK, serProviderErrors: &s.ProviderErrors,
		serAttemptSecondsMean: &s.AttemptSecondsMean, serTokensIn: &s.TokensIn,
		serTokensOut: &s.TokensOut, serTurnsRunning: &s.TurnsRunning,
		serTasksUnfinished: &s.TasksUnfinished,
	}
	for k, col := range cols {
		*col = make(serve.Samples, samplePoints)
		for j := range samplePoints {
			age := time.Duration(samplePoints-1-j) * sampleEvery
			(*col)[j] = c.points[k][slotIndex(c.lastSlot.Add(-age))]
		}
	}
	return block
}

// observeCall counts one provider invocation attempt. The labels are decided
// here, against the configuration live now, and never again: a rename or a
// removal later leaves what was counted where it was.
func (c *Collector) observeCall(ev llm.CallEvent) {
	provider, model := c.budget.labels(c.src.Cfg(), ev.Provider, ev.Model)
	attempt := attemptFirst
	if ev.Retry {
		attempt = attemptRetry
	}
	c.attempts.counter("provider", provider, "model", model, "outcome", ev.Outcome, "attempt", attempt).Inc()
	c.attemptSeconds.histogram(attemptBuckets, "provider", provider).Update(ev.Duration.Seconds())
	if ev.FirstContent > 0 {
		c.firstToken.histogram(firstTokenBuckets, "provider", provider).Update(ev.FirstContent.Seconds())
	}
	addTokens(c.inputTokens, ev.InputTokens, provider, model)
	addTokens(c.cachedTokens, ev.CachedInputTokens, provider, model)
	addTokens(c.outputTokens, ev.OutputTokens, provider, model)
}

// addTokens counts n tokens, when an attempt reported any.
func addTokens(f *instruments, n int, provider, model string) {
	if n > 0 {
		f.counter("provider", provider, "model", model).Add(n)
	}
}

// observeTool counts one tool call, and how long the tool ran when it ran.
func (c *Collector) observeTool(ev agent.ToolCallEvent) {
	c.toolCalls.counter("tool", ev.Tool, "outcome", ev.Outcome).Inc()
	if ev.Outcome == agent.ToolOutcomeOK || ev.Outcome == agent.ToolOutcomeError {
		c.toolSeconds.histogram(toolBuckets, "tool", ev.Tool).Update(ev.Duration.Seconds())
	}
}

// observeTurn counts the two edges of a turn and the time between them; a
// woken edge announces a turn that already started.
func (c *Collector) observeTurn(ev session.TurnEvent) {
	switch ev.Phase {
	case session.TurnPhaseStarted:
		c.turnsStarted.Inc()
		c.turnMu.Lock()
		c.turnStarts[ev.SessionID] = ev.At
		c.turnMu.Unlock()
	case session.TurnPhaseEnded:
		c.turnsEnded.Inc()
		c.turnMu.Lock()
		began, ok := c.turnStarts[ev.SessionID]
		delete(c.turnStarts, ev.SessionID)
		c.turnMu.Unlock()
		if ok {
			c.turnSeconds.Update(max(0, ev.At.Sub(began).Seconds()))
		}
	}
}

// observeConfig counts a reload, seeds what it declares, and says so once
// when it leaves the exporter open beyond this machine: the token is read on
// every request, so clearing it changes no listener and no startup warning
// would tell.
func (c *Collector) observeConfig(next *config.Config) {
	c.reloads.Inc()
	c.seed(next)
	exposed := next != nil && next.Metrics.Enable && next.Metrics.ExposedWithoutToken()
	if !c.exposed.Swap(exposed) && exposed && c.src.Log != nil {
		logger.Component(c.src.Log, "metrics").Warn(exposedWarning, "addr", next.Metrics.ListenAddr())
	}
}

// seed gives every provider and declared model of cfg that the budget admits a
// series at zero for each outcome of a first attempt and of a retry and for
// each token counter, and the provider empty duration and first token
// histograms, so a rate or an increase over its first attempt
// has a sample to start from; the other model beside it is seeded for what
// cfg does not declare.
func (c *Collector) seed(cfg *config.Config) {
	if cfg == nil {
		return
	}
	seeded := map[[2]string]bool{}
	seed := func(provider, model string) {
		pair := [2]string{provider, model}
		if provider == otherLabel || seeded[pair] {
			return
		}
		seeded[pair] = true
		for _, outcome := range llm.CallOutcomes() {
			for _, attempt := range []string{attemptFirst, attemptRetry} {
				c.attempts.counter("provider", provider, "model", model, "outcome", outcome, "attempt", attempt)
			}
		}
		for _, tokens := range []*instruments{c.inputTokens, c.cachedTokens, c.outputTokens} {
			tokens.counter("provider", provider, "model", model)
		}
		c.attemptSeconds.histogram(attemptBuckets, "provider", provider)
		c.firstToken.histogram(firstTokenBuckets, "provider", provider)
	}
	for _, p := range cfg.Providers {
		seed(c.budget.labels(cfg, p.Name, ""))
	}
	for _, m := range cfg.Models {
		if provider, api, err := config.SplitModelRef(m.Model); err == nil {
			seed(c.budget.labels(cfg, provider, api))
		}
	}
}

// exposition is one scrape: the instrument families, the state of the process
// from one status snapshot, and the library's Go and process figures.
func (c *Collector) exposition() []byte {
	var b bytes.Buffer
	for _, f := range c.families {
		f.writeTo(&b)
	}
	writeState(&b, c.src.Status(), c.budget.admitted)
	writeLibrary(&b)
	return b.Bytes()
}

// The attempt label: the first attempt of a call or one the wrapper retried.
const (
	attemptFirst = "first"
	attemptRetry = "retry"
)

// The lifetime bound on the labels of the llm families: this many providers
// and named (provider, model) pairs, whatever happens to the configuration.
// Everything else is counted under otherLabel.
const (
	maxProviders  = 16
	maxModelPairs = 64
	otherLabel    = "other"
)

// budget decides the provider and model labels of an attempt and holds the
// bound on them. A name it admitted stays admitted, so a reload never takes a
// series away from a counter, and the series count stays bounded however often
// the configuration is rewritten. A restart starts a fresh budget.
type budget struct {
	mu        sync.Mutex
	providers map[string]struct{}
	pairs     map[[2]string]struct{}
}

func newBudget() *budget {
	return &budget{providers: map[string]struct{}{}, pairs: map[[2]string]struct{}{}}
}

// admitted reports whether provider holds a label of its own.
func (b *budget) admitted(provider string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.providers[provider]
	return ok
}

// labels folds the provider row and the API model id of an attempt against
// cfg: a name the configuration declares and the budget admits (or has
// admitted) stands for itself, anything else is other. The model is other
// under the provider other.
func (b *budget) labels(cfg *config.Config, provider, model string) (string, string) {
	if cfg == nil || provider == "" || cfg.FindProvider(provider) == nil {
		return otherLabel, otherLabel
	}
	declared := cfg.FindModelEntry(provider+"/"+model) != nil
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.providers[provider]; !ok {
		if len(b.providers) >= maxProviders {
			return otherLabel, otherLabel
		}
		b.providers[provider] = struct{}{}
	}
	if !declared {
		return provider, otherLabel
	}
	pair := [2]string{provider, model}
	if _, ok := b.pairs[pair]; !ok {
		if len(b.pairs) >= maxModelPairs {
			return provider, otherLabel
		}
		b.pairs[pair] = struct{}{}
	}
	return provider, model
}
