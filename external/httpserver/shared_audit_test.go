//go:build http

package httpserver

// The audit counters of a node (docs/plans/remote-model-provider-phase3.md, 6):
// counted outcomes per alias and credential class, never content or a token.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

type statsRow struct {
	Alias         string `json:"alias"`
	Class         string `json:"class"`
	Outcome       string `json:"outcome"`
	Calls         int64  `json:"calls"`
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	DurationMS    int64  `json:"duration_ms"`
	MaxDurationMS int64  `json:"max_duration_ms"`
}

type statsDoc struct {
	Since string     `json:"since"`
	Rows  []statsRow `json:"rows"`
}

func (fx *sharedFixture) stats() statsDoc {
	fx.t.Helper()
	resp := fx.get("/coddy/shared-models/stats", sharedTestMainToken)
	if resp.StatusCode != http.StatusOK {
		fx.t.Fatalf("stats: %d %s", resp.StatusCode, bodyString(fx.t, resp))
	}
	var doc statsDoc
	if err := json.Unmarshal([]byte(bodyString(fx.t, resp)), &doc); err != nil {
		fx.t.Fatal(err)
	}
	return doc
}

func (d statsDoc) row(alias, class, outcome string) (statsRow, bool) {
	for _, r := range d.Rows {
		if r.Alias == alias && r.Class == class && r.Outcome == outcome {
			return r, true
		}
	}
	return statsRow{}, false
}

func TestSharedCountersCountEveryOutcomePathOnce(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock(), withRate(60, 1, 1))
	// ok, with the tokens of the response.
	fx.stub.run = func(_ context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
		on(llm.StreamChunk{TextDelta: "hi"})
		return &llm.Response{Content: "hi", InputTokens: 11, OutputTokens: 4}, nil
	}
	finish(t, fx.complete(wireReq(sharedTestAlias)))
	// limited: the window is spent (burst 1).
	readError(t, fx.complete(wireReq(sharedTestAlias)))
	fx.clk.Advance(time.Second)

	// upstream (a plain failure), rate (upstream 429), quota.
	for _, failure := range []error{
		errors.New("boom"),
		upstreamHTTPError(429, nil),
		&llm.QuotaResetError{Delay: 90 * time.Second, Cause: upstreamHTTPError(429, nil)},
	} {
		err := failure
		fx.stub.run = func(context.Context, int, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
			return nil, err
		}
		newSSEReader(t, fx.complete(wireReq(sharedTestAlias))).all()
		fx.clk.Advance(time.Second)
	}
	// invalid: an unknown alias is counted under "-".
	readError(t, fx.complete(wireReq("no-such-alias")))
	fx.clk.Advance(time.Second)
	// auth: a bearer the gate refuses.
	resp := fx.post(llm.CoddyCompletionsPath, "made-up-token", wireReq(sharedTestAlias))
	_ = resp.Body.Close()

	doc := fx.stats()
	want := []struct{ alias, class, outcome string }{
		{sharedTestAlias, "shared", "ok"},
		{"-", "shared", "limited"}, // refused before the body names a row
		{sharedTestAlias, "shared", "upstream"},
		{sharedTestAlias, "shared", "rate"},
		{sharedTestAlias, "shared", "quota"},
		{"-", "shared", "invalid"},
		{"-", "unknown", "auth"},
	}
	for _, w := range want {
		r, ok := doc.row(w.alias, w.class, w.outcome)
		if !ok || r.Calls != 1 {
			t.Errorf("row %v: %+v (found %v), want one call", w, r, ok)
		}
	}
	ok, _ := doc.row(sharedTestAlias, "shared", "ok")
	if ok.InputTokens != 11 || ok.OutputTokens != 4 {
		t.Errorf("tokens of the ok row: %+v", ok)
	}
	if len(doc.Rows) != len(want) {
		t.Errorf("%d rows, want %d: %+v", len(doc.Rows), len(want), doc.Rows)
	}
}

func TestSharedCountersTellTheClassOfTheCredential(t *testing.T) {
	fx := newSharedFixture(t)
	finish(t, fx.post(llm.CoddyCompletionsPath, sharedTestMainToken, wireReq(sharedTestAlias)))
	finish(t, fx.complete(wireReq(sharedTestAlias)))
	doc := fx.stats()
	for _, class := range []string{"main", "shared"} {
		if r, ok := doc.row(sharedTestAlias, class, "ok"); !ok || r.Calls != 1 {
			t.Errorf("class %s: %+v %v", class, r, ok)
		}
	}
}

func TestSharedBusyRefusalIsCountedAsBusyNotLimited(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) { c.HTTPServer.SharedModels.MaxStreams = 1 }))
	hold := make(chan struct{})
	entered := make(chan struct{}, 2)
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
		entered <- struct{}{}
		select {
		case <-hold:
		case <-ctx.Done():
		}
		return &llm.Response{Content: "x"}, nil
	}
	first := newSSEReader(t, fx.complete(wireReq(sharedTestAlias)))
	<-entered
	readError(t, fx.complete(wireReq(sharedTestAlias)))
	close(hold)
	first.all()
	waitFor(t, "counted", func() bool {
		r, ok := fx.stats().row("-", "shared", "busy")
		return ok && r.Calls == 1
	})
}

func TestSharedStatsAreForTheMainTokenOnly(t *testing.T) {
	fx := newSharedFixture(t)
	for token, want := range map[string]int{
		sharedTestMainToken: http.StatusOK,
		sharedTestSharedTok: http.StatusUnauthorized,
		"made-up":           http.StatusUnauthorized,
		"":                  http.StatusUnauthorized,
	} {
		resp := fx.get("/coddy/shared-models/stats", token)
		if resp.StatusCode != want {
			t.Errorf("token %q: %d, want %d", token, resp.StatusCode, want)
		}
		_ = resp.Body.Close()
	}
}

// The document carries exactly the documented keys, and nothing of a credential,
// an alias linked to a token, or a prompt.
func TestSharedStatsKeySetAndNeedleScan(t *testing.T) {
	fx := newSharedFixture(t)
	const needle = "NEEDLE-PROMPT-TEXT"
	fx.stub.run = func(_ context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
		on(llm.StreamChunk{TextDelta: needle})
		return &llm.Response{Content: needle, InputTokens: 2, OutputTokens: 2}, nil
	}
	req := wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Messages[0].Content = needle })
	finish(t, fx.complete(req))
	resp := fx.get("/coddy/shared-models/stats", sharedTestMainToken)
	raw := bodyString(t, resp)
	for _, bad := range []string{needle, sharedTestMainToken, sharedTestSharedTok, "upstream-secret-key", sharedTestSelector, "qwen3-secret"} {
		if strings.Contains(raw, bad) {
			t.Fatalf("the stats document leaks %q: %s", bad, raw)
		}
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &top); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]json.RawMessage) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		return sortedStrings(out)
	}
	if !reflect.DeepEqual(keys(top), []string{"rows", "since"}) {
		t.Fatalf("top-level keys %v", keys(top))
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(top["rows"], &rows); err != nil || len(rows) == 0 {
		t.Fatalf("rows: %v %s", err, top["rows"])
	}
	want := []string{"alias", "calls", "class", "duration_ms", "input_tokens", "max_duration_ms", "outcome", "output_tokens"}
	if !reflect.DeepEqual(keys(rows[0]), want) {
		t.Fatalf("row keys %v, want %v", keys(rows[0]), want)
	}
}

// The log line of a call carries the class and the token counts, and no credential.
func TestSharedCallLogLineCarriesTheClassAndTheTokens(t *testing.T) {
	var buf bytes.Buffer
	fx := newSharedFixture(t, withSharedLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	finish(t, fx.complete(wireReq(sharedTestAlias)))
	waitFor(t, "the call is logged", func() bool { return strings.Contains(buf.String(), "shared model call") })
	line := buf.String()
	for _, want := range []string{"class=shared", "input_tokens=3", "output_tokens=1", "alias=coder"} {
		if !strings.Contains(line, want) {
			t.Errorf("log line does not carry %q:\n%s", want, line)
		}
	}
	for _, bad := range []string{sharedTestMainToken, sharedTestSharedTok} {
		if strings.Contains(line, bad) {
			t.Errorf("log line leaks a credential: %s", line)
		}
	}
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
