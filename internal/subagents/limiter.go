package subagents

import "sync"

// Limiter bounds how many subagent runs are in flight across the process. It
// is a resizable counting semaphore that refuses rather than queues: the pool
// it feeds has the same contract, and a spawn that waited minutes for a slot
// would still need a parent turn to consume its report.
type Limiter struct {
	mu       sync.Mutex
	limit    int
	inFlight int
}

// NewLimiter returns a limiter admitting up to n concurrent runs.
func NewLimiter(n int) *Limiter {
	if n < 0 {
		n = 0
	}
	return &Limiter{limit: n}
}

// TryAcquire claims a slot. The returned release is idempotent, so a spawn
// with several exit paths can call it from each without freeing two slots.
func (l *Limiter) TryAcquire() (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight >= l.limit {
		return nil, false
	}
	l.inFlight++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			if l.inFlight > 0 {
				l.inFlight--
			}
			l.mu.Unlock()
		})
	}, true
}

// SetLimit changes the cap for future acquisitions. Work already in flight is
// never evicted: lowering the cap below usage only refuses new spawns until
// enough runs finish.
func (l *Limiter) SetLimit(n int) {
	if n < 0 {
		n = 0
	}
	l.mu.Lock()
	l.limit = n
	l.mu.Unlock()
}

// Limit returns the current cap.
func (l *Limiter) Limit() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limit
}

// InFlight returns how many slots are held.
func (l *Limiter) InFlight() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inFlight
}
