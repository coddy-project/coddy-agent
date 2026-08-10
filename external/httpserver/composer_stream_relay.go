//go:build http

package httpserver

import (
	"context"
	"errors"
	"net/http"
	"sync"
)

const defaultComposerRelayMaxBytes = 512 << 10

var errComposerRelayClosed = errors.New("composer relay closed")

// composerStreamRelay captures raw SSE bytes for a live composer turn so extra
// HTTP clients can subscribe (e.g. SPA tab reload) while the original POST stream continues.
type composerStreamRelay struct {
	mu       sync.Mutex
	buf      []byte
	maxBytes int
	closed   bool
	subs     map[chan []byte]struct{}
}

func newComposerStreamRelay() *composerStreamRelay {
	return &composerStreamRelay{
		maxBytes: defaultComposerRelayMaxBytes,
		subs:     make(map[chan []byte]struct{}),
	}
}

// Write appends to the replay buffer and fans out to subscribers.
func (r *composerStreamRelay) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return len(p), nil
	}
	r.buf = append(r.buf, p...)
	for len(r.buf) > r.maxBytes {
		drop := len(r.buf) - r.maxBytes
		if drop < len(r.buf)/2 {
			drop = len(r.buf) / 4
		}
		if drop <= 0 {
			drop = len(r.buf) / 2
		}
		r.buf = r.buf[drop:]
	}
	payload := append([]byte(nil), p...)
	for ch := range r.subs {
		block := append([]byte(nil), payload...)
		select {
		case ch <- block:
		default:
		}
	}
	r.mu.Unlock()
	return len(p), nil
}

// Close shuts down subscribers. Safe to call more than once.
func (r *composerStreamRelay) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	for ch := range r.subs {
		close(ch)
	}
	r.subs = nil
}

func (r *composerStreamRelay) unsubscribe(ch chan []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subs, ch)
}

// serveSubscriber replays buffered bytes then streams live fragments until Close or ctx ends.
func (r *composerStreamRelay) serveSubscriber(ctx context.Context, w http.ResponseWriter) error {
	ch := make(chan []byte, 64)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errComposerRelayClosed
	}
	replay := append([]byte(nil), r.buf...)
	r.subs[ch] = struct{}{}
	r.mu.Unlock()

	defer r.unsubscribe(ch)

	fl, ok := w.(http.Flusher)
	if !ok {
		return errors.New("response writer is not a flusher")
	}
	if len(replay) > 0 {
		if _, err := w.Write(replay); err != nil {
			return err
		}
	}
	// Flush even with an empty replay: the response headers are still unsent at this
	// point, so a subscriber that attaches before the turn has produced anything would
	// otherwise see its request hang rather than an open SSE stream.
	fl.Flush()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case b, open := <-ch:
			if !open {
				return nil
			}
			if _, err := w.Write(b); err != nil {
				return err
			}
			fl.Flush()
		}
	}
}

// teeSSEWriter forwards each Write to the client and to relay (same bytes as the primary SSE stream).
type teeSSEWriter struct {
	http.ResponseWriter
	relay *composerStreamRelay
}

func (t *teeSSEWriter) Write(p []byte) (int, error) {
	if t.relay != nil {
		_, _ = t.relay.Write(p)
	}
	return t.ResponseWriter.Write(p)
}

func (t *teeSSEWriter) Flush() {
	if f, ok := t.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

var _ http.Flusher = (*teeSSEWriter)(nil)

func (s *Server) beginComposerRelay(sessionID string) *composerStreamRelay {
	rel := newComposerStreamRelay()
	s.composerRelayMu.Lock()
	if s.composerRelays == nil {
		s.composerRelays = make(map[string]*composerStreamRelay)
	}
	if old := s.composerRelays[sessionID]; old != nil {
		old.Close()
	}
	s.composerRelays[sessionID] = rel
	s.composerRelayMu.Unlock()
	return rel
}

func (s *Server) endComposerRelay(sessionID string, rel *composerStreamRelay) {
	s.composerRelayMu.Lock()
	if cur := s.composerRelays[sessionID]; cur == rel {
		delete(s.composerRelays, sessionID)
	}
	s.composerRelayMu.Unlock()
	rel.Close()
}

func (s *Server) peekComposerRelay(sessionID string) *composerStreamRelay {
	s.composerRelayMu.Lock()
	defer s.composerRelayMu.Unlock()
	return s.composerRelays[sessionID]
}
