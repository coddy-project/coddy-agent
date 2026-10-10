//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The menus are state and placement of rendered components, so each step runs
// the Vitest test that opens one and checks where it went.
func TestWebUIMenusFeature(t *testing.T) {
	steps := []struct{ step, file, name string }{
		{`^Send clicked while the slash menu is open closes the menu$`,
			"src/ui/chat/Composer.test.tsx",
			"Send clicked while the slash menu is open closes the menu"},
		{`^a draft queued while the slash menu is open closes the menu$`,
			"src/ui/chat/Composer.test.tsx",
			"a draft queued while the slash menu is open closes the menu"},
		{`^the environment menu opens beside the rail from the foot of it$`,
			"src/ui/nav/EnvironmentSwitcher.test.tsx",
			"EnvironmentSwitcher menu direction opens beside the rail from the foot of it, growing upward"},
		{`^the environment menu stays inside the window$`,
			"src/ui/nav/EnvironmentSwitcher.test.tsx",
			"EnvironmentSwitcher menu direction stays inside the window when the item is near the right edge"},
		{`^on a phone the choices of a History filter fold out under their row$`,
			"src/ui/sessions/SessionsFilterMenu.test.tsx",
			"History filter menu on a phone folds a section's choices out under its row, inside the screen"},
		{`^the History filter menu is never wider than the room left of its control$`,
			"src/ui/sessions/SessionsFilterMenu.test.tsx",
			"History filter menu on a phone is never wider than the room left of its trigger"},
		{`^clicking a History filter row the pointer just opened keeps it open$`,
			"src/ui/sessions/SessionsFilterMenu.test.tsx",
			"History filter menu with a mouse keeps a section the pointer just opened when the same pointer clicks it"},
		{`^the branch list opens with the branches it already has while the remotes refresh$`,
			"src/ui/chat/WorkspaceChips.test.tsx",
			"WorkspaceChips lists the branches it already has while the remotes refresh"},
		{`^a branch is picked from the list while the remotes refresh$`,
			"src/ui/chat/WorkspaceChips.test.tsx",
			"WorkspaceChips picks a branch while the remotes refresh"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_menus",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, s := range steps {
				file, name := s.file, s.name
				sc.Step(s.step, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_menus.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI menus feature failed")
	}
}
