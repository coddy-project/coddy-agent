package llm

// Godog harness for features/llm_transport_retry.feature: exercises the real
// OpenAI provider against a stub upstream whose first response dies at the
// transport level before any SSE output (a declared Content-Length with an
// empty body yields io.ErrUnexpectedEOF on the client), and whose second
// response streams a normal completion. The resilient wrapper must classify
// the status-less transport failure as retryable and repeat the request.
//
// The handshake scenario runs the same provider over TLS against a listener
// that swallows the first connection: the ClientHello is never answered, the
// transport's TLS handshake timer fires with "net/http: TLS handshake
// timeout", and the request itself never reaches the server.

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

type transportRetryState struct {
	server   *httptest.Server
	listener *stallFirstConnListener
	provider Provider
	requests atomic.Int32
	resp     *Response
	callErr  error
}

func (s *transportRetryState) reset() {
	s.cleanup()
	s.provider = nil
	s.listener = nil
	s.requests.Store(0)
	s.resp = nil
	s.callErr = nil
}

// stallFirstConnListener accepts every connection but keeps the first one
// open and silent instead of handing it to the server, so a TLS client on it
// waits for a ServerHello that never comes.
type stallFirstConnListener struct {
	net.Listener
	accepted atomic.Int32
	mu       sync.Mutex
	held     []net.Conn
}

func (l *stallFirstConnListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.accepted.Add(1) == 1 {
			l.mu.Lock()
			l.held = append(l.held, c)
			l.mu.Unlock()
			continue
		}
		return c, nil
	}
}

func (l *stallFirstConnListener) Close() error {
	l.mu.Lock()
	for _, c := range l.held {
		_ = c.Close()
	}
	l.held = nil
	l.mu.Unlock()
	return l.Listener.Close()
}

func (s *transportRetryState) cleanup() {
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
}

func (s *transportRetryState) aProviderWhoseUpstreamCutsOnce() error {
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if s.requests.Add(1) == 1 {
			// Promise a body and send none: the client reads an unexpected
			// EOF with no HTTP status attached to the failure.
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Content-Length", "1000")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w,
			"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"content\":\"Hello after retry\"}}],\"id\":\"chatcmpl-r1\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n"+
				"data: {\"choices\":[{\"finish_reason\":\"stop\",\"index\":0,\"delta\":{}}],\"id\":\"chatcmpl-r1\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n"+
				"data: [DONE]\n\n")
	}))
	provider, err := NewProvider(ProviderInput{
		Type:          "openai",
		Model:         "test-model",
		BaseURL:       s.server.URL,
		RetryMax:      1,
		RetryBase:     time.Millisecond,
		RetryMaxDelay: time.Millisecond,
	})
	if err != nil {
		return fmt.Errorf("create openai provider: %w", err)
	}
	s.provider = provider
	return nil
}

func (s *transportRetryState) streamCompletion(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w,
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"content\":\"Hello after retry\"}}],\"id\":\"chatcmpl-r2\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n"+
			"data: {\"choices\":[{\"finish_reason\":\"stop\",\"index\":0,\"delta\":{}}],\"id\":\"chatcmpl-r2\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n"+
			"data: [DONE]\n\n")
}

func (s *transportRetryState) aProviderWhoseFirstTLSHandshakeStalls() error {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.requests.Add(1)
		s.streamCompletion(w)
	}))
	s.listener = &stallFirstConnListener{Listener: srv.Listener}
	srv.Listener = s.listener
	srv.StartTLS()
	s.server = srv

	client := srv.Client()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		return fmt.Errorf("test server client transport is %T, want *http.Transport", client.Transport)
	}
	// The default is 10s; a short timer keeps the scenario fast while the
	// failure stays the very one a stalled network produces.
	transport.TLSHandshakeTimeout = 200 * time.Millisecond

	inner := newOpenAIProvider("test-model", "test-key", srv.URL, client, 0, 0, "")
	s.provider = WrapResilient(inner, ResilientOptions{
		RetryMax:      1,
		RetryBase:     time.Millisecond,
		RetryMaxDelay: time.Millisecond,
	})
	return nil
}

func (s *transportRetryState) theCallSucceedsWithTextOverConnections(want string, conns int) error {
	if s.callErr != nil {
		return fmt.Errorf("provider call failed: %v", s.callErr)
	}
	if s.resp == nil || s.resp.Content != want {
		return fmt.Errorf("content = %q, want %q", contentOf(s.resp), want)
	}
	if got := int(s.listener.accepted.Load()); got != conns {
		return fmt.Errorf("upstream connections = %d, want %d", got, conns)
	}
	if got := int(s.requests.Load()); got != 1 {
		return fmt.Errorf("requests served = %d, want 1: the stalled handshake must not reach the handler", got)
	}
	return nil
}

func (s *transportRetryState) aTransportRetryStreamingCompletionIsRequested() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s.resp, s.callErr = s.provider.Stream(ctx,
		[]Message{{Role: RoleUser, Content: "hello"}},
		nil,
		func(StreamChunk) {})
	return nil
}

func (s *transportRetryState) theCallSucceedsWithTextInRequests(want string, requests int) error {
	if s.callErr != nil {
		return fmt.Errorf("provider call failed: %v", s.callErr)
	}
	if s.resp == nil || s.resp.Content != want {
		return fmt.Errorf("content = %q, want %q", contentOf(s.resp), want)
	}
	if got := int(s.requests.Load()); got != requests {
		return fmt.Errorf("upstream requests = %d, want %d", got, requests)
	}
	return nil
}

func initializeTransportRetryScenario(sc *godog.ScenarioContext) {
	s := &transportRetryState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^an "openai" provider whose upstream cuts the connection once before any output and then streams a completion$`, s.aProviderWhoseUpstreamCutsOnce)
	sc.Step(`^an "openai" provider whose upstream leaves the first TLS handshake unanswered and then streams a completion$`, s.aProviderWhoseFirstTLSHandshakeStalls)
	sc.Step(`^a streaming completion is requested$`, s.aTransportRetryStreamingCompletionIsRequested)
	sc.Step(`^the call succeeds with text "([^"]*)" in (\d+) upstream requests$`, s.theCallSucceedsWithTextInRequests)
	sc.Step(`^the call succeeds with text "([^"]*)" over (\d+) upstream connections$`, s.theCallSucceedsWithTextOverConnections)
}

func TestLLMTransportRetryFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "llm-transport-retry",
		ScenarioInitializer: initializeTransportRetryScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/llm_transport_retry.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("LLM transport retry feature suite failed")
	}
}
