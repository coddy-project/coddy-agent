//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The transcript window lives in the SPA, so each step runs the Vitest test
// that renders it: the App against a paged backend, the page mapping, the
// message list and the chat screen. The browser behaviour the DOM cannot show
// under jsdom - rows grown and trimmed while scrolling, the reader's row kept
// still, the frame times - is measured by scripts/transcript-window-check.mjs
// against the real binary (docs/surfaces/web-ui.md, Long sessions).
func TestWebTranscriptWindowFeature(t *testing.T) {
	steps := map[string][2]string{
		`^a long session opens on its newest page, numbered as the whole history$`: {
			"src/ui/App.transcriptWindow.test.tsx",
			"a long session opens on its newest page, numbered as the whole history",
		},
		`^the page above arrives in front, and returning to the newest message lets it go$`: {
			"src/ui/App.transcriptWindow.test.tsx",
			"the page above arrives in front, and returning to the newest message lets it go",
		},
		`^pages read from the end join into what a whole read shows$`: {
			"src/ui/chat/transcriptFromMessages.test.ts",
			"pages read from the end join into what a whole read shows",
		},
		`^an edit names the prompt by the server's index when the list holds only the end of the history$`: {
			"src/ui/messages/MessageList.test.tsx",
			"an edit names the prompt by the server's index when the list holds only the end of the history",
		},
		`^the top of a transcript with history above offers it, says it is loading, and retries$`: {
			"src/ui/chat/ChatScreen.test.tsx",
			"the top of a transcript with history above offers it, says it is loading, and retries",
		},
	}
	suite := godog.TestSuite{
		Name: "web_transcript_window",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for pattern, target := range steps {
				file, name := target[0], target[1]
				sc.Step(pattern, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_transcript_window.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web transcript window feature failed")
	}
}
