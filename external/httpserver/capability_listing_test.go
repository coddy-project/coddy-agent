//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// capListing is a remote Coddy's model listing as the manager's cache reads it.
type capListing struct {
	mu   sync.Mutex
	rows map[string]llm.ModelEntry
}

func (l *capListing) put(rows ...llm.ModelEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rows == nil {
		l.rows = make(map[string]llm.ModelEntry)
	}
	for _, r := range rows {
		l.rows[r.ID] = r
	}
}

func (l *capListing) withdraw(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.rows, id)
}

func (l *capListing) list(context.Context, llm.ProviderInput) ([]llm.ModelEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]llm.ModelEntry, 0, len(l.rows))
	for _, r := range l.rows {
		r.ReasoningLevels = append([]string(nil), r.ReasoningLevels...)
		out = append(out, r)
	}
	return out, nil
}

// capFixture is a local Coddy with one coddy provider row and a few models on
// it, serving /v1/models, /v1/chat/completions and the settings routes.
type capFixture struct {
	cfg     *config.Config
	mgr     *session.Manager
	srv     *Server
	ts      *httptest.Server
	listing *capListing
	capture *passthroughCaptureProvider
}

func newCapFixture(t *testing.T) *capFixture {
	t.Helper()
	home := t.TempDir()
	levels := []string{"low"}
	cfg := &config.Config{
		Paths: config.Paths{Home: home, CWD: "/tmp"},
		Providers: []config.ProviderConfig{
			{Name: "far", Type: "coddy", APIBase: "http://far.example"},
			{Name: "openai", Type: "openai"},
		},
		Models: []config.ModelEntry{
			// Nothing written: the listing decides everything.
			{Model: "far/coder", MaxTokens: 100},
			// Every key written locally: the listing is ignored for it.
			{Model: "far/pinned", MaxTokens: 100, ReasoningLevels: &levels, Multimodal: config.BoolPtr(false),
				AllowReasoningOff: config.BoolPtr(false)},
			// Not in the listing at all.
			{Model: "far/plain", MaxTokens: 100},
			// Another provider type, for the byte-for-byte control.
			{Model: "openai/gpt-5", MaxTokens: 100, ReasoningDefault: "medium"},
		},
		Agent: config.Agent{Model: "far/coder"},
	}
	fx := &capFixture{cfg: cfg, listing: &capListing{}, capture: &passthroughCaptureProvider{}}
	fx.listing.put(
		llm.ModelEntry{ID: "coder", Revision: "r1", Multimodal: true, ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "high", AllowReasoningOff: true},
		llm.ModelEntry{ID: "pinned", Revision: "r1", Multimodal: true, ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "high", AllowReasoningOff: true},
	)
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	fx.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	fx.mgr.SetContextWindowLister(fx.listing.list, nil)
	t.Cleanup(func() { _ = fx.mgr.WaitContextWindowsIdle(5 * time.Second) })
	fx.srv = New(cfg, fx.mgr, slog.Default(), "/tmp")
	fx.srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) { return fx.capture, nil }
	t.Cleanup(fx.srv.Drain)
	fx.ts = httptest.NewServer(fx.srv.Handler())
	t.Cleanup(fx.ts.Close)
	return fx
}

// listingRead makes the manager read the listing behind the row of a model
// (what the first turn or the first /v1/models does) and waits for it.
func (fx *capFixture) listingRead(t *testing.T) {
	t.Helper()
	fx.mgr.AwaitContextWindows(context.Background(), fx.cfg, []string{"far/coder"}, 5*time.Second)
	if err := fx.mgr.WaitContextWindowsIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
}

type modelsRow struct {
	ID               string   `json:"id"`
	Multimodal       bool     `json:"multimodal"`
	ReasoningLevels  []string `json:"reasoning_levels"`
	ReasoningDefault string   `json:"reasoning_default"`
}

func (fx *capFixture) models(t *testing.T) map[string]modelsRow {
	t.Helper()
	res, err := http.Get(fx.ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		Data []modelsRow `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]modelsRow, len(body.Data))
	for _, d := range body.Data {
		out[d.ID] = d
	}
	return out
}

// GET /v1/models: a coddy row's capabilities come from the remote's listing,
// key by key under the row's own keys, and follow a refresh of the listing.
func TestModelsRowsOfACoddyProviderAnswerFromTheListing(t *testing.T) {
	fx := newCapFixture(t)

	// The route reads the listing itself (it waits a bounded moment for one
	// that never answered), so no priming is needed.
	rows := fx.models(t)
	coder := rows["far/coder"]
	if !coder.Multimodal || !reflect.DeepEqual(coder.ReasoningLevels, []string{"low", "high", "off"}) || coder.ReasoningDefault != "high" {
		t.Fatalf("far/coder = %+v, want what the listing says", coder)
	}
	pinned := rows["far/pinned"]
	if pinned.Multimodal || !reflect.DeepEqual(pinned.ReasoningLevels, []string{"low"}) || pinned.ReasoningDefault != "" {
		t.Fatalf("far/pinned = %+v, want its own keys: levels [low], no images, no off, no default", pinned)
	}
	plain := rows["far/plain"]
	if plain.Multimodal || len(plain.ReasoningLevels) != 0 || plain.ReasoningDefault != "" {
		t.Fatalf("far/plain = %+v, a model the listing does not carry is not known", plain)
	}
	// A provider of another type answers exactly as it always did.
	gpt := rows["openai/gpt-5"]
	if gpt.Multimodal || !reflect.DeepEqual(gpt.ReasoningLevels, []string{"minimal", "low", "medium", "high"}) || gpt.ReasoningDefault != "medium" {
		t.Fatalf("openai/gpt-5 = %+v", gpt)
	}

	// The remote narrows the model; the stale-revision refresh a request would
	// trigger reads the listing again, and the next /v1/models says so.
	fx.listing.put(llm.ModelEntry{ID: "coder", Revision: "r2", ReasoningLevels: []string{"low"}, ReasoningDefault: "low"})
	if _, err := fx.mgr.RefreshProviderModelEntry(context.Background(), fx.cfg, "far", "coder", "r1", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	coder = fx.models(t)["far/coder"]
	if coder.Multimodal || !reflect.DeepEqual(coder.ReasoningLevels, []string{"low"}) || coder.ReasoningDefault != "low" {
		t.Fatalf("far/coder after the listing narrowed = %+v", coder)
	}

	// The remote withdraws the alias: the capabilities vanish with the record.
	fx.listing.withdraw("coder")
	if _, err := fx.mgr.RefreshProviderModelEntry(context.Background(), fx.cfg, "far", "coder", "r2", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	coder = fx.models(t)["far/coder"]
	if coder.Multimodal || len(coder.ReasoningLevels) != 0 || coder.ReasoningDefault != "" {
		t.Fatalf("far/coder after the alias was withdrawn = %+v", coder)
	}
	// Nothing was written to the configuration by any of it.
	for _, m := range fx.cfg.Models[:1] {
		if m.Multimodal != nil || m.AllowReasoningOff != nil || m.ReasoningLevels != nil {
			t.Fatalf("a listing refresh wrote a key of the row: %+v", m)
		}
	}
}

// POST /v1/chat/completions on a coddy row: a picture reaches the model only
// when the listing marks the model multimodal and the row does not say no.
func TestChatCompletionsKeepsAnImageForAListedMultimodalRow(t *testing.T) {
	fx := newCapFixture(t)
	post := func(model string) []llm.ImagePart {
		t.Helper()
		body := `{"model":"` + model + `","messages":[{"role":"user","content":[{"type":"text","text":"look"},` +
			`{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}],"stream":false}`
		res, err := http.Post(fx.ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", model, res.StatusCode, raw)
		}
		return fx.capture.seen[len(fx.capture.seen)-1].ImageParts
	}

	fx.listingRead(t)
	if got := post("far/coder"); len(got) != 1 {
		t.Fatalf("far/coder: %d image parts, the listing marks the model multimodal", len(got))
	}
	if got := post("far/pinned"); len(got) != 0 {
		t.Fatalf("far/pinned: %d image parts, the row writes multimodal: false", len(got))
	}
	if got := post("far/plain"); len(got) != 0 {
		t.Fatalf("far/plain: %d image parts, the listing does not carry the model", len(got))
	}

	// The remote stops accepting images for the model: the next request drops them.
	fx.listing.put(llm.ModelEntry{ID: "coder", Revision: "r2", ReasoningLevels: []string{"low"}})
	if _, err := fx.mgr.RefreshProviderModelEntry(context.Background(), fx.cfg, "far", "coder", "r1", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := post("far/coder"); len(got) != 0 {
		t.Fatalf("far/coder after the listing dropped images: %d image parts", len(got))
	}
}

// GET /coddy/config/reasoning-levels is what the settings form's Fetch writes
// into a row: for a coddy row that is nothing, whatever the hint or the saved
// provider says and whatever the listing knows, because a written list would pin
// the row against the remote.
func TestReasoningLevelsRouteNeverDetectsOrPinsForACoddyRow(t *testing.T) {
	fx := newCapFixture(t)
	fx.listingRead(t)
	get := func(query string) (levels []string, detected bool) {
		t.Helper()
		res, err := http.Get(fx.ts.URL + "/coddy/config/reasoning-levels?" + query)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var body struct {
			OK       bool     `json:"ok"`
			Levels   []string `json:"levels"`
			Detected bool     `json:"detected"`
		}
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.OK || body.Levels == nil {
			t.Fatalf("%s: ok=%v levels=%v: an empty answer is [], not null", query, body.OK, body.Levels)
		}
		return body.Levels, body.Detected
	}
	q := func(model, providerType string) string {
		v := url.Values{"model": {model}}
		if providerType != "" {
			v.Set("provider_type", providerType)
		}
		return v.Encode()
	}

	for _, tc := range []struct{ model, hint string }{
		{"far/gpt-5", "coddy"},     // an alias that looks like a detectable id, hinted
		{"far/gpt-5", ""},          // no hint: the saved provider is of type coddy
		{"far/coder", "coddy"},     // the listing knows levels for it: still none
		{"far/coder", ""},          // the same, found through the saved provider
		{"unsaved/gpt-5", "coddy"}, // a row being edited, its provider not saved yet
	} {
		levels, detected := get(q(tc.model, tc.hint))
		if len(levels) != 0 || detected {
			t.Errorf("model=%s provider_type=%q: levels=%v detected=%v, want none", tc.model, tc.hint, levels, detected)
		}
	}

	// Every other type keeps detecting, and the hint still wins over the saved
	// provider (the form shows the type the operator has just picked).
	if levels, detected := get(q("openai/gpt-5", "")); !detected || !reflect.DeepEqual(levels, []string{"minimal", "low", "medium", "high"}) {
		t.Errorf("openai/gpt-5: levels=%v detected=%v", levels, detected)
	}
	if levels, detected := get(q("far/gpt-5", "openai")); !detected || len(levels) != 4 {
		t.Errorf("a row retyped to openai in the form: levels=%v detected=%v", levels, detected)
	}
	if levels, detected := get(q("far/gpt-5.5", "codex")); !detected || levels[0] != "none" {
		t.Errorf("codex remap: levels=%v detected=%v", levels, detected)
	}
}

// What a remote shares of a row of its own is read through the same readers,
// and for a lender row (a provider of any type but coddy) they answer exactly
// as before: detection from the id, the keys as written, an absent key false.
func TestSharedRowOfALenderRowIsUnchanged(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) {
		row := &c.Models[0] // stub/qwen3-secret, shared as "coder"
		row.ReasoningLevels = nil
		row.ReasoningDefault = "medium"
		row.Multimodal = config.BoolPtr(true)
		row.AllowReasoningOff = config.BoolPtr(true)
	}))
	got := listingRow(t, fx)
	if !got.Multimodal || !got.AllowReasoningOff || got.ReasoningDefault != "medium" ||
		!reflect.DeepEqual(got.ReasoningLevels, []string{"low", "medium", "high"}) {
		t.Fatalf("lender row: %+v", got)
	}

	absent := newSharedFixture(t, withSharedConfig(func(c *config.Config) { c.Models[0].ReasoningLevels = nil }))
	got = listingRow(t, absent)
	if got.Multimodal || got.AllowReasoningOff || !reflect.DeepEqual(got.ReasoningLevels, []string{"low", "medium", "high"}) {
		t.Fatalf("lender row with absent keys: %+v", got)
	}
}
