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
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// The application probe of a vanished peer (docs/plans/remote-model-provider-probe.md, model p4-probe). A client that asked for it
// pings while its stream runs. The guard is armed by the first accepted ping (a ping path that carries nothing leaves the call as it is
// today), and from then on more than the grace G of silence cancels the call. G = (L + 1) I + DMAX is the exact boundary at which a
// client that pings every I, with up to L pings lost in a row and a delay of at most DMAX, is never cut: I = 10 s, L = 2, DMAX = 5 s
// give 35 s. The cut comes at G + 1 s on the monotonic clock, so a ping on the tick the grace ends still wins (the model's tie rule).
const (
	sharedProbeEvery = 10 * time.Second
	sharedProbeGrace = 35 * time.Second
	sharedProbeSlack = time.Second
)

var sharedProbeIDRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// sharedProbes holds the calls that opted in, by their id. The id is a capability: 128 random bits, in the response of the call only.
type sharedProbes struct {
	mu    sync.Mutex
	calls map[string]*sharedProbe
}

// sharedProbe is the alive guard of one call. Arming, renewing and expiring happen under mu, so a ping that is answered 204 always
// found the call not cut, and a ping that finds it cut is answered 404.
type sharedProbe struct {
	id     string
	key    string // the limiter key of the credential the call was made with
	probes *sharedProbes
	clk    sharedClock
	grace  time.Duration
	cancel func()

	mu      sync.Mutex
	armed   bool
	stopped bool
	cut     bool
	last    time.Time // the clock's reading at the last accepted ping; a time.Time, so the monotonic reading of the production clock counts
	timer   sharedTimer
	stopCh  chan struct{}
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

// startSharedProbe confirms the probe in the response headers of a call that asked for it and registers its guard (unarmed until the
// first ping), or returns nil, whose methods are no-ops, for a call that did not ask. cancel ends the call exactly as a failed write does.
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
	s.sharedProbes.mu.Lock()
	if s.sharedProbes.calls == nil {
		s.sharedProbes.calls = map[string]*sharedProbe{}
	}
	s.sharedProbes.calls[p.id] = p
	s.sharedProbes.mu.Unlock()
	w.Header().Set(llm.CoddyProbeHeader, fmt.Sprintf("id=%s; every_ms=%d; grace_ms=%d", p.id, every.Milliseconds(), grace.Milliseconds()))
	return p
}

// ping records a sign of life and reports whether the call was alive for it: false when the guard has already cut the call or the
// call has ended. The first accepted ping arms the guard.
func (p *sharedProbe) ping() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cut || p.stopped {
		return false
	}
	p.last = p.clk.Now()
	if !p.armed {
		p.armed = true
		p.timer = p.clk.NewTimer(p.grace + sharedProbeSlack)
		go p.run()
	}
	return true
}

// run waits for G + 1 s to pass since the last accepted ping. A ping only stores the time; the timer re-arms itself for what is left, so
// a ping never races a timer that has fired, and the decision to cut is taken under the same lock as the ping.
func (p *sharedProbe) run() {
	defer close(p.done)
	for {
		select {
		case <-p.stopCh:
			p.timer.Stop()
			return
		case <-p.timer.C():
			p.mu.Lock()
			limit := p.grace + sharedProbeSlack
			elapsed := p.clk.Now().Sub(p.last)
			if elapsed >= limit && !p.stopped {
				p.cut = true
				p.mu.Unlock()
				p.cancel()
				return
			}
			p.mu.Unlock()
			p.timer.Reset(max(limit-elapsed, time.Millisecond))
		}
	}
}

// fired reports whether the guard cancelled the call.
func (p *sharedProbe) fired() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cut
}

// stop ends the guard and forgets the id, on every exit of the call: a ping after it is a 404.
func (p *sharedProbe) stop() {
	if p == nil {
		return
	}
	p.probes.mu.Lock()
	delete(p.probes.calls, p.id)
	p.probes.mu.Unlock()
	p.mu.Lock()
	already := p.stopped
	p.stopped = true
	armed := p.armed
	p.mu.Unlock()
	if already {
		return
	}
	if armed {
		close(p.stopCh)
		<-p.done
	}
}

func (ps *sharedProbes) lookup(id string) *sharedProbe {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.calls[id]
}

// llmAlivePost is the ping: 204 and the guard is armed or renewed, or 404 unknown_call when the id is unknown, malformed, ended, cut, or
// the call belongs to another credential (the same answer for each, so a ping tells nothing about the calls of others). The id is read
// from a header, never a path: a path ends up in the logs of a proxy and of a relay's forwarding errors.
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
	id := r.Header.Get(llm.CoddyProbeIDHeader)
	if !sharedProbeIDRE.MatchString(id) {
		unknown()
		return
	}
	p := s.sharedProbes.lookup(id)
	if p == nil || subtle.ConstantTimeCompare([]byte(p.key), []byte(s.sharedCallerKey(r, pol))) != 1 || !p.ping() {
		unknown()
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
