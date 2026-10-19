//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The files a conversation names open in the Files window: a shared file's
// card and a path an answer names are rendered components, so each step runs
// the Vitest test that drives them.
func TestWebUIFileLinksFeature(t *testing.T) {
	steps := []struct{ step, file, name string }{
		{`^a click on a shared file's card opens its source in the Files window$`,
			"src/ui/messages/ToolArtifacts.test.tsx",
			"a click on a shared file's card opens its source in the Files window"},
		{`^a card checks its file before it opens it and opens it when it is there$`,
			"src/ui/messages/ToolArtifacts.test.tsx",
			"a card checks its file before it opens it and opens it when it is there"},
		{`^a shared file whose source was deleted says so and keeps its copy downloadable$`,
			"src/ui/messages/ToolArtifacts.test.tsx",
			"a shared file whose source was deleted says so and keeps its copy downloadable"},
		{`^a card whose file was deleted since the listing finds it out on the click$`,
			"src/ui/messages/ToolArtifacts.test.tsx",
			"a card whose file was deleted since the listing finds it out on the click"},
		{`^a path an answer names opens in a tab of the Files window once the server says it is a file$`,
			"src/ui/markdown/FileReference.test.tsx",
			"a path an answer names opens in a tab of the Files window once the server says it is a file"},
		{`^what is not a file of the workspace keeps its look and its copy button$`,
			"src/ui/markdown/FileReference.test.tsx",
			"what is not a file of the workspace keeps its look and its copy button"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_file_links",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, s := range steps {
				file, name := s.file, s.name
				sc.Step(s.step, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_file_links.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI file links feature failed")
	}
}
