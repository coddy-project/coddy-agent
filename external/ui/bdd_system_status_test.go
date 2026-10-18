//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The @ui scenario of features/system_status.feature: the System tile of the
// Settings drawer on a phone, rendered by Vitest against a stubbed server. The
// scenario's other half is the status route itself, which the harness of
// external/httpserver runs. The Given and When steps are the page the Vitest
// tests build (fakeStatus names a low disk and a pending restart, the drawer
// opens at 390 px), so the Then steps are where the page is held to them.
func TestSystemStatusUIFeature(t *testing.T) {
	const file = "src/ui/settings/Settings.test.tsx"
	suite := godog.TestSuite{
		Name: "system_status_ui",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a server whose status names a low disk and a pending restart$`, func() error {
				return nil
			})
			sc.Step(`^the operator opens Settings on a phone$`, func() error {
				return nil
			})
			sc.Step(`^the System tile reads "2 need attention"$`, func() error {
				return runVitestScenario(file,
					`system status the System tile reads "2 need attention" on a phone`)
			})
			sc.Step(`^Settings asked for the status once$`, func() error {
				return runVitestScenario(file,
					"system status Settings reads the status once when it opens")
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/system_status.feature"},
			Tags:     "@ui",
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("system status UI feature failed")
	}
}
