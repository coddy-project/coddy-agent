package web

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// cacheMaxEntries bounds the shared cache. Search answers are small and a
// session asks a bounded number of distinct questions, so the ceiling exists to
// keep a long-running serve process from growing without limit rather than to
// ration anything.
const cacheMaxEntries = 256

// engineCache remembers what one engine answered for one query. A model that
// reaches for the same search twice - the second call a page further down, or
// the same call after a detour - is common, and the engines here are public
// pages being read by a scraper: asking them twice for the same answer within
// a few minutes is both slower and worse manners than remembering the first.
//
// Failures are cached too, for a much shorter time: an engine serving a
// challenge page serves it to the next call as well, and re-asking inside one
// turn only adds latency. The short window is what lets a recovering engine be
// noticed quickly.
type engineCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	rows    []Result
	err     error
	expires time.Time
}

var searchCache = &engineCache{entries: make(map[string]cacheEntry)}

// blockedCacheTTL is how long a blocked or failed answer is remembered. It is
// deliberately far below the TTL of a good answer.
const blockedCacheTTL = 30 * time.Second

// cacheKey identifies one engine's answer to one query. Settings that change
// what an engine returns are part of it: two engines asked with different
// SearXNG instances or with and without a Brave key are not the same request.
func cacheKey(engine string, q Query, s Settings) string {
	var b strings.Builder
	b.WriteString(engine)
	b.WriteByte('\n')
	b.WriteString(strings.ToLower(strings.Join(strings.Fields(q.Text), " ")))
	b.WriteByte('\n')
	b.WriteString(strconv.Itoa(q.Page))
	b.WriteByte('\n')
	b.WriteString(strconv.Itoa(q.MaxResults))
	b.WriteByte('\n')
	b.WriteString(strings.ToLower(strings.TrimSpace(q.Site)))
	b.WriteByte('\n')
	b.WriteString(s.SearXNGURL)
	b.WriteByte('\n')
	// The key itself never enters the cache key; whether one is set does.
	if strings.TrimSpace(s.BraveAPIKey) != "" {
		b.WriteString("keyed")
	}
	return b.String()
}

// get returns a remembered answer when one is still fresh.
func (c *engineCache) get(key string) ([]Result, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, nil, false
	}
	if time.Now().After(e.expires) {
		delete(c.entries, key)
		return nil, nil, false
	}
	return e.rows, e.err, true
}

// put remembers one engine's answer. A cancelled call is never stored: the
// answer says nothing about the engine, only about the turn that ended.
func (c *engineCache) put(key string, rows []Result, err error, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	if err != nil {
		if _, isBlocked := asBlocked(err); !isBlocked {
			return
		}
		if ttl > blockedCacheTTL {
			ttl = blockedCacheTTL
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= cacheMaxEntries {
		// Nothing here is worth an eviction policy: the entries expire on
		// their own, and dropping the whole map on overflow costs one extra
		// round of searches at worst.
		c.entries = make(map[string]cacheEntry, cacheMaxEntries)
	}
	c.entries[key] = cacheEntry{rows: rows, err: err, expires: time.Now().Add(ttl)}
}

// reset empties the cache. Tests call it so one scenario cannot answer another.
func (c *engineCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]cacheEntry)
}

// CacheTTL is how long a good answer is remembered, from the settings.
func (s Settings) CacheTTL() time.Duration {
	if s.CacheTTLSeconds < 0 {
		return 0
	}
	if s.CacheTTLSeconds > 0 {
		return time.Duration(s.CacheTTLSeconds) * time.Second
	}
	return time.Duration(DefaultCacheTTLSeconds) * time.Second
}
