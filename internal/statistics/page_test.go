package statistics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func monthsBack(n int) time.Time {
	now := time.Now().UTC()
	first := time.Date(now.Year(), now.Month(), 15, 12, 0, 0, 0, time.UTC)
	return first.AddDate(0, -n, 0)
}

// A period names the journals it can be answered from, so a screen showing
// the last few days opens one file rather than years of them. The journal of
// an old month here carries a checkpoint dated inside the period, which only
// a read that never opened that file can leave out.
func TestEventsReadOnlyTheJournalsOfThePeriod(t *testing.T) {
	s := New(t.TempDir())
	recent, old := monthsBack(0), monthsBack(3)
	if err := s.Append(Event{ID: "recent", Kind: "call", At: recent, Session: "chat", Input: Int(100), Output: Int(20)}); err != nil {
		t.Fatal(err)
	}
	stray, err := json.Marshal(Event{ID: "stray", Kind: "call", At: recent, Session: "chat", Input: Int(7000)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, old.Format("2006-01")+".jsonl"), append(stray, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	day := recent.Format("2006-01-02")
	lower, upper := Filter{From: day, To: day}.window()
	if lower != monthOffset(day, -1) || upper != monthOffset(day, 1) {
		t.Fatalf("a period reaches a month either way for the reader's zone: %q %q", lower, upper)
	}
	narrow, err := s.events(time.Time{}, lower, upper)
	if err != nil {
		t.Fatal(err)
	}
	if len(narrow) != 1 || narrow[0].ID != "recent" {
		t.Fatalf("the read opened a journal outside the period: %+v", narrow)
	}
	whole, err := s.events(time.Time{}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) != 2 {
		t.Fatalf("an unbounded read opens every journal: %+v", whole)
	}
}

// A line nothing can read costs its own checkpoint, not the history around
// it: a journal is counters, and refusing the file would leave every screen
// and every relay with nothing until somebody edited it by hand.
func TestJournalsSurviveALineNothingCanRead(t *testing.T) {
	s := New(t.TempDir())
	at := time.Now().UTC()
	if err := s.Append(Event{ID: "one", Kind: "call", At: at, Session: "chat", Input: Int(100), Output: Int(20)}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir, at.Format("2006-01")+".jsonl")
	whole, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append([]byte("{not json at all\n"), whole...), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := s.Query(Filter{Timezone: "UTC"})
	if err != nil {
		t.Fatalf("one unreadable line refused the whole journal: %v", err)
	}
	if report.Totals.Total == nil || *report.Totals.Total != 120 {
		t.Fatalf("the checkpoints around it were lost too: %+v", report.Totals)
	}
}

// The lifetime summary is read once and handed out again until something is
// appended, since a screen asks for it beside every filtered report it draws.
func TestPageKeepsTheLifetimeSummaryUntilAJournalMoves(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	at := time.Now().UTC()
	if err := s.Append(Event{ID: "one", Kind: "call", At: at, Session: "chat", Input: Int(100), Output: Int(20)}); err != nil {
		t.Fatal(err)
	}
	day := at.Format("2006-01-02")
	if _, lifetime, err := s.Page(Filter{Timezone: "UTC", From: day, To: day}); err != nil || lifetime.Totals.Total == nil || *lifetime.Totals.Total != 120 {
		t.Fatalf("first read: %v %+v", err, lifetime.Totals)
	}
	if err := s.Append(Event{ID: "two", Kind: "call", At: at, Session: "chat", Input: Int(10), Output: Int(2)}); err != nil {
		t.Fatal(err)
	}
	_, lifetime, err := s.Page(Filter{Timezone: "UTC", From: day, To: day})
	if err != nil {
		t.Fatal(err)
	}
	if lifetime.Totals.Total == nil || *lifetime.Totals.Total != 132 {
		t.Fatalf("an append did not reach the lifetime summary: %+v", lifetime.Totals)
	}
}

// The zone the report is laid out in is part of what the kept summary answers
// for: the same journals in another zone are other days.
func TestPageKeepsTheLifetimeSummaryPerZone(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	at := time.Date(time.Now().UTC().Year(), time.Now().UTC().Month(), 10, 22, 0, 0, 0, time.UTC)
	if err := s.Append(Event{ID: "one", Kind: "call", At: at, Session: "chat", Input: Int(100), Output: Int(20)}); err != nil {
		t.Fatal(err)
	}
	day := at.Format("2006-01-02")
	_, utc, err := s.Page(Filter{Timezone: "UTC", From: day, To: day})
	if err != nil {
		t.Fatal(err)
	}
	_, sydney, err := s.Page(Filter{Timezone: "Australia/Sydney", From: day, To: day})
	if err != nil {
		t.Fatal(err)
	}
	if len(utc.Days) != 1 || len(sydney.Days) != 1 || utc.Days[0].Date == sydney.Days[0].Date {
		t.Fatalf("the kept summary was handed out in the wrong zone: %v vs %v", utc.Days, sydney.Days)
	}
}
