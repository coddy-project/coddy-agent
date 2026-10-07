package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderEndpointOfACoddyRow(t *testing.T) {
	for in, want := range map[string]string{
		"https://relay.example/swarm/nodes/n1": "https://relay.example/swarm/nodes/n1",
		"  https://remote.example:8443/  ":     "https://remote.example:8443",
		"https://user:secret@remote.example/x": "https://remote.example/x",
		"":                                     "",
	} {
		if got := ProviderEndpoint("coddy", in); got != want {
			t.Errorf("ProviderEndpoint(coddy, %q) = %q, want %q", in, got, want)
		}
	}
	if strings.Contains(ProviderEndpoint("coddy", "https://user:secret@remote.example"), "secret") {
		t.Fatal("a credential in api_base reaches an error label")
	}
}

func TestCoddyErrorsNameTheProviderAndTheAddress(t *testing.T) {
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		answerWire(w, http.StatusBadRequest, WireError{Status: 400, Kind: WireKindInvalid, Message: "the model is not shared"})
	})
	got := streamOnce(newTestCoddy(t, coddyInput(remote)), userMsg("Hi"), nil)
	if got.err == nil || !strings.Contains(got.err.Error(), `provider "remote" (`+remote.url()+`)`) || !strings.Contains(got.err.Error(), "the model is not shared") {
		t.Fatalf("err %v", got.err)
	}
	if strings.Contains(got.err.Error(), "shared-token") {
		t.Fatal("the credential reaches an error")
	}
}

func TestRequestOptionsValidateAcceptsEverythingForACoddyRow(t *testing.T) {
	zero, neg, hot := 0, -3, 7.5
	for _, o := range []RequestOptions{
		{},
		{MaxTokens: &zero},
		{MaxTokens: &neg, Temperature: &hot, ReasoningEffort: "high"},
		{ReasoningEffort: "off"},
	} {
		if err := o.Validate("coddy"); err != nil {
			t.Errorf("the remote validates, not the client: %+v -> %v", o, err)
		}
	}
	// The other types keep their own rules.
	if err := (RequestOptions{MaxTokens: &zero}).Validate("openai"); err == nil {
		t.Error("openai still refuses max_tokens 0")
	}
}

func TestNewProviderBuildsACoddyRowAsAStreamWhateverDisableStreamSays(t *testing.T) {
	var accepts []string
	remote := newFakeRemote(t, func(w http.ResponseWriter, r *http.Request, n int, req WireRequest) {
		accepts = append(accepts, r.Header.Get("Accept"))
		simpleAnswer("streamed")(w, r, n, req)
	})
	in := coddyInput(remote)
	in.DisableStream = true
	p := newTestCoddy(t, in)
	var chunks int
	resp, err := p.Stream(context.Background(), userMsg("Hi"), nil, func(StreamChunk) { chunks++ })
	if err != nil || resp.Content != "streamed" {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	// A blocking wrapper would replay the finished answer as one chunk per part
	// after a Complete call; the wire here is a stream and its chunks are live.
	if chunks != 1 {
		t.Fatalf("chunks %d", chunks)
	}
	resp, err = p.Complete(context.Background(), userMsg("Hi"), nil)
	if err != nil || resp.Content != "streamed" {
		t.Fatalf("complete %+v err %v", resp, err)
	}
	for _, a := range accepts {
		if a != "text/event-stream" {
			t.Fatalf("Accept %q", a)
		}
	}
}

func TestNewProviderPassesTheStreamIdleTimeoutToACoddyRowWhateverDisableStreamSays(t *testing.T) {
	// A silent stream of a row with stream: false is cut by the guard the
	// HTTP client of NewProvider carries.
	remote := newFakeRemote(t, func(w http.ResponseWriter, r *http.Request, _ int, _ WireRequest) {
		startStream(w).heartbeat()
		<-r.Context().Done()
	})
	in := coddyInput(remote)
	in.DisableStream, in.StreamIdleTimeout = true, 80*time.Millisecond
	in.RetryDisabled = true
	if got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil); !IsStreamStalled(got.err) {
		t.Fatalf("err %v", got.err)
	}
}

func TestNewProviderKnowsTheCoddyType(t *testing.T) {
	_, err := NewProvider(ProviderInput{Type: "coddy", BaseURL: "https://remote.example", ProxyURL: "none"})
	if err != nil {
		t.Fatalf("NewProvider(coddy): %v", err)
	}
}

// providers[].proxy applies to every request a coddy row makes: the completion
// and the listing go to the row's proxy and never straight to the remote.
func TestCoddyRowsHonourTheirProxy(t *testing.T) {
	var direct atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		direct.Add(1)
		http.Error(w, "reached directly", http.StatusTeapot)
	}))
	defer remote.Close()
	var viaProxy atomic.Int32
	var seen []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		viaProxy.Add(1)
		seen = append(seen, r.Method+" "+r.URL.String())
		_, _ = io.Copy(io.Discard, r.Body)
		http.Error(w, "proxy answered", http.StatusServiceUnavailable)
	}))
	defer proxy.Close()

	in := ProviderInput{Name: "remote", Type: "coddy", Model: "coder", BaseURL: remote.URL + "/swarm/nodes/n1", ProxyURL: proxy.URL,
		RetryDisabled: true}
	p, err := NewProvider(in)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = p.Stream(context.Background(), userMsg("Hi"), nil, nil)
	_, _ = ListModels(context.Background(), in)

	if direct.Load() != 0 {
		t.Fatalf("%d requests went straight to the remote around the row's proxy", direct.Load())
	}
	if viaProxy.Load() != 2 {
		t.Fatalf("%d requests went through the proxy, want the completion and the listing: %v", viaProxy.Load(), seen)
	}
	if !strings.Contains(seen[0], "/swarm/nodes/n1/coddy/llm/completions") || !strings.Contains(seen[1], "/swarm/nodes/n1/coddy/llm/models") {
		t.Fatalf("proxied requests: %v", seen)
	}
}

// Expect: 100-continue needs the transport to wait for the remote's verdict
// before it sends the body; the shared provider transport is a clone of the
// default one, which carries that timeout.
func TestProviderTransportHoldsTheBodyBackForExpectContinue(t *testing.T) {
	for _, setting := range []string{"", "none", "http://127.0.0.1:3128"} {
		rt, err := providerTransport(setting)
		if err != nil {
			t.Fatal(err)
		}
		tr, ok := rt.(*http.Transport)
		if !ok || tr.ExpectContinueTimeout <= 0 {
			t.Fatalf("setting %q: transport %T has no ExpectContinueTimeout: a refused call would upload the whole history", setting, rt)
		}
	}
}
