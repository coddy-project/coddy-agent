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
		sc.Step(`^the statistics settings default to USD and the USD/RUB rate of 90$`, func() error {
			if last == nil || last.Settings.Currency != statistics.CurrencyUSD || last.Settings.USDToRub != 90 {
				return fmt.Errorf("default display settings = %+v", last)
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
		sc.Step(`^I save the RUB display currency with a USD/RUB rate of 90$`, func() error {
			w := statisticsDo(srv, http.MethodPut, "/coddy/statistics/settings", `{"currency":"RUB","usdToRub":90}`)
			if w.Code != 200 {
				return fmt.Errorf("currency settings: %d %s", w.Code, w.Body.String())
			}
			return nil
		})
		sc.Step(`^the statistics settings keep RUB and the USD/RUB rate of 90$`, func() error {
			w := statisticsDo(srv, http.MethodGet, "/coddy/statistics", "")
			if w.Code != 200 {
				return fmt.Errorf("read settings: %d %s", w.Code, w.Body.String())
			}
			var out struct {
				Settings struct {
					Currency string  `json:"currency"`
					USDToRub float64 `json:"usdToRub"`
				} `json:"settings"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				return err
			}
			if out.Settings.Currency != "RUB" || out.Settings.USDToRub != 90 {
				return fmt.Errorf("currency settings = %q / %g", out.Settings.Currency, out.Settings.USDToRub)
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
		sc.Step(`^I save a RUB tariff for the model "([^"]+)"$`, func(model string) error {
			body, err := json.Marshal(map[string]interface{}{
				"prices": map[string]interface{}{
					model: map[string]interface{}{"input": 180, "output": 0, "currency": "RUB"},
				},
			})
			if err != nil {
				return err
			}
			w := statisticsDo(srv, http.MethodPut, "/coddy/statistics/settings", string(body))
			if w.Code != 200 {
				return fmt.Errorf("RUB tariff: %d %s", w.Code, w.Body.String())
			}
			return nil
		})
		sc.Step(`^the custom tariff for "([^"]+)" is stored in RUB$`, func(model string) error {
			w := statisticsDo(srv, http.MethodGet, "/coddy/statistics", "")
			if w.Code != 200 {
				return fmt.Errorf("read tariff: %d %s", w.Code, w.Body.String())
			}
			var out struct {
				Settings struct {
					Prices map[string]map[string]json.RawMessage `json:"prices"`
				} `json:"settings"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				return err
			}
			var currency string
			if err := json.Unmarshal(out.Settings.Prices[model]["currency"], &currency); err != nil {
				return fmt.Errorf("tariff currency is missing: %w", err)
			}
			if currency != "RUB" {
				return fmt.Errorf("tariff currency = %q, want RUB", currency)
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
			if st.Prices["test"].Currency != statistics.CurrencyUSD {
				return fmt.Errorf("default tariff currency = %q, want USD", st.Prices["test"].Currency)
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
