package canvas

import (
	"context"
	"sync"
	"time"
	"unicode/utf8"
)

// Bounds of what a browser's report keeps.
const (
	MaxConsoleEntries = 20
	MaxConsoleMessage = 1000
	MaxReports        = 512
	maxReportText     = 300
)

// ConsoleEntry is one line of a frame's console as the bridge saw it: an error
// or a warning the page logged, or an exception it did not catch.
type ConsoleEntry struct {
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Source  string    `json:"source,omitempty"`
	Line    int       `json:"line,omitempty"`
	Column  int       `json:"column,omitempty"`
	At      time.Time `json:"at,omitempty"`
}

// Report is what a browser that has a frame open says about it: whether the
// page loaded, its title, and the newest console lines, for the files of one
// stamp. It is the agent's way of seeing that its script threw.
type Report struct {
	SessionID string `json:"-"`
	FrameID   string `json:"-"`
	Stamp     string `json:"stamp"`
	Loaded    bool   `json:"loaded"`
	// Closed says the browser put the frame away (the canvas closed, the frame
	// left the board); the rest of the report is what it last saw.
	Closed  bool           `json:"closed,omitempty"`
	Error   string         `json:"error,omitempty"`
	Title   string         `json:"title,omitempty"`
	Console []ConsoleEntry `json:"console"`
	At      time.Time      `json:"at"`
}

type reportKey struct{ session, frame string }

// Reports keeps the newest report per frame, in memory: a report is about a
// browser that has the frame open now, and a restart of Coddy loses nothing a
// reload of that browser does not bring back.
type Reports struct {
	mu      sync.Mutex
	entries map[reportKey]Report
	order   []reportKey
	waiters map[reportKey][]chan struct{}
}

// NewReports returns an empty store.
func NewReports() *Reports {
	return &Reports{entries: map[reportKey]Report{}, waiters: map[reportKey][]chan struct{}{}}
}

var defaultReports = NewReports()

// DefaultReports is the process's store, shared by the HTTP routes that take
// reports and the canvas_inspect tool that reads them.
func DefaultReports() *Reports { return defaultReports }

// Put stores a report, bounded, and wakes whoever waits for that frame.
func (r *Reports) Put(rep Report) {
	if rep.At.IsZero() {
		rep.At = time.Now().UTC()
	}
	rep.Title = cut(rep.Title, maxReportText)
	rep.Error = cut(rep.Error, MaxConsoleMessage)
	if len(rep.Console) > MaxConsoleEntries {
		rep.Console = rep.Console[len(rep.Console)-MaxConsoleEntries:]
	}
	console := make([]ConsoleEntry, len(rep.Console))
	for i, e := range rep.Console {
		e.Level = cut(e.Level, 16)
		e.Message = cut(e.Message, MaxConsoleMessage)
		e.Source = cut(e.Source, maxReportText)
		console[i] = e
	}
	rep.Console = console
	k := reportKey{rep.SessionID, rep.FrameID}
	r.mu.Lock()
	if prev, ok := r.entries[k]; ok && rep.Closed {
		// A close says only that: what the page did stays readable.
		prev.Closed, prev.At = true, rep.At
		rep = prev
	}
	if _, ok := r.entries[k]; !ok {
		r.order = append(r.order, k)
		for len(r.order) > MaxReports {
			old := r.order[0]
			r.order = r.order[1:]
			delete(r.entries, old)
		}
	}
	r.entries[k] = rep
	waiters := r.waiters[k]
	delete(r.waiters, k)
	r.mu.Unlock()
	for _, ch := range waiters {
		close(ch)
	}
}

// Get returns the newest report for a frame.
func (r *Reports) Get(session, frame string) (Report, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rep, ok := r.entries[reportKey{session, frame}]
	return rep, ok
}

// Forget drops a frame's report, when the frame leaves the board.
func (r *Reports) Forget(session, frame string) {
	k := reportKey{session, frame}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, k)
	for i, o := range r.order {
		if o == k {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
}

// Len is the number of reports kept.
func (r *Reports) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// Wait returns the frame's report once one for this stamp is in, or what there
// is when the time runs out or ctx ends; fresh says which.
func (r *Reports) Wait(ctx context.Context, session, frame, stamp string, d time.Duration) (rep Report, fresh bool) {
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	k := reportKey{session, frame}
	for {
		r.mu.Lock()
		rep, ok := r.entries[k]
		if ok && rep.Stamp == stamp {
			r.mu.Unlock()
			return rep, true
		}
		ch := make(chan struct{})
		r.waiters[k] = append(r.waiters[k], ch)
		r.mu.Unlock()
		select {
		case <-ch:
		case <-deadline.C:
			r.dropWaiter(k, ch)
			got, _ := r.Get(session, frame)
			return got, false
		case <-ctx.Done():
			r.dropWaiter(k, ch)
			got, _ := r.Get(session, frame)
			return got, false
		}
	}
}

// dropWaiter forgets a wait that ended without a report, so waits that time
// out while no browser reports do not pile up.
func (r *Reports) dropWaiter(k reportKey, ch chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.waiters[k]
	for i, c := range list {
		if c == ch {
			list = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(list) == 0 {
		delete(r.waiters, k)
	} else {
		r.waiters[k] = list
	}
}

// cut shortens s to at most n bytes on a rune boundary.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
