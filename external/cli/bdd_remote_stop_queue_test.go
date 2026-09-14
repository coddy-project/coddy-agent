//go:build cli

package cli

import (
	"context"
	"fmt"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestRemoteStopQueueFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "remote-stop-queue",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var stand *remoteControlStand
			sc.Step(`^a browser is running a turn with a queued message in the shared remote session$`, func() {
				stand = newRemoteControlStand(t)
				stand.mu.Lock()
				stand.active = true
				stand.rows = []session.QueuedMessage{{ID: "q_browser", Text: "browser correction"}}
				stand.version = 8
				stand.mu.Unlock()
			})
			sc.Step(`^the console reopens that session and hydrates its controls$`, func() error {
				if err := stand.app.loadSession(context.Background(), sharedControlSession); err != nil {
					return err
				}
				if len(stand.app.queue.Rows()) == 0 {
					return awaitControls(stand.app, func(updateMsg) bool { return len(stand.app.queue.Rows()) == 1 })
				}
				return nil
			})
			sc.Step(`^the console operator submits(?: the follow-up)? "([^"]*)"$`, func(text string) {
				stand.app.editor.SetText(text)
				stand.app.dispatchInput([]byte("\r"))
			})
			sc.Step(`^the follow-up is queued without a new prompt request$`, func() error {
				if _, err := stand.awaitRequest("queue"); err != nil {
					return err
				}
				if err := awaitControls(stand.app, func(updateMsg) bool { return len(stand.app.queue.Rows()) == 2 }); err != nil {
					return err
				}
				if stand.app.turnActive {
					return fmt.Errorf("queue submission acquired a prompt lifecycle")
				}
				return nil
			})
			sc.Step(`^the console operator presses Escape$`, func() error {
				if !stand.app.handleGlobalKey([]byte("\x1b")) {
					return fmt.Errorf("Escape ignored the browser-owned turn")
				}
				return nil
			})
			sc.Step(`^the remote turn is cancelled and its queue is empty$`, func() error {
				if _, err := stand.awaitRequest("cancel"); err != nil {
					return err
				}
				stand.events <- controlQueueFrame("sess_barrier", nil, 1)
				if err := awaitControls(stand.app, func(msg updateMsg) bool { return msg.sessionID == "sess_barrier" }); err != nil {
					return err
				}
				if len(stand.app.queue.Rows()) != 0 {
					return fmt.Errorf("cancelled turn retained queued messages")
				}
				return nil
			})
			sc.Step(`^the console starts a new prompt request$`, func() error {
				_, err := stand.awaitRequest("prompt")
				return err
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/cli_remote_stop_queue.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("remote Stop/queue feature failed")
	}
}
