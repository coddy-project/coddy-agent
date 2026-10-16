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

	closeOnce sync.Once
	removers  []func()
}

// NewCollector builds the families and starts observing: the provider
// attempts of the whole process, the turns of src.Mgr and the replacements of
// the live configuration. Close stops that.
func NewCollector(src Source) *Collector {
	c := &Collector{src: src, budget: newBudget(), turnStarts: map[string]time.Time{}, inflight: make(chan struct{}, maxScrapesInFlight)}
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
	c.removers = append(c.removers, llm.AddCallObserver(c.observeCall), agent.AddToolCallObserver(c.observeTool))
	if src.Mgr != nil {
		c.removers = append(c.removers, src.Mgr.AddTurnObserver(c.observeTurn))
	}
	if src.AddConfigObserver != nil {
		c.removers = append(c.removers, src.AddConfigObserver(c.observeConfig))
	}
	return c
}

// Close stops the observers. The counters stay readable, and a second Close
// does nothing.
func (c *Collector) Close() {
	c.closeOnce.Do(func() {
		for _, remove := range c.removers {
			remove()
		}
	})
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
