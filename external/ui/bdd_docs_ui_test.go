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
			sc.Step(`^a page mentioned in a sent message opens the reader$`, func() error {
				return runVitestScenario("src/ui/messages/UserMessage.test.tsx",
					"an @coddy: mention in the sent message opens the documentation reader")
			})
			sc.Step(`^/docs in the composer opens the reader on a search instead of reaching the agent$`, func() error {
				if err := runVitestScenario("src/ui/chat/Composer.test.tsx",
					"/docs opens the reader with what follows it, and sends nothing"); err != nil {
					return err
				}
				return runVitestScenario("src/ui/docs/DocsView.test.tsx",
					"/docs in the composer shows the search it was given in the reader")
			})
			sc.Step(`^the chat names a documentation lookup by what it does$`, func() error {
				return runVitestScenario("src/ui/messages/ToolCallMessage.test.tsx",
					"the documentation tools say what they do and name the query or the page")
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
