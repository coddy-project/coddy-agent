package web

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestCacheSparesTheEngineASecondIdenticalCall: a model that reaches for the
// same search twice must not make the engine answer twice.
func TestCacheSparesTheEngineASecondIdenticalCall(t *testing.T) {
	stubEngines(t)
	calls := 0
	braveSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		calls++
		return []Result{{Title: "Go", URL: "https://go.dev"}}, nil
	}
	settings := Settings{Engines: []string{"brave"}}
	q := Query{Text: "golang context", Page: 1, MaxResults: 10}

	first := runEngines(context.Background(), []string{"brave"}, q, settings)
	second := runEngines(context.Background(), []string{"brave"}, q, settings)

	if calls != 1 {
		t.Fatalf("engine asked %d times, expected 1", calls)
	}
	if len(first[0].rows) != 1 || len(second[0].rows) != 1 {
		t.Fatalf("cached answer lost its rows: %+v / %+v", first[0].rows, second[0].rows)
	}
	if !second[0].report.Cached {
		t.Error("the second answer should be marked as cached")
	}
	if first[0].report.Cached {
		t.Error("the first answer was not cached")
	}
}

func TestCacheKeepsDifferentQueriesApart(t *testing.T) {
	stubEngines(t)
	calls := 0
	braveSearchFunc = func(_ context.Context, q Query, _ Settings) ([]Result, error) {
		calls++
		return []Result{{Title: q.Text, URL: "https://example.com/" + fmt.Sprint(calls)}}, nil
	}
	settings := Settings{}
	for _, q := range []Query{
		{Text: "golang context", Page: 1, MaxResults: 10},
		{Text: "golang context", Page: 2, MaxResults: 10},
		{Text: "rust async", Page: 1, MaxResults: 10},
		{Text: "golang context", Page: 1, MaxResults: 10, Site: "go.dev"},
	} {
		runEngines(context.Background(), []string{"brave"}, q, settings)
	}
	if calls != 4 {
		t.Fatalf("expected 4 distinct calls, got %d", calls)
	}
}

// TestCacheNormalisesTheQueryText: the same question typed with different
// spacing or capitalisation is the same question.
func TestCacheNormalisesTheQueryText(t *testing.T) {
	stubEngines(t)
	calls := 0
	braveSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		calls++
		return []Result{{Title: "Go", URL: "https://go.dev"}}, nil
	}
	settings := Settings{}
	runEngines(context.Background(), []string{"brave"}, Query{Text: "Golang  Context", Page: 1, MaxResults: 10}, settings)
	runEngines(context.Background(), []string{"brave"}, Query{Text: "golang context", Page: 1, MaxResults: 10}, settings)
	if calls != 1 {
		t.Fatalf("engine asked %d times for one question", calls)
	}
}

// TestCacheRemembersABlockedEngineOnlyBriefly: an engine serving a challenge
// serves it to the next call too, so not re-asking inside a turn is right; the
// short window is what lets a recovering engine be noticed.
func TestCacheRemembersABlockedEngineOnlyBriefly(t *testing.T) {
	searchCache.reset()
	t.Cleanup(searchCache.reset)
	key := "k"
	searchCache.put(key, nil, blocked("anti-bot"), time.Hour)
	_, _, ok := searchCache.get(key)
	if !ok {
		t.Fatal("a blocked answer should be remembered")
	}
	// The stored deadline is capped well below the hour that was asked for.
	searchCache.mu.Lock()
	entry := searchCache.entries[key]
	searchCache.mu.Unlock()
	if remaining := time.Until(entry.expires); remaining > blockedCacheTTL+time.Second {
		t.Fatalf("a blocked answer is remembered for %s, expected at most %s", remaining, blockedCacheTTL)
	}
}

// TestCacheRemembersAnUnreachableEngineVeryBriefly: an engine that is simply
// down otherwise costs a full per-engine timeout on every search, which is the
// most common failure of all. It is remembered for seconds, not minutes.
func TestCacheRemembersAnUnreachableEngineVeryBriefly(t *testing.T) {
	searchCache.reset()
	t.Cleanup(searchCache.reset)
	searchCache.put("k", nil, fmt.Errorf("dial tcp: connection refused"), time.Hour)
	if _, _, ok := searchCache.get("k"); !ok {
		t.Fatal("an unreachable engine should be remembered briefly")
	}
	searchCache.mu.Lock()
	entry := searchCache.entries["k"]
	searchCache.mu.Unlock()
	if remaining := time.Until(entry.expires); remaining > errorCacheTTL+time.Second {
		t.Fatalf("remembered for %s, expected at most %s", remaining, errorCacheTTL)
	}
	if remaining := time.Until(entry.expires); remaining > blockedCacheTTL {
		t.Fatal("an unreachable engine must be forgotten sooner than a blocked one")
	}
}

func TestCacheOffWhenTTLIsNegative(t *testing.T) {
	stubEngines(t)
	calls := 0
	braveSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		calls++
		return []Result{{Title: "Go", URL: "https://go.dev"}}, nil
	}
	settings := Settings{CacheTTLSeconds: -1}
	q := Query{Text: "golang context", Page: 1, MaxResults: 10}
	runEngines(context.Background(), []string{"brave"}, q, settings)
	runEngines(context.Background(), []string{"brave"}, q, settings)
	if calls != 2 {
		t.Fatalf("caching was asked to be off, engine asked %d times", calls)
	}
}

func TestCacheDropsEverythingOnOverflowRatherThanGrowing(t *testing.T) {
	searchCache.reset()
	t.Cleanup(searchCache.reset)
	for i := 0; i < cacheMaxEntries+10; i++ {
		searchCache.put(fmt.Sprintf("key-%d", i), []Result{{URL: "https://example.com"}}, nil, time.Minute)
	}
	searchCache.mu.Lock()
	size := len(searchCache.entries)
	searchCache.mu.Unlock()
	if size > cacheMaxEntries {
		t.Fatalf("cache grew past its ceiling: %d entries", size)
	}
}
