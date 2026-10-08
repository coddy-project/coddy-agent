//go:build http

package httpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"sync/atomic"
)

// sharedAnonymousKey is the limiter key of a call that presented no bearer:
// the only callers there can be on a node that publishes its API open
// (httpserver.allow_insecure), who then share one budget.
const sharedAnonymousKey = "anonymous"

// sharedKeyFor is the limiter key of a presented bearer: a digest, so the
// limiter never holds a credential, and the same for every token class.
func sharedKeyFor(bearer string) string {
	if bearer == "" {
		return sharedAnonymousKey
	}
	sum := sha256.Sum256([]byte("coddy-shared-stream-key\x00" + bearer))
	return "b:" + hex.EncodeToString(sum[:16])
}

// sharedLimiter counts the shared-model calls running per credential (4.1a of
// docs/plans/remote-model-provider.md). The count of a key is one value under
// one mutex: the test against the limit and the increment are one atomic step,
// so concurrent callers can never overshoot it.
type sharedLimiter struct {
	mu     sync.Mutex
	counts map[string]int
}

func newSharedLimiter() *sharedLimiter {
	return &sharedLimiter{counts: make(map[string]int)}
}

// acquire takes a slot of key, or reports false when max calls of the key are
// running already. max is read by the caller from the configuration of the
// request, so a reload that lowers it applies to the next call and leaves the
// ones in flight alone.
func (l *sharedLimiter) acquire(key string, max int) (*sharedSlot, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts[key] >= max {
		return nil, false
	}
	l.counts[key]++
	return &sharedSlot{lim: l, key: key}, true
}

// inUse is the number of slots of key held now.
func (l *sharedLimiter) inUse(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.counts[key]
}

// tracked is the number of keys with a slot held, for tests of the cleanup.
func (l *sharedLimiter) tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.counts)
}

// free gives a slot of key back. A key at zero stays at zero: the count never
// goes negative.
func (l *sharedLimiter) free(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch n := l.counts[key]; {
	case n > 1:
		l.counts[key] = n - 1
	case n == 1:
		delete(l.counts, key)
	}
}

// sharedSlot is one taken slot. Every exit path of a call - the 400, 404 and
// 413 answers, the body read deadline, the write deadline, a disconnect and
// the normal end - goes through release, which frees the slot exactly once
// however often it is called.
type sharedSlot struct {
	lim      *sharedLimiter
	key      string
	released atomic.Bool

	mu   sync.Mutex
	body []byte
}

// hold remembers the request body the slot's call read, so that release can
// drop the buffer no later than it frees the slot.
func (s *sharedSlot) hold(body []byte) {
	s.mu.Lock()
	s.body = body
	s.mu.Unlock()
}

// bodyLen is the size of the body the slot still holds.
func (s *sharedSlot) bodyLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.body)
}

// drop forgets the body without freeing the slot, for a handler that has
// decoded it and needs only the decoded form.
func (s *sharedSlot) drop() {
	s.mu.Lock()
	s.body = nil
	s.mu.Unlock()
}

// release frees the slot and drops the body buffer. It is safe to call from
// every path and from a defer: only the first call counts.
func (s *sharedSlot) release() {
	if s == nil {
		return
	}
	s.drop()
	if !s.released.CompareAndSwap(false, true) {
		return
	}
	s.lim.free(s.key)
}

// sharedBearerKey is the limiter key of a bearer token. The token is prefixed, as a certificate name and a cookie are, so no
// operator-chosen token can spell another credential's key and share its budget.
func sharedBearerKey(token string) string { return sharedKeyFor("bearer:" + token) }
