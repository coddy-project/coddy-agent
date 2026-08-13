//go:build http

package httpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
)

const defaultComposerRelayMaxBytes = 512 << 10

var errComposerRelayClosed = errors.New("composer relay closed")

// composerStreamRelay captures the SSE frames of a live composer turn so extra HTTP
// clients can subscribe (an SPA tab reload, a second IDE panel, a browser watching a turn
// a script started) while the original stream continues.
//
// History is kept as whole frames rather than raw bytes: the buffer is bounded, and
// trimming a byte window cuts the oldest frame in half, which reaches a subscriber as a
// corrupt event. Frames are numbered so a subscriber that lost its connection can resume
// from where it stopped, and so one that fell too far behind can be told rather than
// silently handed a stream with a hole in it.
type composerStreamRelay struct {
	mu       sync.Mutex
	frames   []relayFrame
	bufBytes int
	// pending holds a frame that arrived split across writes, until its terminator.
	pending  []byte
	lastSeq  uint64
	maxBytes int
	closed   bool
	subs     map[*relaySubscriber]struct{}
}

// relayFrame is one complete SSE frame plus the sequence a client resumes from.
type relayFrame struct {
	seq  uint64
	data []byte
}

// relaySubscriber is one attached client. Sends are non-blocking, so a subscriber that
// cannot keep up is marked desynced instead of stalling the turn that is publishing.
type relaySubscriber struct {
	ch       chan relayFrame
	desynced bool
}

const frameTerminator = "\n\n"

func newComposerStreamRelay() *composerStreamRelay {
	return &composerStreamRelay{
		maxBytes: defaultComposerRelayMaxBytes,
		subs:     make(map[*relaySubscriber]struct{}),
	}
}

// Write splits incoming bytes into complete SSE frames, records them and fans them out.
func (r *composerStreamRelay) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return len(p), nil
	}
	r.pending = append(r.pending, p...)
	var ready []relayFrame
	for {
		idx := bytes.Index(r.pending, []byte(frameTerminator))
		if idx < 0 {
			break
		}
		end := idx + len(frameTerminator)
		frame := relayFrame{
			seq:  r.lastSeq + 1,
			data: append([]byte(nil), r.pending[:end]...),
		}
		r.lastSeq = frame.seq
		r.pending = append([]byte(nil), r.pending[end:]...)
		r.frames = append(r.frames, frame)
		r.bufBytes += len(frame.data)
		ready = append(ready, frame)
	}
	// Trim whole frames from the front, oldest first.
	for r.bufBytes > r.maxBytes && len(r.frames) > 1 {
		r.bufBytes -= len(r.frames[0].data)
		r.frames = r.frames[1:]
	}
	for sub := range r.subs {
		for _, frame := range ready {
			select {
			case sub.ch <- frame:
			default:
				// The queue is full: this subscriber has lost frames, and saying so is
				// the only honest thing left to do.
				sub.desynced = true
			}
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
	for sub := range r.subs {
		close(sub.ch)
	}
	r.subs = nil
}

func (r *composerStreamRelay) unsubscribe(sub *relaySubscriber) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subs, sub)
}

// serveSubscriber replays the turn so far, then streams live frames until Close or ctx ends.
func (r *composerStreamRelay) serveSubscriber(ctx context.Context, w http.ResponseWriter) error {
	return r.serveSubscriberFrom(ctx, w, 0)
}

// serveSubscriberFrom is serveSubscriber resuming after the frame the client last saw.
// lastEventID 0 means "send everything still buffered".
func (r *composerStreamRelay) serveSubscriberFrom(ctx context.Context, w http.ResponseWriter, lastEventID uint64) error {
	sub := &relaySubscriber{ch: make(chan relayFrame, 256)}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errComposerRelayClosed
	}
	replay := make([]relayFrame, 0, len(r.frames))
	oldest := uint64(0)
	if len(r.frames) > 0 {
		oldest = r.frames[0].seq
	}
	for _, f := range r.frames {
		if f.seq > lastEventID {
			replay = append(replay, f)
		}
	}
	// The client asked to continue from a frame that has already been trimmed, so the
	// gap between what it has and what we can still send is unbridgeable.
	gapped := lastEventID > 0 && oldest > lastEventID+1
	r.subs[sub] = struct{}{}
	r.mu.Unlock()

	defer r.unsubscribe(sub)

	fl, ok := w.(http.Flusher)
	if !ok {
		return errors.New("response writer is not a flusher")
	}
	if gapped {
		if _, err := io.WriteString(w, desyncFrame(lastEventID, oldest)); err != nil {
			return err
		}
	}
	for _, f := range replay {
		if _, err := w.Write(subscriberFrame(f)); err != nil {
			return err
		}
	}
	// Flush even with nothing replayed: the response headers are still unsent at this
	// point, so a subscriber that attaches before the turn has produced anything would
	// otherwise see its request hang rather than an open SSE stream.
	fl.Flush()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case f, open := <-sub.ch:
			if !open {
				return nil
			}
			r.mu.Lock()
			lost := sub.desynced
			sub.desynced = false
			r.mu.Unlock()
			if lost {
				if _, err := io.WriteString(w, desyncFrame(0, f.seq)); err != nil {
					return err
				}
			}
			if _, err := w.Write(subscriberFrame(f)); err != nil {
				return err
			}
			fl.Flush()
		}
	}
}

// subscriberFrame prefixes a frame with its sequence, so a client can resume from it.
// Only the subscriber path does this: the primary POST stream keeps the exact bytes API
// clients have always parsed.
func subscriberFrame(f relayFrame) []byte {
	out := make([]byte, 0, len(f.data)+24)
	out = append(out, fmt.Sprintf("id: %d\n", f.seq)...)
	return append(out, f.data...)
}

// desyncFrame tells a subscriber that frames between two sequences are gone, so it can
// reload the transcript instead of rendering a hole.
func desyncFrame(from, to uint64) string {
	return fmt.Sprintf("event: desync\ndata: {\"object\":\"coddy.stream_desync\",\"lastEventId\":%d,\"resumedAt\":%d}\n\n", from, to)
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
