package shareguard

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestCountersAddAndSortedSnapshot(t *testing.T) {
	clk := newClock()
	c := NewCounters(clk.Now, 0, nil)
	c.Add(Key{"b", "x", "ok"}, Delta{Calls: 1, InputTokens: 10, OutputTokens: 5, DurationMS: 30})
	c.Add(Key{"a", "x", "ok"}, Delta{Calls: 1})
	c.Add(Key{"b", "x", "ok"}, Delta{Calls: 1, InputTokens: 1, OutputTokens: 2, DurationMS: 90})
	snap := c.Snapshot()
	if len(snap.Rows) != 2 {
		t.Fatalf("rows = %d", len(snap.Rows))
	}
	if snap.Rows[0].Key != (Key{"a", "x", "ok"}) || snap.Rows[1].Key != (Key{"b", "x", "ok"}) {
		t.Fatalf("rows not sorted: %+v", snap.Rows)
	}
	b := snap.Rows[1]
	if b.Calls != 2 || b.InputTokens != 11 || b.OutputTokens != 7 || b.DurationMS != 120 || b.MaxDurationMS != 90 {
		t.Fatalf("row b = %+v", b)
	}
	if !snap.Since.Equal(clk.Now()) {
		t.Fatalf("since = %v", snap.Since)
	}
	// A snapshot is a copy.
	snap.Rows[0].Calls = 99
	if c.Snapshot().Rows[0].Calls == 99 {
		t.Fatal("the snapshot aliases the counters")
	}
}

func TestCountersSnapshotIsStable(t *testing.T) {
	c := NewCounters(newClock().Now, 0, nil)
	for i := 0; i < 20; i++ {
		c.Add(Key{fmt.Sprint(i % 5), "n", "ok"}, Delta{Calls: 1})
	}
	a, b := c.Snapshot(), c.Snapshot()
	for i := range a.Rows {
		if a.Rows[i] != b.Rows[i] {
			t.Fatal("two snapshots of the same state differ")
		}
	}
}

func TestCountersFoldOverflow(t *testing.T) {
	fold := func(k Key) Key { k[1] = "-"; return k }
	c := NewCounters(newClock().Now, 3, fold)
	for i := 0; i < 50; i++ {
		c.Add(Key{"client", fmt.Sprintf("node%d", i), "ok"}, Delta{Calls: 1})
	}
	snap := c.Snapshot()
	if len(snap.Rows) > 4 {
		t.Fatalf("rows = %d, want the cap of 3 plus the folded row", len(snap.Rows))
	}
	var total int64
	for _, r := range snap.Rows {
		total += r.Calls
	}
	if total != 50 {
		t.Fatalf("calls = %d, folding must not lose counts", total)
	}
	last := snap.Rows[0]
	for _, r := range snap.Rows {
		if r.Key[1] == "-" {
			last = r
		}
	}
	if last.Key[1] != "-" || last.Calls != 47 {
		t.Fatalf("folded row = %+v", last)
	}
}

func TestCountersConcurrentAdd(t *testing.T) {
	c := NewCounters(time.Now, 0, nil)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				c.Add(Key{"a", "b", "ok"}, Delta{Calls: 1, DurationMS: 2})
				_ = c.Snapshot()
			}
		}()
	}
	wg.Wait()
	if got := c.Snapshot().Rows[0].Calls; got != 16*500 {
		t.Fatalf("calls = %d", got)
	}
}
