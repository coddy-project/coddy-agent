//go:build http

package httpserver

// The steps and the stand parts of the scenarios that came with the second step
// of shared models (docs/plans/remote-model-provider-phase2.md, 6.1): the
// capabilities a local Coddy takes from the remote's listing (levels, images,
// "off"), a key written in the local row that a refresh does not overwrite, a
// level the remote narrowed, and the account usage of the remote's provider read
// through the hop. The state is remoteModelState of bdd_remote_model_test.go and
// the stands are those of bdd_remote_model_stand_test.go.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// ---- the account behind the remote's provider ----

// What the remote's provider tells the stand-in hub to report. None of it may
// reach a borrower: the key and the wallet are the lender's own, and the plan
// and the option models name the lender's subscription.
const (
	usageStandKey     = "hub-secret-key-0123"
	usageStandKeyName = "needle-key-name"
	usageStandTier    = "needle-tier-name"
	usageStandOption  = "needle-option-model"
	// The wallet: any amount of money, whatever the unit.
	usageStandBalance = "7777.77"
	usageStandSpent   = "4242.42"
)

// usageStandHub is a stand-in for the hub's GET /limits that a NeuralDeep row of
// the remote reads. The row is pointed at it through the environment override
// the provider honours (llm.EnvNeuralDeepBaseURL), which close puts back.
type usageStandHub struct {
	srv *httptest.Server

	mu      sync.Mutex
	hits    int
	restore []func()
}

// newUsageStandHub starts a hub whose session window is percent used of 15000.
func newUsageStandHub(percent int) *usageStandHub {
	body := usageStandBody(percent)
	h := &usageStandHub{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/limits" {
			http.NotFound(w, r)
			return
		}
		h.mu.Lock()
		h.hits++
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	// The key of the environment would be a second credential for the row.
	h.setenv(llm.EnvNeuralDeepBaseURL, h.srv.URL)
	h.setenv("NEURALDEEP_API_KEY", "")
	return h
}

func (h *usageStandHub) setenv(key, value string) {
	prev, had := os.LookupEnv(key)
	_ = os.Setenv(key, value)
	h.restore = append(h.restore, func() {
		if had {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func (h *usageStandHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hits
}

func (h *usageStandHub) close() {
	h.srv.Close()
	for i := len(h.restore) - 1; i >= 0; i-- {
		h.restore[i]()
	}
}

// usageStandBody is the hub's schema-1 answer for a wallet key: the session
// window of 15000 at percent used, a week window and a daily capacity of other
// numbers, and the plan, the key name, the option models and the wallet that a
// projection must drop.
func usageStandBody(percent int) string {
	const limit = 15000
	used := limit * percent / 100
	doc := map[string]any{
		"schema": 1, "observed_at": "2026-09-06T17:47:02Z", "tier": usageStandTier, "tier_expires_at": nil,
		"unlimited_volume": false, "bypass": false, "fair_use": true,
		"blocked_models": []map[string]any{},
		"options": []map[string]any{{
			"code": "needle_opt", "title": "needle title", "models": []string{usageStandOption},
			"rpm": map[string]any{"limit": 60, "used": 0, "remaining": 60, "window": "1m"}, "unlimited_volume": true, "scope": "account",
		}},
		"key":      map[string]any{"name": usageStandKeyName, "status": "ok", "billing_mode": "wallet", "cap": nil},
		"decision": map[string]any{"scope": "chat", "can_request": true, "blockers": []string{}, "retry_after_sec": nil},
		"chat": map[string]any{
			"session":      map[string]any{"used": used, "limit": limit, "remaining": limit - used, "reset_in_sec": 777, "resets_at": "2026-09-06T17:59:59Z", "window": "3h"},
			"week":         map[string]any{"used": 9981, "limit": 150000, "remaining": 140019, "reset_in_sec": 22378, "resets_at": "2026-09-07T00:00:00Z", "window": "iso-week"},
			"rpm":          map[string]any{"used": 2, "limit": 120, "remaining": 118, "reset_in_sec": 58},
			"cooldown_sec": 0, "scope": "account",
		},
		"daily_capacity": map[string]any{"pct_used": 12.5, "exhausted": false, "resets_at": "2026-09-07T00:00:00+00:00"},
		"wallet":         map[string]any{"balance_rub": json.Number(usageStandBalance), "spent_rub_30d": json.Number(usageStandSpent)},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// ---- the local coddy's rows ----

// localModelRow is what GET /v1/models of the local coddy says about one row.
type localModelRow struct {
	ID               string   `json:"id"`
	MaxContextTokens int      `json:"max_context_tokens"`
	Multimodal       bool     `json:"multimodal"`
	ReasoningLevels  []string `json:"reasoning_levels"`
	ReasoningDefault string   `json:"reasoning_default"`
}

// localModelRowOf reads the local coddy's model list over HTTP and returns the
// row of ref; the list waits a bounded moment for a listing that never answered,
// as every reader of a coddy row does.
func (s *remoteModelState) localModelRowOf(ref string) (localModelRow, error) {
	if err := s.ensureLocal(); err != nil {
		return localModelRow{}, err
	}
	res, err := doHTTP(http.MethodGet, s.local.ts.URL+"/v1/models", "", nil)
	if err != nil {
		return localModelRow{}, err
	}
	var body struct {
		Data []localModelRow `json:"data"`
	}
	if err := json.Unmarshal(res.body, &body); err != nil {
		return localModelRow{}, fmt.Errorf("GET /v1/models: %v: %s", err, res.body)
	}
	for _, m := range body.Data {
		if m.ID == ref {
			return m, nil
		}
	}
	return localModelRow{}, fmt.Errorf("GET /v1/models does not list %q: %s", ref, res.body)
}

func (s *remoteModelState) modelsReportLevels(ref, levels string) error {
	row, err := s.localModelRowOf(ref)
	if err != nil {
		return err
	}
	want := strings.Split(levels, ", ")
	if strings.Join(row.ReasoningLevels, ", ") != levels {
		return fmt.Errorf("%s reports the reasoning levels %q, want %q (in this order)", ref, row.ReasoningLevels, want)
	}
	return nil
}

func (s *remoteModelState) modelsReportMultimodal(ref string) error {
	row, err := s.localModelRowOf(ref)
	if err != nil {
		return err
	}
	if !row.Multimodal {
		return fmt.Errorf("%s is not reported as multimodal: %+v", ref, row)
	}
	return nil
}

// userAttachesImage sends the local coddy an OpenAI-style message with an image
// part for ref. The intake keeps image parts only for a model the local
// configuration says is multimodal, so the picture reaching the remote's model
// is the proof that the local coddy took it from the listing.
func (s *remoteModelState) userAttachesImage(ref string) error {
	if err := s.ensureLocal(); err != nil {
		return err
	}
	raw, err := base64.StdEncoding.DecodeString(remoteModelRedPNG)
	if err != nil {
		return err
	}
	s.image = raw
	body, err := json.Marshal(map[string]any{
		"model": ref, "stream": false,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": "What is in this picture?"},
				{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + remoteModelRedPNG}},
			},
		}},
	})
	if err != nil {
		return err
	}
	res, err := doHTTP(http.MethodPost, s.local.ts.URL+"/v1/chat/completions", "", body)
	if err != nil {
		return err
	}
	if res.status != http.StatusOK {
		return fmt.Errorf("the local coddy refused the message with the image: %d %s", res.status, res.body)
	}
	return s.remoteReceivedImage()
}

// ---- levels narrowed on the remote ----

func (s *remoteModelState) operatorNarrowsLevels(alias, level string) error {
	if err := s.ensureRemote(); err != nil {
		return err
	}
	idx := s.remote.sharedRowIndex(alias)
	if idx < 0 {
		return fmt.Errorf("no row of the remote is shared as %q", alias)
	}
	narrowed := []string{level}
	s.mutateRemote(func(c *config.Config) { c.Models[idx].ReasoningLevels = &narrowed })
	return nil
}

// requestedEffort is the reasoning level a completion request of the local coddy
// asked for, "" when it named none.
func requestedEffort(e remoteExchange) (string, error) {
	var req llm.WireRequest
	if err := json.Unmarshal(e.reqBody, &req); err != nil {
		return "", fmt.Errorf("not a completion request: %v", err)
	}
	if req.Options.ReasoningEffort == nil {
		return "", nil
	}
	return *req.Options.ReasoningEffort, nil
}

// fallsBackToLevel checks the two requests of a refused call: the first asked
// for the level the local view still offered, the second for the one the
// refreshed view falls back to, and the remote's provider got the second.
func (s *remoteModelState) fallsBackToLevel(level, received string) error {
	cs := s.remote.capt.completions()
	if len(cs) != 2 {
		return fmt.Errorf("the remote saw %d completion requests, want the refused one and the one sent again", len(cs))
	}
	first, err := requestedEffort(cs[0])
	if err != nil {
		return err
	}
	second, err := requestedEffort(cs[1])
	if err != nil {
		return err
	}
	if first == "" || first == level {
		return fmt.Errorf("the first request asked for the level %q, want the level the narrowed row no longer offers", first)
	}
	if second != level {
		return fmt.Errorf("the request sent again asked for the level %q, want the fallback %q", second, level)
	}
	return s.providerReceivedLevel(received)
}

// ---- a key written in the local row, and a refresh ----

func (s *remoteModelState) localRowSetsContext(ref string, tokens int) error {
	s.mutateLocal(func(c *config.Config) {
		idx := -1
		for i := range c.Models {
			if c.Models[i].Model == ref {
				idx = i
			}
		}
		if idx < 0 {
			c.Models = append(c.Models, config.ModelEntry{Model: ref, MaxTokens: 4096})
			idx = len(c.Models) - 1
		}
		c.Models[idx].MaxContextTokens = tokens
	})
	return nil
}

// listingRequests is how many times the remote's listing was asked for.
func (s *remoteModelState) listingRequests() int {
	n := 0
	for _, e := range s.remote.capt.snapshot() {
		if e.method == http.MethodGet && e.path == llm.CoddyModelsPath {
			n++
		}
	}
	return n
}

// refreshesListing makes the local coddy read the remote's listing again the
// way a credential change or a stale answer does: the cache forgets the
// provider, the next reader reads it, and the step waits until the read landed.
func (s *remoteModelState) refreshesListing() error {
	if err := s.ensureLocal(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mgr := s.local.mgr
	// The first read, as the admission of a turn makes it.
	mgr.AwaitContextWindows(ctx, mgr.Cfg(), []string{localModelRef}, 10*time.Second)
	if err := mgr.WaitContextWindowsIdle(10 * time.Second); err != nil {
		return err
	}
	before := s.listingRequests()
	if before == 0 {
		return fmt.Errorf("the local coddy never read the remote's listing, so there is nothing to refresh")
	}
	mgr.ForgetContextWindows("remote")
	mgr.AwaitContextWindows(ctx, mgr.Cfg(), []string{localModelRef}, 10*time.Second)
	if err := mgr.WaitContextWindowsIdle(10 * time.Second); err != nil {
		return err
	}
	if after := s.listingRequests(); after <= before {
		return fmt.Errorf("the remote saw %d listing requests after the refresh, %d before: the listing was not read again", after, before)
	}
	if _, ok := mgr.ProviderModelEntry(mgr.Cfg(), "remote", "coder"); !ok {
		return fmt.Errorf("the local coddy holds no record of the model after the refresh")
	}
	return nil
}

// configFileStillSays checks that nothing wrote the local configuration file:
// its bytes are those the stand wrote before it started, and it loads with the
// row at tokens.
func (s *remoteModelState) configFileStillSays(tokens int) error {
	if err := s.ensureLocal(); err != nil {
		return err
	}
	path := s.local.mgr.Cfg().Paths.ConfigPath
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, s.local.configBytes) {
		return fmt.Errorf("the local configuration file changed after the refresh:\n%s", raw)
	}
	loaded, err := config.LoadWithPaths(s.local.mgr.Cfg().Paths)
	if err != nil {
		return fmt.Errorf("the local configuration file does not load: %w", err)
	}
	ent := loaded.FindModelEntry(localModelRef)
	if ent == nil || ent.MaxContextTokens != tokens {
		return fmt.Errorf("the configuration file says %+v for %q, want max_context_tokens %d", ent, localModelRef, tokens)
	}
	return nil
}

// ---- the account usage through the hop ----

// localReadsUsage asks the local coddy for the usage of a model of its coddy
// row, the route the web UI and the remote console use (?model=<alias>).
func (s *remoteModelState) localReadsUsage(ref string) error {
	if err := s.ensureLocal(); err != nil {
		return err
	}
	provider, alias, err := config.SplitModelRef(ref)
	if err != nil {
		return err
	}
	res, err := doHTTP(http.MethodGet, s.local.ts.URL+"/coddy/providers/"+provider+"/usage?model="+alias, "", nil)
	if err != nil {
		return err
	}
	s.localUsage = res
	return nil
}

// localUsageDoc is the part of the local coddy's usage answer the steps read.
type localUsageDoc struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	Usage struct {
		Provider     string `json:"provider"`
		ProviderType string `json:"providerType"`
		Model        string `json:"model"`
		Stale        bool   `json:"stale"`
		Windows      []struct {
			ID          string  `json:"id"`
			UsedPercent float64 `json:"usedPercent"`
		} `json:"windows"`
	} `json:"usage"`
}

// localUsageAnswer decodes the local coddy's usage answer.
func (s *remoteModelState) localUsageAnswer() (localUsageDoc, error) {
	var out localUsageDoc
	if s.localUsage.status == 0 {
		return out, fmt.Errorf("the local coddy has not read a usage yet")
	}
	if s.localUsage.status != http.StatusOK {
		return out, fmt.Errorf("the local coddy's usage answer is %d: %s", s.localUsage.status, s.localUsage.body)
	}
	if err := json.Unmarshal(s.localUsage.body, &out); err != nil {
		return out, fmt.Errorf("not a usage answer: %v: %s", err, s.localUsage.body)
	}
	return out, nil
}

func (s *remoteModelState) reportsPercentUsed(percent int) error {
	ans, err := s.localUsageAnswer()
	if err != nil {
		return err
	}
	if !ans.OK {
		return fmt.Errorf("the local coddy could not read the usage: %s", s.localUsage.body)
	}
	if ans.Usage.ProviderType != "coddy" || ans.Usage.Model != "coder" || ans.Usage.Stale {
		return fmt.Errorf("the usage is %+v, want a fresh one of the coddy row for the alias coder", ans.Usage)
	}
	for _, w := range ans.Usage.Windows {
		if w.ID == "session" {
			if math.Abs(w.UsedPercent-float64(percent)) > 0.05 {
				return fmt.Errorf("the session window is at %.2f percent, want %d", w.UsedPercent, percent)
			}
			// The number is the lender's account's: it came from the hub the
			// remote's provider reads, through the remote, once.
			if hits := s.remote.hub.count(); hits != 1 {
				return fmt.Errorf("the remote's provider read its account %d times, want once", hits)
			}
			return nil
		}
	}
	return fmt.Errorf("the usage has no session window: %+v", ans.Usage.Windows)
}

// usageExchange is the remote's answer to the read of the alias's usage.
func (s *remoteModelState) usageExchange() (remoteExchange, error) {
	want := llm.CoddyUsagePath(s.spec.alias)
	for _, e := range s.remote.capt.snapshot() {
		if e.method == http.MethodGet && e.path == want {
			return e, nil
		}
	}
	return remoteExchange{}, fmt.Errorf("the remote never saw a read of %s", want)
}

func (s *remoteModelState) remoteMarksAccountWide() error {
	e, err := s.usageExchange()
	if err != nil {
		return err
	}
	if e.status != http.StatusOK {
		return fmt.Errorf("the remote answered the usage read with %d: %s", e.status, e.body)
	}
	var doc llm.WireUsage
	if err := json.Unmarshal(e.body, &doc); err != nil {
		return fmt.Errorf("the usage document is not one: %v: %s", err, e.body)
	}
	if !doc.Supported || !doc.AccountWide {
		return fmt.Errorf("the remote's document says supported=%v account_wide=%v: %s", doc.Supported, doc.AccountWide, e.body)
	}
	return nil
}

// usageNeedles are what the document must never carry: the model the alias
// points at, the lender's provider, the key's name, the plan, the option models
// and every amount of money of the wallet.
func (s *remoteModelState) usageNeedles() []string {
	provider, apiModel, _ := config.SplitModelRef(s.spec.selector)
	return []string{
		apiModel, provider, usageStandKeyName, usageStandKey, usageStandTier, usageStandOption,
		usageStandBalance, usageStandSpent, "7777", "4242",
		"wallet", "balance", "rub", "spent", "money", "price", "cost",
	}
}

func (s *remoteModelState) documentNeverMentions() error {
	e, err := s.usageExchange()
	if err != nil {
		return err
	}
	var headers strings.Builder
	for k, vs := range e.header {
		headers.WriteString(k + ": " + strings.Join(vs, ",") + "\n")
	}
	for _, doc := range []struct{ what, text string }{
		{"the document the remote sent", string(e.body)},
		{"the headers the remote sent with it", headers.String()},
		{"the answer of the local coddy", string(s.localUsage.body)},
	} {
		low := strings.ToLower(doc.text)
		for _, needle := range s.usageNeedles() {
			if strings.Contains(low, strings.ToLower(needle)) {
				return fmt.Errorf("%s mentions %q: %s", doc.what, needle, doc.text)
			}
		}
	}
	if len(e.body) == 0 {
		return fmt.Errorf("the remote sent no document, so there is nothing to check")
	}
	return nil
}

// ---- registration ----

func registerRemoteModelPhase2Steps(sc *godog.ScenarioContext, s *remoteModelState) {
	sc.Step(`^the local coddy adds the model "([^"]*)" without (?:reasoning levels|declaring it multimodal)$`, s.localAddsModel)
	sc.Step(`^GET /v1/models of the local coddy reports "([^"]*)" with the reasoning levels "([^"]*)"$`, s.modelsReportLevels)
	sc.Step(`^GET /v1/models of the local coddy reports "([^"]*)" as multimodal$`, s.modelsReportMultimodal)
	sc.Step(`^the local coddy lets a user attach an image to a message sent to "([^"]*)"$`, s.userAttachesImage)
	sc.Step(`^the remote operator narrows "([^"]*)" to the reasoning level "([^"]*)"$`, s.operatorNarrowsLevels)
	sc.Step(`^the local coddy falls back to the level "([^"]*)" and the provider received "([^"]*)"$`, s.fallsBackToLevel)
	sc.Step(`^the local row "([^"]*)" sets max_context_tokens (\d+)$`, s.localRowSetsContext)
	sc.Step(`^the local coddy refreshes its view of the remote listing$`, s.refreshesListing)
	sc.Step(`^the local configuration file still says (\d+)$`, s.configFileStillSays)
	sc.Step(`^the local coddy reads the usage of the model "([^"]*)"$`, s.localReadsUsage)
	sc.Step(`^it reports (\d+) percent of the quota used$`, s.reportsPercentUsed)
	sc.Step(`^the remote marks the reading as account-wide$`, s.remoteMarksAccountWide)
	sc.Step(`^the document never mentions the upstream model id, the provider name, the key name or any amount of money$`, s.documentNeverMentions)
}
