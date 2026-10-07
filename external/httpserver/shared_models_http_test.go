//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func runtimeIsUnix() bool { return runtime.GOOS != "windows" }

func TestSharedListingNamesOnlySharedRowsUnderTheirAliases(t *testing.T) {
	fx := newSharedFixture(t)
	resp := fx.get(llm.CoddyModelsPath, sharedTestSharedTok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	raw := bodyString(t, resp)
	for _, leak := range []string{sharedTestUpstreamID, "private-model", "stub", sharedTestUpstreamAB, "upstream-secret-key", `"stream"`} {
		if strings.Contains(raw, leak) {
			t.Fatalf("the listing mentions %q: %s", leak, raw)
		}
	}
	var listing llm.WireListing
	if err := json.Unmarshal([]byte(raw), &listing); err != nil {
		t.Fatal(err)
	}
	if listing.Protocol != llm.CoddyProtocol || len(listing.Data) != 1 {
		t.Fatalf("listing: %s", raw)
	}
	row := listing.Data[0]
	if row.ID != sharedTestAlias || row.Revision == "" || row.MaxContextTokens != 200000 || row.Multimodal ||
		strings.Join(row.ReasoningLevels, ",") != "low,high" || row.ReasoningDefault != "low" || row.AllowReasoningOff {
		t.Fatalf("row: %+v", row)
	}
}

// With nothing shared the listing is empty, never an error and never a row of
// the private models.
func TestSharedListingIsEmptyWhenNothingIsShared(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) { c.Models[0].SharedAs = "" }))
	resp := fx.get(llm.CoddyModelsPath, sharedTestSharedTok)
	raw := bodyString(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(raw, `"data":[]`) {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
}

func listingRow(t *testing.T, fx *sharedFixture) llm.WireModelRow {
	t.Helper()
	resp := fx.get(llm.CoddyModelsPath, sharedTestSharedTok)
	var listing llm.WireListing
	if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(listing.Data) == 0 {
		t.Fatal("empty listing")
	}
	return listing.Data[0]
}

// The revision is the same while nothing changed - across a restart of the
// remote too, since the key lives in the agent home - and changes with anything
// a client can observe or with the model the alias points at.
func TestSharedRevisionIsKeyedStableAndMovesWithTheRow(t *testing.T) {
	fx := newSharedFixture(t)
	first := listingRow(t, fx).Revision
	if again := listingRow(t, fx).Revision; again != first {
		t.Fatalf("the revision moved with nothing changed: %q then %q", first, again)
	}
	keyPath := filepath.Join(fx.home, "shared-models.key")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("the key was not stored: %v", err)
	}
	if runtimeIsUnix() && info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v, want 0600", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(keyPath)
	if strings.Contains(first, strings.TrimSpace(string(raw))) {
		t.Fatal("the revision contains the key")
	}

	// A restart: another server on the same home reads the same key.
	restarted := newSharedFixtureOnHome(t, fx.home)
	if got := listingRow(t, restarted).Revision; got != first {
		t.Fatalf("the revision changed across a restart: %q then %q", first, got)
	}

	// The context window is observable.
	window := newSharedFixtureOnHome(t, fx.home, withSharedConfig(func(c *config.Config) { c.Models[0].MaxContextTokens = 100000 }))
	if got := listingRow(t, window).Revision; got == first {
		t.Fatal("the revision did not move with the context window")
	}
	// So is the model the alias points at, though nothing the client sees changed.
	other := newSharedFixtureOnHome(t, fx.home, withSharedConfig(func(c *config.Config) {
		c.Models[0].Model = "stub/qwen3-other"
	}))
	if got := listingRow(t, other).Revision; got == first {
		t.Fatal("the revision did not move with the model behind the alias")
	}
	// A different install (another key) never produces the same revision.
	alien := newSharedFixture(t)
	if got := listingRow(t, alien).Revision; got == first {
		t.Fatal("two installs share a revision: it is not keyed")
	}
}

func newSharedFixtureOnHome(t *testing.T, home string, opts ...sharedFixtureOption) *sharedFixture {
	t.Helper()
	return newSharedFixture(t, append([]sharedFixtureOption{func(fx *sharedFixture, c *config.Config) {
		c.Paths.Home, c.Paths.CWD, c.Paths.ConfigPath = home, home, home+"/config.yaml"
	}}, opts...)...)
}

func TestSharedCompletionStreamsFramesAndLeavesNothingBehind(t *testing.T) {
	fx := newSharedFixture(t)
	fx.stub.run = func(_ context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
		on(llm.StreamChunk{ReasoningDelta: "thinking"})
		on(llm.StreamChunk{TextDelta: "Hello "})
		on(llm.StreamChunk{TextDelta: "world."})
		return &llm.Response{Content: "Hello world.", Reasoning: "thinking", ReasoningSignature: "sig-abc",
			InputTokens: 120, OutputTokens: 8, CachedInputTokens: 100, StopReason: "end_turn"}, nil
	}
	tools := []llm.WireTool{{Name: "get_weather", Description: "weather", InputSchema: map[string]any{"type": "object"}}}
	resp := fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) {
		r.Messages = []llm.WireMessage{{Role: "system", Content: "You are the local harness."}, {Role: "user", Content: "Hi"}}
		r.Tools = tools
	}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, bodyString(t, resp))
	}
	for k, want := range map[string]string{"Content-Type": "text/event-stream", "Cache-Control": "no-cache", "X-Accel-Buffering": "no"} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	evs := newSSEReader(t, resp).all()
	if len(evs) == 0 || evs[0].comment != "hb" {
		t.Fatalf("the first event is not the heartbeat comment: %+v", evs)
	}
	var text strings.Builder
	for _, e := range evs {
		if e.typ == llm.WireTypeChunk {
			var c llm.WireChunk
			_ = json.Unmarshal(e.raw, &c)
			text.WriteString(c.TextDelta)
		}
	}
	if text.String() != "Hello world." {
		t.Fatalf("chunks built %q", text.String())
	}
	terms := terminalEvents(evs)
	if len(terms) != 1 || terms[0].typ != llm.WireTypeFinal || evs[len(evs)-1].typ != llm.WireTypeFinal {
		t.Fatalf("want exactly one terminal event, the last: %+v", evs)
	}
	var final llm.WireFinal
	if err := json.Unmarshal(terms[0].raw, &final); err != nil {
		t.Fatal(err)
	}
	if final.Content != "Hello world." || final.InputTokens != 120 || final.OutputTokens != 8 || final.CachedInputTokens != 100 || final.StopReason != "end_turn" {
		t.Fatalf("final: %+v", final)
	}
	// The signature leaves inside an envelope tagged with the model, not raw.
	if final.ReasoningSignature == "" || final.ReasoningSignature == "sig-abc" || !strings.HasPrefix(final.ReasoningSignature, "coddy1:") {
		t.Fatalf("signature not sealed: %q", final.ReasoningSignature)
	}

	// What the provider received is the harness the client sent.
	msgs := fx.stub.lastMessages()
	if len(msgs) != 2 || msgs[0].Role != llm.RoleSystem || msgs[0].Content != "You are the local harness." || msgs[1].Content != "Hi" {
		t.Fatalf("provider saw %+v", msgs)
	}
	if got := fx.stub.tools[0]; len(got) != 1 || got[0].Name != "get_weather" {
		t.Fatalf("provider saw tools %+v", got)
	}
	sel, _ := fx.lastBuild()
	if sel != sharedTestSelector {
		t.Fatalf("the provider was built for %q", sel)
	}

	// Stateless: no session, nothing on disk but the key.
	entries, _ := os.ReadDir(fx.home)
	for _, e := range entries {
		if e.Name() != "shared-models.key" {
			t.Fatalf("the call wrote %q into the home", e.Name())
		}
	}
	listed, err := fx.srv.mgr.HandleSessionList(context.Background(), acp.SessionListParams{})
	if err != nil || len(listed.Sessions) != 0 {
		t.Fatalf("the remote holds sessions: %+v (%v)", listed, err)
	}
	waitFor(t, "the slot to be released", func() bool { return fx.srv.sharedLimit.tracked() == 0 })
}

func TestSharedCompletionFirstHeartbeatPrecedesTheProviderCall(t *testing.T) {
	fx := newSharedFixture(t)
	release := make(chan struct{})
	started := make(chan struct{})
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return &llm.Response{Content: "late"}, nil
	}
	resp := fx.complete(wireReq(sharedTestAlias))
	rd := newSSEReader(t, resp)
	ev, _ := rd.next(5 * time.Second)
	if ev.comment != "hb" {
		t.Fatalf("first event: %+v", ev)
	}
	<-started
	close(release)
	rd.all()
}

// The alias is the only name: a provider/model selector, a private model and a
// missing alias all answer 404 and the answer repeats nothing.
func TestSharedUnknownAliasAnswers404NamingNothing(t *testing.T) {
	fx := newSharedFixture(t)
	for _, model := range []string{sharedTestSelector, sharedTestPrivate, "nope", "", "coder/extra"} {
		resp := fx.complete(wireReq(model))
		raw := bodyString(t, resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("model %q: status %d: %s", model, resp.StatusCode, raw)
		}
		var e llm.WireError
		if err := json.Unmarshal([]byte(raw), &e); err != nil || e.Kind != llm.WireKindInvalid {
			t.Fatalf("model %q: not an invalid error object: %s", model, raw)
		}
		for _, leak := range []string{sharedTestSelector, sharedTestUpstreamID, "private-model", "stub"} {
			if strings.Contains(raw, leak) {
				t.Fatalf("model %q: the 404 mentions %q: %s", model, leak, raw)
			}
		}
	}
	if fx.buildCount() != 0 {
		t.Fatal("a provider was built for an unknown alias")
	}
}

func TestSharedProtocolIsStrict(t *testing.T) {
	fx := newSharedFixture(t)
	resp := fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Protocol = 7 }))
	e := readError(t, resp)
	if resp.StatusCode != http.StatusBadRequest || e.Kind != llm.WireKindInvalid || e.Code != llm.WireCodeProtocolMismatch {
		t.Fatalf("status %d: %+v", resp.StatusCode, e)
	}
	if !strings.Contains(e.Message, "1") || !strings.Contains(e.Message, "7") {
		t.Fatalf("the answer names both versions: %q", e.Message)
	}
	if fx.buildCount() != 0 {
		t.Fatal("a provider was built for a protocol mismatch")
	}
}

func TestSharedStaleRevisionIsAnsweredBeforeTheProviderIsBuilt(t *testing.T) {
	fx := newSharedFixture(t)
	current := listingRow(t, fx).Revision
	stale := "not-the-revision"
	resp := fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Options.ExpectedRevision = &stale }))
	e := readError(t, resp)
	if resp.StatusCode != http.StatusBadRequest || e.Kind != llm.WireKindInvalid || e.Code != llm.WireCodeStaleRevision || e.Revision != current {
		t.Fatalf("status %d: %+v (current %q)", resp.StatusCode, e, current)
	}
	if fx.buildCount() != 0 || fx.stub.callCount() != 0 {
		t.Fatal("the provider was built or called for a stale view")
	}
	// The current revision passes; so does no revision at all.
	resp = fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Options.ExpectedRevision = &current }))
	newSSEReader(t, resp).all()
	if fx.stub.callCount() != 1 {
		t.Fatalf("the provider was called %d times for the current revision", fx.stub.callCount())
	}
	resp = fx.complete(wireReq(sharedTestAlias))
	newSSEReader(t, resp).all()
	if fx.stub.callCount() != 2 {
		t.Fatal("a request without expected_revision was refused")
	}
}

// The revision is checked against the row as it is now: change the row and the
// client's old revision goes stale.
func TestSharedStaleRevisionFollowsAChangedRow(t *testing.T) {
	fx := newSharedFixture(t)
	old := listingRow(t, fx).Revision
	changed := *fx.cfg
	models := append([]config.ModelEntry(nil), fx.cfg.Models...)
	models[0].MaxContextTokens = 100000
	changed.Models = models
	fx.srv.ReplaceConfig(&changed)
	resp := fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Options.ExpectedRevision = &old }))
	if e := readError(t, resp); e.Code != llm.WireCodeStaleRevision {
		t.Fatalf("a changed row did not make the old revision stale: %+v", e)
	}
}

func TestSharedOptionsFollowTheRowsRules(t *testing.T) {
	ptr := func(v int) *int { return &v }
	str := func(v string) *string { return &v }
	t.Run("an omitted reasoning_effort is the row's default", func(t *testing.T) {
		fx := newSharedFixture(t)
		newSSEReader(t, fx.complete(wireReq(sharedTestAlias))).all()
		if _, opts := fx.lastBuild(); opts.ReasoningEffort != "low" {
			t.Fatalf("effort %q, want the row's default low", opts.ReasoningEffort)
		}
	})
	t.Run("a listed level is sent as asked", func(t *testing.T) {
		fx := newSharedFixture(t)
		newSSEReader(t, fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Options.ReasoningEffort = str("high") }))).all()
		if _, opts := fx.lastBuild(); opts.ReasoningEffort != "high" {
			t.Fatalf("effort %q", opts.ReasoningEffort)
		}
	})
	t.Run("off is refused without allow_reasoning_off and names the alias", func(t *testing.T) {
		fx := newSharedFixture(t)
		resp := fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Options.ReasoningEffort = str("off") }))
		e := readError(t, resp)
		if resp.StatusCode != http.StatusBadRequest || e.Kind != llm.WireKindInvalid || !strings.Contains(e.Message, `"coder"`) {
			t.Fatalf("status %d: %+v", resp.StatusCode, e)
		}
		if strings.Contains(e.Message, sharedTestSelector) || fx.buildCount() != 0 {
			t.Fatalf("the refusal names the selector or the provider was built: %+v", e)
		}
	})
	t.Run("off is accepted when the row allows it", func(t *testing.T) {
		fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) { c.Models[0].AllowReasoningOff = true }))
		newSSEReader(t, fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Options.ReasoningEffort = str("off") }))).all()
		if _, opts := fx.lastBuild(); opts.ReasoningEffort != "off" {
			t.Fatalf("effort %q", opts.ReasoningEffort)
		}
	})
	t.Run("a level the row does not list is refused", func(t *testing.T) {
		fx := newSharedFixture(t)
		resp := fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Options.ReasoningEffort = str("ultra") }))
		if e := readError(t, resp); resp.StatusCode != http.StatusBadRequest || !strings.Contains(e.Message, `"coder"`) {
			t.Fatalf("status %d: %+v", resp.StatusCode, e)
		}
	})
	t.Run("max_tokens above the row's ceiling is cut", func(t *testing.T) {
		fx := newSharedFixture(t)
		newSSEReader(t, fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Options.MaxTokens = ptr(1_000_000) }))).all()
		if _, opts := fx.lastBuild(); opts.MaxTokens == nil || *opts.MaxTokens != 4096 {
			t.Fatalf("max_tokens %v, want the row's 4096", opts.MaxTokens)
		}
	})
	t.Run("max_tokens below the ceiling stays, and none stays none", func(t *testing.T) {
		fx := newSharedFixture(t)
		newSSEReader(t, fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Options.MaxTokens = ptr(512) }))).all()
		if _, opts := fx.lastBuild(); opts.MaxTokens == nil || *opts.MaxTokens != 512 {
			t.Fatalf("max_tokens %v", opts.MaxTokens)
		}
		newSSEReader(t, fx.complete(wireReq(sharedTestAlias))).all()
		if _, opts := fx.lastBuild(); opts.MaxTokens != nil || opts.Temperature != nil {
			t.Fatalf("options the client never set were invented: %+v", opts)
		}
	})
	t.Run("max_tokens below one is refused", func(t *testing.T) {
		fx := newSharedFixture(t)
		resp := fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Options.MaxTokens = ptr(0) }))
		if e := readError(t, resp); resp.StatusCode != http.StatusBadRequest || e.Kind != llm.WireKindInvalid {
			t.Fatalf("status %d: %+v", resp.StatusCode, e)
		}
	})
	t.Run("retry_budget_ms reaches the provider input", func(t *testing.T) {
		fx := newSharedFixture(t)
		budget := int64(1500)
		newSSEReader(t, fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Options.RetryBudgetMS = &budget }))).all()
		if _, o := fx.lastBuild(); o.RetryBudget == nil || *o.RetryBudget != 1500*time.Millisecond {
			t.Fatalf("budget %v", o.RetryBudget)
		}
		newSSEReader(t, fx.complete(wireReq(sharedTestAlias))).all()
		if _, o := fx.lastBuild(); o.RetryBudget != nil {
			t.Fatalf("a call without a budget got one: %v", *o.RetryBudget)
		}
	})
}

// A client that never sets temperature never trips a provider's own validation,
// while one that does is refused with the alias named.
func TestSharedTemperatureUnsetNeverTripsValidateForCodex(t *testing.T) {
	codex := func(c *config.Config) {
		c.Providers[0].Type = "codex"
		c.Models[0].SharedSubscriptionAck = true
		c.Models[0].MaxTokens = 0
	}
	fx := newSharedFixture(t, withSharedConfig(codex))
	newSSEReader(t, fx.complete(wireReq(sharedTestAlias))).all()
	if fx.stub.callCount() != 1 {
		t.Fatal("a request with no temperature was refused for a codex row")
	}
	temp := 0.5
	resp := fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Options.Temperature = &temp }))
	e := readError(t, resp)
	if resp.StatusCode != http.StatusBadRequest || e.Kind != llm.WireKindInvalid || !strings.Contains(e.Message, `"coder"`) {
		t.Fatalf("status %d: %+v", resp.StatusCode, e)
	}
	if strings.Contains(e.Message, sharedTestSelector) || strings.Contains(e.Message, "stub") {
		t.Fatalf("the refusal names the selector or the provider: %q", e.Message)
	}
}

// A signature of this model comes back raw to the provider; another model's, or
// one with no envelope, is dropped and the call succeeds.
func TestSharedSignatureEnvelopeIsOpenedForThisModelOnly(t *testing.T) {
	fx := newSharedFixture(t)
	fx.stub.run = func(_ context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
		return &llm.Response{Content: "a", Reasoning: "r", ReasoningSignature: "sig-abc"}, nil
	}
	evs := newSSEReader(t, fx.complete(wireReq(sharedTestAlias))).all()
	var final llm.WireFinal
	_ = json.Unmarshal(terminalEvents(evs)[0].raw, &final)

	history := func(sig string) llm.WireRequest {
		return wireReq(sharedTestAlias, func(r *llm.WireRequest) {
			r.Messages = []llm.WireMessage{
				{Role: "user", Content: "Hi"},
				{Role: "assistant", Content: "a", Reasoning: "r", ReasoningSignature: sig},
				{Role: "user", Content: "again"},
			}
		})
	}
	reasoningSig := func() string { return fx.stub.lastMessages()[1].ReasoningSignature }

	newSSEReader(t, fx.complete(history(final.ReasoningSignature))).all()
	if got := reasoningSig(); got != "sig-abc" {
		t.Fatalf("a valid envelope was not opened: %q", got)
	}

	// The alias is reassigned to another model: the old envelope is dropped.
	reassigned := *fx.cfg
	models := append([]config.ModelEntry(nil), fx.cfg.Models...)
	models[0].Model = "stub/qwen3-other"
	reassigned.Models = models
	fx.srv.ReplaceConfig(&reassigned)
	resp := fx.complete(history(final.ReasoningSignature))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a signature of another model made the call fail: %d", resp.StatusCode)
	}
	newSSEReader(t, resp).all()
	if got := reasoningSig(); got != "" {
		t.Fatalf("another model's signature reached the provider: %q", got)
	}

	// No envelope at all.
	fx.srv.ReplaceConfig(fx.cfg)
	newSSEReader(t, fx.complete(history("sig-abc"))).all()
	if got := reasoningSig(); got != "" {
		t.Fatalf("a bare signature reached the provider: %q", got)
	}
}

func TestSharedRefusesAMalformedRequest(t *testing.T) {
	fx := newSharedFixture(t)
	cases := map[string]any{
		"not json":   []byte("{not json"),
		"no message": wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Messages = nil }),
		"bad role":   wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Messages = []llm.WireMessage{{Role: "wizard", Content: "x"}} }),
	}
	for name, body := range cases {
		resp := fx.post(llm.CoddyCompletionsPath, sharedTestSharedTok, body)
		if e := readError(t, resp); resp.StatusCode != http.StatusBadRequest || e.Kind != llm.WireKindInvalid {
			t.Fatalf("%s: status %d: %+v", name, resp.StatusCode, e)
		}
	}
	if fx.buildCount() != 0 {
		t.Fatal("a provider was built for a malformed request")
	}
	waitFor(t, "slots released", func() bool { return fx.srv.sharedLimit.tracked() == 0 })
}

func TestSharedUsageRouteIsReservedAndAnswers404(t *testing.T) {
	fx := newSharedFixture(t)
	resp := fx.get("/coddy/llm/models/coder/usage", sharedTestSharedTok)
	raw := bodyString(t, resp)
	if resp.StatusCode != http.StatusNotFound || strings.Contains(raw, "stub") {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
}

// The stream limit key is the bearer, whichever alias is asked for.
func TestSharedLimitCountsAllAliasesOfOneBearer(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) {
		c.HTTPServer.SharedModels.MaxStreams = 2
		c.Models[1].SharedAs = "second"
	}))
	hold := make(chan struct{})
	entered := make(chan struct{}, 8)
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
		entered <- struct{}{}
		select {
		case <-hold:
		case <-ctx.Done():
		}
		return &llm.Response{Content: "x"}, nil
	}
	var readers []*sseReader
	for _, alias := range []string{sharedTestAlias, "second"} {
		readers = append(readers, newSSEReader(t, fx.complete(wireReq(alias))))
		<-entered
	}
	resp := fx.complete(wireReq(sharedTestAlias))
	e := readError(t, resp)
	if resp.StatusCode != http.StatusTooManyRequests || e.Kind != llm.WireKindBusy || resp.Header.Get("Retry-After") != "1" {
		t.Fatalf("status %d retry-after %q: %+v", resp.StatusCode, resp.Header.Get("Retry-After"), e)
	}
	// The main token is another key: its calls are counted on their own.
	other := fx.post(llm.CoddyCompletionsPath, sharedTestMainToken, wireReq(sharedTestAlias))
	if other.StatusCode != http.StatusOK {
		t.Fatalf("another credential was refused: %d", other.StatusCode)
	}
	<-entered
	close(hold)
	newSSEReader(t, other).all()
	for _, r := range readers {
		r.all()
	}
	if fx.stub.callCount() != 3 {
		t.Fatalf("the provider was called %d times, want 3 (the refused call never reached it)", fx.stub.callCount())
	}
}

// Draining the response body is not required for the slot to come back: a
// finished call frees it whatever the client does next.
func TestSharedSlotIsFreedWhenTheCallEnds(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) { c.HTTPServer.SharedModels.MaxStreams = 1 }))
	for i := 0; i < 3; i++ {
		resp := fx.complete(wireReq(sharedTestAlias))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("call %d: %d (%s)", i, resp.StatusCode, bodyString(t, resp))
		}
		newSSEReader(t, resp).all()
	}
}
