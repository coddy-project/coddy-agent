package shareguard

import (
	"sync"
	"time"
)

// Limiter is a GCRA (generic cell rate algorithm) limiter keyed by a caller
// string. Per key it stores one time.Time, the theoretical arrival time of the
// next call; the rate and the burst come from the caller at every call, so a
// configuration reload applies to the next call.
type Limiter struct {
	now func() time.Time

	mu  sync.Mutex
	tat map[string]*state
}

// state is the arrival time of a key and when it last admitted a call. The
// admission time is what lets a changed rate re-bound old state: under any
// parameters a call admitted at last leaves tat at most last+tau+t.
type state struct {
	tat  time.Time
	last time.Time
}

// NewLimiter returns a limiter that reads time from now (time.Now when nil).
func NewLimiter(now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{now: now, tat: map[string]*state{}}
}

func interval(perMinute int) time.Duration { return time.Minute / time.Duration(perMinute) }

func normBurst(burst int) int {
	if burst < 1 {
		return 1
	}
	return burst
}

// Take spends one token of key. perMinute <= 0 means no limit: Take admits and
// stores nothing. A refused call spends nothing and returns how long to wait
// until a token exists.
func (l *Limiter) Take(key string, perMinute, burst int) (ok bool, retryAfter time.Duration) {
	if perMinute <= 0 {
		return true, 0
	}
	t := interval(perMinute)
	tau := time.Duration(normBurst(burst)-1) * t

	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	st, seen := l.tat[key]
	if !seen {
		st = &state{tat: now, last: now}
		l.tat[key] = st
	}
	tat := st.tat
	// State written under other parameters never outlasts what the current ones
	// could have produced from the same last admission: a reload from 1 to 60 a
	// minute does not keep refusing for the old minute. The clamp comes first and
	// the floor at now after it: the other way round, an idle key is floored at
	// now and then pulled back below it, and gets one call more than its burst.
	if limit := st.last.Add(tau + t); tat.After(limit) {
		tat = limit
	}
	if tat.Before(now) {
		tat = now
	}
	if wait := tat.Sub(now); wait > tau {
		st.tat = tat
		return false, wait - tau
	}
	st.tat = tat.Add(t)
	st.last = now
	return true, 0
}

// Refund gives back the token of one admitted Take: the stored arrival time
// moves back by one emission interval and never before now, so a bucket cannot
// hold more than its burst however often it is refunded.
func (l *Limiter) Refund(key string, perMinute int) {
	if perMinute <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.tat[key]
	if !ok {
		return
	}
	st.tat = st.tat.Add(-interval(perMinute))
	if now := l.now(); st.tat.Before(now) {
		st.tat = now
	}
}

// Prune drops the keys whose bucket is full again and has been for at least
// idle, so the map is bounded in time by the credentials seen in that window.
func (l *Limiter) Prune(idle time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := l.now().Add(-idle)
	for k, st := range l.tat {
		if !st.tat.After(cutoff) {
			delete(l.tat, k)
		}
	}
}

// Len is the number of keys held.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.tat)
}
