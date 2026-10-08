package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// ErrSharedConnection is returned (wrapped, together with
// platform.ErrUserTimeoutUnsupported) when a request's TCP connection carries
// other requests as well, which is every HTTP/2 stream: a per-call socket
// option set for one of them would apply to all.
var ErrSharedConnection = errors.New("httpx: the request shares its TCP connection with other requests")

// maxUnwrap bounds how many NetConn() layers TCPConn follows, so a wrapper that
// returns itself cannot hang a lookup.
const maxUnwrap = 8

// connKey is the context key under which ConnContext stores a connection.
type connKey struct{}

// connState is what ConnContext stores: the accepted connection and the
// topics already mentioned for it.
type connState struct {
	conn net.Conn

	mu    sync.Mutex
	noted map[string]struct{}
}

// ConnContext is http.Server.ConnContext: it keeps the accepted connection in
// the context every request of that connection derives from, an HTTP/2 stream
// included, so a handler can reach the socket under its request. NewServer sets
// it; a server built another way (an httptest server, a custom listener) sets
// it by hand: srv.ConnContext = httpx.ConnContext.
//
// For a TLS listener the connection stored is the *tls.Conn, and TCPConn
// unwraps it.
func ConnContext(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, connKey{}, &connState{conn: c})
}

func stateOf(r *http.Request) *connState {
	if r == nil {
		return nil
	}
	st, _ := r.Context().Value(connKey{}).(*connState)
	return st
}

// TCPConn returns the *net.TCPConn a request arrived on. A *tls.Conn, and any
// wrapper that offers its connection through a NetConn() method, is unwrapped.
// It reports false for a request that did not pass through ConnContext and for
// a connection that is not TCP at the bottom (a pipe, a Unix socket).
func TCPConn(r *http.Request) (*net.TCPConn, bool) {
	st := stateOf(r)
	if st == nil {
		return nil, false
	}
	c := st.conn
	for i := 0; i < maxUnwrap && c != nil; i++ {
		switch v := c.(type) {
		case *net.TCPConn:
			return v, v != nil
		case interface{ NetConn() net.Conn }:
			c = v.NetConn()
		default:
			return nil, false
		}
	}
	return nil, false
}

// OncePerConn reports true the first time it is asked about topic for the
// connection of r and false afterwards, so a caller can log a thing once per
// connection however many requests (HTTP/2 streams, kept-alive calls) follow.
// The state dies with the connection. A request without ConnContext has no
// connection to count on and always gets true.
func OncePerConn(r *http.Request, topic string) bool {
	st := stateOf(r)
	if st == nil {
		return true
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, seen := st.noted[topic]; seen {
		return false
	}
	if st.noted == nil {
		st.noted = make(map[string]struct{})
	}
	st.noted[topic] = struct{}{}
	return true
}

// UserTimeoutFor sets TCP_USER_TIMEOUT to d on the TCP connection of r and
// returns the function that sets the system default back. The returned
// function is never nil, runs its restore once however often it is called, and
// ignores a failure: a connection the kernel already aborted has nothing left
// to restore. Callers defer it on every exit of the call, success, error frame
// and cancel alike, because an HTTP/1.1 connection that is kept alive would
// otherwise carry the option into its next request.
//
// It refuses what it cannot scope to one call: a request that is not HTTP/1.x
// (an HTTP/2 stream shares its connection) answers ErrSharedConnection, and one
// without a TCP socket under it (no ConnContext, a pipe, the reverse tunnel's
// stream) or on a platform without the option answers
// platform.ErrUserTimeoutUnsupported. Both come with a no-op restore, and both
// mean "this call goes on unprobed", not "fail the call".
//
// The kernel counts the option from the first byte written and not yet
// acknowledged, so a handler sets it after it has read the request body: the
// upload is then bounded by its own deadline and not by this option.
func UserTimeoutFor(r *http.Request, d time.Duration) (restore func(), err error) {
	noop := func() {}
	if r == nil {
		return noop, fmt.Errorf("%w: no request", platform.ErrUserTimeoutUnsupported)
	}
	if r.ProtoMajor != 1 {
		return noop, fmt.Errorf("%w: HTTP/%d stream (%w)", platform.ErrUserTimeoutUnsupported, r.ProtoMajor, ErrSharedConnection)
	}
	tc, ok := TCPConn(r)
	if !ok {
		return noop, fmt.Errorf("%w: the request has no TCP connection", platform.ErrUserTimeoutUnsupported)
	}
	if err := platform.SetTCPUserTimeout(tc, d); err != nil {
		return noop, err
	}
	var once sync.Once
	return func() {
		once.Do(func() { _ = platform.SetTCPUserTimeout(tc, 0) })
	}, nil
}
