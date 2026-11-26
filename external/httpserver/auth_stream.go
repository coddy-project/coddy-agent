//go:build http

package httpserver

import (
	"context"
	"net/http"
	"sync"
	"time"
)

type oidcRequestKey struct{}

// An admitted turn runs under its original authorization. Logout is not Stop:
// it cuts off the response and other subscriptions of that browser, but does
// not discard queued work, turn side effects or streams of other principals.
func acceptedTurnContext(ctx context.Context) context.Context {
	if ctx.Value(oidcRequestKey{}) != nil {
		return context.WithoutCancel(ctx)
	}
	return ctx
}

// serveOIDCRequest owns the underlying writer until the handler finishes or
// the session ends. A detached handler can no longer touch the wire. Abort the
// HTTP response rather than sending a successful but truncated representation.
func (s *Server) serveOIDCRequest(next http.Handler, w http.ResponseWriter, r *http.Request, x oidcSession) {
	if x.life.Err() != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), x.until.Sub(s.authNow()))
	defer cancel()
	stop := context.AfterFunc(x.life, cancel)
	defer stop()
	if ctx.Err() != nil || x.life.Err() != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx = context.WithValue(ctx, oidcRequestKey{}, true)
	out := &oidcResponseWriter{target: w, header: w.Header().Clone(), ctx: ctx, life: x.life}
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		next.ServeHTTP(out, r.WithContext(ctx))
		out.WriteHeader(http.StatusOK)
	}()
	select {
	case p := <-done:
		if p != nil {
			panic(p)
		}
		if ctx.Err() == nil && x.life.Err() == nil {
			return
		}
	case <-ctx.Done():
	}
	// Interrupt a blocked write before detaching. Even if the underlying
	// wrapper cannot set a deadline, the normal server write deadline applies.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now())
	out.mu.Lock()
	out.target = nil
	out.mu.Unlock()
	panic(http.ErrAbortHandler)
}

type oidcResponseWriter struct {
	mu      sync.Mutex
	target  http.ResponseWriter
	header  http.Header
	ctx     context.Context
	life    context.Context
	started bool
}

func (w *oidcResponseWriter) Header() http.Header { return w.header }
func (w *oidcResponseWriter) writeHeader(status int) {
	if w.started {
		return
	}
	w.started = true
	h := w.target.Header()
	for k := range h {
		delete(h, k)
	}
	for k, v := range w.header {
		h[k] = v
	}
	h.Set("Cache-Control", "no-store")
	w.target.WriteHeader(status)
}
func (w *oidcResponseWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.target != nil && w.ctx.Err() == nil && w.life.Err() == nil {
		w.writeHeader(status)
	}
}
func (w *oidcResponseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.target == nil || w.ctx.Err() != nil || w.life.Err() != nil {
		// A detached turn must not interpret the vanished browser as Stop.
		return len(p), nil
	}
	w.writeHeader(http.StatusOK)
	n, err := w.target.Write(p)
	if err != nil && (w.ctx.Err() != nil || w.life.Err() != nil) {
		return len(p), nil
	}
	return n, err
}
func (w *oidcResponseWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.target != nil && w.ctx.Err() == nil && w.life.Err() == nil {
		w.writeHeader(http.StatusOK)
		_ = http.NewResponseController(w.target).Flush()
	}
}
