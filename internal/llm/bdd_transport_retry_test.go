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
	"errors"
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
	// dials counts client-side connections for the scenarios that own the
	// dialer instead of the listener.
	dials atomic.Int32
	// hole opens the black hole on the first connection once the handler
	// has the request; release lets that handler return at cleanup.
	hole    chan struct{}
	release chan struct{}
	resp    *Response
	callErr error
}

func (s *transportRetryState) reset() {
	s.cleanup()
	s.provider = nil
	s.listener = nil
	s.requests.Store(0)
	s.dials.Store(0)
	s.hole = nil
	s.release = nil
	s.resp = nil
	s.callErr = nil
}

// connections is how many upstream connections the scenario saw: the
// listener's count when the scenario wraps the listener, the dialer's
// otherwise.
func (s *transportRetryState) connections() int {
	if s.listener != nil {
		return int(s.listener.accepted.Load())
	}
	return int(s.dials.Load())
}

// blackholeConn is a net.Conn that, once the hole opens, swallows every
// write and blocks every read until it is closed: what a client sees when
// the far side of a tunnel died without a FIN or a RST. Before that it is
// the plain connection, so the TLS handshake and the request go through.
type blackholeConn struct {
	net.Conn
	hole   <-chan struct{}
	closed chan struct{}
	once   sync.Once
}

func (c *blackholeConn) holed() bool {
	select {
	case <-c.hole:
		return true
	default:
		return false
	}
}

func (c *blackholeConn) Read(p []byte) (int, error) {
	for {
		if c.holed() {
			<-c.closed
			return 0, net.ErrClosed
		}
		// Short read deadlines, so the hole is noticed while a read is
		// pending; a deadline that fires is not an error of the connection.
		_ = c.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
		n, err := c.Conn.Read(p)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return n, err
		}
		return n, nil
	}
}

func (c *blackholeConn) Write(p []byte) (int, error) {
	if c.holed() {
		return len(p), nil
	}
	return c.Conn.Write(p)
}

func (c *blackholeConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
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
	if s.release != nil {
		close(s.release)
		s.release = nil
	}
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

// aProviderWhoseUpstreamResetsFirstH2Stream runs the provider over HTTP/2
// against a server whose first handler aborts before writing anything, which
// the HTTP/2 server answers with RST_STREAM(INTERNAL_ERROR): the client sees
// the very text net/http prints for a stream the peer killed, "stream
// error: stream ID 1; INTERNAL_ERROR; received from peer", with no HTTP
// status and no byte of the response behind it.
func (s *transportRetryState) aProviderWhoseUpstreamResetsFirstH2Stream() error {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			http.Error(w, "the scenario needs HTTP/2, got "+r.Proto, http.StatusHTTPVersionNotSupported)
			return
		}
		if s.requests.Add(1) == 1 {
			panic(http.ErrAbortHandler)
		}
		s.streamCompletion(w)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	s.server = srv

	inner := newOpenAIProvider("test-model", "test-key", srv.URL, srv.Client(), 0, 0, "")
	s.provider = WrapResilient(inner, ResilientOptions{
		RetryMax:      1,
		RetryBase:     time.Millisecond,
		RetryMaxDelay: time.Millisecond,
	})
	return nil
}

// aProviderWhoseUpstreamGoesSilentOnFirstConnection runs the provider over
// HTTP/2 with the liveness settings the LLM transports carry, through a
// dialer whose first connection turns into a black hole once the server has
// the request: nothing comes back, the liveness ping included. The
// transport has to notice on its own, close that connection and let the
// resilient wrapper repeat the request over a fresh one.
func (s *transportRetryState) aProviderWhoseUpstreamGoesSilentOnFirstConnection() error {
	s.hole = make(chan struct{})
	s.release = make(chan struct{})
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			http.Error(w, "the scenario needs HTTP/2, got "+r.Proto, http.StatusHTTPVersionNotSupported)
			return
		}
		if s.requests.Add(1) == 1 {
			// The request is in: from here on the client hears nothing.
			close(s.hole)
			<-s.release
			return
		}
		s.streamCompletion(w)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	s.server = srv

	client := srv.Client()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		return fmt.Errorf("test server client transport is %T, want *http.Transport", client.Transport)
	}
	dialer := &net.Dialer{}
	hole := s.hole
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		if s.dials.Add(1) == 1 {
			return &blackholeConn{Conn: c, hole: hole, closed: make(chan struct{})}, nil
		}
		return c, nil
	}
	// The production values are 30 s and 15 s; the scenario only needs the
	// mechanism, not the wait.
	if err := enableHTTP2Liveness(transport, 100*time.Millisecond, 100*time.Millisecond); err != nil {
		return err
	}

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
	if got := s.connections(); got != conns {
		return fmt.Errorf("upstream connections = %d, want %d", got, conns)
	}
	return nil
}

func (s *transportRetryState) requestsReachedTheHandler(n int) error {
	if got := int(s.requests.Load()); got != n {
		return fmt.Errorf("requests served = %d, want %d", got, n)
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
	sc.Step(`^an "openai" provider whose upstream resets the first HTTP/2 stream before any output and then streams a completion$`, s.aProviderWhoseUpstreamResetsFirstH2Stream)
	sc.Step(`^an "openai" provider whose upstream goes silent on the first connection, pings included, and then streams a completion$`, s.aProviderWhoseUpstreamGoesSilentOnFirstConnection)
	sc.Step(`^(\d+) requests? reached the upstream handler$`, s.requestsReachedTheHandler)
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
