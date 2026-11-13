//go:build http

package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// directStubServer serves p as every direct model over the real handlers and a
// real file store.
func directStubServer(t *testing.T, p llm.Provider) (*Server, *httptest.Server, string) {
	t.Helper()
	_, srv, sessRoot := testHTTPServerPersist(t)
	srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) { return p, nil }
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts, sessRoot
}

func assertStoreEmpty(t *testing.T, sessRoot string) {
	t.Helper()
	entries, err := os.ReadDir(sessRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the store holds %d entries, first %q", len(entries), entries[0].Name())
	}
}

func postDirect(t *testing.T, url, body string, headers map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, string(raw)
}

// A picture has no bundle to be kept in: it reaches the model from the request
// and is not written anywhere.
func TestDirectWithoutSessionSendsPicturesAndWritesNone(t *testing.T) {
	const picture = "data:image/png;base64,iVBORw0KGgo="
	for name, tc := range map[string]struct{ path, body string }{
		"chat": {"/v1/chat/completions", `{"model":"openai/gpt-4o","messages":[{"role":"user","content":[
			{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"` + picture + `"}}]}]}`},
		"responses": {"/v1/responses", `{"model":"openai/gpt-4o","input":"look",
			"inline_files":[{"name":"img.png","data_url":"` + picture + `"}]}`},
	} {
		t.Run(name, func(t *testing.T) {
			p := &directRecordingProvider{}
			srv, ts, sessRoot := directStubServer(t, p)
			srv.activeCfg().Models[0].Multimodal = true
			res, raw := postDirect(t, ts.URL+tc.path, tc.body, nil)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status %d: %s", res.StatusCode, raw)
			}
			last := p.lastCall()
			if len(last) != 1 || len(last[0].ImageParts) != 1 || last[0].ImageParts[0].DataURL != picture {
				t.Fatalf("the model received %+v", last)
			}
			assertStoreEmpty(t, sessRoot)

			// The same request kept in a session writes the picture, so an
			// empty store above is the absence of a bundle and not of a write.
			res, raw = postDirect(t, ts.URL+tc.path, tc.body, map[string]string{"X-Coddy-Session-ID": "sess_kept_picture"})
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status %d: %s", res.StatusCode, raw)
			}
			if _, err := os.Stat(session.AssetsPath((&session.FileStore{Root: sessRoot}).SessionPath("sess_kept_picture"))); err != nil {
				t.Fatalf("a kept session has no assets: %v", err)
			}
		})
	}
}

type failingDirectProvider struct{ err error }

func (p failingDirectProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, p.err
}

func (p failingDirectProvider) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
	return nil, p.err
}

// The error path records the failure on the state; with no bundle that is
// nothing on disk, and the client still gets the error.
func TestDirectWithoutSessionFailureLeavesNothing(t *testing.T) {
	_, ts, sessRoot := directStubServer(t, failingDirectProvider{err: errors.New("the provider refused")})
	for path, input := range map[string]string{
		"/v1/chat/completions": `"messages":[{"role":"user","content":"hi"}]`,
		"/v1/responses":        `"input":"hi"`,
	} {
		for _, stream := range []bool{false, true} {
			res, raw := postDirect(t, ts.URL+path, fmt.Sprintf(`{"model":"openai/gpt-4o","stream":%v,%s}`, stream, input), nil)
			if !strings.Contains(raw, `"error"`) && !strings.Contains(raw, "response.failed") {
				t.Fatalf("%s stream %v: status %d, no error in %s", path, stream, res.StatusCode, raw)
			}
			if sid := res.Header.Get("X-Coddy-Session-ID"); sid != "" {
				t.Fatalf("%s stream %v: named session %s", path, stream, sid)
			}
		}
	}
	assertStoreEmpty(t, sessRoot)
}

// barrierProvider answers each call with the text it was asked, once parallel
// calls are inside at the same time.
type barrierProvider struct {
	parallel int
	mu       sync.Mutex
	arrived  int
	all      chan struct{}
}

func (p *barrierProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	p.mu.Lock()
	p.arrived++
	if p.arrived == p.parallel {
		close(p.all)
	}
	p.mu.Unlock()
	select {
	case <-p.all:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Second):
		return nil, errors.New("the calls did not run side by side")
	}
	return &llm.Response{Content: msgs[len(msgs)-1].Content, StopReason: "end_turn"}, nil
}

func (p *barrierProvider) Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
	return p.Complete(ctx, msgs, tools)
}

// Calls without the header share nothing: they run side by side, none is
// refused as busy, and each gets the answer to its own question.
func TestDirectWithoutSessionCallsRunSideBySide(t *testing.T) {
	const parallel = 4
	p := &barrierProvider{parallel: parallel, all: make(chan struct{})}
	_, ts, sessRoot := directStubServer(t, p)
	var wg sync.WaitGroup
	errs := make(chan error, parallel)
	for i := range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			question := fmt.Sprintf("question-%d", i)
			res, err := http.Post(ts.URL+"/v1/chat/completions", "application/json",
				strings.NewReader(`{"model":"openai/gpt-4o","messages":[{"role":"user","content":"`+question+`"}]}`))
			if err != nil {
				errs <- err
				return
			}
			raw, err := ioReadAllClose(res.Body)
			if err != nil {
				errs <- err
				return
			}
			if got := gjson.GetBytes(raw, "choices.0.message.content").String(); res.StatusCode != http.StatusOK || got != question {
				errs <- fmt.Errorf("%s: status %d, answer %q", question, res.StatusCode, got)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	assertStoreEmpty(t, sessRoot)
}

// untilCancelledProvider waits for the call's context to end, the only way a
// stateless call can be stopped.
type untilCancelledProvider struct {
	entered chan struct{}
	ended   chan error
}

func (p *untilCancelledProvider) wait(ctx context.Context) (*llm.Response, error) {
	close(p.entered)
	select {
	case <-ctx.Done():
		p.ended <- ctx.Err()
		return nil, ctx.Err()
	case <-time.After(10 * time.Second):
		p.ended <- errors.New("still running 10 s after the client left")
		return nil, errors.New("timeout")
	}
}

func (p *untilCancelledProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	return p.wait(ctx)
}

func (p *untilCancelledProvider) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
	return p.wait(ctx)
}

// With no id to cancel by, a client stops a stateless call by closing the
// connection, and the provider's request ends with it.
func TestDirectWithoutSessionStopsWhenTheClientLeaves(t *testing.T) {
	for path, input := range map[string]string{
		"/v1/chat/completions": `"messages":[{"role":"user","content":"hi"}]`,
		"/v1/responses":        `"input":"hi"`,
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s stream %v", path, stream), func(t *testing.T) {
				p := &untilCancelledProvider{entered: make(chan struct{}), ended: make(chan error, 1)}
				_, ts, sessRoot := directStubServer(t, p)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+path,
					strings.NewReader(fmt.Sprintf(`{"model":"openai/gpt-4o","stream":%v,%s}`, stream, input)))
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan struct{})
				// Only wait closes entered, so a regression that answers the
				// request before the provider is reached would hang the whole
				// package until its test timeout. Every wait below is bounded
				// and names what did not happen.
				refused := make(chan error, 1)
				go func() {
					defer close(done)
					res, err := http.DefaultClient.Do(req)
					if err != nil {
						refused <- err
						return
					}
					_ = res.Body.Close()
				}()
				select {
				case <-p.entered:
				case err := <-refused:
					t.Fatalf("the request ended before the provider was called: %v", err)
				case <-time.After(10 * time.Second):
					t.Fatal("the provider was never called")
				}
				cancel()
				select {
				case err := <-p.ended:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("the provider's call ended with %v", err)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("the provider's call did not end after the client left")
				}
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Fatal("the request did not end after the client left")
				}
				assertStoreEmpty(t, sessRoot)
			})
		}
	}
}

func TestDirectWithoutSessionOriginHeader(t *testing.T) {
	_, ts, sessRoot := directStubServer(t, &directRecordingProvider{})
	const body = `{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`

	// A print run on a direct model has no session to mark.
	res, raw := postDirect(t, ts.URL+"/v1/chat/completions", body, map[string]string{session.OriginHeader: session.PrintOrigin})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	assertStoreEmpty(t, sessRoot)

	// Any other value is refused here as it is with a session.
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		invalid := body
		if path == "/v1/responses" {
			invalid = `{"model":"openai/gpt-4o","input":"hi"}`
		}
		res, raw = postDirect(t, ts.URL+path, invalid, map[string]string{session.OriginHeader: "telegram"})
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400: %s", path, res.StatusCode, raw)
		}
	}
	assertStoreEmpty(t, sessRoot)
}

// A header is still read: a malformed one is an error and does not fall through
// to a stateless call.
func TestDirectMalformedSessionHeaderIsRefused(t *testing.T) {
	p := &directRecordingProvider{}
	_, ts, sessRoot := directStubServer(t, p)
	res, raw := postDirect(t, ts.URL+"/v1/chat/completions",
		`{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`,
		map[string]string{"X-Coddy-Session-ID": "../outside"})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", res.StatusCode, raw)
	}
	if p.lastCall() != nil {
		t.Fatal("a refused request reached the model")
	}
	assertStoreEmpty(t, sessRoot)
}
