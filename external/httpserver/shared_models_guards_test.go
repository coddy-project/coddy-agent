//go:build http

package httpserver

// The guards of the shared-model routes: the three timers, the heartbeat, the
// slot on every exit path and the error object. Time is the test's: the clock
// of the heartbeat and of the stall guard is advanced by hand, and the
// deadlines handed to the operating system are set to a few milliseconds.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// expectNone fails if an event arrives within d.
func (r *sseReader) expectNone(d time.Duration) {
	r.t.Helper()
	select {
	case ev, ok := <-r.ch:
		if ok {
			r.t.Fatalf("unexpected event: %+v", ev)
		}
		r.t.Fatal("the stream ended")
	case <-time.After(d):
	}
}

func (r *sseReader) expectHeartbeat() {
	r.t.Helper()
	ev, ok := r.next(5 * time.Second)
	if !ok || ev.comment != "hb" {
		r.t.Fatalf("want a heartbeat comment, got %+v", ev)
	}
}

// The heartbeat comment is written at most 15 s after the last byte, a chunk
// counts as a byte, and the first one is the first thing on the wire.
func TestSharedHeartbeatKeepsTheGapUnderFifteenSeconds(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock())
	chunk := make(chan struct{})
	finish := make(chan struct{})
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
		select {
		case <-chunk:
		case <-ctx.Done():
		}
		on(llm.StreamChunk{TextDelta: "x"})
		select {
		case <-finish:
		case <-ctx.Done():
		}
		return &llm.Response{Content: "x"}, nil
	}
	rd := newSSEReader(t, fx.complete(wireReq(sharedTestAlias)))
	rd.expectHeartbeat()
	waitFor(t, "the heartbeat and the stall guard to arm", func() bool { return fx.clk.armed() >= 2 })

	fx.clk.Advance(14 * time.Second)
	rd.expectNone(60 * time.Millisecond)
	fx.clk.Advance(1 * time.Second)
	rd.expectHeartbeat()

	// A chunk is a byte too: it moves the next comment 15 s past itself.
	fx.clk.Advance(10 * time.Second)
	rd.expectNone(40 * time.Millisecond)
	close(chunk)
	if ev, _ := rd.next(5 * time.Second); ev.typ != llm.WireTypeChunk {
		t.Fatalf("want the chunk, got %+v", ev)
	}
	fx.clk.Advance(14 * time.Second)
	rd.expectNone(60 * time.Millisecond)
	fx.clk.Advance(1 * time.Second)
	rd.expectHeartbeat()

	close(finish)
	if ev, _ := rd.next(5 * time.Second); ev.typ != llm.WireTypeFinal {
		t.Fatalf("want the final frame, got %+v", ev)
	}
}

// A blocking row computes in silence for as long as max_call_ms allows: the
// remote's stall guard does not cut it, and the heartbeat keeps the response
// alive meanwhile.
func TestSharedBlockingRowIsNotStallGuardedAndKeepsHeartbeating(t *testing.T) {
	blocking := func(c *config.Config) { f := false; c.Models[0].Stream = &f }
	fx := newSharedFixture(t, withFakeClock(), withSharedConfig(blocking))
	finish := make(chan struct{})
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
		select {
		case <-finish:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		on(llm.StreamChunk{TextDelta: "six minutes later"})
		return &llm.Response{Content: "six minutes later"}, nil
	}
	rd := newSSEReader(t, fx.complete(wireReq(sharedTestAlias)))
	rd.expectHeartbeat()
	waitFor(t, "the heartbeat to arm", func() bool { return fx.clk.armed() >= 1 })
	for i := 0; i < 24; i++ {
		fx.clk.Advance(15 * time.Second)
		rd.expectHeartbeat()
	}
	if fx.clk.armed() != 1 {
		t.Fatalf("%d timers armed for a blocking row, want only the heartbeat", fx.clk.armed())
	}
	close(finish)
	var terminal sseEvent
	for {
		ev, ok := rd.next(5 * time.Second)
		if !ok {
			t.Fatal("the stream ended without a terminal event")
		}
		if ev.typ == llm.WireTypeFinal || ev.typ == llm.WireTypeError {
			terminal = ev
			break
		}
	}
	if terminal.typ != llm.WireTypeFinal {
		t.Fatalf("a blocking row that took six minutes was cut: %s", terminal.raw)
	}
}

// A blocking row is bounded by max_call_ms, from the start of the provider call
// to its result, and ends as error{upstream, timeout}.
func TestSharedBlockingRowIsBoundedByMaxCall(t *testing.T) {
	ms := 60
	blocking := func(c *config.Config) {
		f := false
		c.Models[0].Stream = &f
		c.HTTPServer.SharedModels.MaxCallMS = &ms
	}
	fx := newSharedFixture(t, withSharedConfig(blocking))
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	evs := newSSEReader(t, fx.complete(wireReq(sharedTestAlias))).all()
	terms := terminalEvents(evs)
	if len(terms) != 1 || terms[0].typ != llm.WireTypeError {
		t.Fatalf("events: %+v", evs)
	}
	var e llm.WireError
	_ = json.Unmarshal(terms[0].raw, &e)
	if e.Kind != llm.WireKindUpstream || e.Cause != llm.WireCauseTimeout || e.Emitted {
		t.Fatalf("error: %+v", e)
	}
	waitFor(t, "the slot to be released", func() bool { return fx.srv.sharedLimit.tracked() == 0 })
}

// An upstream that accepts the request and never answers ends as a stall after
// S, from the start of the call - before the first byte - with emitted false;
// one that goes silent after a chunk ends as a stall with emitted true.
func TestSharedStallBeforeAndAfterTheFirstChunk(t *testing.T) {
	short := func(c *config.Config) { ms := 1000; c.Agent.LLMStreamIdleTimeoutMS = &ms }
	t.Run("before the first chunk", func(t *testing.T) {
		fx := newSharedFixture(t, withFakeClock(), withSharedConfig(short))
		entered := make(chan struct{})
		var cancelled atomic.Bool
		fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
			close(entered)
			<-ctx.Done()
			cancelled.Store(true)
			return nil, ctx.Err()
		}
		rd := newSSEReader(t, fx.complete(wireReq(sharedTestAlias)))
		rd.expectHeartbeat()
		<-entered
		fx.clk.Advance(1 * time.Second)
		e := terminalError(t, rd)
		if e.Kind != llm.WireKindUpstream || e.Cause != llm.WireCauseStall || e.Emitted {
			t.Fatalf("error: %+v", e)
		}
		if !cancelled.Load() {
			t.Fatal("the upstream call was not cancelled")
		}
	})
	t.Run("after a chunk", func(t *testing.T) {
		fx := newSharedFixture(t, withFakeClock(), withSharedConfig(short))
		fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
			on(llm.StreamChunk{TextDelta: "partial"})
			<-ctx.Done()
			return &llm.Response{Content: "partial"}, ctx.Err()
		}
		rd := newSSEReader(t, fx.complete(wireReq(sharedTestAlias)))
		rd.expectHeartbeat()
		if ev, _ := rd.next(5 * time.Second); ev.typ != llm.WireTypeChunk {
			t.Fatalf("want a chunk, got %+v", ev)
		}
		fx.clk.Advance(1 * time.Second)
		e := terminalError(t, rd)
		if e.Kind != llm.WireKindUpstream || e.Cause != llm.WireCauseStall || !e.Emitted {
			t.Fatalf("error: %+v", e)
		}
	})
	t.Run("a chunk re-arms the guard", func(t *testing.T) {
		fx := newSharedFixture(t, withFakeClock(), withSharedConfig(short))
		step := make(chan struct{})
		fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
			for i := 0; i < 3; i++ {
				select {
				case <-step:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				on(llm.StreamChunk{TextDelta: "."})
			}
			return &llm.Response{Content: "..."}, nil
		}
		rd := newSSEReader(t, fx.complete(wireReq(sharedTestAlias)))
		rd.expectHeartbeat()
		for i := 0; i < 3; i++ {
			fx.clk.Advance(900 * time.Millisecond)
			step <- struct{}{}
			if ev, _ := rd.next(5 * time.Second); ev.typ != llm.WireTypeChunk {
				t.Fatalf("want a chunk, got %+v", ev)
			}
		}
		if ev, _ := rd.next(5 * time.Second); ev.typ != llm.WireTypeFinal {
			t.Fatalf("a call that made progress every 900 ms was cut: %+v", ev)
		}
	})
	t.Run("a zero guard is off", func(t *testing.T) {
		off := func(c *config.Config) { z := 0; c.Agent.LLMStreamIdleTimeoutMS = &z }
		fx := newSharedFixture(t, withFakeClock(), withSharedConfig(off))
		finish := make(chan struct{})
		fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
			select {
			case <-finish:
			case <-ctx.Done():
			}
			return &llm.Response{Content: "ok"}, nil
		}
		rd := newSSEReader(t, fx.complete(wireReq(sharedTestAlias)))
		rd.expectHeartbeat()
		waitFor(t, "the heartbeat to arm", func() bool { return fx.clk.armed() >= 1 })
		if fx.clk.armed() != 1 {
			t.Fatalf("%d timers armed, want only the heartbeat", fx.clk.armed())
		}
		close(finish)
		rd.all()
	})
}

// terminalError reads until the terminal event and decodes it as an error.
func terminalError(t *testing.T, rd *sseReader) llm.WireError {
	t.Helper()
	for {
		ev, ok := rd.next(5 * time.Second)
		if !ok {
			t.Fatal("the stream ended without a terminal event")
		}
		switch ev.typ {
		case llm.WireTypeError:
			var e llm.WireError
			if err := json.Unmarshal(ev.raw, &e); err != nil {
				t.Fatal(err)
			}
			return e
		case llm.WireTypeFinal:
			t.Fatalf("want an error frame, got final: %s", ev.raw)
		}
	}
}

// The message of an error frame is built from the kind and the status: the
// provider's name, its address, the selector and the upstream model id - all of
// which the provider wraps its errors with and an upstream text can carry -
// never leave the host.
func TestSharedErrorMessagesNameNothingOfTheUpstream(t *testing.T) {
	type upstreamCase struct {
		name    string
		status  int
		header  map[string]string
		body    string
		budget  *int64
		retries *int
		kind    string
		cause   string
		message string
	}
	zero := 0
	one := int64(1000)
	cases := []upstreamCase{
		{name: "502", status: 502, body: `{"error":{"message":"model qwen3-secret-2026-01-15 of org-abc exploded"}}`, retries: &zero,
			kind: llm.WireKindUpstream, cause: llm.WireCauseStatus, message: "upstream provider returned 502"},
		{name: "401 is the remote's own credential, not the client's", status: 401, body: `{"error":{"message":"bad key sk-live-123 for org-abc"}}`, retries: &zero,
			kind: llm.WireKindUpstream, cause: llm.WireCauseStatus, message: "upstream provider returned 401"},
		{name: "400 is an invalid request", status: 400, body: `{"error":{"message":"qwen3-secret-2026-01-15 context is 131072 tokens, you sent 140000"}}`, retries: &zero,
			kind: llm.WireKindInvalid, message: "the upstream provider rejected the request (returned 400)"},
		{name: "429 with no pause is a rate error", status: 429, body: `{"error":{"message":"slow down org-abc"}}`, retries: &zero,
			kind: llm.WireKindRate, message: "the upstream provider is rate limiting this model (returned 429)"},
		{name: "429 that names a pause is a quota error with its reset", status: 429, header: map[string]string{"Retry-After": "7"}, body: `{"error":{"message":"slow down org-abc"}}`, retries: &zero,
			kind: llm.WireKindQuota, message: "the usage limit of the upstream provider is reached"},
		{name: "a long pause over the caller's budget is a quota error", status: 429, header: map[string]string{"Retry-After": "120"}, body: `{"error":{"message":"quota org-abc"}}`, budget: &one,
			kind: llm.WireKindQuota, message: "the usage limit of the upstream provider is reached"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				for k, v := range tc.header {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) {
				c.Providers[0].APIBase = upstream.URL + "/v1"
				c.Providers[0].Proxy = "none"
				if tc.retries != nil {
					c.Agent.LLMRetryMax = tc.retries
				}
			}))
			fx.srv.makeLLMFromYAML = defaultMakeLLMFromYAML

			started := time.Now()
			resp := fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Options.RetryBudgetMS = tc.budget }))
			evs := newSSEReader(t, resp).all()
			var raw strings.Builder
			for _, e := range evs {
				raw.Write(e.raw)
			}
			for _, leak := range []string{"stub", sharedTestUpstreamID, "qwen3", upstream.URL, "org-abc", "sk-live", "127.0.0.1", "131072", sharedTestSelector} {
				if strings.Contains(raw.String(), leak) {
					t.Fatalf("the stream mentions %q: %s", leak, raw.String())
				}
			}
			terms := terminalEvents(evs)
			if len(terms) != 1 || terms[0].typ != llm.WireTypeError {
				t.Fatalf("want one error frame: %+v", evs)
			}
			var e llm.WireError
			_ = json.Unmarshal(terms[0].raw, &e)
			if e.Kind != tc.kind || e.Cause != tc.cause || e.Message != tc.message || e.Emitted {
				t.Fatalf("error: %+v", e)
			}
			switch tc.kind {
			case llm.WireKindQuota:
				if e.RetryAfterS < 5 || e.ResetAt == "" {
					t.Fatalf("a quota error names its reset: %+v", e)
				}
				if took := time.Since(started); took > 3*time.Second {
					t.Fatalf("the quota pause was slept through (%v) instead of reported at once", took)
				}
			}
		})
	}
}

// classifySharedFailure maps what the provider layer says, case by case.
func TestClassifySharedFailureNeverReadsTheErrorText(t *testing.T) {
	text := errors.New(`provider "stub" (http://stub-upstream.invalid/v1): qwen3-secret-2026 org-abc`)
	cases := map[string]struct {
		err               error
		stalled, timedOut bool
		emitted           int
		kind, cause       string
	}{
		"plain failure":      {err: text, kind: llm.WireKindUpstream, cause: llm.WireCauseStatus},
		"guard fired":        {err: context.Canceled, stalled: true, emitted: 2, kind: llm.WireKindUpstream, cause: llm.WireCauseStall},
		"max_call_ms":        {err: context.DeadlineExceeded, timedOut: true, kind: llm.WireKindUpstream, cause: llm.WireCauseTimeout},
		"a provider timeout": {err: context.DeadlineExceeded, kind: llm.WireKindUpstream, cause: llm.WireCauseTimeout},
	}
	for name, tc := range cases {
		e := classifySharedFailure(tc.err, tc.stalled, tc.timedOut, tc.emitted)
		if e.Kind != tc.kind || e.Cause != tc.cause || e.Emitted != (tc.emitted > 0) {
			t.Errorf("%s: %+v", name, e)
		}
		for _, leak := range []string{"stub", "qwen3", "org-abc", "invalid"} {
			if strings.Contains(e.Message, leak) {
				t.Errorf("%s: the message mentions %q: %q", name, leak, e.Message)
			}
		}
	}
}

// Every way a call can end leaves the slot count at zero.
func TestSharedEveryExitPathReleasesTheSlot(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		fx := newSharedFixture(t)
		newSSEReader(t, fx.complete(wireReq(sharedTestAlias))).all()
		waitFor(t, "release", func() bool { return fx.srv.sharedLimit.tracked() == 0 })
	})
	t.Run("provider error", func(t *testing.T) {
		fx := newSharedFixture(t)
		fx.stub.run = func(context.Context, int, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
			return nil, errors.New("boom")
		}
		evs := newSSEReader(t, fx.complete(wireReq(sharedTestAlias))).all()
		if terms := terminalEvents(evs); len(terms) != 1 || terms[0].typ != llm.WireTypeError {
			t.Fatalf("events: %+v", evs)
		}
		waitFor(t, "release", func() bool { return fx.srv.sharedLimit.tracked() == 0 })
	})
	t.Run("provider setup error", func(t *testing.T) {
		fx := newSharedFixture(t)
		fx.srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) {
			return nil, errors.New(`provider "stub" has no credential`)
		}
		resp := fx.complete(wireReq(sharedTestAlias))
		raw := bodyString(t, resp)
		if resp.StatusCode != http.StatusBadGateway || strings.Contains(raw, "stub") || strings.Contains(raw, "credential") {
			t.Fatalf("status %d: %s", resp.StatusCode, raw)
		}
		waitFor(t, "release", func() bool { return fx.srv.sharedLimit.tracked() == 0 })
	})
	t.Run("client disconnect cancels the upstream call", func(t *testing.T) {
		fx := newSharedFixture(t)
		entered := make(chan struct{})
		cancelled := make(chan struct{})
		fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
			close(entered)
			<-ctx.Done()
			close(cancelled)
			return nil, ctx.Err()
		}
		resp := fx.complete(wireReq(sharedTestAlias))
		rd := newSSEReader(t, resp)
		rd.expectHeartbeat()
		<-entered
		_ = resp.Body.Close()
		select {
		case <-cancelled:
		case <-time.After(5 * time.Second):
			t.Fatal("the disconnect did not cancel the upstream call")
		}
		waitFor(t, "release", func() bool { return fx.srv.sharedLimit.tracked() == 0 })
	})
	t.Run("400 404 and 413 release when answered", func(t *testing.T) {
		fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) { c.HTTPServer.SharedModels.MaxStreams = 1 }))
		for i := 0; i < 3; i++ {
			readError(t, fx.post(llm.CoddyCompletionsPath, sharedTestSharedTok, []byte("{bad")))
			readError(t, fx.complete(wireReq("nope")))
		}
		conn, br := fx.rawConn(t)
		_, _ = io.WriteString(conn, rawHeaders(llm.CoddyCompletionsPath, sharedTestSharedTok, llm.CoddyMaxRequestBytes+1))
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("an oversized Content-Length answered %d, want 413 before any read", resp.StatusCode)
		}
		waitFor(t, "release", func() bool { return fx.srv.sharedLimit.tracked() == 0 })
		// And the single slot is takeable again.
		newSSEReader(t, fx.complete(wireReq(sharedTestAlias))).all()
	})
}

// A busy remote answers without reading the body: the request below promises a
// megabyte and sends none of it.
func TestSharedBusyAnswersWithoutReadingTheBody(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) { c.HTTPServer.SharedModels.MaxStreams = 1 }))
	hold := make(chan struct{})
	entered := make(chan struct{})
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
		close(entered)
		select {
		case <-hold:
		case <-ctx.Done():
		}
		return &llm.Response{Content: "x"}, nil
	}
	first := newSSEReader(t, fx.complete(wireReq(sharedTestAlias)))
	<-entered

	conn, br := fx.rawConn(t)
	_, _ = io.WriteString(conn, rawHeaders(llm.CoddyCompletionsPath, sharedTestSharedTok, 1<<20))
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("no answer while the body was withheld: %v", err)
	}
	var e llm.WireError
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &e)
	if resp.StatusCode != http.StatusTooManyRequests || e.Kind != llm.WireKindBusy || resp.Header.Get("Retry-After") != "1" {
		t.Fatalf("status %d retry-after %q: %s", resp.StatusCode, resp.Header.Get("Retry-After"), raw)
	}
	close(hold)
	first.all()
	if fx.stub.callCount() != 1 {
		t.Fatalf("the provider was called %d times, the busy call must not reach it", fx.stub.callCount())
	}
}

// A client that opens a call and then sends its body too slowly is cut by the
// body deadline P, which frees the slot.
func TestSharedSlowBodyIsReleasedByTheBodyDeadline(t *testing.T) {
	fx := newSharedFixture(t)
	fx.srv.sharedBodyP = 150 * time.Millisecond
	conn, br := fx.rawConn(t)
	_, _ = io.WriteString(conn, rawHeaders(llm.CoddyCompletionsPath, sharedTestSharedTok, 1000)+`{"protocol":1,`)
	waitFor(t, "the slot to be taken", func() bool { return fx.srv.sharedLimit.tracked() == 1 })
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("the stalled body was not answered: %v", err)
	}
	var e llm.WireError
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &e)
	if resp.StatusCode != http.StatusRequestTimeout || e.Kind != llm.WireKindInvalid || e.Code != "body_timeout" {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	waitFor(t, "release", func() bool { return fx.srv.sharedLimit.tracked() == 0 })
	if fx.buildCount() != 0 {
		t.Fatal("a provider was built for a body that never arrived")
	}
}

// A peer that stops reading is cut by the per-write deadline: the write fails,
// the upstream call is cancelled and the slot is released.
func TestSharedStalledWriterIsReleasedByTheWriteDeadline(t *testing.T) {
	fx := newSharedFixture(t)
	fx.srv.sharedWriteW = 200 * time.Millisecond
	cancelled := make(chan struct{})
	big := strings.Repeat("x", 256<<10)
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
		for i := 0; i < 600; i++ {
			if ctx.Err() != nil {
				close(cancelled)
				return nil, ctx.Err()
			}
			on(llm.StreamChunk{TextDelta: big})
		}
		return &llm.Response{Content: "never read"}, nil
	}
	conn, _ := fx.rawConn(t)
	raw, _ := json.Marshal(wireReq(sharedTestAlias))
	_, _ = io.WriteString(conn, rawHeaders(llm.CoddyCompletionsPath, sharedTestSharedTok, len(raw))+string(raw))
	// The client never reads a byte.
	select {
	case <-cancelled:
	case <-time.After(20 * time.Second):
		t.Fatal("a stalled writer did not cancel the upstream call")
	}
	waitFor(t, "release", func() bool { return fx.srv.sharedLimit.tracked() == 0 })
}

// The stream has exactly one terminal event, and nothing is written after it.
func TestSharedStreamWritesOneTerminalEvent(t *testing.T) {
	rec := httptest.NewRecorder()
	cancelled := false
	st := newSharedStream(rec, realSharedClock{}, 0, func() { cancelled = true })
	if err := st.start(); err != nil {
		t.Fatal(err)
	}
	if err := st.chunk(llm.StreamChunk{TextDelta: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := st.finish(llm.WireFinalFromResponse(&llm.Response{Content: "a"})); err != nil {
		t.Fatal(err)
	}
	// Late arrivals: a second terminal, a chunk, a heartbeat.
	_ = st.finish(llm.WireError{Kind: llm.WireKindUpstream})
	_ = st.chunk(llm.StreamChunk{TextDelta: "late"})
	if _, live := st.heartbeat(time.Nanosecond); live {
		t.Fatal("a heartbeat is still due after the terminal event")
	}
	body := rec.Body.String()
	if strings.Count(body, `"type":"final"`) != 1 || strings.Contains(body, `"type":"error"`) || strings.Contains(body, "late") {
		t.Fatalf("body: %q", body)
	}
	if !strings.HasPrefix(body, ": hb\n\n") || st.emittedChunks() != 1 || cancelled {
		t.Fatalf("body %q emitted %d cancelled %v", body, st.emittedChunks(), cancelled)
	}
}

// emitted counts the chunk frames that were written: a chunk whose write failed
// is not one, and the failure breaks the stream and cancels the upstream call.
func TestSharedStreamCountsOnlyWrittenChunksAndCancelsOnAFailedWrite(t *testing.T) {
	w := &failingWriter{header: http.Header{}, failAfter: 2}
	cancelled := 0
	st := newSharedStream(w, realSharedClock{}, 0, func() { cancelled++ })
	_ = st.start()
	if err := st.chunk(llm.StreamChunk{TextDelta: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := st.chunk(llm.StreamChunk{TextDelta: "b"}); err == nil {
		t.Fatal("a failed write was not reported")
	}
	_ = st.chunk(llm.StreamChunk{TextDelta: "c"})
	if st.emittedChunks() != 1 || cancelled != 1 || st.writeFailure() == nil {
		t.Fatalf("emitted %d cancelled %d failure %v", st.emittedChunks(), cancelled, st.writeFailure())
	}
	if err := st.finish(llm.WireFinalFromResponse(&llm.Response{})); err == nil {
		t.Fatal("a terminal frame was written on a broken stream")
	}
}

type failingWriter struct {
	header    http.Header
	writes    int
	failAfter int
}

func (f *failingWriter) Header() http.Header { return f.header }
func (f *failingWriter) WriteHeader(int)     {}
func (f *failingWriter) Write(b []byte) (int, error) {
	f.writes++
	if f.writes > f.failAfter {
		return 0, errors.New("write: broken pipe")
	}
	return len(b), nil
}

// The log of a call records the alias, the kind, the status, the duration and a
// request id - never content - and an upstream text only at debug level.
func TestSharedLogRecordsTheCallAndNeverItsContent(t *testing.T) {
	sink := &lockedBuffer{}
	fx := newSharedFixture(t, withSharedLogger(slog.New(slog.NewTextHandler(sink, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	const prompt = "TOP-SECRET-PROMPT-CONTENT"
	const upstreamText = "UPSTREAM-RAW-TEXT-WITH-ORG-ID"
	fx.stub.run = func(_ context.Context, call int, _ []llm.Message, _ []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
		if call == 1 {
			on(llm.StreamChunk{TextDelta: "TOP-SECRET-ANSWER"})
			return &llm.Response{Content: "TOP-SECRET-ANSWER"}, nil
		}
		return nil, errors.New(upstreamText)
	}
	for i := 0; i < 2; i++ {
		resp := fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) {
			r.Messages = []llm.WireMessage{{Role: "user", Content: prompt}}
		}))
		reqID := resp.Header.Get("X-Coddy-Request-ID")
		if reqID == "" {
			t.Fatal("the response carries no request id")
		}
		newSSEReader(t, resp).all()
		waitFor(t, "the call to be logged", func() bool { return strings.Contains(sink.String(), "request_id="+reqID) })
	}
	logs := sink.String()
	for _, leak := range []string{prompt, "TOP-SECRET-ANSWER"} {
		if strings.Contains(logs, leak) {
			t.Fatalf("the log carries content %q:\n%s", leak, logs)
		}
	}
	var warn, info, debug string
	for _, line := range strings.Split(logs, "\n") {
		switch {
		case strings.Contains(line, "level=INFO") && strings.Contains(line, "shared model call"):
			info = line
		case strings.Contains(line, "level=WARN") && strings.Contains(line, "shared model call"):
			warn = line
		case strings.Contains(line, "level=DEBUG"):
			debug = line
		}
	}
	for what, line := range map[string]string{"info": info, "warn": warn} {
		for _, field := range []string{"alias=coder", "kind=", "status=", "duration_ms=", "request_id="} {
			if !strings.Contains(line, field) {
				t.Errorf("the %s line lacks %s: %q", what, field, line)
			}
		}
	}
	if strings.Contains(info, upstreamText) || strings.Contains(warn, upstreamText) {
		t.Fatalf("an upstream text reached a line above debug level:\n%s\n%s", info, warn)
	}
	if !strings.Contains(debug, upstreamText) {
		t.Fatalf("the upstream text is not in the debug line: %q", debug)
	}
	if strings.Contains(warn, sharedTestSelector) || strings.Contains(info, sharedTestSelector) {
		t.Fatal("a log line names the selector")
	}
}
