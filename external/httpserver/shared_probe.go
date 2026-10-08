//go:build http

package httpserver

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// The application probe of a vanished peer (docs/plans/remote-model-provider-probe.md, model p4-probe). A client that asked for it
// pings while its stream runs; the node cancels the call when more than the grace G passes without a ping. G = (L + 1) I + DMAX is the
// exact boundary at which a client that pings every I, with up to L pings lost in a row and a delay of at most DMAX, is never cut:
// I = 10 s, L = 2, DMAX = 5 s give 35 s. The guard is armed at the call's start, and only a call that asked and was confirmed has one.
const (
	sharedProbeEvery = 10 * time.Second
	sharedProbeGrace = 35 * time.Second
)

var sharedProbeIDRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// sharedProbes holds the calls that opted in, by their id. The id is a capability: 128 random bits, in the response of the call only.
type sharedProbes struct {
	mu    sync.Mutex
	calls map[string]*sharedProbe
}

// sharedProbe is the alive guard of one call.
type sharedProbe struct {
	id      string
	key     string // the limiter key of the credential the call was made with
	probes  *sharedProbes
	clk     sharedClock
	grace   time.Duration
	last    atomic.Int64 // the clock's unix nanoseconds of the call's start or of the last ping
	cancel  func()
	timer   sharedTimer
	stopCh  chan struct{}
	stopped sync.Once
	cut     atomic.Bool
	done    chan struct{}
}

// sharedProbeTimings are I and G, the defaults unless a test set shorter ones.
func (s *Server) sharedProbeTimings() (every, grace time.Duration) {
	every, grace = sharedProbeEvery, sharedProbeGrace
	if s.sharedProbeI > 0 {
		every = s.sharedProbeI
	}
	if s.sharedProbeG > 0 {
		grace = s.sharedProbeG
	}
	return every, grace
}

// startSharedProbe confirms the probe in the response headers of a call that asked for it and arms its guard, or returns a nil guard
// (whose methods are no-ops) for a call that did not ask. cancel ends the call exactly as a failed write does.
func (s *Server) startSharedProbe(w http.ResponseWriter, r *http.Request, clk sharedClock, key string, cancel func()) *sharedProbe {
	if strings.TrimSpace(r.Header.Get(llm.CoddyProbeHeader)) != "1" {
		return nil
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil // no id, no probe: the call goes on as a call that did not ask
	}
	every, grace := s.sharedProbeTimings()
	p := &sharedProbe{id: hex.EncodeToString(raw[:]), key: key, probes: &s.sharedProbes, clk: clk, grace: grace, cancel: cancel,
		stopCh: make(chan struct{}), done: make(chan struct{})}
	p.last.Store(clk.Now().UnixNano())
	s.sharedProbes.mu.Lock()
	if s.sharedProbes.calls == nil {
		s.sharedProbes.calls = map[string]*sharedProbe{}
	}
	s.sharedProbes.calls[p.id] = p
	s.sharedProbes.mu.Unlock()
	w.Header().Set(llm.CoddyProbeHeader, fmt.Sprintf("id=%s; every_ms=%d; grace_ms=%d", p.id, every.Milliseconds(), grace.Milliseconds()))
	p.timer = clk.NewTimer(grace)
	go p.run()
	return p
}

// run waits for the grace to pass since the last ping. A ping only stores the time; the timer re-arms itself for what is left, so a
// ping never races a timer that has fired.
func (p *sharedProbe) run() {
	defer close(p.done)
	for {
		select {
		case <-p.stopCh:
			p.timer.Stop()
			return
		case <-p.timer.C():
			elapsed := time.Duration(p.clk.Now().UnixNano() - p.last.Load())
			if elapsed >= p.grace {
				p.cut.Store(true)
				p.cancel()
				return
			}
			p.timer.Reset(p.grace - elapsed)
		}
	}
}

// ping records a sign of life.
func (p *sharedProbe) ping() { p.last.Store(p.clk.Now().UnixNano()) }

// fired reports whether the guard cancelled the call.
func (p *sharedProbe) fired() bool { return p != nil && p.cut.Load() }

// stop ends the guard and forgets the id, on every exit of the call: a ping after it is a 404.
func (p *sharedProbe) stop() {
	if p == nil {
		return
	}
	p.stopped.Do(func() {
		p.probes.mu.Lock()
		delete(p.probes.calls, p.id)
		p.probes.mu.Unlock()
		close(p.stopCh)
	})
	<-p.done
}

func (ps *sharedProbes) lookup(id string) *sharedProbe {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.calls[id]
}

// llmAlivePost is the ping: 204 and the guard is re-armed, or 404 unknown_call when the id is unknown, malformed, ended, or the call
// belongs to another credential (the same answer for each, so a ping tells nothing about the calls of others).
func (s *Server) llmAlivePost(w http.ResponseWriter, r *http.Request) {
	pol := s.authSnapshot(r)
	if pol.anonymousSharedRefused() {
		writeSharedAuthRefusal(w)
		return
	}
	unknown := func() {
		writeSharedError(w, http.StatusNotFound, llm.WireError{Kind: llm.WireKindInvalid, Code: llm.WireCodeUnknownCall,
			Message: "no such call is running for this credential"})
	}
	id := r.PathValue("id")
	if !sharedProbeIDRE.MatchString(id) {
		unknown()
		return
	}
	p := s.sharedProbes.lookup(id)
	if p == nil || subtle.ConstantTimeCompare([]byte(p.key), []byte(s.sharedCallerKey(r, pol))) != 1 {
		unknown()
		return
	}
	p.ping()
	w.WriteHeader(http.StatusNoContent)
}
