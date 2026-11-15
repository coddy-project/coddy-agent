//go:build http

package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/statistics"
)

// The REST half of features/statistics.feature: what a surface reads, saves
// and clears over /coddy/statistics. The store's own scenarios run in
// internal/statistics, which skips these.
func TestStatisticsHTTPFeatures(t *testing.T) {
	var srv *Server
	var mgr *session.Manager
	var last *struct {
		Report   statistics.Report   `json:"report"`
		Settings statistics.Settings `json:"settings"`
	}
	setup := func(sc *godog.ScenarioContext) {
		sc.Step(`^a coddy HTTP server with recorded usage$`, func() error {
			srv, mgr = statisticsTestServer(t)
			last = nil
			return mgr.Statistics().Append(statistics.Event{
				ID: "call", Session: "chat", Kind: "call", At: time.Now(), Source: "webui",
				Model: "test", Input: statistics.Int(100), Output: statistics.Int(20),
			})
		})
		sc.Step(`^I read the statistics over REST$`, func() error {
			w := statisticsDo(srv, http.MethodGet, "/coddy/statistics", "")
			if w.Code != 200 {
				return fmt.Errorf("read: %d %s", w.Code, w.Body.String())
			}
			last = nil
			return json.Unmarshal(w.Body.Bytes(), &last)
		})
		sc.Step(`^the report carries the recorded tokens$`, func() error {
			if last == nil || last.Report.Totals.Total == nil || *last.Report.Totals.Total != 120 {
				return fmt.Errorf("unexpected report: %+v", last)
			}
			return nil
		})
		sc.Step(`^I save the statistics time zone over REST$`, func() error {
			w := statisticsDo(srv, http.MethodPut, "/coddy/statistics/settings", `{"timezone":"Europe/Moscow"}`)
			if w.Code != 200 {
				return fmt.Errorf("time zone: %d %s", w.Code, w.Body.String())
			}
			return nil
		})
		sc.Step(`^I save a custom tariff over REST$`, func() error {
			w := statisticsDo(srv, http.MethodPut, "/coddy/statistics/settings", `{"prices":{"test":{"input":2,"output":10,"cached":0.5}}}`)
			if w.Code != 200 {
				return fmt.Errorf("tariff: %d %s", w.Code, w.Body.String())
			}
			return nil
		})
		sc.Step(`^the saved time zone and the custom tariff are both kept$`, func() error {
			st, err := mgr.Statistics().Settings()
			if err != nil {
				return err
			}
			if st.Timezone != "Europe/Moscow" {
				return fmt.Errorf("time zone: %q", st.Timezone)
			}
			if _, ok := st.Prices["test"]; !ok {
				return fmt.Errorf("tariffs: %+v", st.Prices)
			}
			return nil
		})
		sc.Step(`^the daily export carries the recorded tokens$`, func() error {
			w := statisticsDo(srv, http.MethodGet, "/coddy/statistics/export?format=csv", "")
			if w.Code != 200 || !strings.Contains(w.Body.String(), "120") {
				return fmt.Errorf("export: %d %s", w.Code, w.Body.String())
			}
			return nil
		})
		sc.Step(`^I clear the statistics over REST$`, func() error {
			w := statisticsDo(srv, http.MethodDelete, "/coddy/statistics?keep=none", "")
			if w.Code != 200 {
				return fmt.Errorf("clear: %d %s", w.Code, w.Body.String())
			}
			return nil
		})
		sc.Step(`^the report carries no chats$`, func() error {
			report, err := mgr.Statistics().Query(statistics.Filter{})
			if err != nil {
				return err
			}
			if report.Totals.Chats != 0 {
				return fmt.Errorf("cleared report still has %d chats", report.Totals.Chats)
			}
			return nil
		})
	}
	suite := godog.TestSuite{
		Name:                "statistics-http",
		ScenarioInitializer: setup,
		Options:             &godog.Options{Format: "progress", Tags: "@http", Paths: []string{"../../features/statistics.feature"}, TestingT: t},
	}
	if suite.Run() != 0 {
		t.Fatal("statistics REST specifications failed")
	}
}
