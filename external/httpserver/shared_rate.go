//go:build http

package httpserver

import (
	"math"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/shareguard"
)

// sharedRatePruneEvery is how many window checks pass between two sweeps of the
// idle keys. A key exists only for a credential that passed the gate, so the map
// is bounded by the credentials and sessions in use, and the sweep bounds it in
// time.
const sharedRatePruneEvery = 512

// newSharedRate is the window limiter of the shared-model routes, on the same
// clock as the call's timers so a test that moves the clock moves the window.
func (s *Server) newSharedRate() *shareguard.Limiter {
	return shareguard.NewLimiter(func() time.Time { return s.sharedClockNow().Now() })
}

// sharedRatePruned counts window checks, to sweep now and then.
var sharedRatePruned atomic.Uint64

// takeSharedWindow spends one token of the credential's window
// (httpserver.shared_models.rate_per_minute), or reports how long to wait for
// the next one. A node with no rate set never refuses. It is taken after the
// stream slot and before the body is read, so a refusal for a full slot spends
// no token and a window refusal gives its slot back at once.
func (s *Server) takeSharedWindow(cfg *config.Config, key string) (ok bool, wait time.Duration, perMinute int) {
	perMinute, burst := cfg.HTTPServer.EffectiveSharedRate()
	if perMinute <= 0 {
		return true, 0, 0
	}
	if sharedRatePruned.Add(1)%sharedRatePruneEvery == 0 {
		s.sharedRate.Prune(2 * time.Minute)
	}
	ok, wait = s.sharedRate.Take(key, perMinute, burst)
	return ok, wait, perMinute
}

// sharedWindowRefusal is the answer to a call past the window: kind busy, so a
// client that predates the window waits it out like a full slot, with the code
// that names it and the wait to the next token in the header and the field.
func sharedWindowRefusal(perMinute int, wait time.Duration) llm.WireError {
	retry := math.Ceil(wait.Seconds())
	if retry < 1 {
		retry = 1
	}
	return llm.WireError{
		Kind:        llm.WireKindBusy,
		Code:        llm.WireCodeRateWindow,
		RetryAfterS: retry,
		Message:     "the remote accepts " + strconv.Itoa(perMinute) + " calls per minute for this credential and the window is spent",
	}
}
