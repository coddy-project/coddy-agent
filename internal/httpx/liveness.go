package httpx

import (
	"net/http"
	"sync/atomic"
	"time"
)

// The liveness promise of a shared-model call: the slot of a peer that vanished
// without a FIN or a RST is freed at most LivenessBound after it. The handler
// that streams the call and the relay that bounds its own client share these
// two numbers, so they live here and neither package imports the other.
const (
	// LivenessBound (B) is the longest a vanished peer holds a call.
	LivenessBound = 45 * time.Second
	// LivenessHeartbeat (H) is the longest gap between two frames of a call.
	// The first heartbeat written after the peer disappears is the first
	// unacknowledged byte, so the bound includes one gap: B = H + U.
	LivenessHeartbeat = 15 * time.Second
)

// scaled is a test's replacement for the pair; nil means the constants.
var scaled atomic.Pointer[livenessPair]

type livenessPair struct{ bound, heartbeat time.Duration }

// Liveness returns the bound and the heartbeat a call works to: LivenessBound
// and LivenessHeartbeat unless a test scaled them with ScaleLiveness.
func Liveness() (bound, heartbeat time.Duration) {
	if p := scaled.Load(); p != nil {
		return p.bound, p.heartbeat
	}
	return LivenessBound, LivenessHeartbeat
}

// ScaleLiveness replaces the pair Liveness returns until the returned function
// runs, so a test can shorten B (and H with it) and run in seconds what takes
// 45 s. It is the test seam of the relay's mount; production code never calls
// it. The returned function undoes only this call's scaling, however late it
// runs.
func ScaleLiveness(bound, heartbeat time.Duration) (restore func()) {
	p := &livenessPair{bound: bound, heartbeat: heartbeat}
	scaled.Store(p)
	return func() { scaled.CompareAndSwap(p, nil) }
}

// CallUserTimeout is the user timeout U one call sets for a bound B and a
// heartbeat H: U = B - H, with H clamped to B/3 so U stays at two thirds of B
// or more. U grows as H shrinks; there is no minimum, because a U of one
// second cuts a live peer on a one-tick outage, and an H of B or more cannot be
// bounded at all. A bound that is not positive means no option (0).
func CallUserTimeout(bound, heartbeat time.Duration) time.Duration {
	if bound <= 0 {
		return 0
	}
	if heartbeat < 0 {
		heartbeat = 0
	}
	if limit := bound / 3; heartbeat > limit {
		heartbeat = limit
	}
	return bound - heartbeat
}

// ProbeCall bounds the peer of one shared-model call: it sets the user timeout
// CallUserTimeout(bound, heartbeat) on the connection of r and returns the
// restore the caller defers on every exit (UserTimeoutFor says when it cannot,
// and why). Call it after the request body has been read.
func ProbeCall(r *http.Request, bound, heartbeat time.Duration) (restore func(), err error) {
	return UserTimeoutFor(r, CallUserTimeout(bound, heartbeat))
}
