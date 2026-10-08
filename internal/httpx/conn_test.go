package httpx

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// tcpServer is a plain HTTP/1.1 server whose connections carry the hook.
func tcpServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ConnContext = ConnContext
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// tlsServer is a TLS server whose connections carry the hook; h2 says whether
// it offers HTTP/2 in ALPN.
func tlsServer(t *testing.T, h2 bool, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ConnContext = ConnContext
	srv.EnableHTTP2 = h2
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// h1Client talks HTTP/1.1 to srv whatever the server offers.
func h1Client(srv *httptest.Server) *http.Client {
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = false
	tr.Protocols = new(http.Protocols)
	tr.Protocols.SetHTTP1(true)
	// Clone prepares the original for HTTP/2 and carries its ALPN list over.
	tr.TLSClientConfig.NextProtos = []string{"http/1.1"}
	return &http.Client{Transport: tr}
}

// h2Client talks HTTP/2 to a TLS server that offers it.
func h2Client(srv *httptest.Server) *http.Client {
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = true
	return &http.Client{Transport: tr}
}

// requireUserTimeoutSupport skips a test that reads the socket option on a
// platform that has none. The stub builds still run every other test.
func requireUserTimeoutSupport(t *testing.T, srv *httptest.Server) {
	t.Helper()
	c, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := platform.TCPUserTimeout(c); errors.Is(err, platform.ErrUserTimeoutUnsupported) {
		t.Skip("this platform has no TCP_USER_TIMEOUT")
	}
}

// The server every coddy listener is built from carries the hook, so a handler
// can find its connection without each listener remembering to ask.
func TestNewServerStoresTheConnection(t *testing.T) {
	if NewServer(":0", http.NotFoundHandler()).ConnContext == nil {
		t.Fatal("NewServer does not set ConnContext")
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tc, ok := TCPConn(r)
		if !ok || tc.RemoteAddr().String() != r.RemoteAddr {
			t.Errorf("TCPConn = %v, %v for a request from %s", tc, ok, r.RemoteAddr)
		}
	}))
	srv.Config = NewServer("", srv.Config.Handler)
	srv.Start()
	defer srv.Close()
	if _, err := http.Get(srv.URL); err != nil {
		t.Fatal(err)
	}
}

// The relay listens under TLS: the connection the server sees is a *tls.Conn
// and the socket the option belongs to is one NetConn below it.
func TestTCPConnUnwrapsATLSConnection(t *testing.T) {
	var found *net.TCPConn
	var ok bool
	srv := tlsServer(t, false, func(w http.ResponseWriter, r *http.Request) {
		found, ok = TCPConn(r)
		if r.TLS == nil {
			t.Error("the test server did not speak TLS")
		}
	})
	if _, err := srv.Client().Get(srv.URL); err != nil {
		t.Fatal(err)
	}
	if !ok || found == nil {
		t.Fatal("a TLS connection was not unwrapped to its TCP socket")
	}
}

// wrapConn hides a TCP socket behind a type of its own that reaches it through
// NetConn, the way a test double or a counting wrapper would.
type wrapConn struct{ net.Conn }

func (c wrapConn) NetConn() net.Conn { return c.Conn }

type wrapListener struct{ net.Listener }

func (l wrapListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return wrapConn{c}, nil
}

func TestTCPConnUnwrapsAWrapperThatOffersNetConn(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var ok bool
	srv := &http.Server{
		ConnContext: ConnContext,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, ok = TCPConn(r)
		}),
	}
	go func() { _ = srv.Serve(wrapListener{ln}) }()
	defer func() { _ = srv.Close() }()
	if _, err := http.Get("http://" + ln.Addr().String()); err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("a wrapper with NetConn was not unwrapped")
	}
}

// A connection that is not TCP at the bottom (a pipe, a Unix socket) has no
// option to set.
func TestTCPConnIsAbsentWithoutATCPSocket(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(ConnContext(context.Background(), a))
	if tc, ok := TCPConn(r); ok || tc != nil {
		t.Fatalf("TCPConn found %v on a pipe", tc)
	}
	// And none at all for a request that never went through the hook.
	if tc, ok := TCPConn(httptest.NewRequest(http.MethodGet, "/", nil)); ok || tc != nil {
		t.Fatalf("TCPConn found %v without the hook", tc)
	}
}

// A wrapper that wraps itself in a loop must not hang the lookup.
type loopConn struct{ net.Conn }

func (c *loopConn) NetConn() net.Conn { return c }

func TestTCPConnGivesUpOnAWrapperLoop(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(ConnContext(context.Background(), &loopConn{a}))
	if _, ok := TCPConn(r); ok {
		t.Fatal("a self-referential wrapper produced a connection")
	}
}

// UserTimeoutFor sets the option for the call and its restore puts the system
// default back.
func TestUserTimeoutForSetsAndRestores(t *testing.T) {
	var before, during, after time.Duration
	var setErr error
	srv := tcpServer(t, func(w http.ResponseWriter, r *http.Request) {
		tc, _ := TCPConn(r)
		before, _ = platform.TCPUserTimeout(tc)
		var restore func()
		restore, setErr = UserTimeoutFor(r, 30*time.Second)
		during, _ = platform.TCPUserTimeout(tc)
		restore()
		after, _ = platform.TCPUserTimeout(tc)
	})
	requireUserTimeoutSupport(t, srv)
	if _, err := srv.Client().Get(srv.URL); err != nil {
		t.Fatal(err)
	}
	if setErr != nil {
		t.Fatalf("UserTimeoutFor: %v", setErr)
	}
	if before != 0 || during != 30*time.Second || after != 0 {
		t.Fatalf("option before/during/after = %v/%v/%v, want 0/30s/0", before, during, after)
	}
}

// Over TLS the option lands on the TCP socket under the *tls.Conn.
func TestUserTimeoutForReachesTheSocketUnderTLS(t *testing.T) {
	var during time.Duration
	var setErr error
	srv := tlsServer(t, false, func(w http.ResponseWriter, r *http.Request) {
		var restore func()
		restore, setErr = UserTimeoutFor(r, 20*time.Second)
		defer restore()
		tc, _ := TCPConn(r)
		during, _ = platform.TCPUserTimeout(tc)
	})
	requireUserTimeoutSupport(t, srv)
	if _, err := h1Client(srv).Get(srv.URL); err != nil {
		t.Fatal(err)
	}
	if setErr != nil || during != 20*time.Second {
		t.Fatalf("under TLS: err %v, option %v, want nil and 20s", setErr, during)
	}
}

// The restore is idempotent and a late second call does not clobber what the
// connection carries by then.
func TestUserTimeoutForRestoreRunsOnce(t *testing.T) {
	var final time.Duration
	srv := tcpServer(t, func(w http.ResponseWriter, r *http.Request) {
		tc, _ := TCPConn(r)
		restore, _ := UserTimeoutFor(r, 30*time.Second)
		restore()
		_, _ = UserTimeoutFor(r, 20*time.Second)
		restore() // a stale second call
		final, _ = platform.TCPUserTimeout(tc)
	})
	requireUserTimeoutSupport(t, srv)
	if _, err := srv.Client().Get(srv.URL); err != nil {
		t.Fatal(err)
	}
	if final != 20*time.Second {
		t.Fatalf("a stale restore reset the option to %v, want the 20s a later call set", final)
	}
}

// One TCP connection carries many HTTP/2 streams, so a per-call option would
// reach streams that are not the call: it is refused, the socket is untouched
// and the error names both reasons.
func TestUserTimeoutForRefusesAnHTTP2Stream(t *testing.T) {
	var proto int
	var restore func()
	var setErr error
	var socket time.Duration
	srv := tlsServer(t, true, func(w http.ResponseWriter, r *http.Request) {
		proto = r.ProtoMajor
		restore, setErr = UserTimeoutFor(r, 30*time.Second)
		if tc, ok := TCPConn(r); ok {
			socket, _ = platform.TCPUserTimeout(tc)
		}
	})
	if _, err := h2Client(srv).Get(srv.URL); err != nil {
		t.Fatal(err)
	}
	if proto != 2 {
		t.Fatalf("the test client spoke HTTP/%d, want 2", proto)
	}
	if !errors.Is(setErr, ErrSharedConnection) {
		t.Fatalf("error = %v, want ErrSharedConnection", setErr)
	}
	if !errors.Is(setErr, platform.ErrUserTimeoutUnsupported) {
		t.Fatalf("error = %v, want it to count as unsupported so a caller can treat both alike", setErr)
	}
	if restore == nil {
		t.Fatal("restore is nil")
	}
	restore() // a no-op that must not touch the shared socket
	if socket != 0 {
		t.Fatalf("an HTTP/2 stream changed the socket's option to %v", socket)
	}
}

// ALPN offered by the server is not what decides: an HTTP/1.1 client of the
// same TLS server is probed.
func TestUserTimeoutForAcceptsHTTP1OnAServerThatOffersHTTP2(t *testing.T) {
	var proto int
	var setErr error
	srv := tlsServer(t, true, func(w http.ResponseWriter, r *http.Request) {
		proto = r.ProtoMajor
		var restore func()
		restore, setErr = UserTimeoutFor(r, 30*time.Second)
		restore()
	})
	requireUserTimeoutSupport(t, srv)
	if _, err := h1Client(srv).Get(srv.URL); err != nil {
		t.Fatal(err)
	}
	if proto != 1 || setErr != nil {
		t.Fatalf("HTTP/%d client: error %v, want an HTTP/1 client to be probed", proto, setErr)
	}
}

// Without the hook (a test server built another way) there is nothing to set.
func TestUserTimeoutForWithoutTheHookIsANoop(t *testing.T) {
	var restore func()
	var setErr error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		restore, setErr = UserTimeoutFor(r, 30*time.Second)
	}))
	defer srv.Close()
	if _, err := http.Get(srv.URL); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(setErr, platform.ErrUserTimeoutUnsupported) || restore == nil {
		t.Fatalf("error %v, restore nil=%v", setErr, restore == nil)
	}
	restore()
}

// Every request of one connection shares one state; another connection has its
// own.
func TestOncePerConn(t *testing.T) {
	type answers struct{ topic, another, topicAgain bool }
	var mu sync.Mutex
	var seen []answers
	srv := tlsServer(t, true, func(w http.ResponseWriter, r *http.Request) {
		a := answers{
			topic:      OncePerConn(r, "topic"),
			another:    OncePerConn(r, "another"),
			topicAgain: OncePerConn(r, "topic"),
		}
		mu.Lock()
		seen = append(seen, a)
		mu.Unlock()
	})
	take := func() []answers {
		mu.Lock()
		defer mu.Unlock()
		out := seen
		seen = nil
		return out
	}

	client := h2Client(srv)
	for i := 0; i < 2; i++ {
		res, err := client.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
	}
	got := take()
	want := []answers{{true, true, false}, {false, false, false}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("two requests on one HTTP/2 connection answered %v, want %v", got, want)
	}

	// A second connection starts again.
	client.CloseIdleConnections()
	res, err := h2Client(srv).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	got = take()
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("a fresh connection answered %v, want %v", got, want[:1])
	}
}

// With no hook there is no connection to count on: the answer is yes, so the
// one line a caller logs is never lost.
func TestOncePerConnWithoutTheHookAlwaysAnswersYes(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for i := 0; i < 2; i++ {
		if !OncePerConn(r, "x") {
			t.Fatalf("OncePerConn without the hook answered no on call %d", i+1)
		}
	}
}
