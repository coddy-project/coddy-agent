package agent

import (
	"log/slog"
	"testing"

	"github.com/cucumber/godog"
)

func TestRequestLatencyDiagnosticsFeature(t *testing.T) {
	var records []map[string]any
	suite := godog.TestSuite{Name: "request_latency_diagnostics", Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/request_latency_diagnostics.feature"}, TestingT: t}, ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.Step(`^an agent with debug request diagnostics$`, func() { records = nil })
		sc.Step(`^a scripted model calls a tool and then answers$`, func() error { var err error; records, err = runLatencyScript(t.TempDir(), slog.LevelDebug); return err })
		sc.Step(`^the request timings and completion records share unique call identifiers$`, func() error { return checkLatencyRecords(records) })
		sc.Step(`^the diagnostics contain no request or response content$`, func() error { return checkLatencyRecords(records) })
	}}
	if suite.Run() != 0 {
		t.Fatal("request latency diagnostics feature failed")
	}
}
