package shareguard

import (
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *fakeClock { return &fakeClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func TestTakeAllowsABurstThenRefuses(t *testing.T) {
	clk := newClock()
	l := NewLimiter(clk.Now)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Take("k", 60, 3); !ok {
			t.Fatalf("call %d of the burst refused", i+1)
		}
	}
	ok, retry := l.Take("k", 60, 3)
	if ok {
		t.Fatal("the call past the burst was admitted")
	}
	if retry != time.Second {
		t.Fatalf("retryAfter = %v, want exactly one emission interval (1s)", retry)
	}
}

func TestTakeRefillsAtTheRate(t *testing.T) {
	clk := newClock()
	l := NewLimiter(clk.Now)
	l.Take("k", 60, 1)
	if ok, retry := l.Take("k", 60, 1); ok || retry != time.Second {
		t.Fatalf("ok=%v retry=%v, want a refusal for 1s", ok, retry)
	}
	clk.Advance(400 * time.Millisecond)
	if ok, retry := l.Take("k", 60, 1); ok || retry != 600*time.Millisecond {
		t.Fatalf("ok=%v retry=%v, want a refusal for 600ms", ok, retry)
	}
	clk.Advance(600 * time.Millisecond)
	if ok, _ := l.Take("k", 60, 1); !ok {
		t.Fatal("the token was not refilled after the interval")
	}
}

func TestRefusalSpendsNothing(t *testing.T) {
	clk := newClock()
	l := NewLimiter(clk.Now)
	l.Take("k", 60, 1)
	for i := 0; i < 50; i++ {
		l.Take("k", 60, 1) // refused attempts must not push the window out
	}
	clk.Advance(time.Second)
	if ok, _ := l.Take("k", 60, 1); !ok {
		t.Fatal("refused attempts moved the arrival time")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l := NewLimiter(newClock().Now)
	l.Take("a", 1, 1)
	if ok, _ := l.Take("b", 1, 1); !ok {
		t.Fatal("key b was limited by key a")
	}
}

func TestZeroRateIsOff(t *testing.T) {
	l := NewLimiter(newClock().Now)
	for i := 0; i < 1000; i++ {
		if ok, _ := l.Take("k", 0, 0); !ok {
			t.Fatal("a limiter with no rate refused a call")
		}
	}
	if l.Len() != 0 {
		t.Fatal("an off limiter stored state")
	}
}

func TestBurstBelowOneIsOne(t *testing.T) {
	l := NewLimiter(newClock().Now)
	if ok, _ := l.Take("k", 60, 0); !ok {
		t.Fatal("the first call was refused")
	}
	if ok, _ := l.Take("k", 60, 0); ok {
		t.Fatal("a burst of 0 admitted two calls")
	}
}

func TestParameterChangeBetweenCalls(t *testing.T) {
	clk := newClock()
	l := NewLimiter(clk.Now)
	// Written under 1 call a minute: the stored arrival time sits a minute ahead.
	l.Take("k", 1, 1)
	// Reloaded at 60 a minute: the clamp must not keep the old minute.
	clk.Advance(time.Second)
	if ok, _ := l.Take("k", 60, 1); !ok {
		t.Fatal("a raised rate kept refusing on state written under the old rate")
	}
	// Lowered: 60 a minute to 1 a minute applies to the next call.
	l.Take("k", 60, 1)
	if ok, retry := l.Take("k", 1, 1); ok || retry <= 0 || retry > time.Minute {
		t.Fatalf("ok=%v retry=%v after a lowered rate", ok, retry)
	}
}

func TestRefundGivesTheTokenBack(t *testing.T) {
	clk := newClock()
	l := NewLimiter(clk.Now)
	if ok, _ := l.Take("k", 60, 1); !ok {
		t.Fatal("first call refused")
	}
	l.Refund("k", 60)
	if ok, _ := l.Take("k", 60, 1); !ok {
		t.Fatal("a refunded token was not usable")
	}
}

func TestRefundNeverPassesNow(t *testing.T) {
	clk := newClock()
	l := NewLimiter(clk.Now)
	l.Take("k", 60, 3)
	for i := 0; i < 10; i++ {
		l.Refund("k", 60)
	}
	// A bucket cannot hold more than its burst, whatever was refunded.
	admitted := 0
	for i := 0; i < 10; i++ {
		if ok, _ := l.Take("k", 60, 3); ok {
			admitted++
		}
	}
	if admitted != 3 {
		t.Fatalf("admitted %d after over-refunding, want the burst of 3", admitted)
	}
	l.Refund("unknown", 60) // no panic, no state
}

func TestRefundLoopKeepsTheBucket(t *testing.T) {
	// A client waiting out a node's busy at one request a second: each attempt
	// is refunded, so the bucket never drains.
	clk := newClock()
	l := NewLimiter(clk.Now)
	for i := 0; i < 100; i++ {
		if ok, _ := l.Take("k", 6, 2); !ok {
			t.Fatalf("attempt %d refused although every earlier one was refunded", i)
		}
		l.Refund("k", 6)
		clk.Advance(time.Second)
	}
}

func TestPruneDropsRefilledKeys(t *testing.T) {
	clk := newClock()
	l := NewLimiter(clk.Now)
	l.Take("a", 60, 1)
	l.Take("b", 60, 1)
	if l.Len() != 2 {
		t.Fatalf("len = %d", l.Len())
	}
	l.Prune(0)
	if l.Len() != 2 {
		t.Fatal("pruned a key still inside its window")
	}
	clk.Advance(2 * time.Second)
	l.Prune(time.Second)
	if l.Len() != 0 {
		t.Fatalf("len = %d after the keys refilled and idled", l.Len())
	}
}

func TestConcurrentTakersNeverExceedTheBudget(t *testing.T) {
	clk := newClock()
	l := NewLimiter(clk.Now)
	const perMinute, burst = 60, 5
	var admitted int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if ok, _ := l.Take("k", perMinute, burst); ok {
					mu.Lock()
					admitted++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if admitted != burst {
		t.Fatalf("admitted %d with a frozen clock, want exactly the burst %d", admitted, burst)
	}
}
