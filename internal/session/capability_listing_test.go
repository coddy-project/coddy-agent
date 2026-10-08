package session

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// coddyListing is a remote Coddy's model listing as the manager's cache reads
// it: the records of the shared models, a switchable failure and a call
// counter.
type coddyListing struct {
	mu    sync.Mutex
	rows  map[string]llm.ModelEntry
	err   error
	calls atomic.Int32
}

func newCoddyListing(rows ...llm.ModelEntry) *coddyListing {
	l := &coddyListing{rows: make(map[string]llm.ModelEntry)}
	l.put(rows...)
	return l
}

func (l *coddyListing) put(rows ...llm.ModelEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range rows {
		l.rows[r.ID] = r
	}
}

func (l *coddyListing) withdraw(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.rows, id)
}

func (l *coddyListing) fail(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.err = err
}

func (l *coddyListing) list(_ context.Context, _ llm.ProviderInput) ([]llm.ModelEntry, error) {
	l.calls.Add(1)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	out := make([]llm.ModelEntry, 0, len(l.rows))
	for _, r := range l.rows {
		r.ReasoningLevels = append([]string(nil), r.ReasoningLevels...)
		out = append(out, r)
	}
	return out, nil
}

var coderRecord = llm.ModelEntry{
	ID: "coder", Revision: "r1", Multimodal: true,
	ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "high", AllowReasoningOff: true,
}

func coddyTestConfig(home string) *config.Config {
	return &config.Config{
		Paths:     config.Paths{Home: home},
		Providers: []config.ProviderConfig{{Name: "far", Type: "coddy", APIBase: "http://far.example"}},
		Models:    []config.ModelEntry{{Model: "far/coder"}, {Model: "far/plain"}},
		Agent:     config.Agent{Model: "far/coder"},
	}
}

func newCoddyTestManager(t *testing.T, cfg *config.Config, listing *coddyListing, clock *windowTestClock) *Manager {
	t.Helper()
	m := NewManager(cfg, &contextUsageCapture{}, nil, slog.Default(), t.TempDir(), nil)
	var now func() time.Time
	if clock != nil {
		now = clock.Now
	}
	m.SetContextWindowLister(listing.list, now)
	t.Cleanup(func() {
		if err := m.WaitContextWindowsIdle(5 * time.Second); err != nil {
			t.Error(err)
		}
	})
	return m
}

// await reads the listing behind cfg's rows and waits for it.
func await(t *testing.T, m *Manager, cfg *config.Config) {
	t.Helper()
	m.AwaitContextWindows(context.Background(), cfg, []string{"far/coder"}, 5*time.Second)
	if err := m.WaitContextWindowsIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
}

type capabilityRead struct {
	levels     []string
	choices    []string
	off        bool
	def        string
	multimodal bool
}

func readCapabilities(cfg *config.Config, model string) capabilityRead {
	ent := cfg.FindModelEntry(model)
	return capabilityRead{
		levels:     cfg.ReasoningLevelsFor(ent),
		choices:    cfg.ReasoningChoicesFor(ent),
		off:        cfg.ReasoningOffOffered(ent),
		def:        cfg.DefaultReasoningLevelFor(ent),
		multimodal: cfg.ModelMultimodal(ent),
	}
}

var coderCapabilities = capabilityRead{
	levels: []string{"low", "high"}, choices: []string{"low", "high", "off"}, off: true, def: "high", multimodal: true,
}

// The manager is the listing source of its configuration: until the listing of
// the row has answered a coddy model is not known (no levels, no images), and
// once it has, the five reads answer from the record.
func TestManagerIsTheListingSourceOfItsConfig(t *testing.T) {
	cfg := coddyTestConfig(t.TempDir())
	listing := newCoddyListing(coderRecord)
	m := newCoddyTestManager(t, cfg, listing, nil)

	if got := readCapabilities(cfg, "far/coder"); !reflect.DeepEqual(got, capabilityRead{}) {
		t.Fatalf("before the listing answered: %+v", got)
	}
	await(t, m, cfg)
	if got := readCapabilities(cfg, "far/coder"); !reflect.DeepEqual(got, coderCapabilities) {
		t.Fatalf("after the listing answered: %+v, want %+v", got, coderCapabilities)
	}
	// A model the listing does not carry is not known, while its neighbour is.
	if got := readCapabilities(cfg, "far/plain"); !reflect.DeepEqual(got, capabilityRead{}) {
		t.Fatalf("a model the listing does not carry: %+v", got)
	}
	// The read never fetches: it is the cache as it is.
	if n := listing.calls.Load(); n != 1 {
		t.Fatalf("listing read %d times, want 1", n)
	}
	// A local key still wins, key by key, and a refresh never writes one.
	cfg.Models[0].Multimodal = config.BoolPtr(false)
	cfg.Models[0].ReasoningDefault = "low"
	if got := readCapabilities(cfg, "far/coder"); got.multimodal || got.def != "low" || !reflect.DeepEqual(got.levels, coderCapabilities.levels) {
		t.Fatalf("local keys do not win key by key: %+v", got)
	}
	if cfg.Models[0].AllowReasoningOff != nil || cfg.Models[0].ReasoningLevels != nil {
		t.Fatalf("a read wrote a key of the row: %+v", cfg.Models[0])
	}
}

// What the manager reports is the record of the row of the CONFIG that reads:
// the cache key follows the provider row's api_base, so a configuration that
// repoints the row is not answered from the listing of the old address.
func TestListingSourceKeysTheRecordByTheReadersOwnRow(t *testing.T) {
	cfg := coddyTestConfig(t.TempDir())
	listing := newCoddyListing(coderRecord)
	m := newCoddyTestManager(t, cfg, listing, nil)
	await(t, m, cfg)

	moved := *cfg
	moved.Providers = []config.ProviderConfig{{Name: "far", Type: "coddy", APIBase: "http://elsewhere.example"}}
	moved.Models = []config.ModelEntry{{Model: "far/coder"}}
	if got := readCapabilities(&moved, "far/coder"); !reflect.DeepEqual(got, capabilityRead{}) {
		t.Fatalf("the repointed row was answered from the old address's listing: %+v", got)
	}
	// The original configuration is still answered for its own row.
	if got := readCapabilities(cfg, "far/coder"); !reflect.DeepEqual(got, coderCapabilities) {
		t.Fatalf("the original configuration lost its answer: %+v", got)
	}
	// Once the new address has been read the repointed config has its own answer.
	listing.put(llm.ModelEntry{ID: "coder", Revision: "r9", ReasoningLevels: []string{"low"}, ReasoningDefault: "low"})
	m.AwaitContextWindows(context.Background(), &moved, []string{"far/coder"}, 5*time.Second)
	if err := m.WaitContextWindowsIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if got := readCapabilities(&moved, "far/coder"); !reflect.DeepEqual(got.levels, []string{"low"}) || got.multimodal {
		t.Fatalf("repointed row after its own listing: %+v", got)
	}
	if got := readCapabilities(cfg, "far/coder"); !reflect.DeepEqual(got, coderCapabilities) {
		t.Fatalf("the old row's record changed with the new row's: %+v", got)
	}
}

// ListedModel itself: the record in the shape the configuration wants, only for
// a coddy row, copied out of the cache.
func TestManagerListedModel(t *testing.T) {
	cfg := coddyTestConfig(t.TempDir())
	cfg.Providers = append(cfg.Providers, config.ProviderConfig{Name: "hub", Type: "openai", APIBase: "https://hub.example/v1"})
	listing := newCoddyListing(coderRecord, llm.ModelEntry{ID: "gpt-5", ReasoningLevels: []string{"zzz"}})
	m := newCoddyTestManager(t, cfg, listing, nil)
	await(t, m, cfg)

	got, ok := m.ListedModel(cfg, "far", "coder")
	if !ok || !got.Multimodal || !got.AllowReasoningOff || got.ReasoningDefault != "high" || !reflect.DeepEqual(got.ReasoningLevels, []string{"low", "high"}) {
		t.Fatalf("ListedModel = %+v, %v", got, ok)
	}
	got.ReasoningLevels[0] = "tampered"
	if again, _ := m.ListedModel(cfg, "far", "coder"); again.ReasoningLevels[0] != "low" {
		t.Fatalf("a caller's edit reached the cache: %v", again.ReasoningLevels)
	}
	if _, ok := m.ListedModel(cfg, "far", "missing"); ok {
		t.Fatal("a model the listing does not carry is known")
	}
	if _, ok := m.ListedModel(cfg, "ghost", "coder"); ok {
		t.Fatal("a provider the configuration does not have is known")
	}
	if _, ok := m.ListedModel(nil, "far", "coder"); ok {
		t.Fatal("a nil configuration is known")
	}
	// Only a coddy row's listing carries capabilities: another type's cache
	// holds a window and nothing else, and is never reported as a capability.
	if _, ok := m.ListedModel(cfg, "hub", "gpt-5"); ok {
		t.Fatal("a provider of another type was reported as a capability record")
	}
}

// NewManager binds the manager to its configuration before anything can read
// it, even when the configuration was built by hand with no loader to give its
// Paths a cell.
func TestNewManagerBindsBeforeTheFirstRead(t *testing.T) {
	cfg := coddyTestConfig(t.TempDir())
	if cfg.Paths.WithListing() == cfg.Paths {
		t.Fatal("a hand-built Paths already has a cell: the premise of the test")
	}
	listing := newCoddyListing(coderRecord)
	m := newCoddyTestManager(t, cfg, listing, nil)
	if cfg.Paths.WithListing() != cfg.Paths {
		t.Fatal("NewManager returned with the configuration still without a cell")
	}
	await(t, m, cfg)
	if got := readCapabilities(cfg, "far/coder"); !reflect.DeepEqual(got, coderCapabilities) {
		t.Fatalf("a hand-built configuration is not answered from the listing: %+v", got)
	}
}

const coddyConfigFile = `providers:
  - name: far
    type: coddy
    api_base: http://far.example
models:
  - model: far/coder
  - model: far/plain
agent:
  model: far/coder
`

func loadedCoddyConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(coddyConfigFile), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: dir, Config: path})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// Every configuration loaded from the Paths of the manager's own answers from
// its listing the moment it exists: the reload of the session manager, the
// settings save, the agent's private reloads and the watcher need no step of
// their own.
func TestConfigsLoadedFromTheManagersPathsAnswerFromTheListing(t *testing.T) {
	cfg := loadedCoddyConfig(t)
	listing := newCoddyListing(coderRecord)
	m := newCoddyTestManager(t, cfg, listing, nil)
	await(t, m, cfg)

	reloaded, err := config.LoadWithPaths(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if got := readCapabilities(reloaded, "far/coder"); !reflect.DeepEqual(got, coderCapabilities) {
		t.Fatalf("LoadWithPaths(c.Paths): %+v", got)
	}
	fromDTO := config.JSONDTOToConfig(config.ConfigToJSONDTO(cfg), cfg.Paths)
	if got := readCapabilities(fromDTO, "far/coder"); !reflect.DeepEqual(got, coderCapabilities) {
		t.Fatalf("JSONDTOToConfig(j, c.Paths): %+v", got)
	}
	// The manager's own reload goes through storeConfig and keeps the lineage.
	if _, err := m.ReloadConfigForSession(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if m.Cfg() == cfg {
		t.Fatal("the reload did not install a new configuration")
	}
	if got := readCapabilities(m.Cfg(), "far/coder"); !reflect.DeepEqual(got, coderCapabilities) {
		t.Fatalf("the manager's reloaded configuration: %+v", got)
	}
	// And the old one, which an agent may still hold, is still answered.
	if got := readCapabilities(cfg, "far/coder"); !reflect.DeepEqual(got, coderCapabilities) {
		t.Fatalf("the replaced configuration: %+v", got)
	}
}

// A configuration no loader made - a test's, or a caller's - is bound by
// storeConfig before it is published; one that already belongs to another
// manager's lineage is never taken over.
func TestReplaceConfigBindsAHandBuiltConfigAndKeepsAnotherManagersCell(t *testing.T) {
	cfg := coddyTestConfig(t.TempDir())
	listing := newCoddyListing(coderRecord)
	m := newCoddyTestManager(t, cfg, listing, nil)
	await(t, m, cfg)

	handBuilt := &config.Config{
		Providers: []config.ProviderConfig{{Name: "far", Type: "coddy", APIBase: "http://far.example"}},
		Models:    []config.ModelEntry{{Model: "far/coder"}},
		Agent:     config.Agent{Model: "far/coder"},
	}
	if got := readCapabilities(handBuilt, "far/coder"); !reflect.DeepEqual(got, capabilityRead{}) {
		t.Fatalf("an unbound configuration answered: %+v", got)
	}
	m.ReplaceConfig(handBuilt)
	if got := readCapabilities(handBuilt, "far/coder"); !reflect.DeepEqual(got, coderCapabilities) {
		t.Fatalf("a hand-built configuration is not bound by ReplaceConfig: %+v", got)
	}

	// Another manager owns a lineage of its own, with its own cache.
	otherCfg := coddyTestConfig(t.TempDir())
	otherListing := newCoddyListing(llm.ModelEntry{ID: "coder", Revision: "o1", ReasoningLevels: []string{"low"}, ReasoningDefault: "low"})
	other := newCoddyTestManager(t, otherCfg, otherListing, nil)
	await(t, other, otherCfg)
	sibling := *otherCfg // shares otherCfg's Paths, so its cell, which holds `other`
	m.ReplaceConfig(&sibling)
	if got := readCapabilities(&sibling, "far/coder"); !reflect.DeepEqual(got.levels, []string{"low"}) {
		t.Fatalf("storeConfig took over another manager's cell: %+v", got)
	}
}

// A second manager built on the very configuration object the first holds takes
// the lineage over (last attach wins): the tests that restart a manager on one
// config build their own for the second, and this documents why.
func TestSecondManagerOnTheSameConfigObjectTakesTheLineageOver(t *testing.T) {
	cfg := coddyTestConfig(t.TempDir())
	first := newCoddyTestManager(t, cfg, newCoddyListing(coderRecord), nil)
	await(t, first, cfg)
	if got := readCapabilities(cfg, "far/coder"); !reflect.DeepEqual(got, coderCapabilities) {
		t.Fatalf("first manager: %+v", got)
	}
	second := newCoddyTestManager(t, cfg, newCoddyListing(llm.ModelEntry{ID: "coder", Revision: "s1", ReasoningLevels: []string{"low"}}), nil)
	await(t, second, cfg)
	if got := readCapabilities(cfg, "far/coder"); !reflect.DeepEqual(got.levels, []string{"low"}) {
		t.Fatalf("the newer manager did not take the lineage over: %+v", got)
	}
	// A config of a lineage of its own is answered by its own manager only.
	own := coddyTestConfig(t.TempDir())
	third := newCoddyTestManager(t, own, newCoddyListing(coderRecord), nil)
	await(t, third, own)
	if got := readCapabilities(own, "far/coder"); !reflect.DeepEqual(got, coderCapabilities) {
		t.Fatalf("a config of its own lineage: %+v", got)
	}
	if got := readCapabilities(cfg, "far/coder"); !reflect.DeepEqual(got.levels, []string{"low"}) {
		t.Fatalf("a config was answered from another lineage's manager: %+v", got)
	}
}

// A configuration of another lineage - a loader that started its own, with no
// manager attached - is never answered from this manager's cache, whatever the
// cache holds under the same row.
func TestAConfigOfAnotherLineageIsNeverAnsweredFromThisCache(t *testing.T) {
	cfg := coddyTestConfig(t.TempDir())
	m := newCoddyTestManager(t, cfg, newCoddyListing(coderRecord), nil)
	await(t, m, cfg)
	if got := readCapabilities(cfg, "far/coder"); !reflect.DeepEqual(got, coderCapabilities) {
		t.Fatalf("the manager's own config: %+v", got)
	}

	stranger := loadedCoddyConfig(t) // its own loader, its own cell, no source
	if got := readCapabilities(stranger, "far/coder"); !reflect.DeepEqual(got, capabilityRead{}) {
		t.Fatalf("a stranger was answered: %+v", got)
	}
}

// A refresh that fails keeps the last record that answered, and a model the
// listing no longer carries is gone: the cache is the source's whole memory.
func TestFailedRefreshKeepsTheLastRecordAndAWithdrawnAliasVanishes(t *testing.T) {
	cfg := coddyTestConfig(t.TempDir())
	clock := &windowTestClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	listing := newCoddyListing(coderRecord)
	m := newCoddyTestManager(t, cfg, listing, clock)
	await(t, m, cfg)
	if got := readCapabilities(cfg, "far/coder"); !reflect.DeepEqual(got, coderCapabilities) {
		t.Fatalf("first read: %+v", got)
	}

	clock.Advance(contextWindowTTL + time.Minute)
	listing.fail(errors.New("the remote is down"))
	await(t, m, cfg)
	if listing.calls.Load() != 2 {
		t.Fatalf("the refresh did not run: %d reads", listing.calls.Load())
	}
	if got := readCapabilities(cfg, "far/coder"); !reflect.DeepEqual(got, coderCapabilities) {
		t.Fatalf("a failed refresh dropped the record: %+v", got)
	}

	// The remote comes back narrower: the next refresh shows it at once.
	clock.Advance(contextWindowRetry + time.Minute)
	listing.fail(nil)
	listing.put(llm.ModelEntry{ID: "coder", Revision: "r2", ReasoningLevels: []string{"low"}, ReasoningDefault: "low"})
	await(t, m, cfg)
	if got := readCapabilities(cfg, "far/coder"); !reflect.DeepEqual(got.levels, []string{"low"}) || got.off || got.multimodal || got.def != "low" {
		t.Fatalf("narrowed listing: %+v", got)
	}

	// And withdrawn: the alias is gone from the next listing.
	clock.Advance(contextWindowTTL + time.Minute)
	listing.withdraw("coder")
	listing.put(llm.ModelEntry{ID: "other"})
	await(t, m, cfg)
	if got := readCapabilities(cfg, "far/coder"); !reflect.DeepEqual(got, capabilityRead{}) {
		t.Fatalf("a withdrawn alias is still known: %+v", got)
	}
}

// Reads race with reloads, refreshes and a replaced configuration: whatever
// they see is one whole record, never a torn one (go test -race).
func TestListingSourceIsRaceFree(t *testing.T) {
	cfg := loadedCoddyConfig(t)
	clock := &windowTestClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	listing := newCoddyListing(coderRecord)
	m := newCoddyTestManager(t, cfg, listing, clock)
	await(t, m, cfg)

	wide, narrow := []string{"low", "high", "off"}, []string{"low"}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				c := m.Cfg()
				// One call reads one record: the choices are the whole wide
				// record or the whole narrow one, never a level of one with the
				// off of the other, and an empty answer is the "not known" of a
				// configuration that has not been read yet.
				got := c.ReasoningChoicesFor(c.FindModelEntry("far/coder"))
				if got != nil && !reflect.DeepEqual(got, wide) && !reflect.DeepEqual(got, narrow) {
					panic("a torn read of the listing record")
				}
				_ = readCapabilities(c, "far/coder")
			}
		}()
	}
	paths := config.Paths{Home: cfg.Paths.Home, CWD: cfg.Paths.CWD, ConfigPath: cfg.Paths.ConfigPath}
	for i := range 20 {
		clock.Advance(contextWindowTTL + time.Second)
		if i%2 == 0 {
			listing.put(llm.ModelEntry{ID: "coder", Revision: "x", ReasoningLevels: narrow})
		} else {
			listing.put(coderRecord)
		}
		m.AwaitContextWindows(context.Background(), m.Cfg(), []string{"far/coder"}, time.Second)
		if i%5 == 0 {
			if _, err := m.ReloadConfigForSession(context.Background(), nil); err != nil {
				t.Error(err)
			}
		}
		// A configuration no loader made, with the same file behind it.
		m.ReplaceConfig(&config.Config{
			Paths:     paths,
			Providers: []config.ProviderConfig{{Name: "far", Type: "coddy", APIBase: "http://far.example"}},
			Models:    []config.ModelEntry{{Model: "far/coder"}},
		})
	}
	close(stop)
	wg.Wait()
}

// A level a session selected on a coddy model before the listing answered (a
// session loaded from disk) is kept as it was stored: while the model is not
// known the session reads the model's default, which is none, and the
// selection returns when the listing lands - and again when a narrower listing
// widens, because nothing rewrote it (plan 3.8, item 5).
func TestEffectiveReasoningWaitsForTheListingWithoutLosingTheSelection(t *testing.T) {
	cfg := coddyTestConfig(t.TempDir())
	clock := &windowTestClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	listing := newCoddyListing(coderRecord)
	m := newCoddyTestManager(t, cfg, listing, clock)
	st := &State{ID: "sess_loaded", Mode: ModeAgent, SelectedReasoning: "high"}

	if got := st.EffectiveReasoning(cfg); got != "" {
		t.Fatalf("before the listing answered the session runs on level %q, want none", got)
	}
	await(t, m, cfg)
	if got := st.EffectiveReasoning(cfg); got != "high" {
		t.Fatalf("after the listing answered: %q, want the stored selection high", got)
	}

	// The remote narrows the model: the selection is not offered any more and
	// the model's own default stands in, without the stored value being touched.
	clock.Advance(contextWindowTTL + time.Minute)
	listing.put(llm.ModelEntry{ID: "coder", Revision: "r2", ReasoningLevels: []string{"low"}, ReasoningDefault: "low"})
	await(t, m, cfg)
	if got := st.EffectiveReasoning(cfg); got != "low" {
		t.Fatalf("after the listing narrowed: %q, want the default low", got)
	}
	if st.SelectedReasoning != "high" {
		t.Fatalf("the stored selection was rewritten to %q", st.SelectedReasoning)
	}
	clock.Advance(contextWindowTTL + time.Minute)
	listing.put(coderRecord)
	await(t, m, cfg)
	if got := st.EffectiveReasoning(cfg); got != "high" {
		t.Fatalf("after the listing widened again: %q, want the stored selection back", got)
	}
}
