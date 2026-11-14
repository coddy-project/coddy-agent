package statistics

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStatisticsJournalRecoversAnInterruptedAppend(t *testing.T) {
	s := New(t.TempDir())
	at := time.Now().UTC()
	if err := s.Append(Event{ID: "first", Kind: "turn", At: at, Requests: 1}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, at.Format("2006-01")+".jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"unfinished`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(Event{ID: "second", Kind: "turn", At: at, Requests: 1}); err != nil {
		t.Fatal(err)
	}
	r, err := s.Query(Filter{})
	if err != nil || r.Totals.Requests != 2 {
		t.Fatalf("journal recovery: %+v %v", r, err)
	}
}

func TestStatisticsConcurrentWritersKeepEveryCall(t *testing.T) {
	home := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := New(home).Append(Event{ID: time.Unix(int64(i), 0).String(), Kind: "call", At: time.Now(), Input: Int(100), Output: Int(20)}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	r, err := New(home).Query(Filter{})
	if err != nil || r.Totals.Calls != 16 || r.Totals.Total == nil || *r.Totals.Total != 1920 {
		t.Fatalf("concurrent history: %+v %v", r, err)
	}
}

func TestStatisticsPauseExcludesOverlappingHumanWaits(t *testing.T) {
	s := New(t.TempDir())
	ctx, r := Begin(context.Background(), s, Event{Session: "s", Requests: 1}, func(err error) { t.Error(err) })
	pauseOne := Pause(ctx)
	pauseTwo := Pause(ctx)
	// Move the clock under the recorder's lock so this test has no sleep or
	// timing threshold: overlapping waits must be subtracted only once.
	r.mu.Lock()
	r.started = time.Now().Add(-3 * time.Second)
	r.pausedAt = time.Now().Add(-2 * time.Second)
	r.mu.Unlock()
	pauseOne()
	pauseTwo()
	pauseTwo()
	r.Finish()
	report, err := s.Query(Filter{})
	if err != nil || report.Totals.ActiveMS == nil || *report.Totals.ActiveMS < 900 || *report.Totals.ActiveMS > 1300 {
		t.Fatalf("human wait: %+v %v", report.Totals, err)
	}
}

func TestStatisticsUsesNewestCheckpointAcrossSwarmRoutes(t *testing.T) {
	now := time.Now()
	e := Event{ID: "turn", Dataset: "d", Session: "s", Kind: "turn", At: now, RecordedAt: now, Requests: 1}
	finished := e
	finished.RecordedAt = now.Add(time.Second)
	finished.ActiveMS = Int(1234)
	for _, events := range [][]Event{{e, finished}, {finished, e}} {
		r, err := Summarize(events, Filter{}, nil, now)
		if err != nil || r.Totals.Requests != 1 || r.Totals.LongestMS == nil || *r.Totals.LongestMS != 1234 {
			t.Fatalf("checkpoint merge: %+v %v", r.Totals, err)
		}
	}
}

func TestHistorySurvivesRestartAndDeduplicatesImport(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	at := time.Date(2026, 10, 31, 22, 30, 0, 0, time.UTC)
	e := Event{ID: "one", Session: "deleted-chat", Project: "/project", Source: "webui", Kind: "turn", At: at, Requests: 1, ActiveMS: Int(1200)}
	if err := s.Append(e); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(e); err != nil {
		t.Fatal(err)
	}
	got, err := New(dir).Query(Filter{Timezone: "Europe/Moscow"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Totals.Requests != 1 || got.Totals.Chats != 1 || len(got.Days) != 1 || got.Days[0].Date != "2026-11-01" {
		t.Fatalf("unexpected history: %+v", got)
	}
}

func TestCleanupCannotResurrectImportedHistory(t *testing.T) {
	s := New(t.TempDir())
	old := time.Now().Add(-10 * 24 * time.Hour)
	if err := s.Append(Event{ID: "old", Kind: "turn", At: old, Requests: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(time.Now().Add(-24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(Event{ID: "old-import", Kind: "turn", At: old, Requests: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Query(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Totals.Requests != 0 {
		t.Fatalf("cleared history reappeared: %+v", got)
	}
}

func TestMissingUsageIsUnknownAndCacheIsNotCountedTwice(t *testing.T) {
	s := New(t.TempDir())
	at := time.Now().UTC()
	for _, e := range []Event{
		{ID: "turn", Kind: "turn", At: at, Session: "s", Requests: 1},
		{ID: "call", Kind: "call", At: at, Session: "s", Input: Int(100), Output: Int(20), Cached: Int(50)},
	} {
		if err := s.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Query(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Totals.Total == nil || *got.Totals.Total != 120 || got.Totals.LongestMS != nil {
		t.Fatalf("incorrect totals: %+v", got.Totals)
	}
}
