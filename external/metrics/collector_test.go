//go:build metrics

package metrics

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	vm "github.com/VictoriaMetrics/metrics"

	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// live stands in for the runtime: the configuration and the snapshot the
// collector reads, and the observers a reload tells.
type live struct {
	mu        sync.Mutex
	cfg       *config.Config
	snap      serve.Snapshot
	observers map[int]func(*config.Config)
	next      int
	removed   int
}

func newLive(cfg *config.Config) *live {
	return &live{cfg: cfg, observers: map[int]func(*config.Config){}}
}

func (l *live) source() Source {
	return Source{
		Status: func() serve.Snapshot {
			l.mu.Lock()
			defer l.mu.Unlock()
			return l.snap
		},
		Cfg: func() *config.Config {
			l.mu.Lock()
			defer l.mu.Unlock()
			return l.cfg
		},
		AddConfigObserver: func(fn func(*config.Config)) func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			id := l.next
			l.next++
			l.observers[id] = fn
			return func() {
				l.mu.Lock()
				defer l.mu.Unlock()
				if _, ok := l.observers[id]; ok {
					delete(l.observers, id)
					l.removed++
				}
			}
		},
		Log: slog.New(slog.DiscardHandler),
	}
}

// reload replaces the configuration and tells the observers, as the runtime does.
func (l *live) reload(cfg *config.Config) {
	l.mu.Lock()
	l.cfg = cfg
	fns := make([]func(*config.Config), 0, len(l.observers))
	for _, fn := range l.observers {
		fns = append(fns, fn)
	}
	l.mu.Unlock()
	for _, fn := range fns {
		fn(cfg)
	}
}

func (l *live) setStatus(snap serve.Snapshot) {
	l.mu.Lock()
	l.snap = snap
	l.mu.Unlock()
}

func (l *live) observerCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.observers)
}

// configWith declares providers and the models "provider/model" of each.
func configWith(models map[string][]string) *config.Config {
	cfg := &config.Config{}
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		cfg.Providers = append(cfg.Providers, config.ProviderConfig{Name: name, Type: "openai"})
		for _, m := range models[name] {
			cfg.Models = append(cfg.Models, config.ModelEntry{Model: name + "/" + m})
		}
	}
	return cfg
}

// newCollector builds a collector over l and closes it with the test.
func newCollector(t *testing.T, l *live) *Collector {
	t.Helper()
	c := NewCollector(l.source())
	t.Cleanup(c.Close)
	return c
}

// fakeProvider answers every call the same way.
type fakeProvider struct {
	resp *llm.Response
	err  error
}

func (f fakeProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return f.resp, f.err
}

func (f fakeProvider) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
	return f.resp, f.err
}

// attempt makes one invocation attempt through the resilient wrapper, which is
// what tells the process-wide call observers.
func attempt(provider, model string, resp *llm.Response, err error) {
	p := llm.WrapResilient(fakeProvider{resp: resp, err: err}, llm.ResilientOptions{
		Name: provider, Model: model, RetryDisabled: true,
	})
	_, _ = p.Complete(context.Background(), nil, nil)
}

func answered(input, cached, output int) *llm.Response {
	return &llm.Response{InputTokens: input, CachedInputTokens: cached, OutputTokens: output}
}

// sample is one line of the text exposition format.
type sample struct {
	name   string
	labels map[string]string
	value  float64
}

var (
	sampleLine = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{(.*)\})? (\S+)$`)
	labelPair  = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\.)*)"`)
	// unescape undoes the escapes of a label value in the text format.
	unescape = strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\n`, "\n")
)

// parseSamples reads an exposition. A line that is not a sample panics: the
// tests only read what the handler wrote.
func parseSamples(body string) []sample {
	var out []sample
	for _, line := range strings.Split(body, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := sampleLine.FindStringSubmatch(line)
		if m == nil {
			panic(fmt.Sprintf("not a sample line: %q", line))
		}
		v, err := strconv.ParseFloat(m[3], 64)
		if err != nil {
			panic(fmt.Sprintf("sample value of %q: %v", line, err))
		}
		labels := map[string]string{}
		for _, p := range labelPair.FindAllStringSubmatch(m[2], -1) {
			labels[p[1]] = unescape.Replace(p[2])
		}
		out = append(out, sample{name: m[1], labels: labels, value: v})
	}
	return out
}

// find returns the value of the one sample of name whose labels include want.
func find(body, name string, want map[string]string) (float64, bool) {
	for _, s := range parseSamples(body) {
		if s.name != name {
			continue
		}
		match := true
		for k, v := range want {
			if s.labels[k] != v {
				match = false
				break
			}
		}
		if match {
			return s.value, true
		}
	}
	return 0, false
}

// sumOf adds up every sample of name whose labels include want, so a family
// split by a label the caller does not name (attempt) still reads whole.
func sumOf(body, name string, want map[string]string) (total float64, found bool) {
	for _, s := range parseSamples(body) {
		if s.name != name {
			continue
		}
		match := true
		for k, v := range want {
			if s.labels[k] != v {
				match = false
				break
			}
		}
		if match {
			total += s.value
			found = true
		}
	}
	return total, found
}

// valueOf is find for a series the test requires.
func valueOf(t testing.TB, body, name string, want map[string]string) float64 {
	t.Helper()
	v, ok := find(body, name, want)
	if !ok {
		t.Fatalf("no %s%v in the scrape:\n%s", name, want, body)
	}
	return v
}

func lbl(kv ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

// scrape reads /metrics from the collector's handler the way Prometheus does.
func scrape(t testing.TB, c *Collector) string {
	t.Helper()
	rec := httptest.NewRecorder()
	c.Handler(func() string { return "" }).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics: %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func freeAddr(t testing.TB) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().String()
}

// attemptsOf is the attempts of a pair and an outcome, first ones and retries.
func attemptsOf(t testing.TB, body, provider, model, outcome string) float64 {
	t.Helper()
	v, ok := sumOf(body, "coddy_llm_attempts_total", lbl("provider", provider, "model", model, "outcome", outcome))
	if !ok {
		t.Fatalf("no coddy_llm_attempts_total for %s/%s/%s in the scrape:\n%s", provider, model, outcome, body)
	}
	return v
}

// A model renamed or removed after the event keeps the label it was counted
// under, and an attempt after the change is counted by what the configuration
// says then.
func TestTheModelLabelIsDecidedWhenTheAttemptIsObserved(t *testing.T) {
	l := newLive(configWith(map[string][]string{"stub": {"echo"}}))
	c := newCollector(t, l)

	attempt("stub", "echo", answered(10, 4, 3), nil)
	l.reload(configWith(map[string][]string{"stub": {"echo-2"}}))

	body := scrape(t, c)
	if got := attemptsOf(t, body, "stub", "echo", "ok"); got != 1 {
		t.Fatalf("the attempt counted before the rename: %v, want 1", got)
	}
	if got := valueOf(t, body, "coddy_llm_input_tokens_total", lbl("provider", "stub", "model", "echo")); got != 10 {
		t.Fatalf("input tokens of the renamed model: %v, want 10", got)
	}

	attempt("stub", "echo", answered(1, 0, 1), nil)
	attempt("stub", "echo-2", answered(1, 0, 1), nil)
	body = scrape(t, c)
	if got := attemptsOf(t, body, "stub", "echo", "ok"); got != 1 {
		t.Fatalf("a model no longer declared grew its old label: %v, want 1", got)
	}
	if got := attemptsOf(t, body, "stub", "other", "ok"); got != 1 {
		t.Fatalf("a model no longer declared counts as other: %v, want 1", got)
	}
	if got := attemptsOf(t, body, "stub", "echo-2", "ok"); got != 1 {
		t.Fatalf("a model declared now: %v, want 1", got)
	}
}

func TestAnUnknownOrEmptyProviderIsOther(t *testing.T) {
	l := newLive(configWith(map[string][]string{"stub": {"echo"}}))
	c := newCollector(t, l)

	attempt("", "", nil, nil)
	attempt("ghost", "echo", nil, nil)
	attempt("stub", "ghost-model", nil, nil)

	body := scrape(t, c)
	if got := attemptsOf(t, body, "other", "other", "ok"); got != 2 {
		t.Fatalf("attempts of an empty or undeclared provider: %v, want 2", got)
	}
	if got := attemptsOf(t, body, "stub", "other", "ok"); got != 1 {
		t.Fatalf("attempts of an undeclared model: %v, want 1", got)
	}
	for _, s := range parseSamples(body) {
		if s.labels["provider"] == "ghost" || s.labels["model"] == "ghost-model" {
			t.Fatalf("a name the configuration does not declare reached a label: %+v", s)
		}
	}
}

// Every admitted pair has its attempts, for each outcome, and its token
// counters at zero, and its provider an empty duration histogram, so an
// increase over the first attempt of a quiet node counts that attempt.
func TestEveryOutcomeIsSeededAtZeroForEachAdmittedPair(t *testing.T) {
	l := newLive(configWith(map[string][]string{"stub": {"echo", "big"}}))
	c := newCollector(t, l)
	outcomes := llm.CallOutcomes()
	if len(outcomes) != 9 {
		t.Fatalf("the outcome set has %d members, the catalogue 9", len(outcomes))
	}

	body := scrape(t, c)
	for _, model := range []string{"echo", "big", "other"} {
		for _, outcome := range outcomes {
			if got := attemptsOf(t, body, "stub", model, outcome); got != 0 {
				t.Errorf("seed stub/%s/%s = %v, want 0", model, outcome, got)
			}
		}
	}

	for _, model := range []string{"echo", "big", "other"} {
		for _, name := range []string{"coddy_llm_input_tokens_total", "coddy_llm_cached_input_tokens_total", "coddy_llm_output_tokens_total"} {
			if got, ok := find(body, name, map[string]string{"provider": "stub", "model": model}); !ok || got != 0 {
				t.Errorf("seed %s stub/%s = %v (present %v), want 0", name, model, got, ok)
			}
		}
	}
	if got, ok := find(body, "coddy_llm_attempt_duration_seconds_count", map[string]string{"provider": "stub"}); !ok || got != 0 {
		t.Errorf("the duration histogram of stub is not seeded empty: %v (present %v)", got, ok)
	}

	l.reload(configWith(map[string][]string{"stub": {"echo", "big"}, "second": {"fast"}}))
	body = scrape(t, c)
	for _, outcome := range outcomes {
		if got := attemptsOf(t, body, "second", "fast", outcome); got != 0 {
			t.Errorf("seed second/fast/%s after a reload = %v, want 0", outcome, got)
		}
		if got := attemptsOf(t, body, "second", "other", outcome); got != 0 {
			t.Errorf("seed second/other/%s after a reload = %v, want 0", outcome, got)
		}
	}
	for _, s := range parseSamples(body) {
		if s.name == "coddy_llm_attempts_total" && s.labels["provider"] == "other" {
			t.Errorf("the other provider is seeded: %+v", s)
		}
	}
}

// A configuration that is rewritten a thousand times never makes the series
// count grow past 16 providers, 64 named pairs and one other model each, and
// a counter that was admitted never goes down.
func TestTheLabelBudgetHoldsUnderConfigChurn(t *testing.T) {
	l := newLive(configWith(map[string][]string{"keep": {"m-0"}}))
	c := newCollector(t, l)
	attempt("keep", "m-0", nil, nil)

	for i := 1; i <= 1000; i++ {
		models := map[string][]string{
			"keep":                       {fmt.Sprintf("m-%d", i)},
			fmt.Sprintf("renamed-%d", i): {fmt.Sprintf("model-%d", i)},
		}
		l.reload(configWith(models))
		attempt("keep", fmt.Sprintf("m-%d", i), nil, nil)
		attempt(fmt.Sprintf("renamed-%d", i), fmt.Sprintf("model-%d", i), nil, nil)
	}

	body := scrape(t, c)
	providers := map[string]bool{}
	pairs := map[[2]string]bool{}
	series := 0
	for _, s := range parseSamples(body) {
		if s.name != "coddy_llm_attempts_total" {
			continue
		}
		series++
		providers[s.labels["provider"]] = true
		if s.labels["model"] != "other" {
			pairs[[2]string{s.labels["provider"], s.labels["model"]}] = true
		}
	}
	if len(providers) > 16+1 {
		t.Errorf("%d providers in the attempts, the budget is 16 and other", len(providers))
	}
	if len(pairs) > 64 {
		t.Errorf("%d named model pairs, the budget is 64", len(pairs))
	}
	if limit := (64 + 17) * 9 * 2; series > limit {
		t.Errorf("%d attempt series, the bound is %d", series, limit)
	}
	if got := attemptsOf(t, body, "keep", "m-0", "ok"); got != 1 {
		t.Errorf("an admitted counter moved: keep/m-0 = %v, want 1", got)
	}
	if got := attemptsOf(t, body, "other", "other", "ok"); got < 900 {
		t.Errorf("the attempts of folded providers = %v, want at least 900 under other", got)
	}
}

func TestTheKindsOfCountersAreFedByWhatTheyCount(t *testing.T) {
	l := newLive(configWith(map[string][]string{"stub": {"echo"}}))
	c := newCollector(t, l)

	attempt("stub", "echo", answered(100, 40, 7), nil)
	attempt("stub", "echo", nil, context.Canceled)
	attempt("stub", "echo", nil, errors.New("plain failure"))
	c.observeTurn(session.TurnEvent{SessionID: "s", Phase: session.TurnPhaseStarted})
	c.observeTurn(session.TurnEvent{SessionID: "s", Phase: session.TurnPhaseWoken})
	c.observeTurn(session.TurnEvent{SessionID: "s", Phase: session.TurnPhaseEnded})
	l.reload(configWith(map[string][]string{"stub": {"echo"}}))
	l.reload(configWith(map[string][]string{"stub": {"echo"}}))

	body := scrape(t, c)
	for outcome, want := range map[string]float64{"ok": 1, "canceled": 1, "error": 1, "timeout": 0} {
		if got := attemptsOf(t, body, "stub", "echo", outcome); got != want {
			t.Errorf("outcome %s = %v, want %v", outcome, got, want)
		}
	}
	for name, want := range map[string]float64{
		"coddy_llm_input_tokens_total":        100,
		"coddy_llm_cached_input_tokens_total": 40,
		"coddy_llm_output_tokens_total":       7,
	} {
		if got := valueOf(t, body, name, lbl("provider", "stub", "model", "echo")); got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	if got := valueOf(t, body, "coddy_llm_attempt_duration_seconds_count", lbl("provider", "stub")); got != 3 {
		t.Errorf("duration observations = %v, want one per attempt", got)
	}
	if got := valueOf(t, body, "coddy_llm_attempt_duration_seconds_bucket", lbl("provider", "stub", "le", "0.5")); got != 3 {
		t.Errorf("attempts under 0.5 s = %v, want 3", got)
	}
	if got := valueOf(t, body, "coddy_llm_attempt_duration_seconds_bucket", lbl("provider", "stub", "le", "600")); got != 3 {
		t.Errorf("attempts under 600 s = %v, want 3", got)
	}
	if _, ok := find(body, "coddy_llm_attempt_duration_seconds_bucket", lbl("provider", "stub", "le", "0.25")); ok {
		t.Error("the histogram has a bucket the catalogue does not")
	}
	if got := valueOf(t, body, "coddy_turns_started_total", nil); got != 1 {
		t.Errorf("turns started = %v, want 1 (a woken edge is not a start)", got)
	}
	if got := valueOf(t, body, "coddy_turns_ended_total", nil); got != 1 {
		t.Errorf("turns ended = %v, want 1", got)
	}
	if got := valueOf(t, body, "coddy_config_reloads_total", nil); got != 2 {
		t.Errorf("config reloads = %v, want 2", got)
	}
}

func TestCloseRemovesTheObserversAndIsIdempotent(t *testing.T) {
	l := newLive(configWith(map[string][]string{"stub": {"echo"}}))
	c := NewCollector(l.source())
	if l.observerCount() != 1 {
		t.Fatalf("the collector registered %d config observers, want 1", l.observerCount())
	}
	attempt("stub", "echo", nil, nil)
	c.Close()
	c.Close()
	if l.observerCount() != 0 {
		t.Fatalf("a closed collector still observes the configuration")
	}
	attempt("stub", "echo", nil, nil)
	if got := attemptsOf(t, scrape(t, c), "stub", "echo", "ok"); got != 1 {
		t.Fatalf("a closed collector kept counting: %v, want 1", got)
	}
}

func TestSubsystemUpLeavesTheExporterOut(t *testing.T) {
	l := newLive(configWith(nil))
	l.setStatus(serve.Snapshot{Subsystems: []serve.SubsystemStatus{
		{Kind: "httpserver", State: serve.StateRunning},
		{Kind: "metrics", State: serve.StateRunning},
		{Kind: "gateway", State: serve.StateStopped},
		{Kind: "swarm", State: serve.StateStarting},
		{Kind: "scheduler", State: serve.StateRetained},
	}})
	body := scrape(t, newCollector(t, l))

	for kind, want := range map[string]float64{"httpserver": 1, "gateway": 0, "swarm": 1, "scheduler": 1} {
		if got := valueOf(t, body, "coddy_subsystem_up", lbl("subsystem", kind)); got != want {
			t.Errorf("coddy_subsystem_up{subsystem=%q} = %v, want %v", kind, got, want)
		}
	}
	if _, ok := find(body, "coddy_subsystem_up", lbl("subsystem", "metrics")); ok {
		t.Error("the exporter reports itself as a subsystem")
	}
}

func TestTheStateGaugesReadOneSnapshot(t *testing.T) {
	l := newLive(configWith(nil))
	l.setStatus(serve.Snapshot{
		Build:               serve.Build{Version: "v9.9.9", Revision: "abc123", GoVersion: "go1.26", GOOS: "linux", GOARCH: "arm64", Tags: []string{"gateway", "metrics"}},
		RestartRequired:     true,
		TurnsRunning:        2,
		OldestTurnStartedAt: time.Now().Add(-90 * time.Second),
		Tasks:               map[string]map[string]int{"command": {"running": 3, "queued": 1}, "server": {"running": 1}},
		Disk:                &serve.Disk{AvailableBytes: 123456, SizeBytes: 654321},
	})
	body := scrape(t, newCollector(t, l))

	if got := valueOf(t, body, "coddy_build_info", lbl("version", "v9.9.9", "revision", "abc123", "goversion", "go1.26", "goos", "linux", "goarch", "arm64", "tags", "gateway,metrics")); got != 1 {
		t.Errorf("coddy_build_info = %v, want 1", got)
	}
	if got := valueOf(t, body, "coddy_restart_required", nil); got != 1 {
		t.Errorf("coddy_restart_required = %v, want 1", got)
	}
	if got := valueOf(t, body, "coddy_turns_running", nil); got != 2 {
		t.Errorf("coddy_turns_running = %v, want 2", got)
	}
	if got := valueOf(t, body, "coddy_turn_oldest_running_seconds", nil); got < 89 || got > 120 {
		t.Errorf("coddy_turn_oldest_running_seconds = %v, want about 90", got)
	}
	for _, row := range []struct {
		kind, status string
		want         float64
	}{
		{"command", "running", 3}, {"command", "queued", 1},
		{"server", "running", 1}, {"server", "queued", 0},
		{"agent", "running", 0}, {"agent", "queued", 0},
	} {
		if got := valueOf(t, body, "coddy_background_tasks", lbl("kind", row.kind, "status", row.status)); got != row.want {
			t.Errorf("coddy_background_tasks{%s,%s} = %v, want %v", row.kind, row.status, got, row.want)
		}
	}
	if got := valueOf(t, body, "coddy_sessions_disk_available_bytes", nil); got != 123456 {
		t.Errorf("available bytes = %v", got)
	}
	if got := valueOf(t, body, "coddy_sessions_disk_size_bytes", nil); got != 654321 {
		t.Errorf("size bytes = %v", got)
	}

	l.setStatus(serve.Snapshot{})
	body = scrape(t, newCollector(t, l))
	if got := valueOf(t, body, "coddy_turn_oldest_running_seconds", nil); got != 0 {
		t.Errorf("oldest turn without a turn = %v, want 0", got)
	}
	if got := valueOf(t, body, "coddy_restart_required", nil); got != 0 {
		t.Errorf("coddy_restart_required = %v, want 0", got)
	}
}

func TestTheDiskGaugesAreAbsentWithoutAReading(t *testing.T) {
	l := newLive(configWith(nil))
	body := scrape(t, newCollector(t, l))
	for _, name := range []string{"coddy_sessions_disk_available_bytes", "coddy_sessions_disk_size_bytes"} {
		if _, ok := find(body, name, nil); ok {
			t.Errorf("%s is exported with no disk reading", name)
		}
	}
}

// exposedFamily is what a scrape says about one family: the text of every HELP
// and TYPE line that names it, the label names of its first sample (le left
// out) and how many samples it has.
type exposedFamily struct {
	helps, types []string
	labels       string
	samples      int
}

// families groups a scrape by family. A histogram's _bucket, _sum and _count
// samples belong to the family its TYPE line names.
func families(t testing.TB, body string) map[string]*exposedFamily {
	t.Helper()
	out := map[string]*exposedFamily{}
	of := func(name string) *exposedFamily {
		if out[name] == nil {
			out[name] = &exposedFamily{}
		}
		return out[name]
	}
	for _, line := range strings.Split(body, "\n") {
		switch {
		case line == "":
		case strings.HasPrefix(line, "# HELP "):
			name, text, _ := strings.Cut(strings.TrimPrefix(line, "# HELP "), " ")
			of(name).helps = append(of(name).helps, text)
		case strings.HasPrefix(line, "# TYPE "):
			name, typ, _ := strings.Cut(strings.TrimPrefix(line, "# TYPE "), " ")
			of(name).types = append(of(name).types, typ)
		case strings.HasPrefix(line, "#"):
			t.Fatalf("a comment line that is neither HELP nor TYPE: %q", line)
		}
	}
	for _, s := range parseSamples(body) {
		name := s.name
		for _, suffix := range []string{"_bucket", "_sum", "_count"} {
			if base, ok := strings.CutSuffix(name, suffix); ok && out[base] != nil && slices.Equal(out[base].types, []string{"histogram"}) {
				name = base
				break
			}
		}
		f := of(name)
		if f.samples == 0 {
			var labels []string
			for k := range s.labels {
				if k != "le" {
					labels = append(labels, k)
				}
			}
			slices.Sort(labels)
			f.labels = strings.Join(labels, ",")
		}
		f.samples++
	}
	return out
}

// The scrape holds exactly the catalogue and the library's Go and process
// families, with the catalogue's types, labels and the words its help owes,
// one HELP for each family and one TYPE for each of the catalogue's.
func TestTheFamiliesAreTheCatalogue(t *testing.T) {
	l := newLive(configWith(map[string][]string{"stub": {"echo"}}))
	l.setStatus(fullSnapshot())
	c := newCollector(t, l)
	attempt("stub", "echo", answered(1, 1, 1), nil)
	c.observeTool(agent.ToolCallEvent{Tool: "read", Outcome: agent.ToolOutcomeOK, Duration: time.Second})
	c.observeCall(llm.CallEvent{Provider: "stub", Model: "echo", Outcome: "ok", FirstContent: time.Second})

	catalogue := map[string]struct {
		kind, labels string
		help         []string
	}{
		"coddy_build_info":                             {"gauge", "goarch,goos,goversion,revision,tags,version", nil},
		"coddy_subsystem_up":                           {"gauge", "subsystem", nil},
		"coddy_restart_required":                       {"gauge", "", nil},
		"coddy_turns_running":                          {"gauge", "", []string{"independently"}},
		"coddy_turn_oldest_running_seconds":            {"gauge", "", []string{"not prove"}},
		"coddy_background_tasks":                       {"gauge", "kind,status", nil},
		"coddy_sessions_disk_available_bytes":          {"gauge", "", []string{"non-root"}},
		"coddy_sessions_disk_size_bytes":               {"gauge", "", []string{"session store"}},
		"coddy_llm_attempts_total":                     {"counter", "attempt,model,outcome,provider", []string{"Provider invocation attempts, retries included; some fail before a request is sent."}},
		"coddy_llm_time_to_first_token_seconds":        {"histogram", "provider", []string{"first chunk"}},
		"coddy_llm_attempt_duration_seconds":           {"histogram", "provider", nil},
		"coddy_llm_input_tokens_total":                 {"counter", "model,provider", nil},
		"coddy_llm_cached_input_tokens_total":          {"counter", "model,provider", []string{"a subset of the input tokens"}},
		"coddy_llm_output_tokens_total":                {"counter", "model,provider", nil},
		"coddy_turns_started_total":                    {"counter", "", []string{"independently"}},
		"coddy_turns_ended_total":                      {"counter", "", []string{"independently"}},
		"coddy_turn_duration_seconds":                  {"histogram", "", nil},
		"coddy_tool_calls_total":                       {"counter", "outcome,tool", []string{"mcp for any tool of an MCP server"}},
		"coddy_tool_call_duration_seconds":             {"histogram", "tool", nil},
		"coddy_prompts_pending":                        {"gauge", "kind", []string{"wait for a person"}},
		"coddy_provider_quota_used_ratio":              {"gauge", "provider,window", []string{"cache"}},
		"coddy_provider_quota_reset_timestamp_seconds": {"gauge", "provider,window", nil},
		"coddy_swarm_joins":                            {"gauge", "relay,state,transport", nil},
		"coddy_swarm_nodes":                            {"gauge", "kind,state,transport", nil},
		"coddy_config_reloads_total":                   {"counter", "", nil},
	}
	got := families(t, scrape(t, c))
	for name, f := range got {
		if f.samples == 0 {
			t.Errorf("%s has a HELP or TYPE line and no sample", name)
		}
		if len(f.helps) != 1 || strings.TrimSpace(f.helps[0]) == "" {
			t.Errorf("%s has the HELP lines %q, want exactly one that says something", name, f.helps)
		}
		if strings.HasPrefix(name, "go_") || strings.HasPrefix(name, "process_") {
			if len(f.types) != 0 {
				t.Errorf("%s is typed %q: the library's families stay untyped", name, f.types)
			}
			continue
		}
		want, ok := catalogue[name]
		if !ok {
			t.Errorf("family %s is not in the catalogue", name)
			continue
		}
		if len(f.types) != 1 {
			t.Errorf("%s has the TYPE lines %q, want exactly one", name, f.types)
			continue
		}
		if f.types[0] != want.kind || f.labels != want.labels {
			t.Errorf("%s is a %s with labels [%s], the catalogue says a %s with [%s]", name, f.types[0], f.labels, want.kind, want.labels)
		}
		for _, words := range want.help {
			if len(f.helps) == 1 && !strings.Contains(f.helps[0], words) {
				t.Errorf("the help of %s lacks %q: %q", name, words, f.helps[0])
			}
		}
	}
	for name := range catalogue {
		if _, ok := got[name]; !ok {
			t.Errorf("the catalogue family %s is not exported", name)
		}
	}
	if f, ok := got["go_goroutines"]; !ok {
		t.Error("the Go runtime figures are not exported")
	} else if len(f.helps) == 1 && f.helps[0] != "Go runtime metric written by github.com/VictoriaMetrics/metrics." {
		t.Errorf("the help of go_goroutines: %q", f.helps[0])
	}
	// The library reads no process figures on the platforms it has no reader for.
	if slices.Contains([]string{"linux", "darwin", "windows"}, runtime.GOOS) {
		var processFamilies int
		for name := range got {
			if strings.HasPrefix(name, "process_") {
				processFamilies++
			}
		}
		if processFamilies == 0 {
			t.Error("no process_ family is exported")
		}
		for _, name := range []string{"process_open_fds", "process_max_fds"} {
			if _, ok := got[name]; !ok {
				t.Errorf("%s is not exported", name)
			}
		}
	}
}

// A family with no sample is absent altogether: neither its HELP nor its TYPE
// is written, as for a vector that has no series.
func TestAFamilyWithoutSamplesHasNoHelpOrType(t *testing.T) {
	l := newLive(configWith(nil))
	body := scrape(t, newCollector(t, l))

	for _, name := range []string{
		"coddy_llm_attempts_total", "coddy_llm_attempt_duration_seconds",
		"coddy_llm_input_tokens_total", "coddy_llm_cached_input_tokens_total", "coddy_llm_output_tokens_total",
		"coddy_subsystem_up", "coddy_sessions_disk_available_bytes", "coddy_sessions_disk_size_bytes",
	} {
		for _, line := range []string{"# HELP " + name + " ", "# TYPE " + name + " "} {
			if strings.Contains(body, line) {
				t.Errorf("a family with no sample is announced: %q", line)
			}
		}
	}
	for _, name := range []string{"coddy_build_info", "coddy_restart_required", "coddy_background_tasks", "coddy_turns_started_total", "coddy_config_reloads_total"} {
		for _, line := range []string{"# HELP " + name + " ", "# TYPE " + name + " "} {
			if !strings.Contains(body, line) {
				t.Errorf("a family with a sample is not announced: %q", line)
			}
		}
	}
	for name, f := range families(t, body) {
		if f.samples == 0 {
			t.Errorf("%s is announced and has no sample", name)
		}
	}
}

// A name that holds a quote or a backslash reaches the series name escaped, in
// the counters, the histogram and the zero series seeded for it, and parses
// back to what the configuration says.
func TestLabelValuesWithQuotesAndBackslashesAreEscaped(t *testing.T) {
	provider, model := `we"ird\prov`, `m"o\del`
	l := newLive(configWith(map[string][]string{provider: {model}}))
	c := newCollector(t, l)
	attempt(provider, model, answered(5, 2, 3), nil)

	body := scrape(t, c)
	const escaped = `provider="we\"ird\\prov",model="m\"o\\del"`
	for _, line := range []string{
		`coddy_llm_attempts_total{` + escaped + `,outcome="ok",attempt="first"} 1`,
		`coddy_llm_attempts_total{` + escaped + `,outcome="timeout",attempt="retry"} 0`,
		`coddy_llm_input_tokens_total{` + escaped + `} 5`,
		`coddy_llm_attempt_duration_seconds_count{provider="we\"ird\\prov"} 1`,
	} {
		if !strings.Contains(body, line+"\n") {
			t.Errorf("the scrape lacks the line %s", line)
		}
	}
	if got := attemptsOf(t, body, provider, model, "ok"); got != 1 {
		t.Errorf("the attempt under the original names: %v, want 1", got)
	}
	for _, outcome := range llm.CallOutcomes() {
		want := 0.0
		if outcome == "ok" {
			want = 1
		}
		if got := attemptsOf(t, body, provider, model, outcome); got != want {
			t.Errorf("outcome %s = %v, want %v", outcome, got, want)
		}
	}
	if got := valueOf(t, body, "coddy_llm_attempt_duration_seconds_count", lbl("provider", provider)); got != 1 {
		t.Errorf("duration observations under the original name: %v, want 1", got)
	}
}

func TestSeriesEscapesTheLabelValuesOfTheTextFormat(t *testing.T) {
	got := series("m", "a", "x\ny", "b", `q"\`, "c", "plain")
	if want := `m{a="x\ny",b="q\"\\",c="plain"}`; got != want {
		t.Errorf("series = %s, want %s", got, want)
	}
	if err := vm.ValidateMetric(got); err != nil {
		t.Errorf("the library rejects %s: %v", got, err)
	}
	if got := series("m"); got != "m" {
		t.Errorf("series without labels = %q", got)
	}
}

func TestHelpIsEscapedLikeTheTextFormat(t *testing.T) {
	var b bytes.Buffer
	family{name: "m", typ: "gauge", help: "a\\b\nc \"d\""}.writeHeader(&b)
	if want := "# HELP m a\\\\b\\nc \"d\"\n# TYPE m gauge\n"; b.String() != want {
		t.Errorf("header %q, want %q", b.String(), want)
	}
	b.Reset()
	family{name: "m", help: "untyped"}.writeHeader(&b)
	if want := "# HELP m untyped\n"; b.String() != want {
		t.Errorf("untyped header %q, want %q", b.String(), want)
	}
}

// The library's figures get one HELP in front of the first sample of each
// name, once even when a name comes back, and no TYPE; metadata the library
// wrote itself (after a process-wide vm.ExposeMetadata) is dropped, so no name
// is announced twice.
func TestTheLibraryFamiliesAreAnnotatedOnceAndLeftUntyped(t *testing.T) {
	raw := strings.Join([]string{
		`go_a 1`, `go_a{x="1"} 2`, `# HELP kept already`, `process_b 3`, `go_a 4`,
		`other_c{y="z"} 5`, `go_info_ext{compiler="gc", GOARCH="amd64"} 1`, ``,
	}, "\n")
	var b bytes.Buffer
	annotate(&b, raw)
	const lib = " written by github.com/VictoriaMetrics/metrics.\n"
	want := "# HELP go_a Go runtime metric" + lib + `go_a 1` + "\n" + `go_a{x="1"} 2` + "\n" +
		"# HELP process_b Process metric" + lib + "process_b 3\n" +
		"go_a 4\n" +
		"# HELP other_c Metric" + lib + `other_c{y="z"} 5` + "\n" +
		"# HELP go_info_ext Go runtime metric" + lib + `go_info_ext{compiler="gc", GOARCH="amd64"} 1` + "\n"
	if b.String() != want {
		t.Errorf("annotated:\n%s\nwant:\n%s", b.String(), want)
	}
}

// The duration is a histogram with real le buckets, +Inf, a sum and a count,
// never the library's vmrange buckets.
func TestTheDurationHistogramIsExposedWithLeBuckets(t *testing.T) {
	l := newLive(configWith(map[string][]string{"stub": {"echo"}}))
	c := newCollector(t, l)
	c.observeCall(llm.CallEvent{Provider: "stub", Model: "echo", Outcome: "ok", Duration: 3 * time.Second})
	c.observeCall(llm.CallEvent{Provider: "stub", Model: "echo", Outcome: "ok", Duration: 700 * time.Second})

	body := scrape(t, c)
	if strings.Contains(body, "vmrange") {
		t.Errorf("the scrape has vmrange buckets:\n%s", body)
	}
	var les []string
	var counts []float64
	for _, s := range parseSamples(body) {
		if s.name == "coddy_llm_attempt_duration_seconds_bucket" && s.labels["provider"] == "stub" {
			les = append(les, s.labels["le"])
			counts = append(counts, s.value)
		}
	}
	wantLes := []string{"0.5", "1", "2.5", "5", "10", "30", "60", "120", "300", "600", "+Inf"}
	if !slices.Equal(les, wantLes) {
		t.Errorf("le buckets %q, want %q", les, wantLes)
	}
	if want := []float64{0, 0, 0, 1, 1, 1, 1, 1, 1, 1, 2}; !slices.Equal(counts, want) {
		t.Errorf("cumulative bucket counts %v, want %v", counts, want)
	}
	count := valueOf(t, body, "coddy_llm_attempt_duration_seconds_count", lbl("provider", "stub"))
	if inf := valueOf(t, body, "coddy_llm_attempt_duration_seconds_bucket", lbl("provider", "stub", "le", "+Inf")); inf != count || count != 2 {
		t.Errorf("+Inf bucket %v, count %v, want both 2", inf, count)
	}
	if got := valueOf(t, body, "coddy_llm_attempt_duration_seconds_sum", lbl("provider", "stub")); got != 703 {
		t.Errorf("duration sum %v, want 703", got)
	}
}

func TestOnlyTheMetricsPathAnswersAndOnlyWithTheToken(t *testing.T) {
	l := newLive(configWith(nil))
	c := newCollector(t, l)
	token := "scrape-me"
	srv := httptest.NewServer(c.Handler(func() string { return token }))
	t.Cleanup(srv.Close)

	do := func(method, path, auth string) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp, string(body)
	}

	for _, auth := range []string{"", "Bearer wrong", "Bearer ", "Basic " + token, token, "bearer " + token + "x"} {
		resp, body := do(http.MethodGet, "/metrics", auth)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET /metrics with %q: %d, want 401", auth, resp.StatusCode)
		}
		if got := resp.Header.Get("WWW-Authenticate"); got != `Bearer realm="coddy-metrics"` {
			t.Errorf("WWW-Authenticate with %q = %q", auth, got)
		}
		if strings.Contains(body, "coddy_") {
			t.Errorf("a refused scrape leaked metrics:\n%s", body)
		}
	}
	// The scheme is case-insensitive and the credential is trimmed, as for the HTTP API.
	for _, auth := range []string{"Bearer " + token, "bearer " + token, "BEARER " + token, "Bearer   " + token + "  "} {
		if resp, body := do(http.MethodGet, "/metrics", auth); resp.StatusCode != http.StatusOK || !strings.Contains(body, "coddy_build_info") {
			t.Errorf("GET /metrics with %q: %d\n%s", auth, resp.StatusCode, body)
		}
	}
	if resp, body := do(http.MethodHead, "/metrics", "Bearer "+token); resp.StatusCode != http.StatusOK || body != "" {
		t.Errorf("HEAD /metrics with the token: %d, %d body bytes", resp.StatusCode, len(body))
	}
	if resp, _ := do(http.MethodHead, "/metrics", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("HEAD /metrics without the token: %d, want 401", resp.StatusCode)
	}
	for _, path := range []string{"/", "/metrics/", "/metric", "/coddy/status", "/v1/models", "/debug/pprof/"} {
		if resp, _ := do(http.MethodGet, path, "Bearer "+token); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", path, resp.StatusCode)
		}
	}

	// The token is read on every request: a rotated one needs no rebuild.
	token = "rotated"
	if resp, _ := do(http.MethodGet, "/metrics", "Bearer scrape-me"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the old token after a rotation: %d, want 401", resp.StatusCode)
	}
	if resp, _ := do(http.MethodGet, "/metrics", "Bearer rotated"); resp.StatusCode != http.StatusOK {
		t.Errorf("the new token after a rotation: %d, want 200", resp.StatusCode)
	}
	token = ""
	if resp, _ := do(http.MethodGet, "/metrics", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("no token configured: %d, want an open /metrics", resp.StatusCode)
	}
}

// A scrape is plain text in the 0.0.4 format, written once and never
// compressed, whatever the client accepts.
func TestAScrapeIsPlainTextAndNeverCompressed(t *testing.T) {
	c := newCollector(t, newLive(configWith(nil)))
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	c.Handler(func() string { return "" }).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics: %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; version=0.0.4; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want none", got)
	}
	if !strings.Contains(rec.Body.String(), "# TYPE coddy_build_info gauge\n") {
		t.Errorf("the body is not the exposition:\n%s", rec.Body.String())
	}
}

// At most two scrapes are in flight: the third is refused at once, without
// reading the process, even through the handler of a rebuilt listener, and a
// slot comes back when a scrape returns.
func TestAThirdConcurrentScrapeIsRefused(t *testing.T) {
	l := newLive(configWith(nil))
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	open := sync.OnceFunc(func() { close(release) })
	t.Cleanup(open)
	src := l.source()
	src.Status = func() serve.Snapshot {
		entered <- struct{}{}
		<-release
		return serve.Snapshot{}
	}
	c := NewCollector(src)
	t.Cleanup(c.Close)
	handler := c.Handler(func() string { return "" })
	serveOne := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		return rec
	}

	var wg sync.WaitGroup
	held := make([]*httptest.ResponseRecorder, 2)
	for i := range held {
		wg.Add(1)
		go func() {
			defer wg.Done()
			held[i] = serveOne()
		}()
	}
	for range held {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("two scrapes never got to read the process")
		}
	}

	rebuilt := httptest.NewRecorder()
	c.Handler(func() string { return "" }).ServeHTTP(rebuilt, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	third := rebuilt
	if third.Code != http.StatusServiceUnavailable || !strings.Contains(third.Body.String(), "too many scrapes in flight") {
		t.Errorf("the third concurrent scrape: %d %q, want 503 too many scrapes in flight", third.Code, third.Body.String())
	}
	if len(entered) != 0 {
		t.Error("the refused scrape read the process")
	}

	open()
	wg.Wait()
	for i, rec := range held {
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "coddy_build_info") {
			t.Errorf("scrape %d in flight: %d", i, rec.Code)
		}
	}
	if rec := serveOne(); rec.Code != http.StatusOK {
		t.Errorf("a scrape after the others returned: %d, want 200", rec.Code)
	}
}

// serving runs Serve on addr until the returned stop is called, and returns
// what Serve returned.
func serving(t *testing.T, opts Options) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, opts) }()
	waitListening(t, opts.Addr)
	var once sync.Once
	var result error
	stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case result = <-done:
			case <-time.After(10 * time.Second):
				t.Error("Serve did not return after its context was cancelled")
			}
		})
		return result
	}
	t.Cleanup(func() { _ = stop() })
	return stop
}

func waitListening(t testing.TB, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("nothing listens on %s", addr)
}

func get(t testing.TB, addr, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// The counters live in the collector, not in the listener, so a listener that
// is rebuilt on another address continues them.
func TestCountersSurviveARebuildOfTheListener(t *testing.T) {
	l := newLive(configWith(map[string][]string{"stub": {"echo"}}))
	cfg := l.cfg
	cfg.Metrics.Token = "scrape-me"
	c := newCollector(t, l)
	attempt("stub", "echo", nil, nil)

	first := freeAddr(t)
	stop := serving(t, Options{Addr: first, Cfg: l.source().Cfg, Log: l.source().Log, Collector: c})
	code, body := get(t, first, "scrape-me")
	if code != http.StatusOK {
		t.Fatalf("first listener: %d", code)
	}
	if got := attemptsOf(t, body, "stub", "echo", "ok"); got != 1 {
		t.Fatalf("before the rebuild: %v, want 1", got)
	}
	if code, _ := get(t, first, "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("a wrong token on the listener: %d, want 401", code)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve returned %v after its context was cancelled", err)
	}

	attempt("stub", "echo", nil, nil)
	second := freeAddr(t)
	serving(t, Options{Addr: second, Cfg: l.source().Cfg, Log: l.source().Log, Collector: c})
	code, body = get(t, second, "scrape-me")
	if code != http.StatusOK {
		t.Fatalf("second listener: %d", code)
	}
	if got := attemptsOf(t, body, "stub", "echo", "ok"); got != 2 {
		t.Fatalf("after the rebuild: %v, want 2 (the counter continues)", got)
	}
}

func TestServeReturnsAtOnceWhenTheAddressIsTaken(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	l := newLive(configWith(nil))
	c := newCollector(t, l)

	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Options{Addr: held.Addr().String(), Cfg: l.source().Cfg, Log: l.source().Log, Collector: c})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Serve on a taken address returned nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve on a taken address did not return")
	}
}

// capture is a log handler that keeps the records.
type capture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *capture) Enabled(context.Context, slog.Level) bool { return true }
func (c *capture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r)
	return nil
}
func (c *capture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *capture) WithGroup(string) slog.Handler      { return c }

func (c *capture) logged(message string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.Message == message {
			return true
		}
	}
	return false
}

func (c *capture) warnings() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, r := range c.records {
		if r.Level == slog.LevelWarn {
			out = append(out, r.Message)
		}
	}
	return out
}

func TestAnExporterOffLoopbackWithoutATokenWarnsOnceAtStart(t *testing.T) {
	cases := []struct {
		name, host, token string
		warns             bool
	}{
		{"every interface, no token", "0.0.0.0", "", true},
		{"every interface, a token", "0.0.0.0", "scrape-me", false},
		{"loopback, no token", "127.0.0.1", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := newLive(configWith(nil))
			l.cfg.Metrics = config.MetricsConfig{Enable: true, Host: tc.host, Token: tc.token}
			c := newCollector(t, l)
			rec := &capture{}
			// Port 0: the warning is said at start, so no request is needed, and
			// no port picked ahead can be taken by another test in between.
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- Serve(ctx, Options{Addr: net.JoinHostPort(tc.host, "0"), Cfg: l.source().Cfg, Log: slog.New(rec), Collector: c})
			}()
			deadline := time.Now().Add(5 * time.Second)
			for !rec.logged("metrics exporter listening") {
				if time.Now().After(deadline) {
					cancel()
					t.Fatalf("the exporter never started: %v", <-done)
				}
				time.Sleep(10 * time.Millisecond)
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("Serve: %v", err)
			}
			got := rec.warnings()
			if tc.warns && len(got) != 1 {
				t.Fatalf("warnings %q, want exactly one", got)
			}
			if !tc.warns && len(got) != 0 {
				t.Fatalf("warnings %q, want none", got)
			}
		})
	}
}

// A tool call is counted under its tool and outcome; only a call that ran has
// a duration.
func TestToolCallsAreCountedByToolAndOutcome(t *testing.T) {
	c := newCollector(t, newLive(configWith(nil)))
	for _, ev := range []agent.ToolCallEvent{
		{Tool: "read", Outcome: agent.ToolOutcomeOK, Duration: 200 * time.Millisecond},
		{Tool: "read", Outcome: agent.ToolOutcomeError, Duration: 2 * time.Second},
		{Tool: "run_command", Outcome: agent.ToolOutcomeDenied},
		{Tool: "mcp", Outcome: agent.ToolOutcomeRefused},
	} {
		c.observeTool(ev)
	}
	body := scrape(t, c)
	for _, tc := range []struct {
		tool, outcome string
	}{{"read", "ok"}, {"read", "error"}, {"run_command", "denied"}, {"mcp", "refused"}} {
		if got := total(body, "coddy_tool_calls_total", map[string]string{"tool": tc.tool, "outcome": tc.outcome}); got != 1 {
			t.Errorf("tool calls %s/%s = %v, want 1", tc.tool, tc.outcome, got)
		}
	}
	if got := total(body, "coddy_tool_call_duration_seconds_count", map[string]string{"tool": "read"}); got != 2 {
		t.Errorf("read ran %v times, want 2", got)
	}
	if got := total(body, "coddy_tool_call_duration_seconds_sum", map[string]string{"tool": "read"}); got != 2.2 {
		t.Errorf("read ran for %v s, want 2.2", got)
	}
	for _, tool := range []string{"run_command", "mcp"} {
		if _, ok := find(body, "coddy_tool_call_duration_seconds_count", map[string]string{"tool": tool}); ok {
			t.Errorf("a %s call that never ran has a duration", tool)
		}
	}
}

// A turn's duration runs from its started edge to its ended edge, per
// session; an ended edge with no start seen is not a duration.
func TestTheTurnDurationIsTheTimeBetweenTheEdges(t *testing.T) {
	c := newCollector(t, newLive(configWith(nil)))
	body := scrape(t, c)
	if got, ok := find(body, "coddy_turn_duration_seconds_count", nil); !ok || got != 0 {
		t.Fatalf("the turn histogram is not there from the start: %v (present %v)", got, ok)
	}
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	c.observeTurn(session.TurnEvent{SessionID: "sess_a", Phase: session.TurnPhaseStarted, At: at})
	c.observeTurn(session.TurnEvent{SessionID: "sess_b", Phase: session.TurnPhaseStarted, At: at.Add(time.Minute)})
	c.observeTurn(session.TurnEvent{SessionID: "sess_a", Phase: session.TurnPhaseWoken, At: at})
	c.observeTurn(session.TurnEvent{SessionID: "sess_a", Phase: session.TurnPhaseEnded, At: at.Add(90 * time.Second)})
	c.observeTurn(session.TurnEvent{SessionID: "sess_c", Phase: session.TurnPhaseEnded, At: at})

	body = scrape(t, c)
	if got := total(body, "coddy_turn_duration_seconds_count", nil); got != 1 {
		t.Errorf("turn durations = %v, want 1", got)
	}
	if got := total(body, "coddy_turn_duration_seconds_sum", nil); got != 90 {
		t.Errorf("turn duration sum = %v, want 90", got)
	}
	if got := total(body, "coddy_turns_ended_total", nil); got != 2 {
		t.Errorf("turns ended = %v, want 2", got)
	}
}

// The token is read on every request, so a reload that clears it on an
// exporter bound beyond loopback rebuilds nothing and no start warns: the
// reload says so itself, once, until the exporter is closed off again.
func TestAReloadThatOpensTheExporterWarnsOnce(t *testing.T) {
	closed := configWith(nil)
	closed.Metrics = config.MetricsConfig{Enable: true, Host: "0.0.0.0", Token: "scrape-me"}
	open := configWith(nil)
	open.Metrics = config.MetricsConfig{Enable: true, Host: "0.0.0.0"}
	loopback := configWith(nil)
	loopback.Metrics = config.MetricsConfig{Enable: true, Host: "127.0.0.1"}

	l := newLive(closed)
	rec := &capture{}
	src := l.source()
	src.Log = slog.New(rec)
	c := NewCollector(src)
	t.Cleanup(c.Close)

	for i, step := range []struct {
		cfg   *config.Config
		warns int
	}{{open, 1}, {open, 1}, {closed, 1}, {loopback, 1}, {open, 2}} {
		l.reload(step.cfg)
		if got := len(rec.warnings()); got != step.warns {
			t.Fatalf("after reload %d: %d warnings %q, want %d", i, got, rec.warnings(), step.warns)
		}
	}
}

// The Grafana dashboard and the alert rules shipped under examples/ read only
// series a scrape holds, so renaming a family without them fails here rather
// than leaving an operator with empty panels. The Go and process figures
// depend on the platform and are checked where the library reads them all.
func TestTheDashboardAndTheRulesReadOnlyExportedSeries(t *testing.T) {
	l := newLive(configWith(map[string][]string{"stub": {"echo"}}))
	l.setStatus(fullSnapshot())
	c := newCollector(t, l)
	attempt("stub", "echo", answered(1, 1, 1), nil)
	c.observeTool(agent.ToolCallEvent{Tool: "read", Outcome: agent.ToolOutcomeOK, Duration: time.Second})
	c.observeCall(llm.CallEvent{Provider: "stub", Model: "echo", Outcome: "ok", FirstContent: time.Second})
	exported := map[string]bool{"up": true}
	for _, s := range parseSamples(scrape(t, c)) {
		exported[s.name] = true
	}

	name := regexp.MustCompile(`\b(?:coddy|go|process)_[a-z_]+[a-z]\b`)
	for _, file := range []string{"../../examples/grafana/coddy-dashboard.json", "../../examples/prometheus/coddy-rules.yml"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, ref := range name.FindAllString(string(raw), -1) {
			if seen[ref] || (!strings.HasPrefix(ref, "coddy_") && runtime.GOOS != "linux") {
				continue
			}
			seen[ref] = true
			if !exported[ref] {
				t.Errorf("%s reads %s, which no scrape holds", filepath.Base(file), ref)
			}
		}
		if len(seen) == 0 {
			t.Errorf("%s names no series at all", file)
		}
	}
}

// An attempt is counted as the first of its call or as a retry, and both are
// seeded at zero for every outcome, so a retry rate has a sample to start
// from.
func TestAttemptsAreSplitIntoFirstAndRetry(t *testing.T) {
	c := newCollector(t, newLive(configWith(map[string][]string{"stub": {"echo"}})))
	body := scrape(t, c)
	for _, attempt := range []string{"first", "retry"} {
		for _, outcome := range llm.CallOutcomes() {
			if got, ok := find(body, "coddy_llm_attempts_total", lbl("provider", "stub", "model", "echo", "outcome", outcome, "attempt", attempt)); !ok || got != 0 {
				t.Errorf("seed stub/echo/%s/%s = %v (present %v), want 0", outcome, attempt, got, ok)
			}
		}
	}
	c.observeCall(llm.CallEvent{Provider: "stub", Model: "echo", Outcome: "server_error"})
	c.observeCall(llm.CallEvent{Provider: "stub", Model: "echo", Outcome: "ok", Retry: true})
	body = scrape(t, c)
	if got := total(body, "coddy_llm_attempts_total", lbl("provider", "stub", "model", "echo", "outcome", "server_error", "attempt", "first")); got != 1 {
		t.Errorf("first attempts that failed = %v, want 1", got)
	}
	if got := total(body, "coddy_llm_attempts_total", lbl("provider", "stub", "model", "echo", "outcome", "ok", "attempt", "retry")); got != 1 {
		t.Errorf("retries that succeeded = %v, want 1", got)
	}
}

// The time to the first token is observed for an attempt that streamed
// content, under its provider, and the histogram is there, empty, for every
// admitted provider from the start.
func TestTheTimeToFirstTokenIsObservedForStreamedAttempts(t *testing.T) {
	c := newCollector(t, newLive(configWith(map[string][]string{"stub": {"echo"}})))
	body := scrape(t, c)
	if got, ok := find(body, "coddy_llm_time_to_first_token_seconds_count", lbl("provider", "stub")); !ok || got != 0 {
		t.Fatalf("the first token histogram of stub is not seeded empty: %v (present %v)", got, ok)
	}
	c.observeCall(llm.CallEvent{Provider: "stub", Model: "echo", Outcome: "ok", FirstContent: 1500 * time.Millisecond})
	c.observeCall(llm.CallEvent{Provider: "stub", Model: "echo", Outcome: "ok"})
	body = scrape(t, c)
	if got := total(body, "coddy_llm_time_to_first_token_seconds_count", lbl("provider", "stub")); got != 1 {
		t.Errorf("first tokens observed = %v, want 1", got)
	}
	if got := total(body, "coddy_llm_time_to_first_token_seconds_sum", lbl("provider", "stub")); got != 1.5 {
		t.Errorf("first token sum = %v, want 1.5", got)
	}
}

// The prompts that wait for a person are one gauge by kind, both kinds always
// present.
func TestPendingPromptsAreAGaugeByKind(t *testing.T) {
	l := newLive(configWith(nil))
	l.setStatus(serve.Snapshot{PermissionPrompts: 2})
	body := scrape(t, newCollector(t, l))
	if got := total(body, "coddy_prompts_pending", lbl("kind", "permission")); got != 2 {
		t.Errorf("permission prompts = %v, want 2", got)
	}
	if got, ok := find(body, "coddy_prompts_pending", lbl("kind", "question")); !ok || got != 0 {
		t.Errorf("questions = %v (present %v), want 0", got, ok)
	}
}

// A provider's quota windows are exported by provider and window, the reset
// as a timestamp when the provider names one. Only an admitted provider and a
// window from the closed set of the usage readers reach a label.
func TestProviderQuotasAreExportedForAdmittedProvidersAndKnownWindows(t *testing.T) {
	l := newLive(configWith(map[string][]string{"hub": {"m"}}))
	resets := time.Date(2026, 10, 15, 18, 0, 0, 0, time.UTC)
	l.setStatus(serve.Snapshot{ProviderQuotas: []serve.ProviderQuota{
		{Provider: "hub", Window: "session", UsedRatio: 0.25, ResetsAt: resets},
		{Provider: "hub", Window: "window-86400-secondary", UsedRatio: 0.5},
		{Provider: "hub", Window: "codex:1:some extra limit", UsedRatio: 0.9},
		{Provider: "ghost", Window: "week", UsedRatio: 1},
	}})
	body := scrape(t, newCollector(t, l))
	if got := total(body, "coddy_provider_quota_used_ratio", lbl("provider", "hub", "window", "session")); got != 0.25 {
		t.Errorf("session used = %v, want 0.25", got)
	}
	if got := total(body, "coddy_provider_quota_used_ratio", lbl("provider", "hub", "window", "window-86400-secondary")); got != 0.5 {
		t.Errorf("long window used = %v, want 0.5", got)
	}
	if got := total(body, "coddy_provider_quota_reset_timestamp_seconds", lbl("provider", "hub", "window", "session")); got != float64(resets.Unix()) {
		t.Errorf("session reset = %v, want %d", got, resets.Unix())
	}
	if _, ok := find(body, "coddy_provider_quota_reset_timestamp_seconds", lbl("window", "window-86400-secondary")); ok {
		t.Error("a window without a reset moment has a reset timestamp")
	}
	for _, s := range parseSamples(body) {
		if strings.HasPrefix(s.name, "coddy_provider_quota") && (s.labels["provider"] == "ghost" || strings.Contains(s.labels["window"], "extra")) {
			t.Errorf("a name outside the budget or the closed set of windows reached a label: %+v", s)
		}
	}
}

// The registrations into parent relays are counted by relay, transport and
// state, both states written for every pair; a relay's nodes by kind,
// transport and state, and only in a process that runs a relay.
func TestTheSwarmStateIsCountedByClosedLabels(t *testing.T) {
	l := newLive(configWith(nil))
	l.setStatus(serve.Snapshot{SwarmJoins: []swarm.JoinStatus{
		{Relay: "relay.example:8443", Kind: swarm.KindAgent, Transport: swarm.TransportDirect, Online: true},
		{Relay: "relay.example:8443", Kind: swarm.KindAgent, Transport: swarm.TransportDirect, Online: false},
		{Relay: "10.0.0.2:12346", Kind: swarm.KindRelay, Transport: swarm.TransportTunnel, Online: false},
	}})
	c := newCollector(t, l)
	body := scrape(t, c)
	for _, tc := range []struct {
		relay, transport, state string
		want                    float64
	}{
		{"relay.example:8443", "direct", "online", 1},
		{"relay.example:8443", "direct", "offline", 1},
		{"10.0.0.2:12346", "tunnel", "online", 0},
		{"10.0.0.2:12346", "tunnel", "offline", 1},
	} {
		if got, ok := find(body, "coddy_swarm_joins", lbl("relay", tc.relay, "transport", tc.transport, "state", tc.state)); !ok || got != tc.want {
			t.Errorf("joins %s/%s/%s = %v (present %v), want %v", tc.relay, tc.transport, tc.state, got, ok, tc.want)
		}
	}
	if strings.Contains(body, "coddy_swarm_nodes") {
		t.Error("a process that runs no relay exports relay nodes")
	}

	l.setStatus(serve.Snapshot{RelayNodes: []serve.RelayNodeCount{
		{Kind: "agent", Transport: "tunnel", State: "online", Count: 3},
		{Kind: "relay", Transport: "direct", State: "offline", Count: 0},
	}})
	body = scrape(t, c)
	if got := total(body, "coddy_swarm_nodes", lbl("kind", "agent", "transport", "tunnel", "state", "online")); got != 3 {
		t.Errorf("online tunnel agents = %v, want 3", got)
	}
	if got, ok := find(body, "coddy_swarm_nodes", lbl("kind", "relay", "transport", "direct", "state", "offline")); !ok || got != 0 {
		t.Errorf("offline direct relays = %v (present %v), want 0", got, ok)
	}
}

// fullSnapshot is a snapshot with every part present, for the tests that need
// every family in the scrape.
func fullSnapshot() serve.Snapshot {
	return serve.Snapshot{
		Subsystems:     []serve.SubsystemStatus{{Kind: serve.KindHTTP, State: serve.StateRunning}},
		Tasks:          map[string]map[string]int{},
		Disk:           &serve.Disk{AvailableBytes: 1, SizeBytes: 2},
		ProviderQuotas: []serve.ProviderQuota{{Provider: "stub", Window: "session", UsedRatio: 0.5, ResetsAt: time.Unix(1_800_000_000, 0)}},
		SwarmJoins:     []swarm.JoinStatus{{Relay: "relay.example:8443", Kind: swarm.KindAgent, Transport: swarm.TransportDirect, Online: true}},
		RelayNodes:     []serve.RelayNodeCount{{Kind: swarm.KindAgent, Transport: swarm.TransportDirect, State: serve.NodeOnline, Count: 1}},
	}
}
