//go:build http

package httpserver

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// The timers of the shared-model routes (4.2 of docs/plans/remote-model-provider.md):
// P bounds the read of a request body, W bounds each write of the response,
// and H is the longest the response may go without a byte. B is the longest a
// peer that vanished without a FIN or a RST holds its slot
// (docs/plans/remote-model-provider-phase2.md, 5.2): the call sets the user
// timeout U = B - H on the peer's connection, because the first heartbeat written
// after the peer is gone is the first byte nobody acknowledges and the kernel
// aborts U after it. The relay's mount shares B and H, and cannot import this
// package, so they live in internal/httpx too and a test holds the two pairs equal.
const (
	sharedBodyDeadline  = 30 * time.Second
	sharedWriteDeadline = 60 * time.Second
	sharedHeartbeat     = 15 * time.Second
	sharedLivenessBound = 45 * time.Second
)

// sharedClock is the source of the timers a shared-model call runs on: the
// heartbeat and the stall guard. A test replaces it with a clock it advances by
// hand, so the timing rules are checked without sleeping. The deadlines handed
// to the operating system (the body read and every write) are real time.
type sharedClock interface {
	Now() time.Time
	NewTimer(d time.Duration) sharedTimer
}

// sharedTimer is the part of time.Timer the call uses.
type sharedTimer interface {
	C() <-chan time.Time
	Reset(d time.Duration)
	Stop()
}

type realSharedClock struct{}

func (realSharedClock) Now() time.Time { return time.Now() }

func (realSharedClock) NewTimer(d time.Duration) sharedTimer {
	return &realSharedTimer{t: time.NewTimer(d)}
}

type realSharedTimer struct{ t *time.Timer }

func (r *realSharedTimer) C() <-chan time.Time   { return r.t.C }
func (r *realSharedTimer) Reset(d time.Duration) { r.t.Reset(d) }
func (r *realSharedTimer) Stop()                 { r.t.Stop() }

// sharedStream writes the event stream of one call: headers, heartbeat
// comments, chunk frames and the one terminal frame. Every write is serialised
// (the provider's callback and the heartbeat share the response), flushed, and
// bounded by a deadline of its own refreshed before the write, so a peer that
// stopped reading cuts the call instead of holding its slot.
type sharedStream struct {
	w             http.ResponseWriter
	rc            *http.ResponseController
	clk           sharedClock
	writeDeadline time.Duration
	// onBroken cancels the upstream call when a write fails.
	onBroken func()

	mu        sync.Mutex
	started   bool
	terminal  bool
	broken    error
	emitted   int
	lastWrite time.Time
	// heartbeats counts the comments written, for the log line and the tests.
	heartbeats int
}

func newSharedStream(w http.ResponseWriter, clk sharedClock, writeDeadline time.Duration, onBroken func()) *sharedStream {
	return &sharedStream{
		w:             w,
		rc:            http.NewResponseController(w),
		clk:           clk,
		writeDeadline: writeDeadline,
		onBroken:      onBroken,
	}
}

// start sends the headers and the first heartbeat. The remote calls it right
// after it has read and checked the body and before the provider call, so the
// first bytes of the response are on their way while the model is still
// working and every hop sees traffic from the start.
func (st *sharedStream) start() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.started {
		return nil
	}
	st.started = true
	h := st.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	st.w.WriteHeader(http.StatusOK)
	st.heartbeats++
	return st.writeLocked([]byte(llm.CoddyHeartbeat))
}

// heartbeat writes a comment when the response has been silent for interval and
// reports how long until the next one is due. It writes nothing once a
// terminal frame went out or a write failed.
func (st *sharedStream) heartbeat(interval time.Duration) (next time.Duration, live bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.terminal || st.broken != nil {
		return 0, false
	}
	if quiet := st.clk.Now().Sub(st.lastWrite); quiet < interval {
		return interval - quiet, true
	}
	st.heartbeats++
	if st.writeLocked([]byte(llm.CoddyHeartbeat)) != nil {
		return 0, false
	}
	return interval, true
}

// chunk writes one chunk frame.
func (st *sharedStream) chunk(c llm.StreamChunk) error {
	frame, err := llm.EncodeWireFrame(llm.WireChunkFromStream(c))
	if err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.terminal {
		return nil
	}
	if err := st.writeLocked(frame); err != nil {
		return err
	}
	st.emitted++
	return nil
}

// finish writes the one terminal frame, a final or an error. Anything after it,
// and a second terminal, is dropped: a stream has exactly one terminal event.
func (st *sharedStream) finish(frame any) error {
	raw, err := llm.EncodeWireFrame(frame)
	if err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.terminal {
		return nil
	}
	st.terminal = true
	return st.writeLocked(raw)
}

// emittedChunks is the number of chunk frames actually written.
func (st *sharedStream) emittedChunks() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.emitted
}

// writeFailure is the error of the write that broke the stream, or nil.
func (st *sharedStream) writeFailure() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.broken
}

// writeLocked writes and flushes b under a fresh write deadline. A failed write
// breaks the stream for good and cancels the upstream call. The caller holds mu.
func (st *sharedStream) writeLocked(b []byte) error {
	if st.broken != nil {
		return st.broken
	}
	if st.writeDeadline > 0 {
		// An unsupported deadline (a writer that cannot set one) leaves the
		// stream bounded by the connection alone; nothing else to do about it.
		_ = st.rc.SetWriteDeadline(time.Now().Add(st.writeDeadline))
	}
	_, err := st.w.Write(b)
	if err == nil {
		if ferr := st.rc.Flush(); ferr != nil && !errors.Is(ferr, http.ErrNotSupported) {
			err = ferr
		}
	}
	if err != nil {
		st.broken = err
		if st.onBroken != nil {
			st.onBroken()
		}
		return err
	}
	st.lastWrite = st.clk.Now()
	return nil
}

// runHeartbeat writes a comment whenever the response has been silent for
// interval, so no two bytes of it are further apart, also while a blocking row
// computes or the remote's provider backs off. The returned function stops it
// and waits until it has left any write in progress: the handler is about to
// return, and the response must not be written afterwards.
func (st *sharedStream) runHeartbeat(ctx context.Context, interval time.Duration) (stop func()) {
	hctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := st.clk.NewTimer(interval)
		defer timer.Stop()
		for {
			select {
			case <-hctx.Done():
				return
			case <-timer.C():
				next, live := st.heartbeat(interval)
				if !live {
					return
				}
				timer.Reset(next)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// sharedStallGuard is the remote's model-progress guard S: it is armed when the
// provider call starts, not at the first byte, so an upstream that accepts the
// request and never answers ends as a stall after S, and every chunk of the
// call (the progress it can see) re-arms it. When it fires it cancels the call.
type sharedStallGuard struct {
	d     time.Duration
	timer sharedTimer
	fired atomic.Bool
	done  chan struct{}
	once  sync.Once
}

// startSharedStallGuard arms a guard of d, or returns nil for a guard that is
// off (d <= 0). cancel is called once when the guard fires.
func startSharedStallGuard(clk sharedClock, d time.Duration, cancel func()) *sharedStallGuard {
	if d <= 0 {
		return nil
	}
	g := &sharedStallGuard{d: d, timer: clk.NewTimer(d), done: make(chan struct{})}
	go func() {
		select {
		case <-g.timer.C():
			g.fired.Store(true)
			cancel()
		case <-g.done:
		}
	}()
	return g
}

// progress re-arms the guard: the call produced something.
func (g *sharedStallGuard) progress() {
	if g == nil || g.fired.Load() {
		return
	}
	g.timer.Reset(g.d)
}

// stop disarms the guard.
func (g *sharedStallGuard) stop() {
	if g == nil {
		return
	}
	g.once.Do(func() {
		close(g.done)
		g.timer.Stop()
	})
}

// stalled reports whether the guard fired.
func (g *sharedStallGuard) stalled() bool { return g != nil && g.fired.Load() }
