//go:build http

package httpserver

import (
	"sync"
	"sync/atomic"
	"testing"
)

// N concurrent callers of one key never hold more slots than the limit, however
// the scheduler interleaves the test and the increment (the race detector runs
// this too). The barrier keeps every winner holding its slot until every caller
// has asked, so the count of winners is exact: the limit.
func TestSharedLimiterNeverExceedsTheLimitUnderContention(t *testing.T) {
	const callers, limit = 64, 5
	lim := newSharedLimiter()
	key := sharedKeyFor("token-a")

	var held, peak atomic.Int64
	var granted, refused atomic.Int64
	var asked sync.WaitGroup
	asked.Add(callers)
	start := make(chan struct{})
	hold := make(chan struct{})
	var done sync.WaitGroup
	for i := 0; i < callers; i++ {
		done.Add(1)
		go func() {
			defer done.Done()
			<-start
			slot, ok := lim.acquire(key, limit)
			asked.Done()
			if !ok {
				refused.Add(1)
				return
			}
			granted.Add(1)
			n := held.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			<-hold
			held.Add(-1)
			slot.release()
		}()
	}
	close(start)
	asked.Wait()
	if got := lim.inUse(key); got != limit {
		t.Fatalf("slots in use while every caller holds: %d, want %d", got, limit)
	}
	close(hold)
	done.Wait()

	if granted.Load() != limit || refused.Load() != callers-limit {
		t.Fatalf("granted %d refused %d, want %d and %d", granted.Load(), refused.Load(), limit, callers-limit)
	}
	if peak.Load() > limit {
		t.Fatalf("peak concurrent holders %d exceeds the limit %d", peak.Load(), limit)
	}
	if got := lim.inUse(key); got != 0 {
		t.Fatalf("slots in use after every release: %d, want 0", got)
	}
	if got := lim.tracked(); got != 0 {
		t.Fatalf("the limiter still tracks %d keys after every release", got)
	}
}

// A second release of the same slot is a no-op: the count never goes negative
// and never frees a slot some other caller holds.
func TestSharedLimiterReleaseIsExactlyOnce(t *testing.T) {
	lim := newSharedLimiter()
	key := sharedKeyFor("token-a")
	first, ok := lim.acquire(key, 2)
	if !ok {
		t.Fatal("first acquire refused")
	}
	second, ok := lim.acquire(key, 2)
	if !ok {
		t.Fatal("second acquire refused")
	}
	first.release()
	first.release()
	first.release()
	if got := lim.inUse(key); got != 1 {
		t.Fatalf("in use after releasing one slot three times: %d, want 1 (the other slot is still held)", got)
	}
	if _, ok := lim.acquire(key, 2); !ok {
		t.Fatal("a slot was freed and must be takeable")
	}
	if _, ok := lim.acquire(key, 2); ok {
		t.Fatal("a repeated release freed a slot the second holder still has")
	}
	second.release()
	second.release()
	if got := lim.inUse(key); got != 1 {
		t.Fatalf("in use: %d, want 1", got)
	}
}

// The release drops the body buffer the slot held, so a refused or finished
// call does not keep up to 32 MiB alive.
func TestSharedLimiterReleaseDropsTheBody(t *testing.T) {
	lim := newSharedLimiter()
	slot, ok := lim.acquire(sharedKeyFor("token-a"), 1)
	if !ok {
		t.Fatal("acquire refused")
	}
	slot.hold(make([]byte, 1<<20))
	if slot.bodyLen() != 1<<20 {
		t.Fatalf("held body: %d bytes", slot.bodyLen())
	}
	slot.release()
	if slot.bodyLen() != 0 {
		t.Fatalf("the release kept the body: %d bytes", slot.bodyLen())
	}
}

// The limit is per key: another credential's calls, and every alias of one
// credential, are counted separately and together respectively.
func TestSharedLimiterKeysAreIndependent(t *testing.T) {
	lim := newSharedLimiter()
	a, b := sharedKeyFor("token-a"), sharedKeyFor("token-b")
	if a == b || a == sharedAnonymousKey || a == "token-a" {
		t.Fatalf("keys must be distinct hashes: %q %q", a, b)
	}
	for i := 0; i < 2; i++ {
		if _, ok := lim.acquire(a, 2); !ok {
			t.Fatalf("a #%d refused", i)
		}
	}
	if _, ok := lim.acquire(a, 2); ok {
		t.Fatal("a third call of the same key was admitted")
	}
	if _, ok := lim.acquire(b, 2); !ok {
		t.Fatal("another key was refused because of a's calls")
	}
	if _, ok := lim.acquire(sharedAnonymousKey, 2); !ok {
		t.Fatal("the anonymous key was refused because of a's calls")
	}
}

// A lowered limit (a configuration reload) applies to the next acquire without
// disturbing the slots already held.
func TestSharedLimiterFollowsALoweredLimit(t *testing.T) {
	lim := newSharedLimiter()
	key := sharedKeyFor("token-a")
	var slots []*sharedSlot
	for i := 0; i < 4; i++ {
		s, ok := lim.acquire(key, 4)
		if !ok {
			t.Fatalf("acquire %d refused", i)
		}
		slots = append(slots, s)
	}
	if _, ok := lim.acquire(key, 2); ok {
		t.Fatal("a lowered limit of 2 admitted a call with 4 in use")
	}
	for _, s := range slots {
		s.release()
	}
	if got := lim.inUse(key); got != 0 {
		t.Fatalf("in use: %d", got)
	}
}

// A bearer token that spells another credential's key never shares its budget.
func TestSharedBearerKeyIsSeparatedFromCookieKeys(t *testing.T) {
	for _, spelled := range []string{"cookie:abc"} {
		if sharedBearerKey(spelled) == sharedKeyFor(spelled) {
			t.Errorf("a token %q shares the key of the credential it spells", spelled)
		}
	}
}
