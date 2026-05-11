//go:build scheduler

package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseCronUTC_MatchesStandardFiveField(t *testing.T) {
	_, err := ParseCronUTC("0 9 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}
}

func TestScheduleMinimumInterval_EveryMinute(t *testing.T) {
	s, err := ParseCronUTC("* * * * *")
	if err != nil {
		t.Fatal(err)
	}
	if g := ScheduleMinimumInterval(s); g != time.Minute {
		t.Fatalf("got %v want 1m", g)
	}
}

func TestScheduleMinimumInterval_Hourly(t *testing.T) {
	s, err := ParseCronUTC("0 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	if g := ScheduleMinimumInterval(s); g != time.Hour {
		t.Fatalf("got %v want 1h", g)
	}
}

func TestScheduleMinimumInterval_EveryTwoMinutes(t *testing.T) {
	s, err := ParseCronUTC("*/2 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	if g := ScheduleMinimumInterval(s); g != 2*time.Minute {
		t.Fatalf("got %v want 2m", g)
	}
}

func TestTruncateUTCToMinute(t *testing.T) {
	raw, err := time.Parse(time.RFC3339Nano, "2026-05-12T14:35:44.123456789+02:00")
	if err != nil {
		t.Fatal(err)
	}
	got := TruncateUTCToMinute(raw)
	want, err := time.Parse(time.RFC3339, "2026-05-12T12:35:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Fatalf("got %s want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestCronMinuteMatchesUTC_StepThreeMinutes(t *testing.T) {
	s, err := ParseCronUTC("*/3 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	for _, minute := range []int{0, 3, 6, 9, 57} {
		m := time.Date(2026, 5, 12, 8, minute, 0, 0, time.UTC)
		if !CronMinuteMatchesUTC(s, m) {
			t.Fatalf("minute %d should match */3", minute)
		}
	}
	for _, minute := range []int{1, 2, 4, 5, 7} {
		m := time.Date(2026, 5, 12, 8, minute, 0, 0, time.UTC)
		if CronMinuteMatchesUTC(s, m) {
			t.Fatalf("minute %d should not match */3", minute)
		}
	}
}

func TestCronMinuteMatchesUTC_AtMinute15(t *testing.T) {
	s, err := ParseCronUTC("15 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := time.Parse(time.RFC3339, "2026-05-12T11:15:00Z")
	if err != nil {
		t.Fatal(err)
	}
	bad, err := time.Parse(time.RFC3339, "2026-05-12T11:16:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if !CronMinuteMatchesUTC(s, ok) {
		t.Fatal("expected fire at minute 15")
	}
	if CronMinuteMatchesUTC(s, bad) {
		t.Fatal("expected no fire at minute 16")
	}
}

func TestCronJobEligibleForMinute_StaleCheckpointIgnored(t *testing.T) {
	s, err := ParseCronUTC("* * * * *")
	if err != nil {
		t.Fatal(err)
	}
	stale, err := time.Parse(time.RFC3339, "1970-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	eval, err := time.Parse(time.RFC3339, "2026-05-12T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if !CronJobEligibleForMinute(s, stale, eval) {
		t.Fatal("stale checkpoint should not block current minute")
	}
}

func TestDueFireSlotUTC_StepEveryTwoMinutesUsesClockGrid(t *testing.T) {
	s, err := ParseCronUTC("*/2 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	last, err := time.Parse(time.RFC3339, "2026-05-12T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339, "2026-05-12T10:03:45Z")
	if err != nil {
		t.Fatal(err)
	}
	slot := DueFireSlotUTC(s, last, now)
	if want := "2026-05-12T10:02:00Z"; slot.Format(time.RFC3339) != want {
		t.Fatalf("got %s want %s", slot.Format(time.RFC3339), want)
	}
}

func TestDueFireSlotUTC_StepEveryFiveMinutesUsesClockGrid(t *testing.T) {
	s, err := ParseCronUTC("*/5 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	last, err := time.Parse(time.RFC3339, "2026-05-12T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339, "2026-05-12T10:12:30Z")
	if err != nil {
		t.Fatal(err)
	}
	slot := DueFireSlotUTC(s, last, now)
	// Single Next(last): first scheduled instant strictly after last checkpoint.
	if want := "2026-05-12T10:05:00Z"; slot.Format(time.RFC3339) != want {
		t.Fatalf("got %s want %s", slot.Format(time.RFC3339), want)
	}
}

func TestNextScheduledUTCHourly(t *testing.T) {
	s, err := ParseCronUTC("0 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	last, err := time.Parse(time.RFC3339, "2020-01-15T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	next := NextScheduledUTC(s, last)
	if want := "2020-01-15T11:00:00Z"; next.Format(time.RFC3339) != want {
		t.Fatalf("got %s want %s", next.Format(time.RFC3339), want)
	}
}

func TestNextScheduledDisplayUTC_NoLastUsesNowNotEpoch(t *testing.T) {
	s, err := ParseCronUTC("0 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339, "2026-05-11T22:15:00Z")
	if err != nil {
		t.Fatal(err)
	}
	next := NextScheduledDisplayUTC(s, time.Time{}, now)
	if want := "2026-05-11T23:00:00Z"; next.Format(time.RFC3339) != want {
		t.Fatalf("got %s want %s", next.Format(time.RFC3339), want)
	}
	if !next.After(now) {
		t.Fatalf("display next should be after now, got %v now %v", next, now)
	}
}

func TestDueFireSlotUTC_NoCheckpointWaitsForNextSlot(t *testing.T) {
	s, err := ParseCronUTC("0 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339, "2026-05-11T14:35:20Z")
	if err != nil {
		t.Fatal(err)
	}
	slot := DueFireSlotUTC(s, time.Time{}, now)
	if want := "2026-05-11T15:00:00Z"; slot.Format(time.RFC3339) != want {
		t.Fatalf("got %s want %s", slot.Format(time.RFC3339), want)
	}
	if !slot.After(now) {
		t.Fatalf("slot should be after now before the hour boundary")
	}
}

func TestDueFireSlotUTC_NoCheckpointFiresSameHourBoundary(t *testing.T) {
	s, err := ParseCronUTC("0 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339, "2026-05-11T15:00:30Z")
	if err != nil {
		t.Fatal(err)
	}
	slot := DueFireSlotUTC(s, time.Time{}, now)
	if want := "2026-05-11T15:00:00Z"; slot.Format(time.RFC3339) != want {
		t.Fatalf("got %s want %s", slot.Format(time.RFC3339), want)
	}
	if slot.After(now) {
		t.Fatalf("slot should be due on or before now")
	}
}

func TestDueFireSlotUTC_StaleEpochCheckpointUsesWallClock(t *testing.T) {
	s, err := ParseCronUTC("0 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	last, err := time.Parse(time.RFC3339, "1970-01-01T01:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339, "2026-05-11T14:35:20Z")
	if err != nil {
		t.Fatal(err)
	}
	slot := DueFireSlotUTC(s, last, now)
	if want := "2026-05-11T15:00:00Z"; slot.Format(time.RFC3339) != want {
		t.Fatalf("got %s want %s", slot.Format(time.RFC3339), want)
	}
}

func TestDueFireSlotUTC_PerMinuteCronWithCheckpointSkipsSameWallMinute(t *testing.T) {
	s, err := ParseCronUTC("* * * * *")
	if err != nil {
		t.Fatal(err)
	}
	last, err := time.Parse(time.RFC3339, "2026-05-11T23:32:00Z")
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339, "2026-05-11T23:32:30Z")
	if err != nil {
		t.Fatal(err)
	}
	slot := DueFireSlotUTC(s, last, now)
	if !slot.After(now) {
		t.Fatalf("with last on this minute boundary, next slot must be after now; got slot=%s now=%s",
			slot.Format(time.RFC3339), now.Format(time.RFC3339))
	}
	if want := "2026-05-11T23:33:00Z"; slot.Format(time.RFC3339) != want {
		t.Fatalf("got %s want %s", slot.Format(time.RFC3339), want)
	}
}

func TestDueFireSlotUTC_WithCheckpointUsesStrictlyAfterLast(t *testing.T) {
	s, err := ParseCronUTC("0 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	last, err := time.Parse(time.RFC3339, "2026-05-11T15:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339, "2026-05-11T15:30:00Z")
	if err != nil {
		t.Fatal(err)
	}
	slot := DueFireSlotUTC(s, last, now)
	if want := "2026-05-11T16:00:00Z"; slot.Format(time.RFC3339) != want {
		t.Fatalf("got %s want %s", slot.Format(time.RFC3339), want)
	}
}

func TestNextScheduledDisplayUTC_StaleLastAdvancesToNow(t *testing.T) {
	s, err := ParseCronUTC("0 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	last, err := time.Parse(time.RFC3339, "2026-05-11T08:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339, "2026-05-11T22:15:00Z")
	if err != nil {
		t.Fatal(err)
	}
	next := NextScheduledDisplayUTC(s, last, now)
	if want := "2026-05-11T23:00:00Z"; next.Format(time.RFC3339) != want {
		t.Fatalf("got %s want %s", next.Format(time.RFC3339), want)
	}
}

func TestStatePathLockPath(t *testing.T) {
	p := filepath.FromSlash("/x/y/job.md")
	if g := StatePath(p); g != filepath.FromSlash("/x/y/job.state") {
		t.Fatalf("StatePath %q", g)
	}
	if g := LockPath(p); g != filepath.FromSlash("/x/y/job.lock") {
		t.Fatalf("LockPath %q", g)
	}
}

func TestCanonicalSchedulerJobPath_ResolvesSymlinkDir(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(dir, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skip(err)
	}
	jobViaLink := filepath.Join(linkDir, "job.md")
	jobReal := filepath.Join(realDir, "job.md")
	if err := os.WriteFile(jobViaLink, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	canLink := CanonicalSchedulerJobPath(jobViaLink)
	canReal := CanonicalSchedulerJobPath(jobReal)
	if canLink != canReal {
		t.Fatalf("canonical mismatch: %q vs %q", canLink, canReal)
	}
}

func TestParseJobFromBytes(t *testing.T) {
	raw := `---
description: "Test"
schedule: "0 0 * * *"
---
Do something
`
	fm, err := ParseJobFromBytes([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Description != "Test" {
		t.Fatalf("description %q", fm.Description)
	}
}

func TestReadWriteJobStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "job.state")
	slot, err := time.Parse(time.RFC3339, "2024-06-01T12:30:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteJobState(p, slot); err != nil {
		t.Fatal(err)
	}
	got, err := ReadJobState(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(slot.UTC()) {
		t.Fatalf("ReadJobState got %v want %v", got, slot.UTC())
	}
	empty, err := ReadJobState(filepath.Join(dir, "missing.state"))
	if err != nil {
		t.Fatal(err)
	}
	if !empty.IsZero() {
		t.Fatalf("missing file should yield zero time, got %v", empty)
	}
}

func TestReadSchedulerLockFireSlotUTC(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "demo.lock")
	want, err := time.Parse(time.RFC3339, "2026-05-12T00:02:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte(want.UTC().Format(time.RFC3339)+"\ntrailer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadSchedulerLockFireSlotUTC(lock)
	if !ok || !got.Equal(want.UTC()) {
		t.Fatalf("got %v ok=%v want %v", got, ok, want.UTC())
	}
	missing := filepath.Join(dir, "nope.lock")
	if _, ok := ReadSchedulerLockFireSlotUTC(missing); ok {
		t.Fatal("missing lock should return ok=false")
	}
}

func TestWriteJobStateOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "job.state")
	a, err := time.Parse(time.RFC3339, "2024-06-01T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	b, err := time.Parse(time.RFC3339, "2024-06-01T13:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteJobState(p, a); err != nil {
		t.Fatal(err)
	}
	if err := WriteJobState(p, b); err != nil {
		t.Fatal(err)
	}
	got, err := ReadJobState(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(b.UTC()) {
		t.Fatalf("ReadJobState got %v want %v", got, b.UTC())
	}
}

func TestListFlatJobMarkdownFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "b.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err := ListFlatJobMarkdownFiles([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("want 1 flat md, got %d: %v", len(paths), paths)
	}
	if filepath.Base(paths[0]) != "a.md" {
		t.Fatalf("unexpected path %q", paths[0])
	}
}
