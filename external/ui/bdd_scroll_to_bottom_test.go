//go:build http && ui

package ui

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Run the DOM assertion in Vitest, where the actual React component is rendered
// and the scroll viewport can be given real metrics. The http,ui test matrix
// installs these dependencies through make ui-build.
func TestTranscriptScrollToBottomFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "transcript_scroll_to_bottom",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^the transcript scroll-to-bottom button appears when the reader scrolls up and returns them to the newest message$`, func() error {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, "node", "node_modules/vitest/vitest.mjs", "run",
					"src/ui/chat/ChatScreen.test.tsx", "--testNamePattern",
					"^scrolling up reveals the scroll-to-bottom button, which returns the transcript to the newest message$")
				if out, err := cmd.CombinedOutput(); err != nil {
					return fmt.Errorf("scroll-to-bottom DOM scenario: %w\n%s", err, out)
				}
				return nil
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/transcript_scroll_to_bottom.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("transcript scroll to bottom feature failed")
	}
}
