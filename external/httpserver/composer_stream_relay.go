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
	"time"
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
	// rev reads the revision of the session's message history, so each frame knows
	// which persisted snapshot it predates. Nil stamps every frame zero.
	rev func() uint64
	// trimmedAny and trimmedRev remember the history dropped from the front: a
	// client resuming by snapshot has a gap only if a trimmed frame is one its
	// snapshot does not hold.
	trimmedAny bool
	trimmedRev uint64
}

// relayFrame is one complete SSE frame plus the sequence a client resumes from.
type relayFrame struct {
	seq  uint64
	data []byte
	// rev is the message history revision when the frame was written. Whatever the
	// frame describes is persisted by a later revision, so a snapshot at rev R holds
	// every frame stamped below R and none stamped R or later.
	rev uint64
	// at is when the frame was written, so a replay can say how old it is.
	at time.Time
}

// relayResume is where a subscriber picks the turn up: after the frame it last saw,
// or after the transcript snapshot it loaded, or from the start of the buffer.
type relayResume struct {
	lastEventID uint64
	sinceRev    uint64
	bySnapshot  bool
}

// relayAgeThreshold is how old a frame must be before the subscriber is told its age.
// Live frames reach a client within it and stay byte-identical to the primary stream
// apart from their id; replayed history is older than that.
const relayAgeThreshold = 250 * time.Millisecond

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
	// Read before taking the relay lock: the revision lives behind the session's own
	// lock, and nothing here should ever hold both.
	var rev uint64
	if r.rev != nil {
		rev = r.rev()
	}
	now := time.Now()
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
			rev:  rev,
			at:   now,
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
		r.trimmedAny = true
		if r.frames[0].rev > r.trimmedRev {
			r.trimmedRev = r.frames[0].rev
		}
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
	return r.serveSubscriberAfter(ctx, w, relayResume{lastEventID: lastEventID})
}

// serveSubscriberAfter replays from the resume point, then streams live frames until
// Close or ctx ends. A frame cursor wins over a snapshot: it is exact.
func (r *composerStreamRelay) serveSubscriberAfter(ctx context.Context, w http.ResponseWriter, from relayResume) error {
	sub := &relaySubscriber{ch: make(chan relayFrame, 256)}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errComposerRelayClosed
	}
	lastEventID := from.lastEventID
	bySnapshot := lastEventID == 0 && from.bySnapshot
	replay := make([]relayFrame, 0, len(r.frames))
	oldest := uint64(0)
	if len(r.frames) > 0 {
		oldest = r.frames[0].seq
	}
	for _, f := range r.frames {
		if f.seq <= lastEventID {
			continue
		}
		// A client holding a transcript loaded at sinceRev has every frame written
		// before that revision in it already.
		if bySnapshot && f.rev < from.sinceRev {
			continue
		}
		replay = append(replay, f)
	}
	var gapped bool
	var gapFrom uint64
	switch {
	case lastEventID > 0:
		// The client asked to continue from a frame that has already been trimmed, so
		// the gap between what it has and what we can still send is unbridgeable.
		gapped = oldest > lastEventID+1
		gapFrom = lastEventID
	case bySnapshot:
		gapped = r.trimmedAny && r.trimmedRev >= from.sinceRev
	}
	r.subs[sub] = struct{}{}
	r.mu.Unlock()

	defer r.unsubscribe(sub)

	fl, ok := w.(http.Flusher)
	if !ok {
		return errors.New("response writer is not a flusher")
	}
	if gapped {
		if _, err := io.WriteString(w, desyncFrame(gapFrom, oldest)); err != nil {
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

// subscriberFrame prefixes a frame with its sequence, so a client can resume from it,
// and a frame older than relayAgeThreshold with its age in milliseconds (an `age:`
// field, which SSE parsers that do not know it skip), so a client can date what it
// replays when it happened rather than when it arrived. Only the subscriber path does
// this: the primary POST stream keeps the exact bytes API clients have always parsed.
func subscriberFrame(f relayFrame) []byte {
	out := make([]byte, 0, len(f.data)+40)
	out = append(out, fmt.Sprintf("id: %d\n", f.seq)...)
	if !f.at.IsZero() {
		if age := time.Since(f.at); age >= relayAgeThreshold {
			out = append(out, fmt.Sprintf("age: %d\n", age.Milliseconds())...)
		}
	}
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
	if st := s.mgr.SessionByID(sessionID); st != nil {
		rel.rev = st.MessagesRev
	}
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
