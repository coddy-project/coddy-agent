package statistics

import (
	"fmt"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

func TestStatisticsFeatures(t *testing.T) {
	var s *Store
	var old Event
	var events []Event
	var report Report
	setup := func(sc *godog.ScenarioContext) {
		sc.Step(`^a statistics store with calls on two days$`, func() error {
			s = New(t.TempDir())
			old = Event{ID: "old", Dataset: "one", Session: "chat", Kind: "call", At: time.Now().Add(-48 * time.Hour), Input: Int(100), Output: Int(20), Cached: Int(50)}
			if err := s.Append(old); err != nil {
				return err
			}
			current := old
			current.ID = "new"
			current.At = time.Now()
			return s.Append(current)
		})
		sc.Step(`^I reopen the statistics store$`, func() error { s = &Store{Dir: s.Dir}; var err error; report, err = s.Query(Filter{}); return err })
		sc.Step(`^the recorded usage is (\d+) tokens in one chat$`, func(tokens int64) error {
			if s != nil {
				var err error
				report, err = s.Query(Filter{})
				if err != nil {
					return err
				}
			}
			if report.Totals.Total == nil || *report.Totals.Total != tokens || report.Totals.Chats != 1 {
				return fmt.Errorf("unexpected totals: %+v", report.Totals)
			}
			return nil
		})
		sc.Step(`^the cached input is counted within input tokens$`, func() error {
			if report.Totals.Input == nil || *report.Totals.Input != 200 || report.Totals.Cached == nil || *report.Totals.Cached != 100 {
				return fmt.Errorf("cache accounting: %+v", report.Totals)
			}
			return nil
		})
		sc.Step(`^I clear statistics except the last day$`, func() error { return s.Clear(time.Now().Add(-24 * time.Hour)) })
		sc.Step(`^the old event is imported again$`, func() error { return s.Append(old) })
		sc.Step(`^two routes to the same statistics dataset$`, func() {
			s = nil
			e := Event{ID: "call", Dataset: "one", Session: "chat", Kind: "call", At: time.Now(), Input: Int(100), Output: Int(20)}
			events = []Event{e, e}
		})
		sc.Step(`^I merge their statistics$`, func() error { var err error; report, err = Summarize(events, Filter{}, nil, time.Now()); return err })
	}
	suite := godog.TestSuite{Name: "statistics", ScenarioInitializer: setup, Options: &godog.Options{Format: "progress", Paths: []string{"../../features/statistics.feature"}, TestingT: t}}
	if suite.Run() != 0 {
		t.Fatal("statistics specifications failed")
	}
}
