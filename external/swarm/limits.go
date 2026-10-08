//go:build swarm

package swarm

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/shareguard"
)

// The relay's own limits of a scoped client (docs/plans/remote-model-provider-phase3.md, 5.3; D3 closed by
// docs/plans/remote-model-provider-models/p3-d3-rate-windows.md): a count of the client's shared-model calls running through this
// relay (swarm.clients[].max_streams) and a window of calls per minute (rate_per_minute, rate_burst), keyed by the entry's name. The
// node sees the relay as ONE credential, so only the relay can hold one client to its own contract; the node's limit still protects the
// provider from all the relay's clients together.

// The codes a relay refusal carries, kind busy like every other refusal of a window: a client that predates them waits them out.
const (
	wireCodeClientStreams = "client_streams"
	wireCodeClientRate    = "client_rate"
)

// clientLimitsPruneEvery is how many window checks pass between two sweeps of the idle keys of the limiter.
const clientLimitsPruneEvery = 512

// busyRefusal is the flat error object of a refusal, the same keys as the node's (llm.WireError), written without importing the
// node's package; a test pins the key set. Message is built here from the code, never from anything the client sent.
type busyRefusal struct {
	Status      int     `json:"status,omitempty"`
	Kind        string  `json:"kind"`
	Code        string  `json:"code,omitempty"`
	Message     string  `json:"message,omitempty"`
	RetryAfterS float64 `json:"retry_after_s,omitempty"`
	Emitted     bool    `json:"emitted"`
}

// clientLimits counts slots under one mutex and spends window tokens in a shareguard.Limiter.
type clientLimits struct {
	mu   sync.Mutex
	use  map[string]int
	rate *shareguard.Limiter
	n    atomic.Uint64
}

func newClientLimits(now func() time.Time) *clientLimits {
	return &clientLimits{use: map[string]int{}, rate: shareguard.NewLimiter(now)}
}

// inUse is the number of slots of a client held now.
func (l *clientLimits) inUse(name string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.use[name]
}

// clientLease is what an admitted call holds: its slot, and the token it took (to give back when the node refuses).
type clientLease struct {
	l       *clientLimits
	c       config.SwarmClient
	slot    bool
	token   bool
	freed   sync.Once
	refunds sync.Once
}

// acquire takes the client's slot and then its window token, in that order: a call refused for a slot spends no token. A refusal
// holds nothing. The limits are read from the entry of this request, so a reload that changes them applies to the next call and
// leaves the calls in flight alone. A client with no limit set gets a lease that holds nothing.
func (l *clientLimits) acquire(c config.SwarmClient) (*clientLease, *busyRefusal) {
	le := &clientLease{l: l, c: c}
	if c.MaxStreams > 0 {
		l.mu.Lock()
		if l.use[c.Name] >= c.MaxStreams {
			l.mu.Unlock()
			return nil, &busyRefusal{
				Status: http.StatusTooManyRequests, Kind: "busy", Code: wireCodeClientStreams, RetryAfterS: 1,
				Message: "the relay allows " + strconv.Itoa(c.MaxStreams) + " concurrent shared-model calls for this client and they are running",
			}
		}
		l.use[c.Name]++
		l.mu.Unlock()
		le.slot = true
	}
	if c.RatePerMinute > 0 {
		if l.n.Add(1)%clientLimitsPruneEvery == 0 {
			l.rate.Prune(2 * time.Minute)
		}
		ok, wait := l.rate.Take(c.Name, c.RatePerMinute, c.EffectiveBurst())
		if !ok {
			le.release()
			retry := math.Ceil(wait.Seconds())
			if retry < 1 {
				retry = 1
			}
			return nil, &busyRefusal{
				Status: http.StatusTooManyRequests, Kind: "busy", Code: wireCodeClientRate, RetryAfterS: retry,
				Message: "the relay accepts " + strconv.Itoa(c.RatePerMinute) + " shared-model calls per minute for this client and the window is spent",
			}
		}
		le.token = true
	}
	return le, nil
}

// release gives the slot back, once, however often it is called.
func (le *clientLease) release() {
	if le == nil {
		return
	}
	le.freed.Do(func() {
		if !le.slot {
			return
		}
		le.l.mu.Lock()
		if le.l.use[le.c.Name] > 1 {
			le.l.use[le.c.Name]--
		} else {
			delete(le.l.use, le.c.Name)
		}
		le.l.mu.Unlock()
	})
}

// refund gives the window token back, once: the node refused a forwarded call before any stream started, so the call was not
// admitted and a client that waits out the node's busy does not drain its relay window by waiting.
func (le *clientLease) refund() {
	if le == nil || !le.token {
		return
	}
	le.refunds.Do(func() { le.l.rate.Refund(le.c.Name, le.c.RatePerMinute) })
}

// writeBusyRefusal writes the refusal as the node writes its own: JSON, no-store, a Retry-After in whole seconds.
func writeBusyRefusal(w http.ResponseWriter, ref *busyRefusal) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Retry-After", strconv.Itoa(int(ref.RetryAfterS)))
	w.WriteHeader(ref.Status)
	_ = json.NewEncoder(w).Encode(ref)
}
