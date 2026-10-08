package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// fakeListing is a ListingSource the tests drive: what the manager's cache
// holds, a call counter, and the arguments of the last call so a test can see
// which configuration and which row the resolver passed.
type fakeListing struct {
	mu       sync.Mutex
	rows     map[string]config.ListedModel
	calls    atomic.Int64
	lastCfg  *config.Config
	lastProv string
	lastAPI  string
}

func newFakeListing() *fakeListing {
	return &fakeListing{rows: make(map[string]config.ListedModel)}
}

func (f *fakeListing) set(provider, apiModel string, lm config.ListedModel) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[provider+"|"+apiModel] = lm
}

func (f *fakeListing) drop(provider, apiModel string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, provider+"|"+apiModel)
}

func (f *fakeListing) ListedModel(cfg *config.Config, provider, apiModel string) (config.ListedModel, bool) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastCfg, f.lastProv, f.lastAPI = cfg, provider, apiModel
	lm, ok := f.rows[provider+"|"+apiModel]
	// A source hands out what it holds; the resolver copies the levels again.
	return lm, ok
}

// withSource gives cfg a listing cell and attaches src to it.
func withSource(cfg *config.Config, src config.ListingSource) *config.Config {
	cfg.Paths = cfg.Paths.WithListing()
	cfg.Paths.AttachListing(src)
	return cfg
}

func coddyConfig(rows ...config.ModelEntry) *config.Config {
	return &config.Config{
		Providers: []config.ProviderConfig{{Name: "far", Type: "coddy", APIBase: "http://127.0.0.1:1"}},
		Models:    rows,
	}
}

func strs(s ...string) *[]string { return &s }

var listedHigh = config.ListedModel{
	Multimodal:        true,
	ReasoningLevels:   []string{"low", "high"},
	ReasoningDefault:  "high",
	AllowReasoningOff: true,
}

// The resolution table of the plan (3.3), per key, for a row of a provider of
// type coddy: a key written locally wins, key by key; an absent key asks the
// remote's listing; no listing means "not known" - never a detection from the
// alias, never a guess.
func TestCoddyResolutionTable(t *testing.T) {
	type want struct {
		levels     []string
		choices    []string
		off        bool
		defaultLvl string
		multimodal bool
	}
	cases := []struct {
		name     string
		row      config.ModelEntry
		noSource bool
		listing  *config.ListedModel // nil: the source does not know the model
		want     want
	}{
		{
			name: "no source attached: nothing is known, the alias is never detected",
			row:  config.ModelEntry{Model: "far/gpt-5"}, noSource: true,
			want: want{},
		},
		{
			name: "source attached, model not listed: still nothing",
			row:  config.ModelEntry{Model: "far/gpt-5"},
			want: want{},
		},
		{
			name: "nothing written, listing knows the model: the listing decides every key",
			row:  config.ModelEntry{Model: "far/coder"}, listing: &listedHigh,
			want: want{levels: []string{"low", "high"}, choices: []string{"low", "high", "off"}, off: true, defaultLvl: "high", multimodal: true},
		},
		{
			name: "listing without off",
			row:  config.ModelEntry{Model: "far/coder"},
			listing: &config.ListedModel{Multimodal: false, ReasoningLevels: []string{"low", "high"},
				ReasoningDefault: "low", AllowReasoningOff: false},
			want: want{levels: []string{"low", "high"}, choices: []string{"low", "high"}, defaultLvl: "low"},
		},
		{
			name: "levels written narrower: they win; the listing's default is not one of them; off and images still come from the listing",
			row:  config.ModelEntry{Model: "far/coder", ReasoningLevels: strs("low")}, listing: &listedHigh,
			want: want{levels: []string{"low"}, choices: []string{"low", "off"}, off: true, defaultLvl: "", multimodal: true},
		},
		{
			name: "levels written empty: the explicit opt-out hides the selector, off has nothing to switch, images still come from the listing",
			row:  config.ModelEntry{Model: "far/coder", ReasoningLevels: strs()}, listing: &listedHigh,
			want: want{multimodal: true},
		},
		{
			name:    "levels written wider than the listing: the written list stands as written",
			row:     config.ModelEntry{Model: "far/coder", ReasoningLevels: strs("low", "medium", "high")},
			listing: &config.ListedModel{ReasoningLevels: []string{"low"}, ReasoningDefault: "low"},
			want:    want{levels: []string{"low", "medium", "high"}, choices: []string{"low", "medium", "high"}, defaultLvl: "low"},
		},
		{
			name: "levels written, no listing at all: the written list stands",
			row:  config.ModelEntry{Model: "far/coder", ReasoningLevels: strs("low", "high"), ReasoningDefault: "high"}, noSource: true,
			want: want{levels: []string{"low", "high"}, choices: []string{"low", "high"}, defaultLvl: "high"},
		},
		{
			name: "no remap for a coddy row: minimal stays minimal",
			row:  config.ModelEntry{Model: "far/coder", ReasoningLevels: strs("minimal", "low"), ReasoningDefault: "minimal"}, noSource: true,
			want: want{levels: []string{"minimal", "low"}, choices: []string{"minimal", "low"}, defaultLvl: "minimal"},
		},
		{
			name: "off written false beats a listing that offers it",
			row:  config.ModelEntry{Model: "far/coder", AllowReasoningOff: config.BoolPtr(false)}, listing: &listedHigh,
			want: want{levels: []string{"low", "high"}, choices: []string{"low", "high"}, off: false, defaultLvl: "high", multimodal: true},
		},
		{
			name:    "off written true beats a listing that does not offer it",
			row:     config.ModelEntry{Model: "far/coder", AllowReasoningOff: config.BoolPtr(true)},
			listing: &config.ListedModel{ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "low"},
			want:    want{levels: []string{"low", "high"}, choices: []string{"low", "high", "off"}, off: true, defaultLvl: "low"},
		},
		{
			name: "off written true with no levels anywhere has nothing to switch",
			row:  config.ModelEntry{Model: "far/coder", AllowReasoningOff: config.BoolPtr(true)}, noSource: true,
			want: want{},
		},
		{
			name: "multimodal written false beats a listing that marks images",
			row:  config.ModelEntry{Model: "far/coder", Multimodal: config.BoolPtr(false)}, listing: &listedHigh,
			want: want{levels: []string{"low", "high"}, choices: []string{"low", "high", "off"}, off: true, defaultLvl: "high", multimodal: false},
		},
		{
			name:    "multimodal written true beats a listing without images and a missing listing",
			row:     config.ModelEntry{Model: "far/coder", Multimodal: config.BoolPtr(true)},
			listing: &config.ListedModel{ReasoningLevels: []string{"low"}},
			want:    want{levels: []string{"low"}, choices: []string{"low"}, multimodal: true},
		},
		{
			name: "default written and offered: the local default wins over the listing's",
			row:  config.ModelEntry{Model: "far/coder", ReasoningDefault: "low"}, listing: &listedHigh,
			want: want{levels: []string{"low", "high"}, choices: []string{"low", "high", "off"}, off: true, defaultLvl: "low", multimodal: true},
		},
		{
			name: "default written but not offered: the key wins and resolves to none, the listing's default does not step in",
			row:  config.ModelEntry{Model: "far/coder", ReasoningDefault: "medium"}, listing: &listedHigh,
			want: want{levels: []string{"low", "high"}, choices: []string{"low", "high", "off"}, off: true, defaultLvl: "", multimodal: true},
		},
		{
			name:    "the listing's default must be one of the levels it resolves to",
			row:     config.ModelEntry{Model: "far/coder"},
			listing: &config.ListedModel{ReasoningLevels: []string{"low"}, ReasoningDefault: "high"},
			want:    want{levels: []string{"low"}, choices: []string{"low"}},
		},
		{
			name: "a listed model without reasoning: no levels, no off, images as listed",
			row:  config.ModelEntry{Model: "far/plain"}, listing: &config.ListedModel{Multimodal: true, AllowReasoningOff: true},
			want: want{multimodal: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := coddyConfig(tc.row)
			src := newFakeListing()
			if !tc.noSource {
				withSource(cfg, src)
			}
			if tc.listing != nil {
				src.set("far", cfg.Models[0].APIModel(), *tc.listing)
			}
			ent := &cfg.Models[0]
			got := want{
				levels:     cfg.ReasoningLevelsFor(ent),
				choices:    cfg.ReasoningChoicesFor(ent),
				off:        cfg.ReasoningOffOffered(ent),
				defaultLvl: cfg.DefaultReasoningLevelFor(ent),
				multimodal: cfg.ModelMultimodal(ent),
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("resolved %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A source that never knew the model is asked with the row's own provider name
// and API model id, in the configuration that reads: the alias of a model may
// itself contain a slash.
func TestResolverAsksTheSourceForTheRowOfTheReadersConfig(t *testing.T) {
	src := newFakeListing()
	src.set("far", "team/coder", listedHigh)
	cfg := withSource(coddyConfig(config.ModelEntry{Model: "far/team/coder"}), src)
	if levels := cfg.ReasoningLevelsFor(&cfg.Models[0]); !reflect.DeepEqual(levels, []string{"low", "high"}) {
		t.Fatalf("levels = %v", levels)
	}
	if src.lastProv != "far" || src.lastAPI != "team/coder" {
		t.Fatalf("source asked for %q / %q, want far / team/coder", src.lastProv, src.lastAPI)
	}
	if src.lastCfg != cfg {
		t.Fatalf("source was handed %p, want the reader's own configuration %p", src.lastCfg, cfg)
	}

	// A second configuration of the same lineage, with the row repointed to
	// another provider name: the source is handed that one, so it can find the
	// row there and in no other configuration (the cross-row answer of the
	// decided model, SRCCFG=0).
	other := *cfg
	other.Providers = []config.ProviderConfig{{Name: "near", Type: "coddy", APIBase: "http://127.0.0.1:2"}}
	other.Models = []config.ModelEntry{{Model: "near/team/coder"}}
	src.set("near", "team/coder", config.ListedModel{ReasoningLevels: []string{"low"}})
	if levels := other.ReasoningLevelsFor(&other.Models[0]); !reflect.DeepEqual(levels, []string{"low"}) {
		t.Fatalf("repointed row: levels = %v, want the listing of its own row", levels)
	}
	if src.lastCfg != &other || src.lastProv != "near" {
		t.Fatalf("source handed %p / %q after the repoint", src.lastCfg, src.lastProv)
	}
	// The first configuration still reads its own row.
	if levels := cfg.ReasoningLevelsFor(&cfg.Models[0]); !reflect.DeepEqual(levels, []string{"low", "high"}) {
		t.Fatalf("the first configuration was answered for another row: %v", levels)
	}
}

// Reads are live: nothing resolved is kept in the configuration, so a listing
// that narrows, widens or withdraws the alias is seen by the very next read of
// the same config and the same row.
func TestResolverReadsLiveAndKeepsNothing(t *testing.T) {
	src := newFakeListing()
	cfg := withSource(coddyConfig(config.ModelEntry{Model: "far/coder"}), src)
	ent := &cfg.Models[0]

	read := func() (levels []string, off, mm bool) {
		return cfg.ReasoningLevelsFor(ent), cfg.ReasoningOffOffered(ent), cfg.ModelMultimodal(ent)
	}
	if l, off, mm := read(); l != nil || off || mm {
		t.Fatalf("before any listing: %v %v %v", l, off, mm)
	}
	src.set("far", "coder", listedHigh)
	if l, off, mm := read(); !reflect.DeepEqual(l, []string{"low", "high"}) || !off || !mm {
		t.Fatalf("after the listing landed: %v %v %v", l, off, mm)
	}
	src.set("far", "coder", config.ListedModel{ReasoningLevels: []string{"low"}})
	if l, off, mm := read(); !reflect.DeepEqual(l, []string{"low"}) || off || mm {
		t.Fatalf("after the listing narrowed: %v %v %v", l, off, mm)
	}
	if got := cfg.DefaultReasoningLevelFor(ent); got != "" {
		t.Fatalf("default after the narrowing = %q", got)
	}
	src.drop("far", "coder")
	if l, off, mm := read(); l != nil || off || mm {
		t.Fatalf("after the alias was withdrawn: %v %v %v", l, off, mm)
	}
	if got := cfg.ReasoningChoicesFor(ent); got != nil {
		t.Fatalf("choices after the withdrawal = %v", got)
	}
}

// What the resolver hands out is its own copy: a caller that edits a list it
// was given neither changes what the source holds nor what the next read says.
func TestResolverCopiesTheListedLevels(t *testing.T) {
	src := newFakeListing()
	listed := config.ListedModel{ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "low"}
	src.set("far", "coder", listed)
	cfg := withSource(coddyConfig(config.ModelEntry{Model: "far/coder"}), src)
	ent := &cfg.Models[0]

	got := cfg.ReasoningLevelsFor(ent)
	got[0] = "tampered"
	if again := cfg.ReasoningLevelsFor(ent); !reflect.DeepEqual(again, []string{"low", "high"}) {
		t.Fatalf("a caller's edit reached the next read: %v", again)
	}
	choices := cfg.ReasoningChoicesFor(ent)
	choices[0] = "tampered"
	if listed.ReasoningLevels[0] != "low" {
		t.Fatalf("a caller's edit reached the source's own slice: %v", listed.ReasoningLevels)
	}
}

// Every provider type other than coddy keeps the answers it always had, with
// a source attached to its configuration and a listing that knows the model and
// says something else: the resolver is guarded by the provider type before it
// reads any source, so the source is never even asked.
func TestResolverLeavesOtherProviderTypesAlone(t *testing.T) {
	for _, c := range capCases {
		for _, mm := range allTri {
			for _, off := range allTri {
				mk := func() *config.Config {
					return &config.Config{
						Providers: []config.ProviderConfig{{Name: c.providerType, Type: c.providerType}},
						Models: []config.ModelEntry{{
							Model: c.providerType + "/" + c.apiModel, ReasoningDefault: c.writeDefault,
							Multimodal: mm.ptr(), AllowReasoningOff: off.ptr(),
						}},
					}
				}
				plain := mk()
				src := newFakeListing()
				src.set(c.providerType, c.apiModel, config.ListedModel{
					Multimodal: true, ReasoningLevels: []string{"zzz"}, ReasoningDefault: "zzz", AllowReasoningOff: true,
				})
				bound := withSource(mk(), src)
				name := c.providerType + "/" + c.apiModel + " multimodal=" + mm.String() + " off=" + off.String()
				pe, be := &plain.Models[0], &bound.Models[0]
				if a, b := plain.ReasoningLevelsFor(pe), bound.ReasoningLevelsFor(be); !reflect.DeepEqual(a, b) {
					t.Errorf("%s: levels %v vs %v", name, a, b)
				}
				if a, b := plain.ReasoningChoicesFor(pe), bound.ReasoningChoicesFor(be); !reflect.DeepEqual(a, b) {
					t.Errorf("%s: choices %v vs %v", name, a, b)
				}
				if a, b := plain.ReasoningOffOffered(pe), bound.ReasoningOffOffered(be); a != b {
					t.Errorf("%s: off %v vs %v", name, a, b)
				}
				if a, b := plain.DefaultReasoningLevelFor(pe), bound.DefaultReasoningLevelFor(be); a != b {
					t.Errorf("%s: default %q vs %q", name, a, b)
				}
				if a, b := plain.ModelMultimodal(pe), bound.ModelMultimodal(be); a != b {
					t.Errorf("%s: multimodal %v vs %v", name, a, b)
				}
				if n := src.calls.Load(); n != 0 {
					t.Errorf("%s: the source of a non-coddy row was asked %d times", name, n)
				}
			}
		}
	}
}

// A coddy row never falls back to detecting levels from its alias: an alias
// that looks like a reasoning model id is noise, and the settings form's Fetch
// must not write it into the row.
func TestReasoningLevelsForProviderTypeCoddyIsTheWrittenListOrNone(t *testing.T) {
	if got := config.ReasoningLevelsForProviderType(&config.ModelEntry{Model: "far/gpt-5"}, "coddy"); got != nil {
		t.Fatalf("an alias that looks like gpt-5 was detected: %v", got)
	}
	if got := config.ReasoningLevelsForProviderType(&config.ModelEntry{Model: "far/gpt-5", ReasoningLevels: strs()}, "coddy"); got != nil {
		t.Fatalf("an explicit empty list = %v, want none", got)
	}
	written := strs("low", "high")
	got := config.ReasoningLevelsForProviderType(&config.ModelEntry{Model: "far/x", ReasoningLevels: written}, "coddy")
	if !reflect.DeepEqual(got, []string{"low", "high"}) {
		t.Fatalf("written list = %v", got)
	}
	got[0] = "tampered"
	if (*written)[0] != "low" {
		t.Fatal("the written list was handed out, not copied")
	}
	// The same entry under another type still detects, and the codex remap is
	// untouched.
	if got := config.ReasoningLevelsForProviderType(&config.ModelEntry{Model: "p/gpt-5"}, "openai"); len(got) != 4 || got[0] != "minimal" {
		t.Fatalf("openai detection = %v", got)
	}
	if got := config.ReasoningLevelsForProviderType(&config.ModelEntry{Model: "p/gpt-5.5"}, "codex"); len(got) != 4 || got[0] != "none" {
		t.Fatalf("codex remap = %v", got)
	}
	if got := config.ReasoningLevelsForProviderType(&config.ModelEntry{Model: "p/gpt-5"}, ""); len(got) != 4 {
		t.Fatalf("empty type detection = %v", got)
	}
}

// Reading through a nil configuration or a nil row is safe for a coddy row
// too, and answers "not known".
func TestResolverIsNilSafe(t *testing.T) {
	var cfg *config.Config
	ent := &config.ModelEntry{Model: "far/coder"}
	if cfg.ReasoningLevelsFor(ent) != nil || cfg.ReasoningChoicesFor(ent) != nil || cfg.ReasoningOffOffered(ent) ||
		cfg.DefaultReasoningLevelFor(ent) != "" || cfg.ModelMultimodal(ent) {
		t.Fatal("a nil configuration must know nothing")
	}
	live := withSource(coddyConfig(), newFakeListing())
	if live.ReasoningLevelsFor(nil) != nil || live.ReasoningChoicesFor(nil) != nil || live.ReasoningOffOffered(nil) ||
		live.DefaultReasoningLevelFor(nil) != "" || live.ModelMultimodal(nil) {
		t.Fatal("a nil row must know nothing")
	}
	// A row whose provider is not in this configuration is not a coddy row.
	orphan := &config.ModelEntry{Model: "ghost/coder"}
	src := newFakeListing()
	src.set("ghost", "coder", listedHigh)
	cfg2 := withSource(coddyConfig(), src)
	if cfg2.ModelMultimodal(orphan) || src.calls.Load() != 0 {
		t.Fatal("a row of an unknown provider must not reach the source")
	}
}

// ---- lineage: one cell per Paths, shared by everything loaded from it ----

const lineageYAML = `providers:
  - name: far
    type: coddy
    api_base: http://127.0.0.1:1
models:
  - model: far/coder
agent:
  model: far/coder
`

func lineageFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(lineageYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func multimodalOf(t *testing.T, cfg *config.Config) bool {
	t.Helper()
	ent := cfg.FindModelEntry("far/coder")
	if ent == nil {
		t.Fatal("row far/coder is missing")
	}
	return cfg.ModelMultimodal(ent)
}

// Every loader allocates the cell, so a configuration the loader made can be
// bound; one a caller built by hand with no Paths cannot be, and says "not
// known".
func TestLoadersAllocateTheCell(t *testing.T) {
	path := lineageFile(t)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	src := newFakeListing()
	src.set("far", "coder", listedHigh)
	if !cfg.Paths.AttachListingIfEmpty(src) {
		t.Fatal("a loaded configuration has no cell to attach to")
	}
	if !multimodalOf(t, cfg) {
		t.Fatal("a bound configuration did not answer from the listing")
	}
	if cfg.Paths.AttachListingIfEmpty(newFakeListing()) {
		t.Fatal("AttachListingIfEmpty took over a cell that already holds a source")
	}

	// LoadFromCLI and LoadReadOnly allocate as well.
	t.Setenv(config.EnvCODDYHome, filepath.Dir(path))
	viaCLI, err := config.LoadFromCLI(config.CLIPaths{Config: path})
	if err != nil {
		t.Fatal(err)
	}
	if !viaCLI.Paths.AttachListingIfEmpty(src) {
		t.Fatal("LoadFromCLI left the configuration without a cell")
	}
	readOnly, _, err := config.LoadReadOnly(config.CLIPaths{Config: path})
	if err != nil {
		t.Fatal(err)
	}
	if !readOnly.Paths.AttachListingIfEmpty(src) {
		t.Fatal("LoadReadOnly left the configuration without a cell")
	}

	// Two loads of one file through a Paths that has no cell are two lineages:
	// a source attached to one never answers the other.
	a, _ := config.Load(path)
	b, _ := config.Load(path)
	a.Paths.AttachListing(src)
	if !multimodalOf(t, a) || multimodalOf(t, b) {
		t.Fatal("two independent loads share a cell")
	}
}

// Everything loaded through LoadWithPaths(c.Paths), JSONDTOToConfig(j, c.Paths)
// and ParseConfigJSONPreservingSecrets(raw, c.Paths, c) shares the cell of c, so
// it is bound the moment it exists: the manager's reload, the settings save, the
// skills reload, the watcher and the agent's private reloads need no line of
// their own.
func TestConfigsLoadedFromPathsShareTheCell(t *testing.T) {
	path := lineageFile(t)
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	src := newFakeListing()
	src.set("far", "coder", listedHigh)
	c.Paths.AttachListing(src)

	reloaded, err := config.LoadWithPaths(c.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded == c || !multimodalOf(t, reloaded) {
		t.Fatal("LoadWithPaths(c.Paths) is not bound to the lineage of c")
	}
	twice, err := config.LoadWithPaths(reloaded.Paths)
	if err != nil || !multimodalOf(t, twice) {
		t.Fatalf("a reload of a reload is not bound: %v", err)
	}

	dto := config.ConfigToJSONDTO(c)
	fromDTO := config.JSONDTOToConfig(dto, c.Paths)
	if !multimodalOf(t, fromDTO) {
		t.Fatal("JSONDTOToConfig(j, c.Paths) is not bound to the lineage of c")
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := config.ParseConfigJSONPreservingSecrets(raw, c.Paths, c)
	if err != nil {
		t.Fatal(err)
	}
	if !multimodalOf(t, parsed) {
		t.Fatal("ParseConfigJSONPreservingSecrets(raw, c.Paths, c) is not bound to the lineage of c")
	}

	// Attaching later is seen by every config of the lineage, the ones loaded
	// earlier included: the cell is shared, not copied.
	other := newFakeListing()
	other.set("far", "coder", config.ListedModel{})
	c.Paths.AttachListing(other)
	if multimodalOf(t, reloaded) || multimodalOf(t, fromDTO) || multimodalOf(t, c) {
		t.Fatal("the last attach did not win for the whole lineage")
	}
}

// A Paths with no cell, which a caller built by hand, can neither be attached to
// nor read through: the five reads say "not known".
func TestPathsWithoutACellKnowsNothing(t *testing.T) {
	var p config.Paths
	src := newFakeListing()
	src.set("far", "coder", listedHigh)
	p.AttachListing(src) // never a panic, and nothing to attach to
	if p.AttachListingIfEmpty(src) {
		t.Fatal("AttachListingIfEmpty reported success on a Paths with no cell")
	}
	cfg := coddyConfig(config.ModelEntry{Model: "far/coder"})
	cfg.Paths = p
	ent := &cfg.Models[0]
	if cfg.ReasoningLevelsFor(ent) != nil || cfg.ReasoningChoicesFor(ent) != nil || cfg.ReasoningOffOffered(ent) ||
		cfg.DefaultReasoningLevelFor(ent) != "" || cfg.ModelMultimodal(ent) {
		t.Fatal("a configuration whose Paths has no cell answered from a listing")
	}
	if src.calls.Load() != 0 {
		t.Fatal("the source was asked through a Paths with no cell")
	}
}

func TestWithListingAndAttachSemantics(t *testing.T) {
	var p config.Paths
	q := p.WithListing()
	if q == p {
		t.Fatal("WithListing on a Paths with no cell must hand out one with a fresh cell")
	}
	// Idempotent: a Paths that has a cell comes back unchanged (the same cell).
	if again := q.WithListing(); again != q {
		t.Fatal("WithListing replaced a cell that was already there")
	}
	// A copy by value shares the cell.
	copyOfQ := q
	first, second := newFakeListing(), newFakeListing()
	first.set("far", "coder", listedHigh)
	if !q.AttachListingIfEmpty(first) {
		t.Fatal("a fresh cell refused an attach-if-empty")
	}
	if copyOfQ.AttachListingIfEmpty(second) {
		t.Fatal("attach-if-empty took over through a copy")
	}
	cfg := coddyConfig(config.ModelEntry{Model: "far/coder"})
	cfg.Paths = copyOfQ
	if !cfg.ModelMultimodal(&cfg.Models[0]) {
		t.Fatal("a copy of Paths does not share the cell")
	}
	// Last attach wins; a nil source clears.
	second.set("far", "coder", config.ListedModel{})
	copyOfQ.AttachListing(second)
	if cfg.ModelMultimodal(&cfg.Models[0]) {
		t.Fatal("the last attach did not win")
	}
	q.AttachListing(nil)
	if cfg.ModelMultimodal(&cfg.Models[0]) {
		t.Fatal("a nil attach left a source behind")
	}
	if !q.AttachListingIfEmpty(first) {
		t.Fatal("a cleared cell refused an attach-if-empty")
	}
	if q.AttachListingIfEmpty(nil) {
		t.Fatal("AttachListingIfEmpty(nil) must not report success")
	}
}

// The cell is one atomic pointer: attaches racing with reads of the lineage are
// race-free (go test -race) and every read sees a whole source.
func TestListingCellIsRaceFree(t *testing.T) {
	cfg := withSource(coddyConfig(config.ModelEntry{Model: "far/coder"}), newFakeListing())
	a, b := newFakeListing(), newFakeListing()
	a.set("far", "coder", listedHigh)
	b.set("far", "coder", config.ListedModel{ReasoningLevels: []string{"low"}})
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ent := &cfg.Models[0]
			for {
				select {
				case <-stop:
					return
				default:
				}
				l := cfg.ReasoningLevelsFor(ent)
				if len(l) > 2 {
					panic("a half-installed source")
				}
				_ = cfg.ModelMultimodal(ent)
			}
		}()
	}
	for i := range 2000 {
		if i%2 == 0 {
			cfg.Paths.AttachListing(a)
		} else {
			cfg.Paths.AttachListing(b)
		}
		cfg.Paths.AttachListingIfEmpty(a)
	}
	close(stop)
	wg.Wait()
}
