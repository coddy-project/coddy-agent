package statistics

import (
	"testing"
	"time"
)

func day(date string, tokens int64, manual bool) Day {
	return Day{Date: date, Input: Int(tokens), Total: Int(tokens), Requests: 1, Manual: manual}
}

// A relay merges partial aggregates, so what a node reports has to survive the
// merge with the meaning it had: summed where summing is right, taken once
// where a ring repeats it, and derived again where only the whole fleet's
// series can answer.
func TestMergeReportsCombinesWhatOnlyTheWholeFleetKnows(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	first := Report{
		Dataset: "nas02",
		Totals:  Totals{Input: Int(100), Total: Int(100), ActiveMS: Int(5000), LongestMS: Int(5000), Requests: 2, Calls: 3, Chats: 2, SkillCalls: 1},
		Days:    []Day{day("2026-03-02", 100, true)},
		Models:  []Breakdown{{Name: "gpt-5", Count: 3, Tokens: Int(100)}},
		Skills:  []Breakdown{{Name: "review", Count: 1}},
	}
	second := Report{
		Dataset: "gpu03",
		Totals:  Totals{Input: Int(40), Total: Int(40), ActiveMS: Int(1000), LongestMS: Int(900), Requests: 1, Calls: 1, Chats: 1, SkillCalls: 2},
		Days:    []Day{day("2026-03-03", 40, true), day("2026-03-04", 7, true)},
		Models:  []Breakdown{{Name: "gpt-5", Count: 1, Tokens: Int(40)}, {Name: "claude", Count: 1, Tokens: Int(9)}},
		Skills:  []Breakdown{{Name: "ship", Count: 2}},
	}
	// The same dataset arriving by a second route is the shape of a ring.
	merged, err := MergeReports([]Report{first, second, first}, Filter{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Totals.Total == nil || *merged.Totals.Total != 140 {
		t.Fatalf("a dataset reached twice was counted twice: %v", merged.Totals.Total)
	}
	if merged.Totals.Chats != 3 || merged.Totals.Requests != 3 || merged.Totals.Calls != 4 || merged.Totals.SkillCalls != 3 {
		t.Fatalf("counters: %+v", merged.Totals)
	}
	if merged.Totals.LongestMS == nil || *merged.Totals.LongestMS != 5000 {
		t.Fatalf("the longest turn of a fleet is the longest one, not their sum: %v", merged.Totals.LongestMS)
	}
	if merged.Totals.ActiveMS == nil || *merged.Totals.ActiveMS != 6000 {
		t.Fatalf("active time: %v", merged.Totals.ActiveMS)
	}
	// Three days running, which neither node reported on its own.
	if merged.Totals.LongestStreak != 3 || merged.Totals.CurrentStreak != 3 {
		t.Fatalf("the streak was not derived from the merged days: %+v", merged.Totals)
	}
	if merged.Totals.MaxDailyTokens == nil || *merged.Totals.MaxDailyTokens != 100 {
		t.Fatalf("daily record: %v", merged.Totals.MaxDailyTokens)
	}
	if len(merged.Days) != 3 {
		t.Fatalf("days: %+v", merged.Days)
	}
	if merged.Totals.UniqueSkills != 2 {
		t.Fatalf("skills of the fleet are their union: %+v", merged.Skills)
	}
	if merged.Models[0].Name != "gpt-5" || merged.Models[0].Count != 4 || *merged.Models[0].Tokens != 140 {
		t.Fatalf("breakdowns merge by name: %+v", merged.Models)
	}
	if merged.Dataset != "" {
		t.Fatalf("a merge belongs to no dataset: %q", merged.Dataset)
	}
}

// Two nodes active on the same day are one day of the fleet, counted once in
// the series and summed in its counters.
func TestMergeReportsFoldsTheSameDayOfTwoNodes(t *testing.T) {
	now := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	a := Report{Dataset: "a", Totals: Totals{Total: Int(10)}, Days: []Day{day("2026-03-02", 10, false)}}
	b := Report{Dataset: "b", Totals: Totals{Total: Int(5)}, Days: []Day{day("2026-03-02", 5, true)}}
	merged, err := MergeReports([]Report{a, b}, Filter{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Days) != 1 || *merged.Days[0].Total != 15 || merged.Days[0].Requests != 2 {
		t.Fatalf("days: %+v", merged.Days)
	}
	if !merged.Days[0].Manual || merged.Totals.CurrentStreak != 1 {
		t.Fatalf("a day somebody worked is a day of the fleet: %+v", merged.Days[0])
	}
}

func TestMergeReportsRefusesAnImpossibleFilter(t *testing.T) {
	if _, err := MergeReports(nil, Filter{From: "2026-03-04", To: "2026-03-01"}, time.Now()); err == nil {
		t.Fatal("an inverted period was accepted")
	}
}
