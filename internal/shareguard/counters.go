package shareguard

import (
	"sort"
	"sync"
	"time"
)

// Key is the three labels of a counter row. The caller chooses them, so the
// cardinality of the map is the caller's contract; never put a credential or
// content in one.
type Key [3]string

// Delta is what one event adds to a row.
type Delta struct {
	Calls        int64
	InputTokens  int64
	OutputTokens int64
	DurationMS   int64
}

// Row is one counter row.
type Row struct {
	Key           Key
	Calls         int64
	InputTokens   int64
	OutputTokens  int64
	DurationMS    int64
	MaxDurationMS int64
}

// Snapshot is a sorted copy of the rows and the moment counting began.
type Snapshot struct {
	Since time.Time
	Rows  []Row
}

// Counters is a mutex-guarded in-memory map of rows.
type Counters struct {
	since   time.Time
	maxRows int
	fold    func(Key) Key

	mu   sync.Mutex
	rows map[Key]*Row
}

// NewCounters returns counters that start at now(). With maxRows > 0 a key that
// would be the maxRows+1-th row is replaced by fold(key) first, so counts are
// never lost and the rows stay bounded as long as fold maps into a bounded set.
func NewCounters(now func() time.Time, maxRows int, fold func(Key) Key) *Counters {
	if now == nil {
		now = time.Now
	}
	return &Counters{since: now(), maxRows: maxRows, fold: fold, rows: map[Key]*Row{}}
}

// Add accumulates d into the row of k.
func (c *Counters) Add(k Key, d Delta) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.rows[k]
	if !ok && c.maxRows > 0 && len(c.rows) >= c.maxRows && c.fold != nil {
		k = c.fold(k)
		r, ok = c.rows[k]
	}
	if !ok {
		r = &Row{Key: k}
		c.rows[k] = r
	}
	r.Calls += d.Calls
	r.InputTokens += d.InputTokens
	r.OutputTokens += d.OutputTokens
	r.DurationMS += d.DurationMS
	if d.DurationMS > r.MaxDurationMS {
		r.MaxDurationMS = d.DurationMS
	}
}

// Snapshot returns the rows sorted by key.
func (c *Counters) Snapshot() Snapshot {
	c.mu.Lock()
	out := Snapshot{Since: c.since, Rows: make([]Row, 0, len(c.rows))}
	for _, r := range c.rows {
		out.Rows = append(out.Rows, *r)
	}
	c.mu.Unlock()
	sort.Slice(out.Rows, func(i, j int) bool {
		a, b := out.Rows[i].Key, out.Rows[j].Key
		for n := range a {
			if a[n] != b[n] {
				return a[n] < b[n]
			}
		}
		return false
	})
	return out
}
