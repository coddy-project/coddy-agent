//go:build http

package httpserver

// The window limit of a shared-model credential, beside the stream slot
// (docs/plans/remote-model-provider-phase3.md, 5.3).

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func withRate(perMinute, burst, streams int) sharedFixtureOption {
	return withSharedConfig(func(c *config.Config) {
		c.HTTPServer.SharedModels.RatePerMinute = perMinute
		c.HTTPServer.SharedModels.RateBurst = burst
		if streams > 0 {
			c.HTTPServer.SharedModels.MaxStreams = streams
		}
	})
}

func finish(t *testing.T, resp *http.Response) {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, bodyString(t, resp))
	}
	newSSEReader(t, resp).all()
}

func TestSharedWindowRefusesTheCallPastTheBurst(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock(), withRate(60, 2, 10))
	for i := 0; i < 2; i++ {
		finish(t, fx.complete(wireReq(sharedTestAlias)))
	}
	built := fx.buildCount()
	resp := fx.complete(wireReq(sharedTestAlias))
	e := readError(t, resp)
	if resp.StatusCode != http.StatusTooManyRequests || e.Kind != llm.WireKindBusy || e.Code != llm.WireCodeRateWindow {
		t.Fatalf("status %d: %+v", resp.StatusCode, e)
	}
	// The wait to the next token is one second at 60 a minute, and the header and
	// the field agree.
	if got := resp.Header.Get("Retry-After"); got != "1" || e.RetryAfterS != 1 {
		t.Fatalf("Retry-After %q retry_after_s %v", got, e.RetryAfterS)
	}
	if fx.buildCount() != built {
		t.Fatal("a refused call reached the provider factory")
	}
	// The refused call gave its slot back.
	waitFor(t, "slot released", func() bool { return fx.srv.sharedLimit.tracked() == 0 })

	fx.clk.Advance(time.Second)
	finish(t, fx.complete(wireReq(sharedTestAlias)))
}

func TestSharedWindowRetryAfterIsTheWaitToTheNextToken(t *testing.T) {
	// Two calls a minute: a token every thirty seconds.
	fx := newSharedFixture(t, withFakeClock(), withRate(2, 1, 10))
	finish(t, fx.complete(wireReq(sharedTestAlias)))
	fx.clk.Advance(10 * time.Second)
	resp := fx.complete(wireReq(sharedTestAlias))
	e := readError(t, resp)
	if e.Code != llm.WireCodeRateWindow {
		t.Fatalf("%+v", e)
	}
	if got, want := resp.Header.Get("Retry-After"), "20"; got != want || e.RetryAfterS != 20 {
		t.Fatalf("Retry-After %q retry_after_s %v, want %s", got, e.RetryAfterS, want)
	}
}

func TestSharedWindowIsOffUntilARateIsSet(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock())
	for i := 0; i < 20; i++ {
		finish(t, fx.complete(wireReq(sharedTestAlias)))
	}
}

func TestSharedWindowIsPerCredential(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock(), withRate(60, 1, 10))
	finish(t, fx.complete(wireReq(sharedTestAlias)))
	if e := readError(t, fx.complete(wireReq(sharedTestAlias))); e.Code != llm.WireCodeRateWindow {
		t.Fatalf("%+v", e)
	}
	finish(t, fx.post(llm.CoddyCompletionsPath, sharedTestMainToken, wireReq(sharedTestAlias)))
}

// A call refused for a full slot spends no token, so a client that waits out
// busy at one request a second does not drain the window by waiting.
func TestSharedBusyRefusalSpendsNoToken(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock(), withRate(60, 2, 1))
	hold := make(chan struct{})
	entered := make(chan struct{}, 4)
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
	for i := 0; i < 8; i++ {
		resp := fx.complete(wireReq(sharedTestAlias))
		e := readError(t, resp)
		if resp.StatusCode != http.StatusTooManyRequests || e.Kind != llm.WireKindBusy || e.Code != "" {
			t.Fatalf("attempt %d: %d %+v, want a plain busy for the full slot", i, resp.StatusCode, e)
		}
	}
	close(hold)
	first.all()
	waitFor(t, "slot released", func() bool { return fx.srv.sharedLimit.tracked() == 0 })

	// One token was spent by the call that ran: one is left, then the window.
	fx.stub.run = nil
	finish(t, fx.complete(wireReq(sharedTestAlias)))
	if e := readError(t, fx.complete(wireReq(sharedTestAlias))); e.Code != llm.WireCodeRateWindow {
		t.Fatalf("the window did not close after the second admitted call: %+v", e)
	}
}

// The body is not read for a window refusal, as for a busy one.
func TestSharedWindowRefusalDoesNotReadTheBody(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock(), withRate(60, 1, 10))
	finish(t, fx.complete(wireReq(sharedTestAlias)))

	body := &countingReader{r: bytes.NewReader(bytes.Repeat([]byte("x"), 1<<20))}
	req := httptest.NewRequest(http.MethodPost, llm.CoddyCompletionsPath, body)
	req.Header.Set("Authorization", "Bearer "+sharedTestSharedTok)
	req.ContentLength = 1 << 20
	rec := httptest.NewRecorder()
	fx.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if body.n != 0 {
		t.Fatalf("%d bytes of the body were read for a refused call", body.n)
	}
	if got := rec.Header().Get("Retry-After"); got != strconv.Itoa(1) {
		t.Fatalf("Retry-After %q", got)
	}
}

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// The limiter's keys exist only after authentication, and are the digest of the
// credential that passed the gate.
func TestSharedWindowKeysAreDigestsOfAdmittedCredentials(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock(), withRate(60, 5, 10))
	finish(t, fx.complete(wireReq(sharedTestAlias)))
	// A bearer the gate refuses never reaches the handler, so it makes no key.
	resp := fx.post(llm.CoddyCompletionsPath, "made-up-token", wireReq(sharedTestAlias))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if n := fx.srv.sharedRate.Len(); n != 1 {
		t.Fatalf("the window holds %d keys, want one for the credential that passed", n)
	}
}
