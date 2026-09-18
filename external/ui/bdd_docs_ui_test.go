//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The @web scenario of features/builtin_docs.feature: the reader rendered by
// Vitest against a stubbed /coddy/docs API.
func TestBuiltinDocsWebUIFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "builtin_docs_web_ui",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^the reader shows a page with its contents, its sections and the page before it$`, func() error {
				return runVitestScenario("src/ui/docs/DocsView.test.tsx",
					"DocsView shows a page with its contents, sections, neighbours and working links")
			})
			sc.Step(`^the reader's search opens a hit at its section$`, func() error {
				return runVitestScenario("src/ui/docs/DocsView.test.tsx",
					"DocsView searches as the query is typed and opens a hit at its section")
			})
			sc.Step(`^asking the agent opens a chat with the page mentioned$`, func() error {
				return runVitestScenario("src/ui/docs/DocsView.test.tsx",
					"DocsView asks the agent about the page with the page mentioned")
			})
			sc.Step(`^a coddy: link in any message opens the reader$`, func() error {
				return runVitestScenario("src/ui/markdown/Markdown.test.tsx",
					"coddy: links open the documentation reader")
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/builtin_docs.feature"},
			Tags:     "@web",
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("builtin docs web UI feature failed")
	}
}
