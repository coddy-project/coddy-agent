//go:build scheduler

package daemon

import (
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
)

func TestCronMinuteMatchesEveryMinute(t *testing.T) {
	sch, err := storage.ParseCronUTC("* * * * *")
	if err != nil {
		t.Fatal(err)
	}
	m, err := time.Parse(time.RFC3339, "2026-05-12T12:07:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if !storage.CronMinuteMatchesUTC(sch, m) {
		t.Fatal("expected match at any minute for * * * * *")
	}
}

func TestCronMinuteMatchesStepTwoEvenMinutesOnly(t *testing.T) {
	sch, err := storage.ParseCronUTC("*/2 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	even, err := time.Parse(time.RFC3339, "2026-05-12T12:04:00Z")
	if err != nil {
		t.Fatal(err)
	}
	odd, err := time.Parse(time.RFC3339, "2026-05-12T12:03:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if !storage.CronMinuteMatchesUTC(sch, even) {
		t.Fatal("even minute should match */2")
	}
	if storage.CronMinuteMatchesUTC(sch, odd) {
		t.Fatal("odd minute should not match */2")
	}
}

func TestCronJobEligibleSameMinuteNotTwiceAfterCheckpoint(t *testing.T) {
	sch, err := storage.ParseCronUTC("* * * * *")
	if err != nil {
		t.Fatal(err)
	}
	eval, err := time.Parse(time.RFC3339, "2026-05-12T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if !storage.CronJobEligibleForMinute(sch, time.Time{}, eval) {
		t.Fatal("first fire with no checkpoint")
	}
	if !storage.CronJobEligibleForMinute(sch, time.Time{}, eval) {
		t.Fatal("still eligible with zero last (spawn dedupe is separate)")
	}
	if !storage.CronJobEligibleForMinute(sch, time.Time{}, eval.Add(30*time.Second)) {
		t.Fatal("sub-second offset on eval minute should still normalize to same minute")
	}
	if storage.CronJobEligibleForMinute(sch, eval, eval) {
		t.Fatal("after checkpoint at eval minute must not run again for same minute")
	}
}
