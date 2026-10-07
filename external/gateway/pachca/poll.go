//go:build gateway || gateway.pachca

package pachca

// Reading the events history.
//
// Pachca keeps every event of the bot in a log the bot reads newest first and
// cleans up itself. A pass walks the log from the top down to the watermark -
// the newest event already handled, kept as (created_at, id) because several
// events can share one millisecond - and only then hands the new events to the
// dispatcher, oldest first. A pass longer than the page budget of one tick
// continues from its cursor on the next tick, and the watermark moves only
// when the pass is complete, so a backlog bigger than one tick is never
// skipped. Each event the dispatcher accepted is deleted and moves the
// watermark; an event that arrives while the bot is stopping is left where it
// is, for the next process.
//
// The very first start has no watermark: the newest event in the log becomes
// it, and older events are left alone, the way Pachca's own SDK helpers start
// "from now". Without the right to delete, the bot still works on the
// watermark alone, and the log keeps growing.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	// eventsPageSize is the largest page Pachca serves.
	eventsPageSize = 50
	// pagesPerTick keeps a tick within Pachca's ~5 requests / 2 s for the
	// events history.
	pagesPerTick = 4
)

// mark is a position in the events history.
type mark struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

func (m mark) empty() bool { return m.ID == "" }

func parseEventTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// after reports whether a is newer than b.
func (a mark) after(b mark) bool {
	ta, tb := parseEventTime(a.CreatedAt), parseEventTime(b.CreatedAt)
	if !ta.Equal(tb) {
		return ta.After(tb)
	}
	return a.ID > b.ID
}

func eventMark(e Event) mark { return mark{CreatedAt: e.CreatedAt, ID: e.ID} }

// pollState is the bot's position in the events history, persisted so a
// restart catches up on what was written while it was down.
type pollState struct {
	path string
	mu   sync.Mutex
	// Watermark is the newest event handled.
	Watermark mark `json:"watermark"`
}

func loadPollState(path string) *pollState {
	st := &pollState{path: path}
	if path == "" {
		return st
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	_ = json.Unmarshal(raw, st)
	return st
}

func (s *pollState) watermark() mark {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Watermark
}

func (s *pollState) advance(m mark) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.Watermark.empty() && !m.after(s.Watermark) {
		return
	}
	s.Watermark = m
	s.saveLocked()
}

func (s *pollState) saveLocked() {
	if s.path == "" {
		return
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, s.path)
}

// pass is a walk down the log that did not finish in one tick.
type pass struct {
	cursor    string
	collected []Event
}

// poller carries what one running bot knows about its walk.
type poller struct {
	b         *Bot
	c         *Client
	cur       *pass
	noDelete  bool
	started   time.Time
	sawEvent  bool
	warnedLog bool
}

// pollLoop reads the history every poll interval until ctx ends. Turns run
// under turnCtx.
func (b *Bot) pollLoop(ctx, turnCtx context.Context, c *Client) error {
	p := &poller{b: b, c: c, started: time.Now()}
	interval := b.cfg.PollInterval()
	if b.pollEvery > 0 {
		interval = b.pollEvery
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := p.tick(ctx, turnCtx); err != nil && ctx.Err() == nil {
			b.log.Warn("pachca: events history", "err", err)
			if IsStatus(err, 401) {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// tick continues the current pass by up to pagesPerTick pages and, when the
// pass reaches the watermark or the end of the log, dispatches what it found.
func (p *poller) tick(ctx, turnCtx context.Context) error {
	b := p.b
	wm := b.state.watermark()
	if p.cur == nil {
		p.cur = &pass{}
	}
	complete := false
	for page := 0; page < pagesPerTick; page++ {
		res, err := p.c.ListEvents(ctx, p.cur.cursor, eventsPageSize)
		if err != nil {
			return err
		}
		if len(res.Events) > 0 {
			p.sawEvent = true
		}
		if wm.empty() {
			// The very first start: the newest event in the log is where
			// this bot begins.
			if len(res.Events) > 0 {
				b.state.advance(eventMark(res.Events[0]))
				b.log.Info("pachca: events history starts after its newest event", "event", res.Events[0].ID)
			} else {
				b.state.advance(mark{CreatedAt: time.Unix(0, 0).UTC().Format(time.RFC3339Nano), ID: "0"})
			}
			p.cur = nil
			return nil
		}
		reached := false
		for _, e := range res.Events {
			if !eventMark(e).after(wm) {
				reached = true
				break
			}
			p.cur.collected = append(p.cur.collected, e)
		}
		if reached || !res.HasNext || res.Next == "" {
			complete = true
			break
		}
		p.cur.cursor = res.Next
	}
	p.warnIfLogStaysEmpty()
	if !complete {
		return nil
	}
	events := p.cur.collected
	p.cur = nil
	sort.SliceStable(events, func(i, j int) bool { return eventMark(events[j]).after(eventMark(events[i])) })
	for _, e := range events {
		if ctx.Err() != nil {
			// Stopping: what is left stays in the history, above the
			// watermark, for the next process.
			return nil
		}
		if !p.b.handleEvent(ctx, turnCtx, p.c, e) {
			return nil
		}
		p.ack(ctx, e)
	}
	return nil
}

// ack deletes a handled event and moves the watermark past it.
func (p *poller) ack(ctx context.Context, e Event) {
	p.b.seen.add(e.ID)
	p.b.state.advance(eventMark(e))
	if p.noDelete {
		return
	}
	if err := p.c.DeleteEvent(ctx, e.ID); err != nil {
		if IsStatus(err, 403) {
			p.noDelete = true
			p.b.log.Warn("pachca: the token may not delete handled events (scope webhooks:events:delete); the history will keep growing")
			return
		}
		p.b.log.Debug("pachca: delete event", "err", err, "event", e.ID)
	}
}

// warnIfLogStaysEmpty says once that an events history which never shows an
// event may be switched off in the bot's settings.
func (p *poller) warnIfLogStaysEmpty() {
	if p.sawEvent || p.warnedLog || time.Since(p.started) < p.b.emptyLogWarnAfter {
		return
	}
	p.warnedLog = true
	p.b.log.Warn("pachca: the events history has stayed empty since the start; check that Save events history is on in the bot's outgoing webhook settings")
}

// seenEvents remembers recently handled event and message ids, so an event
// whose deletion failed, or a message Pachca delivered twice, runs one turn.
type seenEvents struct {
	mu    sync.Mutex
	limit int
	set   map[string]bool
	order []string
}

func newSeenEvents(limit int) *seenEvents {
	return &seenEvents{limit: limit, set: make(map[string]bool)}
}

func (s *seenEvents) has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set[id]
}

// add records id and reports whether it was new.
func (s *seenEvents) add(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.set[id] {
		return false
	}
	s.set[id] = true
	s.order = append(s.order, id)
	if len(s.order) > s.limit {
		delete(s.set, s.order[0])
		s.order = s.order[1:]
	}
	return true
}

// idSet is a bounded set of message ids.
type idSet struct {
	mu    sync.Mutex
	limit int
	set   map[int64]bool
	order []int64
}

func newIDSet(limit int) *idSet { return &idSet{limit: limit, set: make(map[int64]bool)} }

func (s *idSet) add(id int64) {
	if id == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.set[id] {
		return
	}
	s.set[id] = true
	s.order = append(s.order, id)
	if len(s.order) > s.limit {
		delete(s.set, s.order[0])
		s.order = s.order[1:]
	}
}

// forget drops id, so the next question about it reads the message.
func (s *idSet) forget(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.set, id)
}

func (s *idSet) has(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set[id]
}

// parentCache remembers who wrote a message the bot looked up.
type parentCache struct {
	mu    sync.Mutex
	limit int
	by    map[int64]int64
	order []int64
}

func newParentCache(limit int) *parentCache {
	return &parentCache{limit: limit, by: make(map[int64]int64)}
}

func (c *parentCache) get(id int64) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.by[id]
	return v, ok
}

func (c *parentCache) put(id, author int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.by[id]; !ok {
		c.order = append(c.order, id)
	}
	c.by[id] = author
	if len(c.order) > c.limit {
		delete(c.by, c.order[0])
		c.order = c.order[1:]
	}
}

// chatKinds remembers whether a chat is a direct one.
type chatKinds struct {
	mu       sync.Mutex
	personal map[int64]bool
}

func newChatKinds() *chatKinds { return &chatKinds{personal: make(map[int64]bool)} }

// isGroup reports whether chatID is anything but a direct chat. A message
// event whose entity is a user says so itself; anything else is read once. A
// chat the bot may not read counts as a group: there the bot answers only
// when it is addressed, which is the stricter of the two. A lookup that
// failed for a passing reason is returned as an error, so the event stays in
// the history and is read again.
func (b *Bot) isGroup(ctx context.Context, c *Client, chatID int64, entityType string) (bool, error) {
	b.chats.mu.Lock()
	personal, ok := b.chats.personal[chatID]
	b.chats.mu.Unlock()
	if ok {
		return !personal, nil
	}
	if entityType == "user" {
		personal = true
	} else {
		ch, err := c.Chat(ctx, chatID)
		if err != nil {
			b.log.Debug("pachca: read chat", "err", err, "chat", chatID)
			if IsTransient(err) {
				return true, err
			}
			return true, nil
		}
		personal = ch.Personal
	}
	b.chats.mu.Lock()
	b.chats.personal[chatID] = personal
	b.chats.mu.Unlock()
	return !personal, nil
}
